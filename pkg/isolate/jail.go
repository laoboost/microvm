package isolate

import "os"

// JailConfig is the OS-confinement request for a group's workerd process. It is
// projected from the driver's JailSpec (internal/runtime/isolate/jail.go, which
// this package cannot import), carrying only the primitives the spawner applies.
//
// Require=true is load-bearing for SECURITY, not a hint: when set, Start MUST
// either realize the confinement or refuse to spawn. It must never run workerd
// unconfined while Require is true — that is the false-confinement bug this
// gate exists to prevent (an operator sets SB_ISOLATE_USE_JAIL=true and
// believes untrusted tenant JS is boxed in). See applyJail / jailRealizable.
type JailConfig struct {
	Require       bool
	ChrootDir     string
	UID           int
	GID           int
	CgroupName    string
	MemoryLimitMB int
	Jitless       bool
}

// JailRealizable is the exported platform-capability check the daemon logs at
// boot so operators can see whether SB_ISOLATE_USE_JAIL can actually be honored
// on this host (Linux) or whether isolate creates will fail closed until it is
// disabled or the host is Linux. Realizable is NOT "fully jailed" — see
// JailCoverage for exactly what is applied.
func JailRealizable() bool { return jailRealizable() }

// JailCoverage names exactly what applyJail realizes today, so boot logs and
// Require-failure messages never over-promise. The SeccompAllowlist in
// internal/runtime/isolate/jail.go is still DEAD CONFIG: installing it needs a
// pre-exec BPF filter (seccomp(2) after PR_SET_NO_NEW_PRIVS), which Go's
// os/exec cannot express without a re-exec shim (tracked separately).
func JailCoverage() string { return jailCoverage() }

func jailCoverage() string {
	return "uid-drop only; seccomp NOT applied"
}

// seccompApplied reports whether applyJail actually installed a seccomp
// filter. False until the pre-exec BPF install lands — Require=true fails
// closed on it unless allowWeakJail().
func seccompApplied() bool { return false }

// allowWeakJail reports whether the operator explicitly accepted the weak
// (uid-drop-only) jail via SB_ISOLATE_ALLOW_WEAK_JAIL=true. The override is
// opt-in and loud on purpose: it is the only way to run with Require=true
// while the seccomp allowlist is not yet applied.
func allowWeakJail() bool {
	switch os.Getenv("SB_ISOLATE_ALLOW_WEAK_JAIL") {
	case "true", "1":
		return true
	}
	return false
}
