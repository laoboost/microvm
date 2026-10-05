package adapters

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/aerol-ai/microvm/pkg/models"
)

// SSHFS mounts a remote directory over SSH using the user-supplied private
// key. The key file lives in the credentials directory for the lifetime of
// the mount; sshfs may re-read it on reconnect, so we do not unlink it after
// the mount becomes ready.
type SSHFS struct{}

func (SSHFS) Build(sandboxID string, index int, spec models.MountSpec, hostTarget, credDir string) (Plan, error) {
	pem := spec.Credentials["private_key_pem"]
	if pem == "" {
		return Plan{}, errors.New("sshfs requires credentials.private_key_pem")
	}
	if err := checkSSHFSSource(spec.Source); err != nil {
		return Plan{}, err
	}

	credFile := filepath.Join(credDir, fmt.Sprintf("%s-%d.id", sandboxID, index))
	opts := "IdentityFile=" + credFile +
		// StrictHostKeyChecking=yes, not accept-new: trust-on-first-use is
		// MITM-able. Operators whose host key is not in the daemon's
		// known_hosts must supply it via credentials if needed.
		",StrictHostKeyChecking=yes" +
		// Pin ProxyCommand to none so neither a crafted source nor an ssh
		// config can turn the mount into an arbitrary command execution.
		",ProxyCommand=none" +
		",ServerAliveInterval=15,ServerAliveCountMax=3" +
		",reconnect,allow_other"
	if spec.ReadOnly {
		opts += ",ro"
	}

	// -f keeps sshfs in the foreground so the manager can supervise it.
	// "foreground" is not a valid -o option: sshfs 3.x (the Ubuntu 22.04+
	// package) rejects it with "fuse: unknown option(s)" and never mounts.
	// "--" ends option parsing so source can never be read as an sshfs/ssh
	// option; models.validateSource is the primary guard.
	argv := []string{"sshfs", "-f", "-o", opts, "--", spec.Source, hostTarget}

	return Plan{
		Argv:       argv,
		CredFile:   credFile,
		CredBody:   []byte(pem),
		UnlinkCred: false,
	}, nil
}
