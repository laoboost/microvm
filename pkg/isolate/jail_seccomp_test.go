package isolate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// (b) The jail is honesty-bound: JailCoverage must name exactly what applyJail
// realizes. The seccomp allowlist (internal/runtime/isolate/jail.go) is still
// dead config — it needs a pre-exec BPF install that Go's os/exec cannot
// express — so the report must never claim seccomp.
func TestJailCoverageNamesWhatIsActuallyApplied(t *testing.T) {
	if got := JailCoverage(); got != "uid-drop only; seccomp NOT applied" {
		t.Fatalf("JailCoverage() = %q, want %q", got, "uid-drop only; seccomp NOT applied")
	}
}

// (a) applyJail must arm PR_SET_NO_NEW_PRIVS before exec — the first half of
// the seccomp(2) install recipe, and a real confinement on its own.
func TestApplyJailSetsNoNewPrivs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("applyJail prctl is linux-only")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	cmd := exec.Command("/bin/true")
	if err := applyJail(cmd, JailConfig{Require: true, UID: 1000, GID: 1000}); err != nil {
		t.Fatalf("applyJail: %v", err)
	}
	v, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("PR_GET_NO_NEW_PRIVS: %v", err)
	}
	if v != 1 {
		t.Fatalf("PR_GET_NO_NEW_PRIVS = %d, want 1 (not set by applyJail)", v)
	}
}

// (c) Require=true is load-bearing: while seccomp is not applied it must FAIL
// (refusing to spawn) unless the operator explicitly accepts the weak jail via
// SB_ISOLATE_ALLOW_WEAK_JAIL=true. The pre-fix behavior ran workerd anyway —
// uid-drop only — while the operator believed the allowlist was in force.
func TestHostStartRequireFailsWithoutSeccompUnlessOverridden(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("applyJail is linux-only; non-linux already fails closed")
	}
	runDir := t.TempDir()
	side := filepath.Join(runDir, "workerd-spawned")
	fakeWorkerd := filepath.Join(runDir, "workerd")
	script := "#!/bin/sh\ntouch '" + side + "'\nsleep 1\n"
	if err := os.WriteFile(fakeWorkerd, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	newJailedHost := func(t *testing.T) *Host {
		t.Helper()
		h, err := NewHost(HostConfig{
			WorkerdPath:  fakeWorkerd,
			GroupKey:     "g1",
			RunDir:       filepath.Join(runDir, "g1"),
			Jail:         JailConfig{Require: true, UID: 1000, GID: 1000},
			StartTimeout: 200 * time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}

	h := newJailedHost(t)
	err := h.Start(context.Background())
	if err == nil {
		_ = h.Stop()
		t.Fatal("Start succeeded with Jail.Require=true while seccomp is NOT applied")
	}
	if !strings.Contains(err.Error(), "seccomp NOT applied") {
		t.Fatalf("Start error = %v, want the honest seccomp-not-applied refusal", err)
	}
	if _, statErr := os.Stat(side); statErr == nil {
		t.Fatal("workerd was spawned despite the Require=true fail-closed gate")
	}

	// The explicit override accepts the weak jail: the gate must let Start
	// proceed past the refusal (the fake workerd then dies at readiness).
	t.Setenv("SB_ISOLATE_ALLOW_WEAK_JAIL", "true")
	h2 := newJailedHost(t)
	err2 := h2.Start(context.Background())
	if err2 != nil && strings.Contains(err2.Error(), "seccomp NOT applied") {
		t.Fatalf("SB_ISOLATE_ALLOW_WEAK_JAIL=true still refused at the jail gate: %v", err2)
	}
	_ = h2.Stop()
}
