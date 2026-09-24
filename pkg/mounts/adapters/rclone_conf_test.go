package adapters

import (
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// A tenant-supplied rclone.conf runs through `rclone mount --config` as root.
// A conf that defines type=local/alias/... backends maps remote paths onto the
// HOST filesystem (source "x:/etc" → host /etc inside the sandbox), so Build
// must reject deny-listed remote types and require the source remote to exist
// in the conf with an allowed type.
func TestRcloneBuild_RejectsConfWithDenyListedRemoteType(t *testing.T) {
	for _, remoteType := range []string{"local", "alias", "combine", "chunker", "crypt", "LOCAL"} {
		_, err := (Rclone{}).Build("sb", 0, models.MountSpec{
			Source:      "x:/etc",
			Credentials: map[string]string{"rclone_conf": "[x]\ntype = " + remoteType + "\n"},
		}, "/mnt/rclone", "/creds")
		if err == nil {
			t.Errorf("expected error for rclone remote type %q", remoteType)
		}
	}
}

func TestRcloneBuild_AllowsS3BackendConf(t *testing.T) {
	plan, err := (Rclone{}).Build("sb", 0, models.MountSpec{
		Source:      "x:bucket/path",
		Credentials: map[string]string{"rclone_conf": "[x]\ntype = s3\n"},
	}, "/mnt/rclone", "/creds")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !contains(plan.Argv, "x:bucket/path") {
		t.Fatalf("argv missing source: %v", plan.Argv)
	}
}

func TestRcloneBuild_RejectsSourceWithUndefinedRemote(t *testing.T) {
	_, err := (Rclone{}).Build("sb", 0, models.MountSpec{
		Source:      "nope:/etc",
		Credentials: map[string]string{"rclone_conf": "[x]\ntype = s3\n"},
	}, "/mnt/rclone", "/creds")
	if err == nil {
		t.Fatal("expected error for source referencing an undefined remote")
	}
}

// The deny-list applies to the whole conf, not just the mounted remote:
// otherwise `[u] type=union, remotes=x:/` + `[x] type=local` reaches host
// paths through a non-deny-listed wrapper remote.
func TestRcloneBuild_RejectsConfWithUnusedDenyListedRemote(t *testing.T) {
	conf := "[good]\ntype = s3\n[evil]\ntype = local\n"
	_, err := (Rclone{}).Build("sb", 0, models.MountSpec{
		Source:      "good:bucket/path",
		Credentials: map[string]string{"rclone_conf": conf},
	}, "/mnt/rclone", "/creds")
	if err == nil {
		t.Fatal("expected error for conf containing a type=local remote")
	}
	if err != nil && !strings.Contains(err.Error(), "evil") {
		t.Fatalf("error should name the offending remote, got: %v", err)
	}
}

// Each conf below hand-rolls a spelling the old blocklist parser missed while
// the real rclone (goconfig) still executes it — every one maps a remote path
// onto the HOST filesystem. Build must reject all of them.
func TestRcloneBuild_RejectsEveryHostFsBypassSpelling(t *testing.T) {
	cases := []struct {
		name   string
		conf   string
		source string
	}{
		{"union_upstreams", "[x]\ntype = union\nupstreams = /\n", "x:/"},
		{"backtick_local", "[x]\ntype = `local`\n", "x:/etc"},
		{"triple_quoted_local", "[x]\ntype = \"\"\"local\"\"\"\n", "x:/etc"},
		{"quoted_key_decoy", "[x]\ntype = s3\n\"type\" = local\n", "x:bucket/p"},
		{"colon_separator_decoy", "[x]\ntype = s3\ntype: local\n", "x:bucket/p"},
		{"alias_remote_field", "[x]\ntype = alias\nremote = /etc\n", "x:/"},
		{"sftp_remote_field", "[x]\ntype = sftp\nremote = /etc\n", "x:bucket/p"},
		{"duplicate_type_key", "[x]\ntype = s3\ntype = local\n", "x:bucket/p"},
		{"duplicate_section", "[x]\ntype = s3\n[x]\ntype = local\n", "x:bucket/p"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (Rclone{}).Build("sb", 0, models.MountSpec{
				Source:      tc.source,
				Credentials: map[string]string{"rclone_conf": tc.conf},
			}, "/mnt/rclone", "/creds")
			if err == nil {
				t.Fatalf("Build accepted host-FS-bypass conf %q", tc.conf)
			}
		})
	}
}

// A plain network-only conf with several remotes keeps working — the strict
// parser must not reject ordinary rclone.conf syntax.
func TestRcloneBuild_AllowsMultiRemoteNetworkConf(t *testing.T) {
	conf := "[one]\ntype = s3\nprovider = AWS\n\n[two]\ntype = sftp\nhost = example.com\nuser = me\n"
	plan, err := (Rclone{}).Build("sb", 0, models.MountSpec{
		Source:      "one:bucket/prefix",
		Credentials: map[string]string{"rclone_conf": conf},
	}, "/mnt/rclone", "/creds")
	if err != nil {
		t.Fatalf("Build rejected a valid network-only conf: %v", err)
	}
	if !contains(plan.Argv, "one:bucket/prefix") {
		t.Fatalf("argv missing source: %v", plan.Argv)
	}
}
