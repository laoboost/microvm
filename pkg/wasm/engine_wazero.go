package wasm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// compileModule is the wazero compile seam for latency regression tests.
var compileModule = func(r wazero.Runtime, ctx context.Context, b []byte) (wazero.CompiledModule, error) {
	return r.CompileModule(ctx, b)
}

// errEngineClosed is returned by guest-entry calls once Close has begun; the
// runtime is being torn down and cannot accept new work.
var errEngineClosed = errors.New("engine closed")

type wazeroEngine struct {
	runtime     wazero.Runtime
	compiled    wazero.CompiledModule
	module      api.Module
	moduleBytes []byte
	memoryPages uint32
	netHook     *NetworkHook
	netHost     *wazeroNetHost
	wasiCompat  bool
	// lastLoad is the sub-stage breakdown of the most recent LoadModule, read
	// back by the worker via LastLoadTimings() (LoadTimingReporter).
	lastLoad LoadTimings

	// callMu guards the lifecycle bookkeeping below. wazero's own module/runtime
	// state is not safe to tear down while a guest call is still executing, so
	// Close/StopInstance interrupt in-flight calls and wait for them to return
	// (callWG) before touching the module. callMu is only held for map/list
	// bookkeeping — never across a guest call — so cancelling an in-flight call
	// cannot deadlock on the lock it is waiting to release.
	callMu      sync.Mutex
	callWG      sync.WaitGroup
	callCancels map[uint64]context.CancelFunc
	callSeq     uint64
	closing     bool
}

func newWazeroEngine(ctx context.Context) (*wazeroEngine, error) {
	e := &wazeroEngine{callCancels: make(map[uint64]context.CancelFunc)}
	if err := e.initRuntime(ctx, 0); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *wazeroEngine) initRuntime(ctx context.Context, memoryMB int) error {
	if e.module != nil {
		_ = e.module.Close(ctx)
		e.module = nil
	}
	if e.compiled != nil {
		_ = e.compiled.Close(ctx)
		e.compiled = nil
	}
	if e.runtime != nil {
		_ = e.runtime.Close(ctx)
		e.runtime = nil
	}
	pages := MemoryLimitPages(memoryMB)
	r, err := newBaseRuntime(ctx, pages)
	if err != nil {
		return err
	}
	e.runtime = r
	e.wasiCompat = false
	e.memoryPages = pages
	if len(e.moduleBytes) > 0 {
		compiled, err := compileModule(r, ctx, e.moduleBytes)
		if err != nil {
			return fmt.Errorf("compile module: %w", err)
		}
		e.compiled = compiled
	}
	return nil
}

func wazeroCompileCacheDir() string {
	return strings.TrimSpace(os.Getenv("AEROL_WASM_COMPILE_CACHE_DIR"))
}

// newBaseRuntime builds a wazero runtime at the given memory-limit pages with
// the shared on-disk compilation cache and the base wasip1 host, but no guest
// module compiled. Shared by the single-instance engine (initRuntime) and the
// MultiInstanceEngine (engine_multi.go) so both get identical runtime config —
// notably the same compilation cache, which is what makes a warm compile cheap.
func newBaseRuntime(ctx context.Context, pages uint32) (wazero.Runtime, error) {
	cfg := wazero.NewRuntimeConfig()
	// Enable wazero's context-done termination so a guest invocation can be
	// interrupted (and its module closed from within the call goroutine, where
	// resource teardown is synchronized) instead of leaving Close to close the
	// module under a still-running guest. This is what makes the invocation
	// deadline real for CPU-bound guests and lets Close/StopInstance stop an
	// in-flight guest before tearing the module down.
	//
	// Only ONE-SHOT invocations carry that deadline (see wasm.InvocationContext).
	// The long-lived serve — a guest _start that blocks in its HTTP accept loop
	// for the sandbox's whole lifetime — is invoked with a deadline-free context
	// and is instead bounded by the sandbox lifecycle: StopInstance/Close cancel
	// it through beginCall/stopInFlight below. Do not wrap the serve in the caps
	// wall timeout again; it would kill a healthy server at that budget.
	cfg = cfg.WithCloseOnContextDone(true)
	if pages > 0 {
		cfg = cfg.WithMemoryLimitPages(pages)
	}
	if dir := wazeroCompileCacheDir(); dir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(dir)
		if err != nil {
			return nil, fmt.Errorf("compilation cache: %w", err)
		}
		cfg = cfg.WithCompilationCache(cache)
	}
	r := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		_ = r.Close(ctx)
		return nil, fmt.Errorf("wasi instantiate: %w", err)
	}
	return r, nil
}

