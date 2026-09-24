package worker

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	wasmengine "github.com/aerol-ai/microvm/pkg/wasm"
)

// ctxRecordingEngine records the context handed to InvokeExport so a test can
// assert whether the worker bounded the guest call with the caps wall timeout.
type ctxRecordingEngine struct {
	mu    sync.Mutex
	calls []recordedInvoke
}

type recordedInvoke struct {
	export string
	// budget is the time left on the call context's deadline; zero means the
	// context carried no deadline at all.
	budget time.Duration
}

func (e *ctxRecordingEngine) record(export string, ctx context.Context) {
	budget := time.Duration(0)
	if deadline, ok := ctx.Deadline(); ok {
		budget = time.Until(deadline)
	}
	e.mu.Lock()
	e.calls = append(e.calls, recordedInvoke{export: export, budget: budget})
	e.mu.Unlock()
}

func (e *ctxRecordingEngine) last() (recordedInvoke, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) == 0 {
		return recordedInvoke{}, false
	}
	return e.calls[len(e.calls)-1], true
}

func (e *ctxRecordingEngine) LoadModule(context.Context, string, wasmengine.LoadOptions) error {
	return nil
}
func (e *ctxRecordingEngine) Instantiate(context.Context, wasmengine.Capabilities) error { return nil }
func (e *ctxRecordingEngine) InvokeExport(ctx context.Context, export string) error {
	e.record(export, ctx)
	return nil
}
func (e *ctxRecordingEngine) Run(context.Context, wasmengine.Capabilities, string) (wasmengine.RunResult, error) {
	return wasmengine.RunResult{}, nil
}
func (e *ctxRecordingEngine) StopInstance(context.Context) error { return nil }
func (e *ctxRecordingEngine) CaptureSnapshot(context.Context) (wasmengine.SnapshotCapture, error) {
	return wasmengine.SnapshotCapture{}, nil
}
func (e *ctxRecordingEngine) RestoreSnapshot(context.Context, wasmengine.SnapshotRestoreInput, wasmengine.Capabilities) error {
	return nil
}
func (e *ctxRecordingEngine) Close(context.Context) error     { return nil }
func (e *ctxRecordingEngine) ResolvedListenPort() (int, bool) { return 0, false }
func (e *ctxRecordingEngine) SupportsListen() bool            { return true }

// newPipeClient wires a Client to a Server over net.Pipe so a test drives the
// real wire path (client encode → frame → Serve decode → engine call).
func newPipeClient(srv *Server) *Client {
	c := NewClient("pipe")
	c.dial = func(string) (net.Conn, error) {
		clientConn, serverConn := net.Pipe()
		go func() { _ = srv.Serve(serverConn) }()
		return clientConn, nil
	}
	return c
}

// The worker must apply the caps wall timeout to one-shot invokes only. A
// background invoke is the long-lived guest entry (the HTTP serve loop) and has
// to outlive the per-request wall timeout, or the sandbox stops serving the
// moment that budget expires.
//
// The payloads below are written as raw JSON on purpose: that pins the on-wire
// contract for the flag independently of the Go struct, which is what lets this
// test state the intended behaviour before the flag exists.
func TestServer_BackgroundInvokeExemptFromWallTimeout(t *testing.T) {
	eng := &ctxRecordingEngine{}
	srv := &Server{eng: eng, lastCaps: wasmengine.Capabilities{WallTimeoutNs: int64(time.Minute)}}
	client := newPipeClient(srv)

	reply, err := client.roundTrip(Envelope{
		Type:      MsgInvoke,
		SandboxID: "sb-serve",
		Payload:   []byte(`{"export":"_start","background":true}`),
	})
	if err != nil {
		t.Fatalf("background invoke round trip: %v", err)
	}
	if err := client.expectOK(reply); err != nil {
		t.Fatalf("background invoke reply: %v", err)
	}
	got, ok := eng.last()
	if !ok {
		t.Fatal("engine never saw the background invoke")
	}
	if got.budget != 0 {
		t.Fatalf("background serve invoke was bounded by a %s wall-timeout deadline; a long-lived guest entry must run without the per-request wall deadline", got.budget)
	}

	reply, err = client.roundTrip(Envelope{
		Type:      MsgInvoke,
		SandboxID: "sb-serve",
		Payload:   []byte(`{"export":"_start"}`),
	})
	if err != nil {
		t.Fatalf("one-shot invoke round trip: %v", err)
	}
	if err := client.expectOK(reply); err != nil {
		t.Fatalf("one-shot invoke reply: %v", err)
	}
	got, ok = eng.last()
	if !ok {
		t.Fatal("engine never saw the one-shot invoke")
	}
	if got.budget <= 0 {
		t.Fatal("one-shot invoke lost its wall-timeout deadline; a CPU-bound guest would no longer be bounded")
	}
	if got.budget > time.Minute {
		t.Fatalf("one-shot invoke deadline budget = %s, want <= %s (caps WallTimeoutNs)", got.budget, time.Minute)
	}
}

// InvokeBackground must mark the call on the wire and plain Invoke must not —
// that flag is the only thing separating the long-lived serve from a one-shot
// invoke by the time the worker decides on the wall deadline.
func TestClient_InvokeBackgroundSetsWireFlag(t *testing.T) {
	capture := func(t *testing.T, call func(*Client) error) invokePayload {
		t.Helper()
		c := NewClient("capture")
		envCh := make(chan Envelope, 1)
		c.dial = func(string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			go func() {
				env, err := readFrame(serverConn)
				if err == nil {
					envCh <- env
				}
				_ = writeFrame(serverConn, Envelope{Type: MsgOK})
				_ = serverConn.Close()
			}()
			return clientConn, nil
		}
		if err := call(c); err != nil {
			t.Fatalf("call: %v", err)
		}
		env := <-envCh
		if env.Type != MsgInvoke {
			t.Fatalf("message type = %q, want %q", env.Type, MsgInvoke)
		}
		var p invokePayload
		if err := decodePayload(env.Payload, &p); err != nil {
			t.Fatalf("decode invoke payload: %v", err)
		}
		return p
	}

	bg := capture(t, func(c *Client) error { return c.InvokeBackground("sb", "_start") })
	if !bg.Background {
		t.Fatal("InvokeBackground sent background=false; the worker would wall-bound the serve")
	}
	if bg.Export != "_start" {
		t.Fatalf("background export = %q, want _start", bg.Export)
	}

	oneShot := capture(t, func(c *Client) error { return c.Invoke("sb", "_start") })
	if oneShot.Background {
		t.Fatal("Invoke sent background=true; a one-shot invoke must stay wall-bounded")
	}
}
