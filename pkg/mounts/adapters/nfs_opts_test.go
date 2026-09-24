package adapters

import (
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

func nfsSpecWithOpts(opts string) models.MountSpec {
	return models.MountSpec{
		Source:  "10.0.0.2:/exports/data",
		Options: map[string]string{"opts": opts},
	}
}

// spec.Options["opts"] is spliced into `mount -t nfs -o <opts>` as root.
// Tokens outside the allowlist must be rejected: suid/dev/exec/user/users/
// owner weaken the mount, umountprog/mountprog make mount.nfs run a helper
// binary, context*/x-systemd.* carry privileged semantics.
func TestNFSBuild_RejectsDisallowedOptsTokens(t *testing.T) {
	for _, opts := range []string{
		"rw,suid,dev",
		"suid",
		"dev",
		"exec",
		"user",
		"users",
		"owner",
		"umountprog=/tmp/x",
		"mountprog=/tmp/x",
		"context=system_u:system_r:container_t",
		"x-systemd.automount",
		"vers=4,suid",
		"vers=4 suid",
	} {
		if _, err := (NFS{}).Build("sb", 0, nfsSpecWithOpts(opts), "/mnt/nfs", "/creds"); err == nil {
			t.Errorf("expected error for nfs opts %q", opts)
		}
	}
}

// ro/rw/nosuid/nodev/noexec are safe spellings (docs/external-storage.md
// documents opts:"ro,vers=4", and operator NFSOptions reuse this function).
// The allowlist used to reject them, so documented configs 400'd. They must be
// accepted without weakening the forced nosuid,nodev suffix.
func TestNFSBuild_AcceptsSafeNoOpOpts(t *testing.T) {
	for _, opts := range []string{"ro,vers=4", "rw", "nosuid", "nodev", "noexec", "rw,noexec"} {
		plan, err := (NFS{}).Build("sb", 0, nfsSpecWithOpts(opts), "/mnt/nfs", "/creds")
		if err != nil {
			t.Errorf("Build(opts %q): %v", opts, err)
			continue
		}
		final := plan.Argv[4]
		toks := strings.Split(final, ",")
		if len(toks) < 2 || toks[len(toks)-2] != "nosuid" || toks[len(toks)-1] != "nodev" {
			t.Errorf("opts %q: final opts %q must end with nosuid,nodev", opts, final)
		}
		for _, tok := range toks {
			switch tok {
			case "suid", "dev", "exec":
				t.Errorf("opts %q: final opts %q carries weakening token %q", opts, final, tok)
			}
		}
	}
}

// A user-supplied rw must not cancel a read-only request: the forced ro wins.
func TestNFSBuild_ReadOnlyBeatsUserRW(t *testing.T) {
	spec := nfsSpecWithOpts("rw,vers=4")
	spec.ReadOnly = true
	plan, err := (NFS{}).Build("sb", 0, spec, "/mnt/nfs", "/creds")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := plan.Argv[4]; got != "vers=4,ro,nosuid,nodev" {
		t.Fatalf("readOnly final opts = %q, want vers=4,ro,nosuid,nodev", got)
	}
}

// nosuid,nodev must be forced onto the final opts — never trusted from the
// user string — and the argv opts must end with exactly those two tokens.
func TestNFSBuild_ForcesNoSUIDNoDevOnFinalOpts(t *testing.T) {
	cases := map[string]string{
		"":                      "rw,nosuid,nodev",
		"vers=4":                "vers=4,nosuid,nodev",
		"hard,vers=4,timeo=300": "hard,vers=4,timeo=300,nosuid,nodev",
	}
	for opts, want := range cases {
		plan, err := (NFS{}).Build("sb", 0, nfsSpecWithOpts(opts), "/mnt/nfs", "/creds")
		if err != nil {
			t.Errorf("Build(opts %q): %v", opts, err)
			continue
		}
		if got := plan.Argv[4]; got != want {
			t.Errorf("Build(opts %q) final opts = %q, want %q", opts, got, want)
		}
	}

	spec := nfsSpecWithOpts("vers=4")
	spec.ReadOnly = true
	plan, err := (NFS{}).Build("sb", 0, spec, "/mnt/nfs", "/creds")
	if err != nil {
		t.Fatalf("Build(readOnly): %v", err)
	}
	if got := plan.Argv[4]; got != "vers=4,ro,nosuid,nodev" {
		t.Fatalf("readOnly final opts = %q, want vers=4,ro,nosuid,nodev", got)
	}
}
