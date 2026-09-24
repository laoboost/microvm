//go:build linux

package isolate

import (
	"fmt"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// applyJail confines cmd per j before exec (Linux realization of the driver's
// JailSpec). What is applied here (the exact coverage is reported by
// JailCoverage()):
//
//   - Privilege drop: the workerd process runs as the unprivileged j.UID/j.GID
//     (NoSetGroups drops supplementary groups too), so a V8/JIT escape lands as
//     a non-root user, not the daemon's root.
//   - PR_SET_NO_NEW_PRIVS: the process (and anything it forks) can never gain
//     privileges via setuid/setgid/file capabilities.
//   - Chroot: when j.ChrootDir is set, the process is confined to it. The
//     directory must already be POPULATED (workerd + its shared libs + the run
//     dir mounted in) — that population is the jailer step tracked as a
//     follow-up; until it exists, leave ChrootDir empty and the process runs
//     un-chrooted but still privilege-dropped.
//
// NOT yet applied here (tracked follow-ups; Require=true FAILS CLOSED on the
// missing seccomp unless SB_ISOLATE_ALLOW_WEAK_JAIL=true):
//
//   - seccomp: the SeccompAllowlist must be installed as a BPF filter via a
//     PR_SET_NO_NEW_PRIVS + seccomp(2) pre-exec hook, which Go's os/exec cannot
//     express without CGO or a re-exec shim. This is the JIT-aware allowlist the
//     plan budgets as its own subproject.
//   - cgroup: j.CgroupName / j.MemoryLimitMB should back the process with a
//     cgroup v2 limit (SysProcAttr.UseCgroupFD).
//
// IMPORTANT: this path executes only on Linux hosts and has NOT been exercised
// in offline CI (macOS dev/build). It must be validated by the tagged real-host
// integration test before the jail is trusted for untrusted multi-tenant code.
func applyJail(cmd *exec.Cmd, j JailConfig) error {
	if j.UID <= 0 || j.GID <= 0 {
		return fmt.Errorf("isolate jail: refusing to run workerd privileged (uid/gid must be > 0, got %d/%d)", j.UID, j.GID)
	}
	attr := cmd.SysProcAttr
	if attr == nil {
		attr = &syscall.SysProcAttr{}
	}
	attr.Credential = &syscall.Credential{
		Uid:         uint32(j.UID),
		Gid:         uint32(j.GID),
		NoSetGroups: true,
	}
	attr.Setpgid = true
	if j.ChrootDir != "" {
		attr.Chroot = j.ChrootDir
	}
	cmd.SysProcAttr = attr
	// PR_SET_NO_NEW_PRIVS is per-OS-thread and inherited at fork, so the
	// caller (Host.Start) holds runtime.LockOSThread across this call and
	// cmd.Start. Caveat: it sticks to that daemon thread afterwards (the
	// setting is one-way); sandboxd runs as root, which loses nothing to it.
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("isolate jail: prctl(PR_SET_NO_NEW_PRIVS): %w", err)
	}
	return nil
}

// jailRealizable reports whether this platform can realize the jail at all.
// Linux can (privilege drop + chroot + no-new-privs today; seccomp + cgroup
// are follow-ups — see JailCoverage for the exact profile).
func jailRealizable() bool { return true }