func (e *wazeroEngine) ensureWasiCompatHosts(ctx context.Context) error {
	if e.wasiCompat || e.runtime == nil {
		return nil
	}
	if err := e.ensureNetworkHost(ctx); err != nil {
		return err
	}
	if err := e.ensureWasiSocketsHost(ctx); err != nil {
		return err
	}
	if err := e.ensureWasiHTTPHost(ctx); err != nil {
		return err
	}
	e.wasiCompat = true
	return nil
}

func (e *wazeroEngine) ensureMemoryLimit(ctx context.Context, memoryMB int) error {
	want := MemoryLimitPages(memoryMB)
	if want == e.memoryPages {
		return nil
	}
	return e.initRuntime(ctx, memoryMB)
}

func (e *wazeroEngine) LoadModule(ctx context.Context, path string, opts LoadOptions) error {
	if e.module != nil {
		_ = e.module.Close(ctx)
		e.module = nil
	}
	if e.compiled != nil {
		_ = e.compiled.Close(ctx)
		e.compiled = nil
	}
	// Drop stale bytes before initRuntime — it recompiles moduleBytes when
	// rebuilding the runtime (ensureMemoryLimit path), and a failed prior load
	// must not poison the next path.
	e.moduleBytes = nil
	e.lastLoad = LoadTimings{}
	initStart := time.Now()
	if err := e.initRuntime(ctx, opts.MemoryMB); err != nil {
		return err
	}
	e.lastLoad.RuntimeInit = time.Since(initStart)
	readStart := time.Now()
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	e.lastLoad.Read = time.Since(readStart)
	compileStart := time.Now()
	compiled, err := compileModule(e.runtime, ctx, b)
	if err != nil {
		return fmt.Errorf("compile module: %w", err)
	}
	e.lastLoad.Compile = time.Since(compileStart)
	e.moduleBytes = append([]byte(nil), b...)
	e.compiled = compiled
	return nil
}

// LastLoadTimings returns the sub-stage breakdown of the most recent LoadModule
// (LoadTimingReporter). NewEngine is left zero here — it is filled in by the
// worker, which owns the NewEngineFor call that precedes LoadModule.
func (e *wazeroEngine) LastLoadTimings() LoadTimings {
	return e.lastLoad
}

func (e *wazeroEngine) Instantiate(ctx context.Context, caps Capabilities) error {
	if len(e.moduleBytes) == 0 && e.compiled == nil {
		return fmt.Errorf("no compiled module loaded")
	}
	if err := e.ensureMemoryLimit(ctx, caps.MemoryMB); err != nil {
		return err
	}
	if e.compiled == nil {
		return fmt.Errorf("no compiled module loaded")
	}
	if e.module != nil {
		_ = e.module.Close(ctx)
		e.module = nil
	}
	cfg := e.moduleConfig(caps)
	if fsCfg := e.fsConfigForCaps(caps); fsCfg != nil {
		cfg = cfg.WithFSConfig(fsCfg)
	}
	instCtx := e.withNetworkContext(ctx, caps)
	if err := e.ensureWasiCompatHosts(instCtx); err != nil {
		return err
	}
	mod, err := e.runtime.InstantiateModule(instCtx, e.compiled, cfg)
	if err != nil {
		return fmt.Errorf("instantiate module: %w", err)
	}
	e.module = mod
	return nil
}

func (e *wazeroEngine) StopInstance(ctx context.Context) error {
	// Interrupt and await any in-flight guest call before closing the module:
	// wazero closes the module from inside the call goroutine when the call's
	// context is canceled, so this waits for a synchronized teardown rather than
	// racing one.
	e.stopInFlight()
	if e.module == nil {
		return nil
	}
	err := e.module.Close(ctx)
	e.module = nil
	return err
}

func (e *wazeroEngine) InvokeExport(ctx context.Context, name string) error {
	// No deadline is applied here: the caller decides. The worker passes the
	// caps wall timeout for a one-shot invoke and a deadline-free context for
	// the long-lived serve (wasm.InvocationContext). Either way the call is
	// registered, so StopInstance/Close can interrupt it.
	callCtx, release, err := e.beginCall(ctx)
	if err != nil {
		return err
	}
	defer release()
	if e.module == nil {
		return fmt.Errorf("no active instance")
	}
	fn := e.module.ExportedFunction(name)
	if fn == nil {
		return fmt.Errorf("export %q not found", name)
	}
	_, err = fn.Call(callCtx)
	return err
}

func (e *wazeroEngine) moduleConfig(caps Capabilities) wazero.ModuleConfig {
	return moduleConfigFor(caps)
}

