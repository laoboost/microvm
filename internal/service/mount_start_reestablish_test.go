package service

// Tests pinning the start-after-stop re-establish contract from
// docs/external-storage.md ("On Start it's re-established"): after ANY stop —
// the API/lifecycle path (stopSandboxInternal) or the involuntary
// die/stop/oom event path — StartSandbox re-establishes ALL mounts fresh
// (new mount processes, arguments re-read from the stored specs, ready-wait)
// BEFORE the runtime container starts. The daemon-restart reconciler and WASM
// rehydrate re-establish paths are pinned too: both trust tracked state for
// RUNNING sandboxes and must keep doing so.
//
// Harness: reuses the task-001 mount teardown fixture (real mounts.Manager +
// fake `mount` binary on PATH via the nfs kernel-mount adapter). An argv-log
// variant of the fake binary proves fresh processes and re-read arguments.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
	"github.com/aerol-ai/microvm/pkg/mounts"
	"github.com/aerol-ai/microvm/pkg/wasm"
)

// installMountBinFromScript installs a `mount` executable running the given
// shell script at the front of PATH.
func installMountBinFromScript(t *testing.T, script string) {
	t.Helper()
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "mount"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake mount: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// installRecordingMountBin installs a fake kernel `mount` that appends one
// space-joined argv line per invocation to logPath, then exits 0.
func installRecordingMountBin(t *testing.T, logPath string) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nexit 0\n", logPath)
	installMountBinFromScript(t, script)
}

func appendOrderLine(logPath, line string) error {
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, line)
	return err
}

// startOrderRuntime records runtime Start calls into the same order log the
// fake mount binary appends to, so test 4 can assert mount-before-start.
type startOrderRuntime struct {
	recordingRuntime
	orderLog string
}

func (r *startOrderRuntime) Start(ctx context.Context, ref string) (*models.SandboxRuntimeState, error) {
	if err := appendOrderLine(r.orderLog, "start"); err != nil {
		// Cannot use t.Fatalf inside the runtime; surface via panic-free error.
		return nil, fmt.Errorf("record start order: %w", err)
	}
	return r.recordingRuntime.Start(ctx, ref)
}

// it re-establishes fresh mounts on start after an API stop
func TestStartReestablishesFreshMountsAfterAPIStop(t *testing.T) {
	ctx := context.Background()
	const id = "sb-start-after-api-stop"
	f := newMountTeardownFixture(t, id)
	f.establishMounts(t, id)

	if _, err := f.svc.stopSandboxInternal(ctx, id, stopModeManual); err != nil {
		t.Fatalf("stopSandboxInternal: %v", err)
	}
	if binds := f.svc.mounts.HostBindsFor(id); len(binds) != 0 {
		t.Fatalf("precondition: tracked state survived API stop: %#v", binds)
	}

	if _, err := f.svc.StartSandbox(ctx, id); err != nil {
		t.Fatalf("StartSandbox: %v", err)
	}
	binds := f.svc.mounts.HostBindsFor(id)
	if len(binds) != 1 {
		t.Fatalf("host binds after start = %#v, want exactly 1 fresh mount", binds)
	}
	if binds[0].ContainerPath != "/data" {
		t.Fatalf("bind container path = %q, want /data", binds[0].ContainerPath)
	}
	rt := f.svc.docker.(*recordingRuntime)
	if len(rt.startRefs) != 1 {
		t.Fatalf("runtime Start calls = %d, want 1", len(rt.startRefs))
	}
}

// it re-establishes fresh mounts on start after an involuntary die-event stop
func TestStartReestablishesFreshMountsAfterDieEventStop(t *testing.T) {
	ctx := context.Background()
	const id = "sb-start-after-die-stop"
	f := newMountTeardownFixture(t, id)
	f.establishMounts(t, id)

	if err := f.svc.handleDockerEvent(ctx, dockerEventDie(id)); err != nil {
		t.Fatalf("handleDockerEvent die: %v", err)
	}
	if binds := f.svc.mounts.HostBindsFor(id); len(binds) != 0 {
		t.Fatalf("precondition: tracked state survived die event: %#v", binds)
	}

	if _, err := f.svc.StartSandbox(ctx, id); err != nil {
		t.Fatalf("StartSandbox: %v", err)
	}
	if binds := f.svc.mounts.HostBindsFor(id); len(binds) != 1 {
		t.Fatalf("host binds after start = %#v, want exactly 1 fresh mount", binds)
	}
	rt := f.svc.docker.(*recordingRuntime)
	if len(rt.startRefs) != 1 {
		t.Fatalf("runtime Start calls = %d, want 1", len(rt.startRefs))
	}
}

