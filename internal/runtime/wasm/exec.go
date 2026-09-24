package wasm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aerol-ai/microvm/pkg/models"
	wasmengine "github.com/aerol-ai/microvm/pkg/wasm"
)

type sandboxExecutor struct {
	driver *Driver
	id     string
}

func (e sandboxExecutor) Exec(r *http.Request, req models.ExecRequest) (models.ExecResult, error) {
	return e.driver.execSandbox(r.Context(), e.id, req)
}

func (d *Driver) execSandbox(ctx context.Context, sandboxID string, req models.ExecRequest) (models.ExecResult, error) {
	inst, snap, err := d.snapshotInstance(sandboxID)
	if err != nil {
		return models.ExecResult{}, err
	}

	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := wasmExecArgs(req.Command, inst.baseArgs)
	env := mergeEnv(inst.baseEnv, req.Env)
	wallTimeout := time.Duration(req.TimeoutSeconds) * time.Second
	if wallTimeout <= 0 {
		wallTimeout = d.cfg.DefaultWallTimeout
	}
	caps := wasmengine.CapsFromResourceLimits(wasmengine.Capabilities{
		Env:  env,
		Args: args,
		Preopens: []wasmengine.Preopen{{
			GuestPath: "/work",
			HostPath:  inst.workDir,
		}},
	}, snap.memoryMB, wallTimeout)

	client := d.newWorkerClient(snap.socketPath)
	start := time.Now()
	run, err := awaitWorkerExec(ctx, client, sandboxID, caps, inst.entryExport)
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		// ctx won the race (request cancelled or TimeoutSeconds elapsed):
		// return promptly with the context error. The WithTimeout above used
		// to be dead — client.Exec took no ctx, so neither could stop an
		// in-flight exec. The abandoned worker call drains in the background
		// (worker IPC has no cancellation without killing the worker).
		return models.ExecResult{ExitCode: 1, Stderr: err.Error()}, fmt.Errorf("wasm exec: %w", err)
	}
	durationMS := run.Usage.WallDurationMs
	if durationMS <= 0 {
		durationMS = time.Since(start).Milliseconds()
	}
	result := models.ExecResult{
		Stdout:     run.Stdout,
		Stderr:     run.Stderr,
		ExitCode:   run.ExitCode,
		DurationMS: durationMS,
	}
	if err != nil && result.Stderr == "" {
		result.Stderr = err.Error()
	}
	if err != nil && result.ExitCode == 0 {
		result.ExitCode = 1
	}
	recordWasmUsage(sandboxID, run.Usage)
	return result, nil
}

// awaitWorkerExec runs the worker exec but returns as soon as ctx is done,
// abandoning the in-flight call (it drains in the background — worker IPC is
// not interruptible without killing the worker process). A completed result
// wins over cancellation when both are ready.
func awaitWorkerExec(ctx context.Context, client WorkerClient, sandboxID string, caps wasmengine.Capabilities, export string) (wasmengine.RunResult, error) {
	type outcome struct {
		run wasmengine.RunResult
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		run, err := client.Exec(ctx, sandboxID, caps, export)
		ch <- outcome{run, err}
	}()
	select {
	case out := <-ch:
		return out.run, out.err
	case <-ctx.Done():
		select {
		case out := <-ch:
			return out.run, out.err
		default:
			return wasmengine.RunResult{}, ctx.Err()
		}
	}
}

func wasmExecArgs(command string, fallback []string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return append([]string(nil), fallback...)
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return append([]string(nil), fallback...)
	}
	return fields
}

func mergeEnv(base, extra map[string]string) map[string]string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
