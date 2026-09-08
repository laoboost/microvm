package service

// Tests pinning and closing the mount lifecycle gaps from
// docs/external-storage.md ("On Stop the host mount is torn down. On Start
// it's re-established."):
//   - API stop teardown (existing behavior — pinned, not re-implemented)
//   - die/stop/oom event teardown (Gap A)
//   - reconciler vs. stop race (Gap B)
//   - destroy path unchanged
//
// The nfs adapter is the only kernel-mount path: it runs `mount -t nfs ...`
// synchronously and does not need a FUSE readiness probe, so a fake `mount`
// binary on PATH lets MountAll/Reestablish succeed without root or real
// network storage.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/internal/store"
	"github.com/aerol-ai/microvm/pkg/docker"
	"github.com/aerol-ai/microvm/pkg/models"
	"github.com/aerol-ai/microvm/pkg/mounts"
)

// installFakeMountBin puts a no-op `mount` executable at the front of PATH so
// the nfs kernel-mount adapter succeeds without privileges.
func installFakeMountBin(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "mount"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake mount: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type mountTeardownFixture struct {
	svc     *Service
	st      *store.Store
	rootDir string
	specs   []models.MountSpec
}

func newMountTeardownFixture(t *testing.T, id string) *mountTeardownFixture {
	t.Helper()
	installFakeMountBin(t)
	svc, st, _ := newServiceRuntimeHarness(t, &recordingRuntime{})
	svc.cipher = newTestCipher(t)

	rootDir := t.TempDir()
	mgr, err := mounts.New(svc.logger, mounts.Config{
		RootDir:     rootDir,
		CredDir:     t.TempDir(),
		WaitTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("mounts.New: %v", err)
	}
	t.Cleanup(mgr.Close)
	svc.mounts = mgr

	specs := []models.MountSpec{{
		Type:   models.MountTypeNFS,
		Source: "host:/exports/ws",
		Target: "/data",
	}}
	now := time.Now().UTC()
	if err := st.Create(context.Background(), &models.Sandbox{
		ID:           id,
		Image:        "alpine:3.20",
		Status:       models.SandboxStatusStarted,
		Runtime:      models.RuntimeDocker,
		ContainerID:  "ctr-" + id,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
	}); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}
	sealed, err := svc.sealMounts(specs)
	if err != nil {
		t.Fatalf("sealMounts: %v", err)
	}
	if err := st.PutMounts(context.Background(), id, sealed); err != nil {
		t.Fatalf("PutMounts: %v", err)
	}

	return &mountTeardownFixture{svc: svc, st: st, rootDir: rootDir, specs: specs}
}

// establishMounts simulates the mounts a running sandbox would have.
func (f *mountTeardownFixture) establishMounts(t *testing.T, id string) {
	t.Helper()
	if _, err := f.svc.mounts.MountAll(context.Background(), id, f.specs); err != nil {
		t.Fatalf("MountAll: %v", err)
	}
	if len(f.svc.mounts.HostBindsFor(id)) == 0 {
		t.Fatal("precondition: no tracked mount state after MountAll")
	}
}

// it tears down host mounts when a sandbox with mounts is stopped via the API
// (pins existing stopSandboxInternal behavior — do not re-implement).
func TestStopSandboxViaAPITearsDownHostMounts(t *testing.T) {
	ctx := context.Background()
	const id = "sb-api-stop-mounts"
	f := newMountTeardownFixture(t, id)
	f.establishMounts(t, id)

	if _, err := f.svc.stopSandboxInternal(ctx, id, stopModeManual); err != nil {
		t.Fatalf("stopSandboxInternal: %v", err)
	}
	if binds := f.svc.mounts.HostBindsFor(id); len(binds) != 0 {
		t.Fatalf("host binds after API stop = %#v, want none", binds)
	}
	if _, err := os.Stat(filepath.Join(f.rootDir, id)); !os.IsNotExist(err) {
		t.Fatalf("mount dir survived API stop: stat err = %v", err)
	}
}

// it clears tracked mount state on stop so start re-establishes fresh.
func TestStopClearsTrackedMountStateSoStartReestablishesFresh(t *testing.T) {
	ctx := context.Background()
	const id = "sb-stop-clear-state"
	f := newMountTeardownFixture(t, id)
	f.establishMounts(t, id)

	if _, err := f.svc.stopSandboxInternal(ctx, id, stopModeManual); err != nil {
		t.Fatalf("stopSandboxInternal: %v", err)
	}
	if binds := f.svc.mounts.HostBindsFor(id); len(binds) != 0 {
		t.Fatalf("tracked state survived stop: %#v", binds)
	}

	// A later start/reconcile must be able to re-establish from scratch: the
	// fresh container must get a NEW FUSE connection, not bind the old one.
	if err := f.svc.mounts.Reestablish(ctx, id, f.specs); err != nil {
		t.Fatalf("Reestablish after stop: %v", err)
	}
	if binds := f.svc.mounts.HostBindsFor(id); len(binds) != 1 {
		t.Fatalf("Reestablish did not mount fresh: binds = %#v", binds)
	}
}

// it tears down host mounts when the container dies involuntarily
// (die/stop/oom event) — Gap A.
func TestDieEventTearsDownHostMounts(t *testing.T) {
	ctx := context.Background()
	const id = "sb-die-event-mounts"
	f := newMountTeardownFixture(t, id)
	f.establishMounts(t, id)

	if err := f.svc.handleDockerEvent(ctx, dockerEventDie(id)); err != nil {
		t.Fatalf("handleDockerEvent die: %v", err)
	}
	if binds := f.svc.mounts.HostBindsFor(id); len(binds) != 0 {
		t.Fatalf("host binds after die event = %#v, want none", binds)
	}
	if _, err := os.Stat(filepath.Join(f.rootDir, id)); !os.IsNotExist(err) {
		t.Fatalf("mount dir survived die event: stat err = %v", err)
	}
	got, err := f.st.Get(ctx, id)
	if err != nil {
		t.Fatalf("row should be preserved on die: %v", err)
	}
	if got.Status != models.SandboxStatusStopped {
		t.Fatalf("row status after die: got %q, want stopped", got.Status)
	}
}

// it logs a warning and keeps the sandbox row updated when mount teardown
// fails on the event path.
func TestDieEventMountTeardownFailureWarnsButRowStops(t *testing.T) {
	ctx := context.Background()
	const id = "sb-die-event-um-fail"
	f := newMountTeardownFixture(t, id)
	f.establishMounts(t, id)
	f.svc.testForceUnmountErr = os.ErrPermission

	if err := f.svc.handleDockerEvent(ctx, dockerEventDie(id)); err != nil {
		t.Fatalf("teardown failure must not fail the event: %v", err)
	}
	got, err := f.st.Get(ctx, id)
	if err != nil {
		t.Fatalf("get row: %v", err)
	}
	if got.Status != models.SandboxStatusStopped {
		t.Fatalf("row status after failed teardown: got %q, want stopped", got.Status)
	}
}

// it logs a warning and still stops the sandbox when mount teardown fails on
// the API path (pins existing behavior).
func TestStopSandboxMountTeardownFailureStillStops(t *testing.T) {
	ctx := context.Background()
	const id = "sb-api-stop-um-fail"
	f := newMountTeardownFixture(t, id)
	f.establishMounts(t, id)
	f.svc.testForceUnmountErr = os.ErrPermission

	if _, err := f.svc.stopSandboxInternal(ctx, id, stopModeManual); err != nil {
		t.Fatalf("stop must still succeed when unmount fails: %v", err)
	}
	got, err := f.st.Get(ctx, id)
	if err != nil {
		t.Fatalf("get row: %v", err)
	}
	if got.Status != models.SandboxStatusStopped {
		t.Fatalf("row status: got %q, want stopped", got.Status)
	}
}

// it does not re-mount a sandbox whose stop is in progress during a reconcile
// pass — Gap B. A control sandbox without a recorded expected stop IS
// re-mounted by the same pass, proving the observability of the assertion.
func TestReconcileDoesNotRemountSandboxWithStopInProgress(t *testing.T) {
	ctx := context.Background()
	const stopping = "sb-reconcile-stopping"
	const control = "sb-reconcile-control"
	f := newMountTeardownFixture(t, stopping)

	// Seed the control sandbox with the same mounts.
	sealed, err := f.svc.sealMounts(f.specs)
	if err != nil {
		t.Fatalf("sealMounts: %v", err)
	}
	now := time.Now().UTC()
	if err := f.st.Create(ctx, &models.Sandbox{
		ID:           control,
		Image:        "alpine:3.20",
		Status:       models.SandboxStatusStarted,
		Runtime:      models.RuntimeDocker,
		ContainerID:  "ctr-" + control,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
	}); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	if err := f.st.PutMounts(ctx, control, sealed); err != nil {
		t.Fatalf("PutMounts: %v", err)
	}

	rt := f.svc.docker.(*recordingRuntime)
	rt.managed = map[string]*models.SandboxRuntimeState{
		stopping: {SandboxID: stopping, ContainerID: "ctr-" + stopping, Status: models.SandboxStatusStarted},
		control:  {SandboxID: control, ContainerID: "ctr-" + control, Status: models.SandboxStatusStarted},
	}

	// The stop is in progress: expectation recorded, row still Started —
	// exactly the window the reconciler can observe mid-stop.
	f.svc.recordExpectedStop(stopping, stopModeManual)

	if err := f.svc.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if binds := f.svc.mounts.HostBindsFor(stopping); len(binds) != 0 {
		t.Fatalf("reconcile re-mounted a sandbox whose stop is in progress: %#v", binds)
	}
	// Control: without an in-progress stop the same pass does re-establish.
	if binds := f.svc.mounts.HostBindsFor(control); len(binds) != 1 {
		t.Fatalf("control sandbox should be re-mounted by reconcile: %#v", binds)
	}
}

// it keeps the destroy path unmounting exactly as before.
func TestDestroyPathStillUnmounts(t *testing.T) {
	ctx := context.Background()
	const id = "sb-destroy-mounts"
	f := newMountTeardownFixture(t, id)
	f.establishMounts(t, id)

	if err := f.svc.DestroySandbox(ctx, id); err != nil {
		t.Fatalf("DestroySandbox: %v", err)
	}
	if binds := f.svc.mounts.HostBindsFor(id); len(binds) != 0 {
		t.Fatalf("host binds after destroy = %#v, want none", binds)
	}
	if _, err := os.Stat(filepath.Join(f.rootDir, id)); !os.IsNotExist(err) {
		t.Fatalf("mount dir survived destroy: stat err = %v", err)
	}
	if _, err := f.st.Get(ctx, id); err == nil {
		t.Fatal("row should be deleted on destroy")
	}
}

func dockerEventDie(id string) docker.DockerEvent {
	return docker.DockerEvent{SandboxID: id, Action: "die", Time: time.Now().UTC()}
}
