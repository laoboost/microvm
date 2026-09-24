package wasm

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// TestResizeConcurrentWithStopHasNoRace pins F2c: Resize read inst.status /
// inst.socketPath and wrote inst.memoryMB / inst.cpu after releasing d.mu, while
// Stop writes inst.status under d.mu — a plain Stop+Resize pair raced. Run with
// -race (the whole package must be race-clean).
func TestResizeConcurrentWithStopHasNoRace(t *testing.T) {
	d := New(Config{ModulesDir: t.TempDir()}, nil)
	d.SetWorkerClientFactory(func(string) WorkerClient { return &recordingWorkerClient{} })
	ctx := context.Background()

	d.mu.Lock()
	d.byID["sb-race"] = &sandboxInstance{
		sandboxID:  "sb-race",
		socketPath: filepath.Join(t.TempDir(), "worker.sock"),
		status:     models.SandboxStatusStarted,
		cpu:        1,
		memoryMB:   128,
	}
	d.mu.Unlock()

	for i := 0; i < 50; i++ {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = d.Resize(ctx, "sb-race", models.ResizeSandboxRequest{CPU: 2, MemoryMB: 256})
		}()
		go func() {
			defer wg.Done()
			_ = d.Stop(ctx, "sb-race")
		}()
		wg.Wait()
	}
}
