package wasmmod

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pushed manifest's artifact and layer media types must match the current
// artifact schema (v2); a v2 config must not be advertised under v1 types.
func TestSnapshotArtifactMediaTypesAreV2(t *testing.T) {
	if got := wasmSnapshotArtifactType; got != "application/vnd.aerolvm.wasm-snapshot.v2" {
		t.Fatalf("wasmSnapshotArtifactType = %q, want v2", got)
	}
	want := map[string]string{
		"config.json":     "application/vnd.aerolvm.wasm-snapshot.v2+json",
		"memory.zstd":     "application/vnd.aerolvm.wasm-snapshot.v2.memory.zstd",
		"globals.cbor":    "application/vnd.aerolvm.wasm-snapshot.v2.globals.cbor",
		"wasi-state.cbor": "application/vnd.aerolvm.wasm-snapshot.v2.wasi-state.cbor",
	}
	if !maps.Equal(snapshotLayerMediaTypes, want) {
		t.Fatalf("snapshotLayerMediaTypes = %v, want %v", snapshotLayerMediaTypes, want)
	}
}

// TestPullSnapshotArtifactAcceptsLegacyV1MediaTypes pins the compatibility
// side of the bump: an artifact pushed under the legacy v1 media types must
// still pull (the read path unpacks layers by name and ignores media type), so
// a rolling upgrade does not lose in-flight checkpoints.
func TestPullSnapshotArtifactAcceptsLegacyV1MediaTypes(t *testing.T) {
	reg := startTestOCIRegistry(t, "cluster/wasm-checkpoints/legacy")
	defer reg.close()

	patFile := filepath.Join(t.TempDir(), "pat")
	if err := os.WriteFile(patFile, []byte("cluster-pat"), 0o600); err != nil {
		t.Fatal(err)
	}
	pushCfg := ORASPushConfig{Host: "ignored", ClusterID: "cluster-1", PATPath: patFile}
	pullCfg := ORASPullConfig{Host: "ignored", ClusterID: "cluster-1", PATPath: patFile}
	ref := reg.ref("latest")
	ctx := context.Background()

	if _, err := PushSnapshotArtifact(ctx, pushCfg, writeTestSnapshotDir(t), ref); err != nil {
		t.Fatalf("push: %v", err)
	}

	// Rewrite the stored manifest's media types down to v1 to model an
	// artifact a pre-bump build pushed.
	_, rest, _ := strings.Cut(reg.baseRef, "/")
	reg.mu.Lock()
	body := string(reg.manifests[rest+"/latest"])
	reg.mu.Unlock()
	if body == "" {
		t.Fatal("pushed manifest not found in test registry")
	}
	legacy := strings.ReplaceAll(body, "wasm-snapshot.v2", "wasm-snapshot.v1")
	if legacy == body {
		t.Fatal("manifest did not contain v2 media types to rewrite")
	}
	reg.setManifest("latest", []byte(legacy))

	dstDir := t.TempDir()
	if err := PullSnapshotArtifact(ctx, pullCfg, ref, dstDir); err != nil {
		t.Fatalf("legacy v1-media-type artifact failed to pull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "config.json")); err != nil {
		t.Fatalf("restored artifact missing config.json: %v", err)
	}
}
