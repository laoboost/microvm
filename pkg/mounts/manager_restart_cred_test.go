package mounts

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/mounts/adapters"
)

// restartTestManager builds a manager plus a tracked supervised mount state
// whose process exits immediately, so calling superviseExit drives the restart
// branch synchronously.
func restartTestManager(t *testing.T, plan adapters.Plan) (*Manager, *mountState, string) {
	t.Helper()
	root := t.TempDir()
	creds := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m, err := New(logger, Config{RootDir: root, CredDir: creds, WaitTimeout: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(m.Close)

	hostPath := filepath.Join(root, "sb-rst", "0")
	if err := os.MkdirAll(hostPath, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	state := &mountState{
		sandboxID:  "sb-rst",
		index:      0,
		hostPath:   hostPath,
		plan:       plan,
		cmd:        cmd,
		startedAt:  time.Now().UTC(),
		supervised: true,
	}
	m.mu.Lock()
	m.state["sb-rst"] = []*mountState{state}
	m.mu.Unlock()
	return m, state, hostPath
}

// stubProbe replaces the mount-readiness probe for the duration of the test.
func stubProbe(t *testing.T, fn func(hostPath string, timeout time.Duration) error) {
	t.Helper()
	orig := waitForMountProbe
	waitForMountProbe = fn
	t.Cleanup(func() { waitForMountProbe = orig })
}

func TestManagerRemovesCredFileAfterSuccessfulSupervisedRestart(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "cred")
	plan := adapters.Plan{Argv: []string{"true"}, CredFile: credPath, CredBody: []byte("secret"), UnlinkCred: true}
	if err := writeCredFile(credPath, plan.CredBody); err != nil {
		t.Fatalf("seed cred: %v", err)
	}
	m, state, _ := restartTestManager(t, plan)
	probeCalled := false
	stubProbe(t, func(string, time.Duration) error { probeCalled = true; return nil })

	m.superviseExit(state)

	if !probeCalled {
		t.Error("readiness probe was not called during restart")
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Errorf("cred file still present after successful restart: err=%v", err)
	}
}

func TestManagerRemovesCredFileAfterRestartProbeTimeout(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "cred")
	plan := adapters.Plan{Argv: []string{"true"}, CredFile: credPath, CredBody: []byte("secret"), UnlinkCred: true}
	if err := writeCredFile(credPath, plan.CredBody); err != nil {
		t.Fatalf("seed cred: %v", err)
	}
	m, state, _ := restartTestManager(t, plan)
	stubProbe(t, func(string, time.Duration) error { return errors.New("timed out waiting for mount") })

	m.superviseExit(state)

	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Errorf("cred file still present after restart probe timeout: err=%v", err)
	}
}

func TestManagerWaitsForMountProbeBeforeRemovingCredFile(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "cred")
	plan := adapters.Plan{Argv: []string{"true"}, CredFile: credPath, CredBody: []byte("secret"), UnlinkCred: true}
	if err := writeCredFile(credPath, plan.CredBody); err != nil {
		t.Fatalf("seed cred: %v", err)
	}
	m, state, _ := restartTestManager(t, plan)

	var once sync.Once
	probeStarted := make(chan struct{})
	release := make(chan struct{})
	stubProbe(t, func(string, time.Duration) error {
		once.Do(func() { close(probeStarted) })
		<-release
		return nil
	})

	done := make(chan struct{})
	go func() {
		m.superviseExit(state)
		close(done)
	}()

	select {
	case <-probeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("restart probe never started")
	}
	// While the probe is still waiting, the credential must still be on disk.
	if _, err := os.Stat(credPath); err != nil {
		t.Fatalf("cred file removed before probe finished: %v", err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("superviseExit did not finish after probe released")
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Errorf("cred file still present after restart: err=%v", err)
	}
}

func TestManagerKeepsSupervisingAfterFailedRestart(t *testing.T) {
	plan := adapters.Plan{Argv: []string{"/does-not-exist-xyz"}}
	m, state, _ := restartTestManager(t, plan)
	stubProbe(t, func(string, time.Duration) error { return nil })

	m.superviseExit(state)

	if !state.disabled {
		t.Error("state should be disabled after a failed restart spawn")
	}
	if state.restarts != 1 {
		t.Errorf("restarts = %d, want 1 (no retry storm)", state.restarts)
	}
	m.mu.Lock()
	_, tracked := m.state["sb-rst"]
	m.mu.Unlock()
	if !tracked {
		t.Error("mount state was dropped from the manager after a failed restart")
	}
}

func TestManagerDoesNotUnlinkWhenPlanHasNoCredFile(t *testing.T) {
	plan := adapters.Plan{Argv: []string{"sleep", "0.5"}}
	m, state, _ := restartTestManager(t, plan)
	oldCmd := state.cmd
	stubProbe(t, func(string, time.Duration) error { return nil })

	m.superviseExit(state) // must not panic or error on empty CredFile

	// Let the re-supervision goroutine (restarted cmd exits immediately) finish
	// mutating state before asserting on it.
	time.Sleep(50 * time.Millisecond)

	if state.cmd == nil || state.cmd == oldCmd {
		t.Error("restart did not publish a new command")
	}
	if state.disabled {
		t.Error("state disabled on a normal restart with no cred file")
	}
}

func TestManagerDoesNotHoldMutexWhileWaitingForRestartProbe(t *testing.T) {
	plan := adapters.Plan{Argv: []string{"true"}}
	m, state, _ := restartTestManager(t, plan)

	var once sync.Once
	probeStarted := make(chan struct{})
	block := make(chan struct{})
	stubProbe(t, func(string, time.Duration) error {
		once.Do(func() { close(probeStarted) })
		<-block
		return nil
	})

	go func() { m.superviseExit(state) }()
	select {
	case <-probeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("restart probe never started")
	}

	// A concurrent mount operation must proceed while the restart probe is
	// blocked waiting for readiness.
	hostBindsDone := make(chan struct{})
	go func() {
		m.HostBindsFor("other-sandbox")
		close(hostBindsDone)
	}()
	select {
	case <-hostBindsDone:
	case <-time.After(2 * time.Second):
		t.Fatal("HostBindsFor blocked while a restart probe was pending: manager mutex is held")
	}

	unmountDone := make(chan struct{})
	go func() {
		_ = m.UnmountAll("other-sandbox-2")
		close(unmountDone)
	}()
	select {
	case <-unmountDone:
	case <-time.After(2 * time.Second):
		t.Fatal("UnmountAll blocked while a restart probe was pending: manager mutex is held")
	}

	close(block)
}
