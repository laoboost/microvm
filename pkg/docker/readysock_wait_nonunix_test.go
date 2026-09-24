package docker

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// nonUnixListener is a net.Listener that is deliberately NOT a *net.UnixListener.
type nonUnixListener struct{ acceptErr error }

func (l nonUnixListener) Accept() (net.Conn, error) { return nil, l.acceptErr }
func (l nonUnixListener) Close() error              { return nil }
func (l nonUnixListener) Addr() net.Addr            { return testAddr{} }

type testAddr struct{}

func (testAddr) Network() string { return "test" }
func (testAddr) String() string  { return "test" }

// ReadyListener.Wait must not blind-assert the concrete *net.UnixListener type:
// a non-unix listener would panic instead of surfacing its Accept error. The
// sibling ParkedListener.WaitParked already uses the safe `ok` form.
func TestReadyListenerWaitNonUnixListenerNoPanic(t *testing.T) {
	rl := &ReadyListener{
		sandboxID: "sb",
		token:     "tok",
		nonce:     "nonce",
		listener:  nonUnixListener{acceptErr: errors.New("accept boom")},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := rl.Wait(ctx)
	if err == nil {
		t.Fatal("expected accept error from non-unix listener")
	}
	if !strings.Contains(err.Error(), "accept boom") {
		t.Fatalf("err = %v, want it to surface the Accept error", err)
	}
}
