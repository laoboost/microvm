package wasm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/models"
	wasmengine "github.com/aerol-ai/microvm/pkg/wasm"
)

// blockingExecClient models a worker RPC that cannot be interrupted: its Exec
// only returns when released, ignoring any context (pkg/wasm/worker's Exec has
// no ctx of its own).
type blockingExecClient struct {
	recordingWorkerClient
	release chan struct{}
}

func (c *blockingExecClient) Exec(context.Context, string, wasmengine.Capabilities, string) (wasmengine.RunResult, error) {
	<-c.release
	return wasmengine.RunResult{}, nil
}

// Regression: execSandbox built a context.WithTimeout around a client.Exec
// that never took a ctx — the timeout and request cancellation were dead and
// an in-flight wasm exec could not be stopped. execSandbox must return
// promptly with a context error once ctx is done.
func TestExecSandboxHonorsContextCancel(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	d := New(Config{ModulesDir: t.TempDir(), RunDir: t.TempDir(), DefaultMemoryMB: 64}, nil)
	d.SetWorkerSupervisor(&fakeSupervisor{})
	d.SetWorkerClientFactory(func(string) WorkerClient { return &blockingExecClient{release: release} })

	workDir := d.sandboxDir("sb-ctx")
	d.mu.Lock()
	d.byID["sb-ctx"] = &sandboxInstance{
		sandboxID:   "sb-ctx",
		socketPath:  "test.sock",
		workDir:     workDir,
		workerKey:   "sb-ctx",
		status:      models.SandboxStatusStarted,
		entryExport: "_start",
		memoryMB:    64,
	}
	d.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		res models.ExecResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := d.execSandbox(ctx, "sb-ctx", models.ExecRequest{Command: "echo hi", TimeoutSeconds: 60})
		done <- outcome{res, err}
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case out := <-done:
		if out.err == nil || !errors.Is(out.err, context.Canceled) {
			t.Fatalf("execSandbox err = %v, want a context.Canceled error", out.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("execSandbox ignored ctx cancellation (returned only after the worker call finished)")
	}
}
