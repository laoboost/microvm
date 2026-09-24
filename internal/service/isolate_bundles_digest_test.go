package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/internal/store"
	"github.com/aerol-ai/microvm/pkg/jsbundle"
)

// normalizeBundleDigest must reject anything that is not a bare 64-hex
// digest (mirroring jsbundle.asDigest): the digest is joined into a
// filesystem path (jsbundle blobPath), so a traversal payload like
// `../../../etc/something` must die at the API entry — especially for
// unscoped (operator) callers that skip the TenantOwns guard.

// newBundleServiceAt is newBundleService with a caller-chosen store dir, so
// tests can place decoy files at known traversal targets relative to it.
func newBundleServiceAt(t *testing.T, dir string) *Service {
	t.Helper()
	svc, _, _ := newServiceRuntimeHarness(t, &recordingRuntime{})
	svc.cfg.EnableIsolate = true
	bundleStore, err := jsbundle.NewStore(jsbundle.StoreConfig{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetIsolateBundleStore(bundleStore)
	return svc
}

// it rejects path-traversal digests at the API entry
func TestJSBundleRejectsTraversalDigest(t *testing.T) {
	base := t.TempDir()
	// A valid bundle blob OUTSIDE the store, reachable via the traversal
	// digest below: blobPath("../../decoy") = <base>/decoy.json. Before the
	// fix an unscoped GetJSBundle read it back; the store never sees the
	// value after the fix.
	decoy := filepath.Join(base, "decoy.json")
	decoyBody := []byte(`{"main_module":"m","modules":{"m":"x"},"compatibility_date":"2026-01-01"}`)
	if err := os.WriteFile(decoy, decoyBody, 0o600); err != nil {
		t.Fatal(err)
	}
	svc := newBundleServiceAt(t, filepath.Join(base, "bundles"))
	ctx := context.Background() // unscoped: the TenantOwns guard does not apply

	got, err := svc.GetJSBundle(ctx, "../../decoy")
	if err == nil {
		t.Fatalf("GetJSBundle(traversal) read a file outside the bundle store: %+v", got)
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetJSBundle(traversal) = %v, want ErrNotFound (rejected)", err)
	}

	for _, id := range []string{
		"../../../etc/something",
		"sha256:../../../etc/something",
		"..",
		"notadigest",
		strings.Repeat("g", 64), // 64 chars but non-hex
	} {
		if _, err := svc.GetJSBundle(ctx, id); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetJSBundle(%q) = %v, want ErrNotFound (rejected)", id, err)
		}
		if err := svc.DeleteJSBundle(ctx, id); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("DeleteJSBundle(%q) = %v, want ErrNotFound (rejected)", id, err)
		}
	}
	if _, err := os.Stat(decoy); err != nil {
		t.Fatalf("decoy must not be touched: %v", err)
	}
}

// it accepts only 64-hex digests, with or without the sha256: prefix
func TestNormalizeBundleDigest(t *testing.T) {
	hex64 := strings.Repeat("ab", 32)
	got, err := normalizeBundleDigest("sha256:" + hex64)
	if err != nil || got != hex64 {
		t.Fatalf("sha256 form = %q err=%v, want %q", got, err, hex64)
	}
	got, err = normalizeBundleDigest(hex64)
	if err != nil || got != hex64 {
		t.Fatalf("bare form = %q err=%v, want %q", got, err, hex64)
	}
	for _, id := range []string{"", "sha256:", "sha256:short", strings.Repeat("A", 64), "../../../etc/something"} {
		if _, err := normalizeBundleDigest(id); err == nil {
			t.Fatalf("normalizeBundleDigest(%q) = nil error, want rejection", id)
		}
	}
}
