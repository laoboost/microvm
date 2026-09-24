package volumes

import (
	"context"
	"testing"
)

// reclaimNFS splices Backend.NFSOptions into `mount -t nfs -o <opts>` as root.
// Same rules as the sandbox NFS adapter: allowlisted tokens only, nosuid,nodev
// forced onto the final opts.
func TestReclaimNFS_OptsAllowlistAndForcedNoSUIDNoDev(t *testing.T) {
	mountOpts := func(t *testing.T, opts string) (string, error) {
		t.Helper()
		var cmds []recordedCmd
		r := NewReclaimer(Backend{Kind: BackendNFS, NFSOptions: opts}, t.TempDir(), recordingRunner(&cmds, nil))
		err := r.Reclaim(context.Background(), BackendNFS, "nfs.example.com:/export/data")
		if err != nil {
			return "", err
		}
		if len(cmds) == 0 || cmds[0].name != "mount" {
			t.Fatalf("commands = %+v, want mount first", cmds)
		}
		args := cmds[0].args
		for i, a := range args {
			if a == "-o" && i+1 < len(args) {
				return args[i+1], nil
			}
		}
		t.Fatalf("mount args missing -o: %v", args)
		return "", nil
	}

	got, err := mountOpts(t, "vers=4")
	if err != nil {
		t.Fatalf("Reclaim(opts vers=4): %v", err)
	}
	if got != "vers=4,nosuid,nodev" {
		t.Fatalf("mount opts = %q, want vers=4,nosuid,nodev", got)
	}

	got, err = mountOpts(t, "")
	if err != nil {
		t.Fatalf("Reclaim(opts empty): %v", err)
	}
	if got != "rw,nosuid,nodev" {
		t.Fatalf("mount opts = %q, want rw,nosuid,nodev", got)
	}

	for _, bad := range []string{"rw,suid,dev", "umountprog=/tmp/x", "dev"} {
		var cmds []recordedCmd
		r := NewReclaimer(Backend{Kind: BackendNFS, NFSOptions: bad}, t.TempDir(), recordingRunner(&cmds, nil))
		if err := r.Reclaim(context.Background(), BackendNFS, "nfs.example.com:/export/data"); err == nil {
			t.Errorf("expected error for nfs opts %q", bad)
		}
		if len(cmds) != 0 {
			t.Errorf("opts %q must be rejected before any command runs, got %+v", bad, cmds)
		}
	}
}

// Volume.Source is frozen at creation and later fed to `mount -t nfs` as root.
// A poisoned ledger/row ("h:/p extra", "-oProxyCommand=x:/") must be
// re-validated through the NFS source check before any command runs.
func TestReclaimNFS_RevalidatesStoredSource(t *testing.T) {
	bad := []string{
		"nfs.example.com:/export/data extra",
		"-oProxyCommand=x:/",
		"/etc/passwd",
		"host:path",
		"host:/path\tx",
	}
	for _, source := range bad {
		var cmds []recordedCmd
		r := NewReclaimer(Backend{Kind: BackendNFS}, t.TempDir(), recordingRunner(&cmds, nil))
		if err := r.Reclaim(context.Background(), BackendNFS, source); err == nil {
			t.Errorf("expected error for stored source %q", source)
		}
		if len(cmds) != 0 {
			t.Errorf("source %q must be rejected before any command runs, got %+v", source, cmds)
		}
	}

	var cmds []recordedCmd
	r := NewReclaimer(Backend{Kind: BackendNFS}, t.TempDir(), recordingRunner(&cmds, nil))
	if err := r.Reclaim(context.Background(), BackendNFS, "nfs.example.com:/export/data"); err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("commands = %+v, want mount + umount", cmds)
	}
}
