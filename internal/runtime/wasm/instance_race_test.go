package wasm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// sandboxInstance is owned by d.mu once published: its mutable fields (status,
// socketPath, resolvedListenPort) were read and written with no lock after the
// map lookup, so concurrent Start/Stop/Inspect/guestHTTPProxy raced on them.
func TestInstanceMutableFieldsDoNotRace(t *testing.T) {
	d := New(Config{ModulesDir: t.TempDir(), RunDir: t.TempDir(), DefaultMemoryMB: 64}, nil)
	d.SetModuleResolver(fakeResolver{path: filepath.Join(t.TempDir(), "m.wasm"), digest: "sha256:deadbeef"})
	d.SetWorkerSupervisor(&fakeSupervisor{})
	d.SetWorkerClientFactory(func(string) WorkerClient { return &recordingWorkerClient{} })

	workDir := d.sandboxDir("sb-race")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.byID["sb-race"] = &sandboxInstance{
		sandboxID:   "sb-race",
		modulePath:  filepath.Join(t.TempDir(), "m.wasm"),
		socketPath:  filepath.Join(workDir, "worker.sock"),
		workDir:     workDir,
		workerKey:   "sb-race",
		status:      models.SandboxStatusStarted,
		entryExport: "_start",
		memoryMB:    64,
	}
	d.mu.Unlock()

	const iters = 300
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if _, err := d.Start(context.Background(), "sb-race"); err != nil {
				t.Errorf("Start: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if err := d.Stop(context.Background(), "sb-race"); err != nil {
				t.Errorf("Stop: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if _, err := d.Inspect(context.Background(), "sb-race"); err != nil {
				t.Errorf("Inspect: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			_ = d.guestHTTPProxy("sb-race", 0, rec, req)
		}
	}()
	wg.Wait()
}

// resolvedListenPort is written by the guest-listen sync path and read by
// guestHTTPProxy; those must not race either.
func TestResolvedListenPortDoesNotRaceProxy(t *testing.T) {
	d := New(Config{ModulesDir: t.TempDir(), RunDir: t.TempDir(), DefaultMemoryMB: 64}, nil)
	d.SetWorkerSupervisor(&fakeSupervisor{})
	d.SetWorkerClientFactory(func(string) WorkerClient { return &recordingWorkerClient{resolvedPort: 8080} })
	d.waitListenReady = func(string, int) error { return nil }

	workDir := d.sandboxDir("sb-port")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.byID["sb-port"] = &sandboxInstance{
		sandboxID:   "sb-port",
		socketPath:  filepath.Join(workDir, "worker.sock"),
		workDir:     workDir,
		workerKey:   "sb-port",
		status:      models.SandboxStatusStarted,
		entryExport: "_start",
		memoryMB:    64,
	}
	d.mu.Unlock()

	const iters = 300
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if err := d.SyncGuestListenPorts(context.Background(), "sb-port", []int{0}); err != nil {
				t.Errorf("SyncGuestListenPorts: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			_ = d.guestHTTPProxy("sb-port", 0, rec, req)
		}
	}()
	wg.Wait()
}