// it re-reads mount arguments from specs on re-establishment after stop
func TestStartReReadsMountArgumentsFromSpecsAfterStop(t *testing.T) {
	ctx := context.Background()
	const id = "sb-start-reread-args"
	f := newMountTeardownFixture(t, id)
	argLog := filepath.Join(t.TempDir(), "mount-args.log")
	installRecordingMountBin(t, argLog)
	f.establishMounts(t, id)

	if _, err := f.svc.stopSandboxInternal(ctx, id, stopModeManual); err != nil {
		t.Fatalf("stopSandboxInternal: %v", err)
	}

	// The operator edits the stored spec while stopped (e.g. loosens the nfs
	// mount options). The next start must build the mount command from the
	// STORED spec, not reuse any cached plan.
	f.specs[0].Options = map[string]string{"opts": "hard,nolock"}
	sealed, err := f.svc.sealMounts(f.specs)
	if err != nil {
		t.Fatalf("sealMounts: %v", err)
	}
	if err := f.st.PutMounts(ctx, id, sealed); err != nil {
		t.Fatalf("PutMounts: %v", err)
	}

	if _, err := f.svc.StartSandbox(ctx, id); err != nil {
		t.Fatalf("StartSandbox: %v", err)
	}
	raw, err := os.ReadFile(argLog)
	if err != nil {
		t.Fatalf("read mount argv log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("mount invocations = %d (%#v), want exactly 2 (initial + re-establish)", len(lines), lines)
	}
	for _, line := range lines {
		if !strings.Contains(line, "-t nfs") {
			t.Fatalf("mount argv line missing nfs type: %q", line)
		}
	}
	if !strings.Contains(lines[0], "-o rw") {
		t.Fatalf("initial mount argv should carry the original opts: %q", lines[0])
	}
	if !strings.Contains(lines[1], "-o hard,nolock") {
		t.Fatalf("re-established mount argv must be re-read from the stored spec: %q", lines[1])
	}
}

// it establishes mounts before starting the runtime container
func TestStartEstablishesMountsBeforeRuntimeStart(t *testing.T) {
	ctx := context.Background()
	const id = "sb-start-mount-before-rt"
	f := newMountTeardownFixture(t, id)
	orderLog := filepath.Join(t.TempDir(), "order.log")
	installRecordingMountBin(t, orderLog)
	ort := &startOrderRuntime{orderLog: orderLog}
	f.svc.docker = ort

	if _, err := f.svc.StartSandbox(ctx, id); err != nil {
		t.Fatalf("StartSandbox: %v", err)
	}
	raw, err := os.ReadFile(orderLog)
	if err != nil {
		t.Fatalf("read order log: %v", err)
	}
	events := strings.Split(strings.TrimSpace(string(raw)), "\n")
	mountIdx, startIdx := -1, -1
	for i, e := range events {
		switch {
		case strings.Contains(e, "-t nfs") && mountIdx == -1:
			mountIdx = i
		case e == "start" && startIdx == -1:
			startIdx = i
		}
	}
	if mountIdx == -1 || startIdx == -1 {
		t.Fatalf("expected one mount and one start event, got %#v", events)
	}
	if startIdx < mountIdx {
		t.Fatalf("runtime started before mounts were established: %#v", events)
	}
}

// it keeps the daemon-restart reconciler re-mounting running sandboxes
func TestReconcileKeepsRemountingRunningSandboxes(t *testing.T) {
	ctx := context.Background()
	const id = "sb-reconcile-remount-running"
	f := newMountTeardownFixture(t, id)
	rt := f.svc.docker.(*recordingRuntime)
	rt.managed = map[string]*models.SandboxRuntimeState{
		id: {SandboxID: id, ContainerID: "ctr-" + id, Status: models.SandboxStatusStarted},
	}

	// Daemon restart: row says Started, but this process tracks no mounts yet.
	if binds := f.svc.mounts.HostBindsFor(id); len(binds) != 0 {
		t.Fatalf("precondition: fresh manager already tracks mounts: %#v", binds)
	}
	if err := f.svc.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	binds := f.svc.mounts.HostBindsFor(id)
	if len(binds) != 1 {
		t.Fatalf("reconcile should re-establish mounts for running sandboxes: %#v", binds)
	}
	if binds[0].ContainerPath != "/data" {
		t.Fatalf("bind container path = %q, want /data", binds[0].ContainerPath)
	}
}

// it keeps the wasm rehydrate re-establish path working
func TestWasmRehydrateKeepsReestablishingMounts(t *testing.T) {
	ctx := context.Background()
	const id = "sb-wasm-rehydrate-mounts"
	f := newMountTeardownFixture(t, id)

	// A valid local checkpoint so the rehydrate path proceeds past
	// ensureWasmCheckpointLocal.
	cpDir := filepath.Join(t.TempDir(), "mem.snap")
	snap := wasm.SnapshotCapture{
		Config: wasm.SnapshotConfig{
			SchemaVersion: 1,
			Engine:        wasm.EngineNameWazero(),
			BaseModule:    wasm.SnapshotBaseModule{Digest: "sha256:abc"},
			Durability:    models.DurabilityPassivatable,
		},
		Memory:    []byte("mem"),
		Globals:   []byte("[]"),
		WASIState: []byte("{}"),
	}
	if err := wasm.WriteSnapshotDir(cpDir, snap); err != nil {
		t.Fatalf("WriteSnapshotDir: %v", err)
	}

	wrt := &mountWasmRehydrateRuntime{}
	f.svc.docker = wrt
	f.svc.SetWasmRuntime(wrt)
	f.svc.cfg.EnableWasm = true

	row, err := f.st.Get(ctx, id)
	if err != nil {
		t.Fatalf("get fixture sandbox: %v", err)
	}
	row.Status = models.SandboxStatusPassivated
	row.Runtime = models.RuntimeWasm
	row.Durability = models.DurabilityPassivatable
	row.CheckpointPath = cpDir
	if err := f.st.Upsert(ctx, row); err != nil {
		t.Fatalf("repoint sandbox at wasm passivated: %v", err)
	}

	if _, err := f.svc.StartSandbox(ctx, id); err != nil {
		t.Fatalf("StartSandbox: %v", err)
	}
	if len(wrt.rehydrateBinds) != 1 {
		t.Fatalf("rehydrate got %#v binds, want the re-established mount", wrt.rehydrateBinds)
	}
	if wrt.rehydrateBinds[0].ContainerPath != "/data" {
		t.Fatalf("rehydrate bind container path = %q, want /data", wrt.rehydrateBinds[0].ContainerPath)
	}
	if len(wrt.hostBinds) != 1 {
		t.Fatalf("host start got %#v binds, want the re-established mount", wrt.hostBinds)
	}
}

// it fails the start when re-establishment of a mount fails
func TestStartFailsWhenMountReestablishmentFails(t *testing.T) {
	ctx := context.Background()
	const id = "sb-start-reestablish-fail"
	f := newMountTeardownFixture(t, id)
	f.establishMounts(t, id)

	if _, err := f.svc.stopSandboxInternal(ctx, id, stopModeManual); err != nil {
		t.Fatalf("stopSandboxInternal: %v", err)
	}

	// The mount backend is now broken: every kernel mount exits non-zero.
	installMountBinFromScript(t, "#!/bin/sh\necho 'mount: no such host' >&2\nexit 1\n")

	_, err := f.svc.StartSandbox(ctx, id)
	if err == nil {
		t.Fatal("StartSandbox must fail when mount re-establishment fails")
	}
	if !strings.Contains(err.Error(), "reestablish mounts") {
		t.Fatalf("StartSandbox error = %v, want reestablish failure", err)
	}
	if !errors.Is(err, os.ErrPermission) && !strings.Contains(err.Error(), "kernel mount failed") {
		t.Fatalf("StartSandbox error should wrap the mount failure: %v", err)
	}
	rt := f.svc.docker.(*recordingRuntime)
	if len(rt.startRefs) != 0 {
		t.Fatalf("runtime must not start when mounts fail: startRefs = %#v", rt.startRefs)
	}
	if binds := f.svc.mounts.HostBindsFor(id); len(binds) != 0 {
		t.Fatalf("failed re-establishment must leave no tracked state: %#v", binds)
	}
}

// mountWasmRehydrateRuntime satisfies StartHost + CheckpointHost so
// StartSandbox drives the passivated-wasm rehydrate path end to end while
// recording the binds handed to each hook.
type mountWasmRehydrateRuntime struct {
	recordingRuntime
	rehydrateBinds []mounts.ContainerBind
	hostBinds      []mounts.ContainerBind
}

func (r *mountWasmRehydrateRuntime) CheckpointSandbox(context.Context, *models.Sandbox) (string, string, error) {
	return "", "", nil
}

func (r *mountWasmRehydrateRuntime) RehydrateSandbox(_ context.Context, _ *models.Sandbox, binds []mounts.ContainerBind) (*models.SandboxRuntimeState, error) {
	r.rehydrateBinds = binds
	return &models.SandboxRuntimeState{ContainerID: "wasm-rh", Status: models.SandboxStatusStarted}, nil
}

func (r *mountWasmRehydrateRuntime) StartSandbox(_ context.Context, _ *models.Sandbox, binds []mounts.ContainerBind) (*models.SandboxRuntimeState, error) {
	r.hostBinds = binds
	return &models.SandboxRuntimeState{ContainerID: "wasm-host", ContainerIP: "10.0.0.9", Status: models.SandboxStatusStarted}, nil
}
