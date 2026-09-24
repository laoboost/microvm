package docker

import (
	"net"
	"testing"
	"time"
)

// fakeTimeoutConn is a net.Conn whose Read always fails with a timeout-style
// net.Error — the shape of Adopt's intentional deadline interrupt.
type fakeTimeoutConn struct{ net.Conn }

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func (c *fakeTimeoutConn) Read([]byte) (int, error) { return 0, timeoutErr{} }

// TestMonitorParked_GuestEOFDuringAdoptingMarksDead pins the liveness monitor
// bug: it treated EVERY n==0 read error during adopting as Adopt's intentional
// deadline interrupt. A real guest EOF (dead connection) arriving in that
// window was swallowed and the slot kept reporting Alive — so the pool handed
// out dead warm slots. Only a net.Error with Timeout() is the interrupt; EOF
// must mark the slot dead even while adopting.
func TestMonitorParked_GuestEOFDuringAdoptingMarksDead(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })

	pl := &ParkedListener{conn: server}
	pl.adopting.Store(true)

	done := make(chan struct{})
	go pl.monitorParked(server, done)

	// Guest dies (EOF) while the host is inside the adopting window.
	_ = client.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("monitorParked did not observe the guest EOF")
	}

	pl.mu.Lock()
	dead := pl.dead
	pl.mu.Unlock()
	if !dead {
		t.Fatal("guest EOF during adopting left the slot Alive (dead conn reported live)")
	}
	if pl.Alive() {
		t.Fatal("Alive() true after guest EOF during adopting")
	}
}

// TestMonitorParked_TimeoutInterruptDuringAdoptingStaysAlive pins the allowed
// interrupt: Adopt kicks the monitor with an immediate read deadline, and a
// timeout error during adopting must NOT mark the slot dead.
func TestMonitorParked_TimeoutInterruptDuringAdoptingStaysAlive(t *testing.T) {
	conn := &fakeTimeoutConn{}
	pl := &ParkedListener{conn: conn}
	pl.adopting.Store(true)

	done := make(chan struct{})
	go pl.monitorParked(conn, done)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("monitorParked did not return on the deadline interrupt")
	}

	pl.mu.Lock()
	dead := pl.dead
	pl.mu.Unlock()
	if dead {
		t.Fatal("deadline interrupt during adopting marked the slot dead")
	}
}

// TestMonitorParked_EOFWhileParkedMarksDead keeps the not-adopting baseline:
// a plain parked-guest EOF is dead regardless of the adopting flag.
func TestMonitorParked_EOFWhileParkedMarksDead(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })

	pl := &ParkedListener{conn: server}
	done := make(chan struct{})
	go pl.monitorParked(server, done)
	_, _ = client.Write([]byte("x")) // protocol violation byte also marks dead
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("monitorParked did not return")
	}
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if !pl.dead {
		t.Fatal("guest byte while parked must mark the slot dead")
	}
}