// moduleConfigFor builds the wazero ModuleConfig for a set of capabilities. It
// is a pure function of caps (no engine state), so both the single-instance
// engine and the MultiInstanceEngine share it — the latter additionally sets a
// per-instance WithName so many instances can coexist on one runtime.
func moduleConfigFor(caps Capabilities) wazero.ModuleConfig {
	cfg := wazero.NewModuleConfig().WithArgs(caps.Args...)
	// Driver/worker invoke _start explicitly (background on create; after listen for HTTP).
	cfg = cfg.WithSysWalltime().WithSysNanotime().WithStartFunctions()
	for k, v := range caps.Env {
		cfg = cfg.WithEnv(k, v)
	}
	// Tell HTTP guests which fd the wasip1 listener landed on. wazero appends the
	// listener after dir preopens, so it is not always fd 3 once /work is mounted.
	if caps.ListenEnabled() {
		cfg = cfg.WithEnv(ListenFDEnv, strconv.Itoa(ListenerFD(caps)))
	}
	return cfg
}

// ListenFDEnv carries the wasip1 listener fd to AerolVM-aware guests. Guests that
// hardcode fd 3 (the bare-wasip1 convention) keep working when no dir preopens are
// configured; guests that also need /work read this env var to find the listener.
const ListenFDEnv = "AEROL_WASM_LISTEN_FD"

// ListenerFD returns the fd wazero assigns the single wasip1 TCP listener:
// stdio (0–2), then len(preopens) dir fds, then the listener (InitFSContext order).
func ListenerFD(caps Capabilities) int {
	return int(fdPreopen) + len(caps.Preopens)
}

func (e *wazeroEngine) Run(ctx context.Context, caps Capabilities, export string) (RunResult, error) {
	if export == "" {
		export = "_start"
	}
	callCtx, release, err := e.beginCall(ctx)
	if err != nil {
		return RunResult{}, err
	}
	defer release()
	// Run is the one-shot path (MsgExec): it always carries the wall timeout.
	// The long-lived serve goes through InvokeExport with a caller-supplied
	// deadline-free context instead.
	invokeCtx, cancel := WithInvocationDeadline(callCtx, caps)
	defer cancel()
	start := time.Now()

	var stdout, stderr bytes.Buffer
	if err := e.instantiateWithIO(invokeCtx, caps, &stdout, &stderr); err != nil {
		return RunResult{}, err
	}
	exitCode, err := e.callExport(invokeCtx, export)
	result := RunResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Usage:    UsageStats{WallDurationMs: time.Since(start).Milliseconds()},
	}
	if err != nil {
		var exitErr *sys.ExitError
		if errors.As(err, &exitErr) {
			return result, nil
		}
		result.Stderr = stringsTrimJoin(result.Stderr, err.Error())
		return result, err
	}
	return result, nil
}

func (e *wazeroEngine) instantiateWithIO(ctx context.Context, caps Capabilities, stdout, stderr *bytes.Buffer) error {
	if err := e.ensureMemoryLimit(ctx, caps.MemoryMB); err != nil {
		return err
	}
	if e.compiled == nil {
		return fmt.Errorf("no compiled module loaded")
	}
	if e.module != nil {
		_ = e.module.Close(ctx)
		e.module = nil
	}
	cfg := e.moduleConfig(caps)
	if stdout != nil {
		cfg = cfg.WithStdout(stdout)
	}
	if stderr != nil {
		cfg = cfg.WithStderr(stderr)
	}
	if fsCfg := e.fsConfigForCaps(caps); fsCfg != nil {
		cfg = cfg.WithFSConfig(fsCfg)
	}
	instCtx := e.withNetworkContext(ctx, caps)
	if err := e.ensureWasiCompatHosts(instCtx); err != nil {
		return err
	}
	mod, err := e.runtime.InstantiateModule(instCtx, e.compiled, cfg)
	if err != nil {
		return fmt.Errorf("instantiate module: %w", err)
	}
	e.module = mod
	return nil
}

// fsConfigForCaps builds wazero FS mounts. Directory preopens are mounted even
// when a wasip1 listener is enabled, so an HTTP guest can also read /work. wazero
// assigns dir fds before TCP listeners (InitFSContext), so the listener lands at
// fd (3 + len(preopens)) rather than fd 3 — guests learn the right fd from the
// AEROL_WASM_LISTEN_FD env var injected by moduleConfig (see listenerFD).
func (e *wazeroEngine) fsConfigForCaps(caps Capabilities) wazero.FSConfig {
	return fsConfigFor(caps)
}

