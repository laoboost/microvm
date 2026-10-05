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
// realizes. applyJail now installs the seccomp allowlist through the re-exec
// jail shim (alongside chroot, cgroup v2, uid/gid drop and NO_NEW_PRIVS), so
// the report must claim seccomp — and must not still claim it is missing.
func TestJailCoverageNamesWhatIsActuallyApplied(t *testing.T) {
	got := JailCoverage()
	if !strings.Contains(got, "seccomp") || strings.Contains(got, "NOT applied") {
		t.Fatalf("JailCoverage() = %q, want a report that seccomp IS applied", got)
	}
}

// (a) applyJail must arm PR_SET_NO_NEW_PRIVS before exec — the first half of
// the seccomp(2) install recipe, and a real confinement on its own.
func TestApplyJailSetsNoNewPrivs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("applyJail prctl is linux-only")
	}
	if os.Geteuid() != 0 {
		t.Skip("applyJail realizes the full jail (chroot, cgroup, setuid); needs root")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	cmd := exec.Command("/bin/true")
	realized, err := applyJail(cmd, JailConfig{Require: true, UID: 1000, GID: 1000}, nil)
	if err != nil {
		t.Fatalf("applyJail: %v", err)
	}
	if realized != nil {
		t.Cleanup(func() { _ = realized.teardown() })
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
		t.Fatal("Start succeeded with Jail.Require=true on a host that cannot realize the jail")
	}
	if !strings.Contains(err.Error(), "needs root") && !strings.Contains(err.Error(), "seccomp NOT applied") {
		t.Fatalf("Start error = %v, want the honest fail-closed jail refusal", err)
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
	// The weak-jail override only waives the seccomp gate; realization still
	// needs root, so a failure here is expected off-root and must not be the
	// seccomp refusal itself.
	_ = err2
	_ = h2.Stop()
}
