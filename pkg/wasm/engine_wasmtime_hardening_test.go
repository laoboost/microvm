//go:build wasmtime && linux

package wasm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	wasmtime "github.com/bytecodealliance/wasmtime-go/v37"
)

func wasmtimeWat2Wasm(wat string) ([]byte, error) {
	return wasmtime.Wat2Wasm(wat)
}

// wasmtimeHardeningEnvArgGuest reports whether the guest sees host env vars and
// argv: it writes "E<n>A<m>" (n/m collapsed to 0 or 1) to stdout.
const wasmtimeHardeningEnvArgGuest = `(module
  (import "wasi_snapshot_preview1" "environ_sizes_get" (func $esg (param i32 i32) (result i32)))
  (import "wasi_snapshot_preview1" "args_sizes_get" (func $asg (param i32 i32) (result i32)))
  (import "wasi_snapshot_preview1" "fd_write" (func $fdw (param i32 i32 i32 i32) (result i32)))
  (memory (export "memory") 1)
  (func $_start
    (drop (call $esg (i32.const 0) (i32.const 4)))
    (drop (call $asg (i32.const 8) (i32.const 12)))
    (i32.store8 (i32.const 100) (i32.const 69))
    (i32.store8 (i32.const 101)
      (select (i32.const 49) (i32.const 48)
        (i32.gt_u (i32.load (i32.const 0)) (i32.const 0))))
    (i32.store8 (i32.const 102) (i32.const 65))
    (i32.store8 (i32.const 103)
      (select (i32.const 49) (i32.const 48)
        (i32.gt_u (i32.load (i32.const 8)) (i32.const 0))))
    (i32.store (i32.const 16) (i32.const 100))
    (i32.store (i32.const 20) (i32.const 4))
    (drop (call $fdw (i32.const 1) (i32.const 16) (i32.const 1) (i32.const 24))))
  (export "_start" (func $_start)))
`

// wasmtimeHardeningHelloGuest writes "hello" to stdout and exports memory.
const wasmtimeHardeningHelloGuest = `(module
  (import "wasi_snapshot_preview1" "fd_write" (func $fdw (param i32 i32 i32 i32) (result i32)))
  (memory (export "memory") 1)
  (data (i32.const 100) "hello")
  (func $_start
    (i32.store (i32.const 16) (i32.const 100))
    (i32.store (i32.const 20) (i32.const 5))
    (drop (call $fdw (i32.const 1) (i32.const 16) (i32.const 1) (i32.const 24))))
  (export "_start" (func $_start)))
`

func loadWasmtimeHardeningGuest(t *testing.T, wat string) *wasmtimeEngine {
	t.Helper()
	bytes, err := wasmtimeWat2Wasm(wat)
	if err != nil {
		t.Fatalf("wat2wasm: %v", err)
	}
	path := filepath.Join(t.TempDir(), "guest.wasm")
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		t.Fatalf("write guest: %v", err)
	}
	eng, err := newWasmtimeEngine(context.Background())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })
	e := eng.(*wasmtimeEngine)
	if err := e.LoadModule(context.Background(), path, LoadOptions{}); err != nil {
		t.Fatalf("load module: %v", err)
	}
	return e
}

// swapHostFD replaces the host file descriptor with the write end of a capture
// file for the duration of fn, returning whatever was written to it. This is
// how we observe whether the engine inherited (dup'd) the host stream: an
// InheritStdout at build time dups the swapped-in fd, so guest output lands in
// the capture file; a /dev/null sink leaves it empty.
func swapHostFD(t *testing.T, fd int, fn func()) []byte {
	t.Helper()
	saved, err := syscall.Dup(fd)
	if err != nil {
		t.Fatalf("dup: %v", err)
	}
	defer syscall.Close(saved)
	capture, err := os.CreateTemp(t.TempDir(), "fd-capture-*")
	if err != nil {
		t.Fatalf("capture file: %v", err)
	}
	capturePath := capture.Name()
	capture.Close()
	defer os.Remove(capturePath)
	target, err := os.OpenFile(capturePath, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open capture: %v", err)
	}
	if err := syscall.Dup3(int(target.Fd()), fd, 0); err != nil {
		target.Close()
		t.Fatalf("dup3: %v", err)
	}
	defer func() {
		_ = syscall.Dup3(saved, fd, 0)
		_ = target.Close()
	}()
	fn()
	out, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	return out
}

func TestWasmtimeDoesNotInheritHostEnvironmentVariables(t *testing.T) {
	e := loadWasmtimeHardeningGuest(t, wasmtimeHardeningEnvArgGuest)
	res, err := e.Run(context.Background(), Capabilities{MemoryMB: 64}, "_start")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasPrefix(res.Stdout, "E0") {
		t.Fatalf("guest saw host environment variables (stdout=%q)", res.Stdout)
	}
}

func TestWasmtimeDoesNotInheritHostArgv(t *testing.T) {
	e := loadWasmtimeHardeningGuest(t, wasmtimeHardeningEnvArgGuest)
	res, err := e.Run(context.Background(), Capabilities{MemoryMB: 64}, "_start")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasSuffix(res.Stdout, "A0") {
		t.Fatalf("guest saw host argv (stdout=%q)", res.Stdout)
	}
}

