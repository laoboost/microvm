package adapters

import (
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// ===========================================================================
// Fork-pinned S3 daemon flags (prod incident 2026-09-09):
//   - mount-s3 wastes ~4s per mount probing IMDS for instance throughput on
//     bare metal (IMDS absent) — the daemon pins --maximum-throughput-gbps so
//     detection is skipped; an explicit maximum_throughput_gbps option wins.
//   - workspace mounts need deletes: mountpoint-s3 rejects unlink by default,
//     so agent rm / payload cleanup fails with "Deletes are disabled". The
//     allow_delete structured key pins --allow-delete.
// ===========================================================================

func TestS3Build_PinsMaximumThroughputByDefault(t *testing.T) {
	plan, err := S3{}.Build("sb", 0, models.MountSpec{
		Type:   models.MountTypeS3,
		Source: "s3://bucket/prefix/",
	}, "/mnt/0", t.TempDir())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	assertArgvContains(t, plan.Argv, "--maximum-throughput-gbps", "10")
}

func TestS3Build_ExplicitThroughputOverridesTheDefault(t *testing.T) {
	plan, err := S3{}.Build("sb", 0, models.MountSpec{
		Type:    models.MountTypeS3,
		Source:  "s3://bucket/prefix/",
		Options: map[string]string{"maximum_throughput_gbps": "1"},
	}, "/mnt/0", t.TempDir())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	assertArgvContains(t, plan.Argv, "--maximum-throughput-gbps", "1")
	// The default must not appear twice (mount-s3 rejects duplicate flags).
	count := 0
	for _, a := range plan.Argv {
		if a == "--maximum-throughput-gbps" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("--maximum-throughput-gbps appears %d times, want 1", count)
	}
}

func TestS3Build_AllowDeletePinsDeleteFlag(t *testing.T) {
	plan, err := S3{}.Build("sb", 0, models.MountSpec{
		Type:    models.MountTypeS3,
		Source:  "s3://bucket/prefix/",
		Options: map[string]string{"allow_delete": "true"},
	}, "/mnt/0", t.TempDir())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	assertArgvContains(t, plan.Argv, "--allow-delete")
}

// it does not pin --allow-delete on a read-only mount: nothing there can be
// deleted, and the read-only+delete combination is meaningless noise.
func TestS3Build_AllowDeleteSkippedOnReadOnlyMounts(t *testing.T) {
	plan, err := S3{}.Build("sb", 0, models.MountSpec{
		Type:     models.MountTypeS3,
		Source:   "s3://bucket/prefix/",
		ReadOnly: true,
		Options:  map[string]string{"allow_delete": "true"},
	}, "/mnt/0", t.TempDir())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, a := range plan.Argv {
		if a == "--allow-delete" {
			t.Fatalf("read-only mount must not carry --allow-delete: %v", plan.Argv)
		}
	}
}

func TestS3Build_AllowDeleteAbsentByDefault(t *testing.T) {
	plan, err := S3{}.Build("sb", 0, models.MountSpec{
		Type:   models.MountTypeS3,
		Source: "s3://bucket/prefix/",
	}, "/mnt/0", t.TempDir())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, a := range plan.Argv {
		if a == "--allow-delete" {
			t.Fatalf("--allow-delete must not be pinned without the allow_delete option: %v", plan.Argv)
		}
	}
}

func TestS3ExtraArgs_DenyPinnedThroughputAndDeleteUnderMixedSpec(t *testing.T) {
	_, err := s3ExtraArgsTokens("--maximum-throughput-gbps 5", true)
	if err == nil {
		t.Fatal("--maximum-throughput-gbps must be denied in extra_args under mixed spec")
	}
	_, err = s3ExtraArgsTokens("--allow-delete", true)
	if err == nil {
		t.Fatal("--allow-delete must be denied in extra_args under mixed spec")
	}
}

func assertArgvContains(t *testing.T, argv []string, want ...string) {
	t.Helper()
	for i := 0; i+len(want) <= len(argv); i++ {
		match := true
		for j, w := range want {
			if argv[i+j] != w {
				match = false
				break
			}
		}
		if match {
			return
		}
	}
	t.Fatalf("argv %v does not contain consecutive %v", argv, want)
}
