package adapters

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

func TestAdapters_DefaultMap(t *testing.T) {
	a := Adapters()
	if len(a) != 4 {
		t.Fatalf("len(Adapters) = %d, want 4", len(a))
	}
	if _, ok := a[models.MountTypeS3]; !ok {
		t.Fatal("missing S3 adapter")
	}
	if _, ok := a[models.MountTypeNFS]; !ok {
		t.Fatal("missing NFS adapter")
	}
	if _, ok := a[models.MountTypeSSHFS]; !ok {
		t.Fatal("missing SSHFS adapter")
	}
	if _, ok := a[models.MountTypeRclone]; !ok {
		t.Fatal("missing Rclone adapter")
	}
}

func TestS3Build(t *testing.T) {
	plan, err := (S3{}).Build("sb-1", 3, models.MountSpec{
		Source:   "s3://bucket/prefix/sub",
		ReadOnly: true,
		Credentials: map[string]string{
			"access_key_id":     "AKIA...",
			"secret_access_key": "secret",
			"session_token":     "token",
		},
		Options: map[string]string{
			"region":     "us-east-1",
			"endpoint":   "https://s3.example.com",
			"extra_args": "--allow-delete --uid 1000",
		},
	}, "/mnt/target", "/creds")
	if err != nil {
		t.Fatalf("S3.Build: %v", err)
	}
	// mountpoint-s3 requires --prefix to end in '/', so the adapter appends one.
	wantPrefix := []string{"mount-s3", "bucket", "/mnt/target", "--foreground", "--profile", "sandbox", "--prefix", "prefix/sub/"}
	if !reflect.DeepEqual(plan.Argv[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("argv prefix mismatch: got=%v want=%v", plan.Argv[:len(wantPrefix)], wantPrefix)
	}
	if !contains(plan.Argv, "--region") || !contains(plan.Argv, "us-east-1") {
		t.Fatalf("argv missing region: %v", plan.Argv)
	}
	if !contains(plan.Argv, "--endpoint-url") || !contains(plan.Argv, "https://s3.example.com") {
		t.Fatalf("argv missing endpoint: %v", plan.Argv)
	}
	if !contains(plan.Argv, "--read-only") || !contains(plan.Argv, "--allow-delete") || !contains(plan.Argv, "--uid") || !contains(plan.Argv, "1000") {
		t.Fatalf("argv missing readonly/extra args: %v", plan.Argv)
	}
	if plan.CredFile != "/creds/sb-1-3.aws" {
		t.Fatalf("CredFile = %q, want /creds/sb-1-3.aws", plan.CredFile)
	}
	if !plan.UnlinkCred {
		t.Fatal("UnlinkCred = false, want true")
	}
	if !contains(plan.Env, "AWS_PROFILE=sandbox") {
		t.Fatalf("env missing profile: %v", plan.Env)
	}
	if !contains(plan.Env, "AWS_SHARED_CREDENTIALS_FILE=/creds/sb-1-3.aws") {
		t.Fatalf("env missing credentials path: %v", plan.Env)
	}
	cred := string(plan.CredBody)
	if !strings.Contains(cred, "[sandbox]") || !strings.Contains(cred, "aws_access_key_id = AKIA...") || !strings.Contains(cred, "aws_secret_access_key = secret") || !strings.Contains(cred, "aws_session_token = token") {
		t.Fatalf("unexpected creds file:\n%s", cred)
	}
}

// With no static keys the operator relies on an ambient instance role; the
// adapter must not pin --profile/AWS_PROFILE to an empty credentials file, which
// would shadow the ambient chain and break the mount.
func TestS3Build_InstanceRole(t *testing.T) {
	plan, err := (S3{}).Build("sb-2", 0, models.MountSpec{
		Source: "s3://bucket/data",
	}, "/mnt/target", "/creds")
	if err != nil {
		t.Fatalf("S3.Build: %v", err)
	}
	if contains(plan.Argv, "--profile") {
		t.Fatalf("argv should omit --profile for instance-role creds: %v", plan.Argv)
	}
	if len(plan.Env) != 0 {
		t.Fatalf("env should be empty for instance-role creds: %v", plan.Env)
	}
	if plan.CredFile != "" || len(plan.CredBody) != 0 || plan.UnlinkCred {
		t.Fatalf("no credentials file expected: %+v", plan)
	}
}

func TestS3Build_MissingBucket(t *testing.T) {
	if _, err := (S3{}).Build("sb", 0, models.MountSpec{Source: "s3://"}, "/mnt", "/creds"); err == nil {
		t.Fatal("expected error for missing bucket")
	}
}

// TestS3Build_PrefixTrailingSlash is the regression guard for cluster-hetero
// UC-81..84: mountpoint-s3 rejects a --prefix that doesn't end in '/'
// ("error: invalid value '…' for '--prefix': prefix must end in '/'"), so the
// adapter must normalize a non-empty prefix to end in exactly one slash and
// carry no leading slash — whatever shape the volume key arrives in.
func TestS3Build_PrefixTrailingSlash(t *testing.T) {
	cases := map[string]string{
		"s3://bucket/team/data":  "team/data/", // no trailing slash -> add one
		"s3://bucket/team/data/": "team/data/", // already has one -> unchanged
	}
	for source, want := range cases {
		plan, err := (S3{}).Build("sb-x", 0, models.MountSpec{Source: source}, "/mnt/t", "/creds")
		if err != nil {
			t.Fatalf("Build(%q): %v", source, err)
		}
		got := ""
		for i, a := range plan.Argv {
			if a == "--prefix" && i+1 < len(plan.Argv) {
				got = plan.Argv[i+1]
			}
		}
		if !strings.HasSuffix(got, "/") {
			t.Errorf("Build(%q): --prefix %q does not end in '/'", source, got)
		}
		if got != want {
			t.Errorf("Build(%q): --prefix = %q, want %q", source, got, want)
		}
	}

	// A bucket-only source has no prefix, so mount-s3 must receive NO --prefix
	// flag at all — emitting an empty "--prefix ''" would itself be rejected.
	plan, err := (S3{}).Build("sb-x", 0, models.MountSpec{Source: "s3://bucket-only"}, "/mnt/t", "/creds")
	if err != nil {
		t.Fatalf("Build(bucket-only): %v", err)
	}
	if contains(plan.Argv, "--prefix") {
		t.Errorf("bucket-only source emitted a --prefix flag: %v", plan.Argv)
	}
}

func TestParseS3Source(t *testing.T) {
	bucket, prefix := parseS3Source(" s3://my-bucket/a/b ")
	if bucket != "my-bucket" || prefix != "a/b" {
		t.Fatalf("parseS3Source mismatch: (%q,%q)", bucket, prefix)
	}
	bucket, prefix = parseS3Source("bucket-only")
	if bucket != "bucket-only" || prefix != "" {
		t.Fatalf("parseS3Source bucket-only mismatch: (%q,%q)", bucket, prefix)
	}
}

func TestNFSBuild(t *testing.T) {
	plan, err := (NFS{}).Build("sb", 0, models.MountSpec{Source: "10.0.0.2:/exports/data", ReadOnly: true}, "/mnt/nfs", "/creds")
	if err != nil {
		t.Fatalf("NFS.Build: %v", err)
	}
	want := []string{"mount", "-t", "nfs", "-o", "ro", "10.0.0.2:/exports/data", "/mnt/nfs"}
	if !reflect.DeepEqual(plan.Argv, want) {
		t.Fatalf("NFS argv mismatch: got=%v want=%v", plan.Argv, want)
	}
	if !plan.IsKernelMount {
		t.Fatal("IsKernelMount = false, want true")
	}

	plan, err = (NFS{}).Build("sb", 0, models.MountSpec{Source: "10.0.0.2:/exports/data", ReadOnly: true, Options: map[string]string{"opts": "rw,vers=4"}}, "/mnt/nfs", "/creds")
	if err != nil {
		t.Fatalf("NFS.Build with opts: %v", err)
	}
	if got := plan.Argv[4]; got != "rw,vers=4,ro" {
		t.Fatalf("NFS opts = %q, want rw,vers=4,ro", got)
	}
}

func TestSSHFSBuild(t *testing.T) {
	plan, err := (SSHFS{}).Build("sb-1", 1, models.MountSpec{
		Source:   "user@example.com:/home/user",
		ReadOnly: true,
		Credentials: map[string]string{
			"private_key_pem": "-----BEGIN PRIVATE KEY-----\n...",
		},
	}, "/mnt/ssh", "/creds")
	if err != nil {
		t.Fatalf("SSHFS.Build: %v", err)
	}
	if plan.CredFile != "/creds/sb-1-1.id" {
		t.Fatalf("CredFile = %q, want /creds/sb-1-1.id", plan.CredFile)
	}
	if plan.UnlinkCred {
		t.Fatal("UnlinkCred = true, want false")
	}
	if !contains(plan.Argv, "sshfs") || !contains(plan.Argv, "/mnt/ssh") {
		t.Fatalf("argv missing sshfs pieces: %v", plan.Argv)
	}
	if !strings.Contains(plan.Argv[2], "IdentityFile=/creds/sb-1-1.id") || !strings.Contains(plan.Argv[2], "ro") {
		t.Fatalf("sshfs opts missing identity or ro: %q", plan.Argv[2])
	}

	if _, err := (SSHFS{}).Build("sb", 0, models.MountSpec{Source: "x"}, "/mnt", "/creds"); err == nil {
		t.Fatal("expected missing key error")
	}
}

func TestRcloneBuild(t *testing.T) {
	plan, err := (Rclone{}).Build("sb-9", 2, models.MountSpec{
		Source:   "remote:bucket/path",
		ReadOnly: true,
		Credentials: map[string]string{
			"rclone_conf": "[remote]\ntype = s3\n",
		},
		Options: map[string]string{"vfs_cache_mode": "full"},
	}, "/mnt/rclone", "/creds")
	if err != nil {
		t.Fatalf("Rclone.Build: %v", err)
	}
	if plan.CredFile != "/creds/sb-9-2.rclone.conf" {
		t.Fatalf("CredFile = %q, want /creds/sb-9-2.rclone.conf", plan.CredFile)
	}
	if !plan.UnlinkCred {
		t.Fatal("UnlinkCred = false, want true")
	}
	if !contains(plan.Argv, "--vfs-cache-mode") || !contains(plan.Argv, "full") || !contains(plan.Argv, "--read-only") {
		t.Fatalf("argv missing rclone options: %v", plan.Argv)
	}

	plan, err = (Rclone{}).Build("sb-9", 2, models.MountSpec{
		Source:      "remote:bucket/path",
		Credentials: map[string]string{"rclone_conf": "[remote]\ntype = s3\n"},
	}, "/mnt/rclone", "/creds")
	if err != nil {
		t.Fatalf("Rclone.Build default cache mode: %v", err)
	}
	if !contains(plan.Argv, "writes") {
		t.Fatalf("argv missing default vfs cache mode: %v", plan.Argv)
	}

	if _, err := (Rclone{}).Build("sb", 0, models.MountSpec{Source: "remote:x"}, "/mnt", "/creds"); err == nil {
		t.Fatal("expected missing rclone conf error")
	}
}

// A tenant-supplied source starting with '-' would land on the mount tool's
// argv and could be parsed as a flag (e.g. sshfs -oProxyCommand=...), so the
// adapter itself must reject it even if a spec skipped spec validation.
func TestSSHFSBuild_RejectsDashPrefixedSource(t *testing.T) {
	_, err := (SSHFS{}).Build("sb", 0, models.MountSpec{
		Source:      "-oProxyCommand=sh -c evil",
		Credentials: map[string]string{"private_key_pem": "key"},
	}, "/mnt/ssh", "/creds")
	if err == nil {
		t.Fatal("expected error for dash-prefixed sshfs source")
	}
}

func TestNFSBuild_RejectsSourceNotInHostColonPathForm(t *testing.T) {
	for _, source := range []string{
		"-oProxyCommand=evil",  // dash-prefixed flag injection
		"/etc/passwd",          // bare local path
		"no-colon-slash",       // not host:/path
		"host:/path extra arg", // trailing argv content
		"",                     // empty
	} {
		_, err := (NFS{}).Build("sb", 0, models.MountSpec{Source: source}, "/mnt/nfs", "/creds")
		if err == nil {
			t.Errorf("expected error for nfs source %q", source)
		}
	}
}

// TestS3Build_RejectsDashPrefixedSource mirrors the sshfs/rclone/nfs guards:
// a source starting with '-' would land on mount-s3's argv as a flag.
// Structured option keys must be emitted as daemon flags BEFORE extra_args,
// and uid/gid must be validated as positive integers (>=1, matching
// mount-s3's value_parser!(u32).range(1..) which rejects 0).
func TestS3Build_EmitsStructuredUIDGidAndAllowFlagsWhenOptionsAreSet(t *testing.T) {
	plan, err := (S3{}).Build("sb", 0, models.MountSpec{
		Source: "s3://bucket/data",
		Options: map[string]string{
			"uid":            "1000",
			"gid":            "1000",
			"allow_other":    "true",
			"allow_overwrite": "1",
			"extra_args":     "--allow-delete",
		},
	}, "/mnt/t", "/creds")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []string{"--uid", "1000", "--gid", "1000", "--allow-other", "--allow-overwrite"}
	for _, w := range want {
		if !contains(plan.Argv, w) {
			t.Errorf("argv missing structured flag %q: %v", w, plan.Argv)
		}
	}
	// Structured flags must precede extra_args.
	if idxExtra := indexOf(plan.Argv, "--allow-delete"); idxExtra >= 0 && idxExtra < indexOf(plan.Argv, "--uid") {
		t.Errorf("structured flags must precede extra_args: %v", plan.Argv)
	}

	// Invalid structured uid/gid values are rejected.
	for _, bad := range []string{"0", "-5", "abc"} {
		for _, key := range []string{"uid", "gid"} {
			opts := map[string]string{key: bad}
			if _, err := (S3{}).Build("sb", 0, models.MountSpec{Source: "s3://bucket", Options: opts}, "/mnt/t", "/creds"); err == nil {
				t.Errorf("expected error for %s=%q", key, bad)
			}
		}
	}
}

// Daemon-pinned flags must be denied in extra_args regardless of argv order:
// mount-s3 (clap, no args_override_self) hard-errors on duplicate flags, so
// the denylist — not ordering — is the only conflict protection.
func TestS3Build_RejectsExtraArgsTokensConflictingWithPrefixEndpointProfileRegionReadOnly(t *testing.T) {
	for _, extra := range []string{
		"--prefix p/",             // space form
		"--endpoint-url=http://x", // = form
		"--profile other",
		"--region eu-west-1",
		"--read-only",
	} {
		_, err := (S3{}).Build("sb", 0, models.MountSpec{
			Source:  "s3://bucket",
			Options: map[string]string{"extra_args": extra},
		}, "/mnt/t", "/creds")
		if err == nil {
			t.Errorf("expected error for extra_args %q", extra)
		}
	}
}

// Mixed spec (structured keys present + uid/gid/allow-* in extra_args) is
// rejected fail-closed with a deprecation warning: otherwise any spec could
// add one structured key to re-enable --uid injection via extra_args.
func TestS3Build_RejectsUidGidAndAllowFlagsInExtraArgsWhenStructuredKeysArePresent(t *testing.T) {
	for _, extra := range []string{
		"--uid=1000",
		"--uid 1000",
		"--gid 1000",
		"--allow-other",
		"--allow-overwrite",
	} {
		_, err := (S3{}).Build("sb", 0, models.MountSpec{
			Source: "s3://bucket",
			Options: map[string]string{
				"uid":        "1000",
				"extra_args": extra,
			},
		}, "/mnt/t", "/creds")
		if err == nil {
			t.Errorf("expected error for mixed-spec extra_args %q", extra)
			continue
		}
		if !strings.Contains(err.Error(), "deprecated") {
			t.Errorf("error for %q should carry a deprecation warning, got: %v", extra, err)
		}
	}
}

func TestS3Build_RejectsDuplicatedDenylistedFlagsWithinExtraArgsAndBareDoubleDashToken(t *testing.T) {
	for _, extra := range []string{
		"--allow-delete --allow-delete",  // duplicate non-denylisted flag
		"--uid 1 --uid 2",                // duplicate uid (legacy, no structured keys)
		"--",                             // bare -- makes clap treat the rest as positionals
		"--allow-delete -- --allow-other", // -- followed by more tokens
	} {
		_, err := (S3{}).Build("sb", 0, models.MountSpec{
			Source:  "s3://bucket",
			Options: map[string]string{"extra_args": extra},
		}, "/mnt/t", "/creds")
		if err == nil {
			t.Errorf("expected error for extra_args %q", extra)
		}
	}
}

// The pre-task-002 subchat shape: uid/gid/allow-* flags arrive via extra_args
// with no structured keys; the daemon must keep accepting them so the window
// between the daemon deploy and the subchat migration stays green.
func TestS3Build_AcceptsLegacyExtraArgsWithUidAndAllowFlagsWhenNoStructuredKeysAreSet(t *testing.T) {
	plan, err := (S3{}).Build("sb", 0, models.MountSpec{
		Source: "s3://bucket/data",
		Options: map[string]string{
			"extra_args": "--allow-other --allow-overwrite --uid=1000 --gid=1000",
		},
	}, "/mnt/t", "/creds")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, want := range []string{"--allow-other", "--allow-overwrite", "--uid=1000", "--gid=1000"} {
		if !contains(plan.Argv, want) {
			t.Errorf("argv missing legacy extra_args token %q: %v", want, plan.Argv)
		}
	}
}

// The exact production subchat options shape must be accepted unchanged.
func TestS3Build_AcceptsProductionSubchatOptionsShapeRegionEndpointAndLegacyExtraArgsUnchanged(t *testing.T) {
	plan, err := (S3{}).Build("sb", 0, models.MountSpec{
		Source: "s3://bucket/data",
		Options: map[string]string{
			"region":     "us-east-1",
			"endpoint":   "http://localhost:9000",
			"extra_args": "--allow-other --allow-overwrite --uid=1000 --gid=1000",
		},
	}, "/mnt/t", "/creds")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, want := range []string{"--region", "us-east-1", "--endpoint-url", "http://localhost:9000", "--allow-other", "--allow-overwrite", "--uid=1000", "--gid=1000"} {
		if !contains(plan.Argv, want) {
			t.Errorf("argv missing %q: %v", want, plan.Argv)
		}
	}
}

func TestS3Build_RejectsDashPrefixedSource(t *testing.T) {
	for _, source := range []string{"-oProxyCommand=evil", "--prefix=evil", "-bucket"} {
		_, err := (S3{}).Build("sb", 0, models.MountSpec{Source: source}, "/mnt/s3", "/creds")
		if err == nil {
			t.Errorf("expected error for dash-prefixed s3 source %q", source)
		}
	}
}

func TestRcloneBuild_RejectsDashPrefixedSource(t *testing.T) {
	_, err := (Rclone{}).Build("sb", 0, models.MountSpec{
		Source:      "-oProxyCommand=evil",
		Credentials: map[string]string{"rclone_conf": "[remote]\ntype = s3\n"},
	}, "/mnt/rclone", "/creds")
	if err == nil {
		t.Fatal("expected error for dash-prefixed rclone source")
	}
}

func contains(items []string, want string) bool {
	return indexOf(items, want) >= 0
}

func indexOf(items []string, want string) int {
	for i, item := range items {
		if item == want {
			return i
		}
	}
	return -1
}