func TestWasmtimeDoesNotInheritHostStdoutWhenNoOutputFileConfigured(t *testing.T) {
	e := loadWasmtimeHardeningGuest(t, wasmtimeHardeningHelloGuest)
	captured := swapHostFD(t, 1, func() {
		if err := e.buildInstance(context.Background(), Capabilities{MemoryMB: 64}, "", ""); err != nil {
			t.Errorf("buildInstance: %v", err)
			return
		}
		if err := e.InvokeExport(context.Background(), "_start"); err != nil {
			t.Errorf("invoke: %v", err)
		}
	})
	if len(captured) != 0 {
		t.Fatalf("guest output leaked to host stdout: %q", captured)
	}
}

func TestWasmtimeCreatesOutputTempFilesWithExclusiveCreation(t *testing.T) {
	e := loadWasmtimeHardeningGuest(t, wasmtimeHardeningHelloGuest)
	res, err := e.Run(context.Background(), Capabilities{MemoryMB: 64}, "_start")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Stdout != "hello" {
		t.Fatalf("stdout through temp file: %q", res.Stdout)
	}
	leftover, err := filepath.Glob(filepath.Join(os.TempDir(), "aerol-wasm-std*-*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(leftover) != 0 {
		t.Fatalf("temp output files leaked: %v", leftover)
	}

	// Exclusive creation: the helper must use O_EXCL semantics (os.CreateTemp),
	// produce the aerol-wasm- prefix, and 0600 permissions.
	path, err := createWasmtimeOutputTemp(os.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("createWasmtimeOutputTemp: %v", err)
	}
	defer os.Remove(path)
	if base := filepath.Base(path); !strings.HasPrefix(base, "aerol-wasm-stdout-") {
		t.Fatalf("unexpected temp name %q", base)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("temp file mode %v, want 0600", info.Mode().Perm())
	}
}

func TestWasmtimeRejectsNonPositiveMemoryLimit(t *testing.T) {
	e := loadWasmtimeHardeningGuest(t, wasmtimeHardeningHelloGuest)
	for _, mb := range []int{0, -1} {
		if err := e.Instantiate(context.Background(), Capabilities{MemoryMB: mb}); err == nil {
			t.Fatalf("Instantiate(MemoryMB=%d): expected error", mb)
		} else if _, ok := err.(*MemoryLimitError); !ok {
			t.Fatalf("Instantiate(MemoryMB=%d): error %v is not *MemoryLimitError", mb, err)
		}
		if _, err := e.Run(context.Background(), Capabilities{MemoryMB: mb}, "_start"); err == nil {
			t.Fatalf("Run(MemoryMB=%d): expected error", mb)
		}
	}
}

func TestWasmtimeInstantiatesWithExplicitMemoryLimitAndEnvAllowlist(t *testing.T) {
	e := loadWasmtimeHardeningGuest(t, wasmtimeHardeningEnvArgGuest)
	caps := Capabilities{
		MemoryMB: 64,
		Env:      map[string]string{"ALLOWED": "yes"},
	}
	res, err := e.Run(context.Background(), caps, "_start")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Stdout != "E1A0" {
		t.Fatalf("allowlisted env not visible to guest (stdout=%q)", res.Stdout)
	}
}

func TestWasmtimeInstantiatesWithoutOutputFilesAndInheritsNoHostStreams(t *testing.T) {
	e := loadWasmtimeHardeningGuest(t, wasmtimeHardeningHelloGuest)

	instantiate := func() {
		if err := e.Instantiate(context.Background(), Capabilities{MemoryMB: 64}); err != nil {
			t.Errorf("instantiate: %v", err)
		}
	}
	if got := swapHostFD(t, 1, func() { instantiate(); _ = e.InvokeExport(context.Background(), "_start") }); len(got) != 0 {
		t.Fatalf("Instantiate: guest output leaked to host stdout: %q", got)
	}

	// RestoreSnapshot rebuilds the instance through the same empty-path path.
	if err := e.Instantiate(context.Background(), Capabilities{MemoryMB: 64}); err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	snap, err := e.CaptureSnapshot(context.Background())
	if err != nil {
		t.Fatalf("capture snapshot: %v", err)
	}
	restore := func() {
		if err := e.RestoreSnapshot(context.Background(), SnapshotRestoreInput{Memory: snap.Memory}, Capabilities{MemoryMB: 64}); err != nil {
			t.Errorf("restore: %v", err)
		}
	}
	if got := swapHostFD(t, 1, func() { restore(); _ = e.InvokeExport(context.Background(), "_start") }); len(got) != 0 {
		t.Fatalf("RestoreSnapshot: guest output leaked to host stdout: %q", got)
	}
	if got := swapHostFD(t, 2, func() { restore(); _ = e.InvokeExport(context.Background(), "_start") }); len(got) != 0 {
		t.Fatalf("RestoreSnapshot: guest output leaked to host stderr: %q", got)
	}
}