// fsConfigFor builds directory preopens for a set of capabilities. Pure function
// of caps (no engine state); shared with the MultiInstanceEngine.
func fsConfigFor(caps Capabilities) wazero.FSConfig {
	preopens := caps.Preopens
	if len(preopens) == 0 {
		return nil
	}
	fsCfg := wazero.NewFSConfig()
	for _, p := range preopens {
		guest := p.GuestPath
		if guest == "" {
			guest = "/work"
		}
		fsCfg = fsCfg.WithDirMount(p.HostPath, guest)
	}
	return fsCfg
}

func (e *wazeroEngine) callExport(ctx context.Context, name string) (int, error) {
	if e.module == nil {
		return 1, fmt.Errorf("no active instance")
	}
	fn := e.module.ExportedFunction(name)
	if fn == nil {
		return 1, fmt.Errorf("export %q not found", name)
	}
	_, err := fn.Call(ctx)
	return exitCodeFromInvoke(err), err
}

func exitCodeFromInvoke(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *sys.ExitError
	if errors.As(err, &exitErr) {
		return int(exitErr.ExitCode())
	}
	return 1
}

func stringsTrimJoin(a, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "\n" + b
	}
}

func (e *wazeroEngine) CaptureSnapshot(_ context.Context) (SnapshotCapture, error) {
	if e.module == nil {
		return SnapshotCapture{}, fmt.Errorf("no active instance")
	}
	mem := e.module.Memory()
	if mem == nil || reflect.ValueOf(mem).IsNil() {
		return SnapshotCapture{}, fmt.Errorf("module has no linear memory")
	}
	data, ok := mem.Read(0, mem.Size())
	if !ok {
		return SnapshotCapture{}, fmt.Errorf("read linear memory failed")
	}
	out := append([]byte(nil), data...)
	return SnapshotCapture{
		Memory:    out,
		Globals:   []byte("[]"),
		WASIState: []byte("{}"),
	}, nil
}

func (e *wazeroEngine) RestoreSnapshot(ctx context.Context, snap SnapshotRestoreInput, caps Capabilities) error {
	if err := e.Instantiate(ctx, caps); err != nil {
		return err
	}
	if len(snap.Memory) == 0 {
		return nil
	}
	mem := e.module.Memory()
	if mem == nil {
		return fmt.Errorf("module has no memory export")
	}
	if !mem.Write(0, snap.Memory) {
		return fmt.Errorf("restore linear memory failed (guest size %d, snapshot %d)", mem.Size(), len(snap.Memory))
	}
	return nil
}

func (e *wazeroEngine) ResolvedListenPort() (int, bool) {
	return ResolvedListenPort(e.module)
}

func (e *wazeroEngine) SupportsListen() bool { return true }

// beginCall registers a guest invocation so Close/StopInstance can interrupt and
// await it. It returns a context wazero watches for termination and a release
// func the caller must invoke when the call returns. Once Close has begun it
// fails fast instead of racing teardown.
func (e *wazeroEngine) beginCall(ctx context.Context) (context.Context, func(), error) {
	e.callMu.Lock()
	if e.closing {
		e.callMu.Unlock()
		return nil, nil, errEngineClosed
	}
	callCtx, cancel := context.WithCancel(ctx)
	e.callSeq++
	id := e.callSeq
	e.callCancels[id] = cancel
	e.callWG.Add(1)
	e.callMu.Unlock()

	release := func() {
		cancel()
		e.callMu.Lock()
		delete(e.callCancels, id)
		e.callMu.Unlock()
		e.callWG.Done()
	}
	return callCtx, release, nil
}

// stopInFlight cancels every in-flight guest invocation and blocks until each
// has returned. Cancellation makes wazero unwind the call and close the module
// from within its own goroutine, so the wait is bounded and cannot deadlock:
// callMu is never held across a guest call, and no host function the guest may
// be blocked in requires callMu.
func (e *wazeroEngine) stopInFlight() {
	e.callMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(e.callCancels))
	for _, cancel := range e.callCancels {
		cancels = append(cancels, cancel)
	}
	e.callMu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	e.callWG.Wait()
}

func (e *wazeroEngine) Close(ctx context.Context) error {
	// Refuse new calls, then stop and observe-complete any in-flight guest before
	// closing the module/runtime — closing wazero state under a running guest
	// races its descriptor table (the pre-fix data race).
	e.callMu.Lock()
	e.closing = true
	e.callMu.Unlock()
	e.stopInFlight()

	if e.module != nil {
		_ = e.module.Close(ctx)
		e.module = nil
	}
	if e.compiled != nil {
		_ = e.compiled.Close(ctx)
		e.compiled = nil
	}
	if e.runtime != nil {
		return e.runtime.Close(ctx)
	}
	return nil
}
