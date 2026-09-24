package oci

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skopeo accepts many transports (oci-archive:, dir:, docker-archive:, …).
// ImageRef is tenant-derived: only docker:// may be passed through, otherwise
// the daemon reads local archive paths and pulls from arbitrary registries on
// the host's network.
func TestBuild_RejectsNonDockerImageRefTransport(t *testing.T) {
	cfg, _ := happyConfig(t)
	b, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, ref := range []string{
		"oci-archive:/etc/passwd",
		"dir:/var/lib/secrets",
		"docker-archive:/tmp/img.tar",
		"oci:/tmp/layout",
	} {
		_, err := b.Build(context.Background(), BuildRequest{ImageRef: ref, OutPath: filepath.Join(t.TempDir(), "out.ext4")})
		if err == nil {
			t.Errorf("ImageRef %q must be rejected (non-docker:// transport)", ref)
		}
	}
}

func TestBuild_RejectsUnsafeTag(t *testing.T) {
	cfg, _ := happyConfig(t)
	b, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out := filepath.Join(t.TempDir(), "out.ext4")
	for _, tag := range []string{"$(id)", "a/b", "-rf", "tag with space", "tag:colon"} {
		_, err := b.Build(context.Background(), BuildRequest{ImageRef: "docker://alpine:3.20", Tag: tag, OutPath: out})
		if err == nil {
			t.Errorf("Tag %q must be rejected", tag)
		}
	}
}

// --insecure-policy silently disables skopeo's signature policy for every
// pull. The builder must instead pass an explicit --policy file (auditable
// and replaceable by the operator).
func TestRunSkopeoUsesExplicitPolicyFile(t *testing.T) {
	dir := t.TempDir()
	bins := filepath.Join(dir, "bins")
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(bins, 0o755); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(dir, "skopeo-args")
	skopeo := writeFake(t, bins, "skopeo", `printf '%s\n' "$@" > "`+argsFile+`"`)
	cfg := Config{SkopeoBin: skopeo, UmociBin: writeFake(t, bins, "umoci", `mkdir -p "$4/rootfs" 2>/dev/null; true`), Mkfs4Bin: writeFake(t, bins, "mkfs.ext4", `printf 'x' > "$2"`), WorkDir: work}
	b, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = b.runSkopeo(context.Background(), "docker://alpine:3.20", filepath.Join(dir, "oci"), "latest")
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("skopeo was not invoked: %v", err)
	}
	args := string(raw)
	if strings.Contains(args, "--insecure-policy") {
		t.Fatalf("skopeo must not receive --insecure-policy; args:\n%s", args)
	}
	if !strings.Contains(args, "--policy") {
		t.Fatalf("skopeo must receive an explicit --policy file; args:\n%s", args)
	}
}
