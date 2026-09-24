package wasm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

const wasip1HTTPWasmName = "wasip1-http.wasm"

func wasip1HTTPSourceDir() (string, error) {
	base, err := filepath.Abs(filepath.Join("testdata", "aerolhttp"))
	if err != nil {
		return "", fmt.Errorf("resolve aerolhttp guest source: %w", err)
	}
	if st, err := os.Stat(base); err != nil || !st.IsDir() {
		return "", fmt.Errorf("aerolhttp wasip1 guest source missing at %s", base)
	}
	return base, nil
}

func ensureWasip1HTTPWasm(t *testing.T) string {
	t.Helper()
	// Absolute output: the build runs with cmd.Dir = the guest source dir, so a
	// relative -o would land next to the source instead of in pkg/wasm/testdata.
	out, err := filepath.Abs(filepath.Join("testdata", wasip1HTTPWasmName))
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		return out
	}
	if runtime.GOOS == "windows" {
		t.Skip("wasip1-http.wasm compile skipped on windows")
	}
	src, err := wasip1HTTPSourceDir()
	if err != nil {
		t.Skip(err)
	}
	// A missing toolchain is an environment limitation, not a defect under
	// test — same class as Windows above. Checked before the build so the
	// failure mode is a clear skip rather than a confusing exec error.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain unavailable; cannot compile wasip1-http.wasm")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, ".")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	cmd.Dir = src
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile wasip1-http.wasm: %v\n%s", err, combined)
	}
	return out
}

func TestTinygoHTTPModuleRequest(t *testing.T) {
	modPath := ensureWasip1HTTPWasm(t)
	ctx := context.Background()
	e, err := newWazeroEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close(ctx) }()
	if err := e.LoadModule(ctx, modPath, LoadOptions{}); err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	caps := Capabilities{
		WASIListenPort: 0,
		WASIListenHost: "127.0.0.1",
		Args:           []string{"wasi", "http"},
	}
	if err := e.Instantiate(ctx, caps); err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	port, ok := ResolvedListenPort(e.module)
	if !ok || port == 0 {
		t.Fatalf("resolved listen port = %d ok=%v", port, ok)
	}

	go func() { _ = e.InvokeExport(ctx, "_start") }()
	time.Sleep(200 * time.Millisecond)

	resp, err := http.Post(
		"http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		"text/plain",
		bytes.NewReader([]byte("wazero")),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "wazero\n" {
		t.Fatalf("body=%q", body)
	}
}

// Close must interrupt an in-flight guest call and observe it exit before tearing
// the module down. Closing wazero state under a still-running guest races its
// descriptor table (TestTinygoHTTPModuleRequest caught that under -race); this
// pins the contract directly by requiring Close to return instead of hanging
// behind the guest's accept loop.
func TestCloseInterruptsInFlightGuest(t *testing.T) {
	modPath := ensureWasip1HTTPWasm(t)
	ctx := context.Background()
	e, err := newWazeroEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.LoadModule(ctx, modPath, LoadOptions{}); err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	caps := Capabilities{
		WASIListenPort: 0,
		WASIListenHost: "127.0.0.1",
		Args:           []string{"wasi", "http"},
	}
	if err := e.Instantiate(ctx, caps); err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	port, ok := ResolvedListenPort(e.module)
	if !ok || port == 0 {
		t.Fatalf("resolved listen port = %d ok=%v", port, ok)
	}

	// _start serves forever; leave it blocked in the accept loop, then close.
	go func() { _ = e.InvokeExport(ctx, "_start") }()
	resp, err := http.Post(
		"http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		"text/plain",
		bytes.NewReader([]byte("wazero")),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()
	time.Sleep(100 * time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- e.Close(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Close blocked behind an in-flight guest; the guest was not interrupted")
	}
}

// StopInstance must interrupt the running serve just as Close does. The serve is
// invoked with a deadline-free context (it is the server, not a request), so
// nothing about the guest's own context will ever expire: the stop path is the
// only thing that can end it, via the in-flight call registry (stopInFlight).
func TestStopInstanceInterruptsInFlightGuest(t *testing.T) {
	modPath := ensureWasip1HTTPWasm(t)
	ctx := context.Background()
	e, err := newWazeroEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close(ctx) }()
	if err := e.LoadModule(ctx, modPath, LoadOptions{}); err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	caps := Capabilities{
		WASIListenPort: 0,
		WASIListenHost: "127.0.0.1",
		Args:           []string{"wasi", "http"},
	}
	if err := e.Instantiate(ctx, caps); err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	port, ok := ResolvedListenPort(e.module)
	if !ok || port == 0 {
		t.Fatalf("resolved listen port = %d ok=%v", port, ok)
	}

	serveCtx, cancelServe := context.WithCancel(ctx)
	defer cancelServe()
	serveDone := make(chan error, 1)
	go func() { serveDone <- e.InvokeExport(serveCtx, "_start") }()
	resp, err := http.Post(
		"http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		"text/plain",
		bytes.NewReader([]byte("wazero")),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()
	time.Sleep(100 * time.Millisecond)

	stopped := make(chan error, 1)
	go func() { stopped <- e.StopInstance(ctx) }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("StopInstance: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("StopInstance blocked behind the serving guest; the serve was not interrupted")
	}
	select {
	case err := <-serveDone:
		if err == nil {
			t.Fatal("serve returned nil after StopInstance; expected the interrupted call to report an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve goroutine still running after StopInstance")
	}
}
