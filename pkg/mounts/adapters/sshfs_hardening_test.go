package adapters

import (
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

func sshfsSpec(source string) models.MountSpec {
	return models.MountSpec{
		Source:      source,
		Credentials: map[string]string{"private_key_pem": "key"},
	}
}

// sshfs hands its source to ssh(1) as root. The adapter must re-apply the
// strict user@host:/path grammar (defense-in-depth behind pkg/models) so a
// spec that skipped Validate still cannot inject ssh options.
func TestSSHFSBuild_RejectsHostOptionInjectionSource(t *testing.T) {
	for _, source := range []string{
		"@-oProxyCommand=x:/",
		"user@-oProxyCommand=x:/",
		"@-oProxyCommand=sh -c 'x':/",
		"user@host:/path extra",
	} {
		if _, err := (SSHFS{}).Build("sb", 0, sshfsSpec(source), "/mnt/ssh", "/creds"); err == nil {
			t.Errorf("expected error for sshfs source %q", source)
		}
	}
	if _, err := (SSHFS{}).Build("sb", 0, sshfsSpec("user@host:/path"), "/mnt/ssh", "/creds"); err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}
}

// StrictHostKeyChecking=accept-new is TOFU and MITM-able on first connect;
// ProxyCommand must be pinned to none so no source or config can turn the
// mount into an arbitrary command execution.
func TestSSHFSBuild_HardensSSHOpts(t *testing.T) {
	plan, err := (SSHFS{}).Build("sb", 0, sshfsSpec("user@host:/path"), "/mnt/ssh", "/creds")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	opts := strings.Join(plan.Argv, " ")
	if !strings.Contains(opts, "ProxyCommand=none") {
		t.Fatalf("sshfs argv missing ProxyCommand=none: %v", plan.Argv)
	}
	if !strings.Contains(opts, "StrictHostKeyChecking=yes") {
		t.Fatalf("sshfs argv missing StrictHostKeyChecking=yes: %v", plan.Argv)
	}
	if strings.Contains(opts, "accept-new") {
		t.Fatalf("sshfs argv must not use TOFU accept-new: %v", plan.Argv)
	}
}
