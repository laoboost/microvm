package models

import "testing"

// An sshfs source is executed by ssh(1) as root via the mount tool. A source
// like "@-oProxyCommand=sh -c 'x':/" passes the loose contains-@/: check and
// smuggles an ssh option (arbitrary command execution). Validate must enforce
// the strict user@host:/path grammar: no whitespace, host in
// [A-Za-z0-9._-]+ and not flag-like, absolute remote path.
func TestMountSpecValidate_SSHFSSourceGrammar(t *testing.T) {
	rejected := []string{
		"@-oProxyCommand=x:/",
		"user@-oProxyCommand=x:/",
		"@-oProxyCommand=sh -c 'x':/",
		"user@-oProxyCommand=sh -c 'x':/",
		"user@host:/path extra",
		"user@ho st:/path",
		"user@host:path",
		"user@-host:/path",
		"user@host=/x:/path",
		"@@host:/path",
	}
	for _, source := range rejected {
		m := MountSpec{Type: MountTypeSSHFS, Source: source, Target: "/mnt/x"}
		if err := m.Validate("/usr/local/bin/toolboxd"); err == nil {
			t.Errorf("Validate(sshfs source %q) = nil, want error", source)
		}
	}

	accepted := []string{
		"user@host:/path",
		"user@example.com:/home/user",
		"ubuntu@build-host:/home/ubuntu",
		"user@10.0.0.2:/",
	}
	for _, source := range accepted {
		m := MountSpec{Type: MountTypeSSHFS, Source: source, Target: "/mnt/x"}
		if err := m.Validate("/usr/local/bin/toolboxd"); err != nil {
			t.Errorf("Validate(sshfs source %q) = %v, want nil", source, err)
		}
	}
}
