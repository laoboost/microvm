package worker

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	wasmengine "github.com/aerol-ai/microvm/pkg/wasm"
)

// The context-done teardown that makes the invocation deadline real for a
// CPU-bound one-shot guest must not apply to the long-lived serve: a wasm
// sandbox that exposes an HTTP port runs _start as a background entry that
// serves for the sandbox's whole lifetime. Bounding it by the per-request wall
// timeout kills the serve at that budget.
//
// This drives the real guest through the worker, so it pins the serve end to
// end: it outlives the wall budget, it still serves requests well past it, and
// it stays killable by StopInstance afterwards. The payload is raw JSON so the
// flag can be stated before the Go struct carries it.
func TestBackgroundServeOutlivesWallTimeout(t *testing.T) {
	modPath := ensureWasip1HTTPWasm(t)
	client, cleanup := startTestWorker(t)
	defer cleanup()

	sandboxID := "sb-bg-serve"
	if _, err := client.LoadModule(sandboxID, modPath, 0); err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	const wall = 400 * time.Millisecond
	caps := wasmengine.Capabilities{
		WASIListenPort: 0,
		WASIListenHost: "127.0.0.1",
		Args:           []string{"wasi", "http"},
		WallTimeoutNs:  int64(wall),
	}
	if err := client.Instantiate(sandboxID, caps); err != nil {
		t.Fatalf("Instantiate: %v", err)
	}

	serveReturned := make(chan error, 1)
	go func() {
		_, err := client.roundTrip(Envelope{
			Type:      MsgInvoke,
			SandboxID: sandboxID,
			Payload:   []byte(`{"export":"_start","background":true}`),
		})
		serveReturned <- err
	}()

	post := func() error {
		req, err := http.NewRequest(http.MethodPost, "http://gateway/", bytes.NewReader([]byte("wazero")))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		rec := httptest.NewRecorder()
		if err := client.ProxyHTTP(sandboxID, 0, rec, req); err != nil {
			return err
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
		}
		return nil
	}

	// _start enters http.Serve shortly after the wasip1 listener is bound.
	var lastErr error
	for i := 0; i < 40; i++ {
		if lastErr = post(); lastErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("guest never served a request: %v", lastErr)
	}

	// Past the wall budget the serve must still be running.
	time.Sleep(3 * wall)
	select {
	case err := <-serveReturned:
		t.Fatalf("background serve returned %s after start, i.e. at/before the %s wall budget (err=%v); the serve must outlive the per-request wall timeout", 3*wall, wall, err)
	default:
	}
	if err := post(); err != nil {
		t.Fatalf("guest stopped serving past the wall budget: %v", err)
	}

	// Unbounded by wall time does not mean unkillable: the sandbox lifecycle
	// must still stop it promptly.
	stopped := make(chan error, 1)
	go func() { stopped <- client.StopInstance(sandboxID) }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("StopInstance: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("StopInstance blocked behind the running serve; the serve is unkillable")
	}
	select {
	case <-serveReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("background serve still running 5s after StopInstance")
	}

	// Boundary preserved: re-instantiated and invoked one-shot, the same
	// never-returning guest entry is wall-bounded again.
	if err := client.Instantiate(sandboxID, caps); err != nil {
		t.Fatalf("re-Instantiate: %v", err)
	}
	oneShot := make(chan error, 1)
	go func() { oneShot <- client.Invoke(sandboxID, "_start") }()
	select {
	case err := <-oneShot:
		if err == nil {
			t.Fatal("one-shot invoke of a never-returning guest returned OK; the wall timeout no longer bounds one-shot invokes")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("one-shot invoke was not bounded by the wall timeout")
	}
}
