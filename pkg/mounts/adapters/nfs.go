package adapters

import (
	"fmt"
	"strings"

	"github.com/aerol-ai/microvm/pkg/models"
)

// NFS uses the kernel mount(2) syscall via /bin/mount; there is no long-running
// user-space process to supervise. The manager runs `mount -t nfs ...` once,
// waits for it to exit (it returns immediately on success), then registers a
// periodic health check via findmnt.
type NFS struct{}

// nfsOptsAllowlist is the set of mount(8) option keys accepted from
// Options["opts"] / Backend.NFSOptions. Everything else is rejected: suid,
// dev, exec, user, users, owner weaken the mount, umountprog/mountprog make
// mount.nfs execute a helper binary, and context*/x-systemd.* carry
// privileged or service-manager semantics.
//
// ro/rw/nosuid/nodev/noexec are the safe spellings callers legitimately write
// (docs/external-storage.md shows opts:"ro,vers=4"). ro/rw are folded into the
// readOnly decision below; nosuid/nodev are re-forced at the end; noexec is
// harmless hardening and is passed through.
var nfsOptsAllowlist = map[string]struct{}{
	"vers": {}, "port": {}, "mountport": {}, "proto": {}, "addr": {},
	"retry": {}, "timeo": {}, "trans": {}, "rsize": {}, "wsize": {},
	"hard": {}, "soft": {}, "retrans": {},
	"ro": {}, "rw": {}, "nosuid": {}, "nodev": {}, "noexec": {},
}

// NFSMountOpts validates user-supplied NFS mount options against an allowlist
// and returns the final `mount -o` string. nosuid,nodev are forced onto the
// end — the user string is never trusted to carry them. A caller-supplied rw
// is ignored in favor of the readOnly flag; an explicit user ro is honored
// only when the caller did not ask for read-only, so it can never widen access.
func NFSMountOpts(userOpts string, readOnly bool) (string, error) {
	userOpts = strings.TrimSpace(userOpts)
	var tokens []string
	userRO := false
	if userOpts != "" {
		if strings.ContainsAny(userOpts, " \t\n\r") {
			return "", fmt.Errorf("nfs opts must not contain whitespace: %q", userOpts)
		}
		for _, tok := range strings.Split(userOpts, ",") {
			key, _, _ := strings.Cut(tok, "=")
			if key == "" {
				return "", fmt.Errorf("nfs opts contain an empty token: %q", userOpts)
			}
			if _, ok := nfsOptsAllowlist[key]; !ok {
				return "", fmt.Errorf("nfs opt %q is not allowed", key)
			}
			switch key {
			case "ro":
				userRO = true
				continue
			case "rw":
				continue
			case "nosuid", "nodev":
				// Re-forced at the end so a user copy can neither duplicate nor
				// move them.
				continue
			}
			tokens = append(tokens, tok)
		}
	}
	// Read-only comes from the caller's flag, never from a user token: an
	// explicit rw must not cancel it. When not read-only, an explicit user ro
	// is still honored.
	if readOnly || userRO {
		tokens = append(tokens, "ro")
	} else if len(tokens) == 0 {
		tokens = append(tokens, "rw")
	}
	tokens = append(tokens, "nosuid", "nodev")
	return strings.Join(tokens, ","), nil
}

func (NFS) Build(sandboxID string, index int, spec models.MountSpec, hostTarget, credDir string) (Plan, error) {
	if err := CheckNFSSource(spec.Source); err != nil {
		return Plan{}, err
	}
	opts, err := NFSMountOpts(spec.Options["opts"], spec.ReadOnly)
	if err != nil {
		return Plan{}, err
	}

	argv := []string{"mount", "-t", "nfs", "-o", opts, spec.Source, hostTarget}

	return Plan{
		Argv:          argv,
		IsKernelMount: true,
	}, nil
}
