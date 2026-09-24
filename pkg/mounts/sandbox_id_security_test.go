package mounts

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/models"
	"github.com/aerol-ai/microvm/pkg/mounts/adapters"
)

// sandboxID is joined into host paths and os.RemoveAll'd; without a charset
// check a caller-supplied "../../etc" walks out of RootDir. ValidateSandboxID
// is the gate every per-id path operation must pass first.
func TestValidateSandboxID(t *testing.T) {
	valid := []string{"a", "sb-1", "sandbox_abc-123", strings.Repeat("A", 128)}
	for _, id := range valid {
		if err := ValidateSandboxID(id); err != nil {
			t.Errorf("ValidateSandboxID(%q) = %v, want nil", id, err)
		}
	}
	invalid := []string{
		"",
		"../evil",
		"../../etc",
		"a/b",
		"/abs",
		"sb 1",
		"sb\t1",
		"..",
		".",
		"sb\x00",
		strings.Repeat("a", 129),
	}
	for _, id := range invalid {
		if err := ValidateSandboxID(id); err == nil {
			t.Errorf("ValidateSandboxID(%q) = nil, want error", id)
		}
	}
}

func newIDTestManager(t *testing.T, rootDir string) *Manager {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m, err := New(logger, Config{
		RootDir:     rootDir,
		CredDir:     t.TempDir(),
		WaitTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.adapters = map[models.MountType]adapters.Adapter{models.MountTypeS3: fakeAdapter{}}
	t.Cleanup(m.Close)
	return m
}

func TestMountAllRejectsPathTraversalSandboxID(t *testing.T) {
	// RootDir is nested under t.TempDir so the `../evil` escape target lands
	// inside the test's temp dir and the absence check is hermetic.
	td := t.TempDir()
	rootDir := filepath.Join(td, "mounts")
	m := newIDTestManager(t, rootDir)

	specs := []models.MountSpec{{Type: models.MountTypeS3, Source: "s3://bucket", Target: "/data"}}
	_, err := m.MountAll(context.Background(), "../evil", specs)
	if err == nil {
		t.Fatal("expected error for path-traversal sandbox id")
	}
	if !strings.Contains(err.Error(), "sandbox id") {
		t.Fatalf("error = %v, want sandbox-id validation error", err)
	}
	outside := filepath.Join(td, "evil")
	if _, statErr := os.Stat(outside); !os.IsNotExist(statErr) {
		t.Fatalf("MountAll(%q) created %s outside rootDir (stat err %v)", "../evil", outside, statErr)
	}
}

func TestUnmountAllRejectsPathTraversalSandboxID(t *testing.T) {
	// Nested so the `../../tmp/x` escape target resolves inside t.TempDir.
	td := t.TempDir()
	rootDir := filepath.Join(td, "a", "b")
	m := newIDTestManager(t, rootDir)

	escaped := filepath.Join(td, "tmp", "x")
	if err := os.MkdirAll(escaped, 0o755); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(escaped, "keep")
	if err := os.WriteFile(canary, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := m.UnmountAll("../../tmp/x")
	if err == nil {
		t.Fatal("expected error for path-traversal sandbox id")
	}
	if !strings.Contains(err.Error(), "sandbox id") {
		t.Fatalf("error = %v, want sandbox-id validation error", err)
	}
	if _, statErr := os.Stat(canary); statErr != nil {
		t.Fatalf("UnmountAll(%q) removed %s outside rootDir: %v", "../../tmp/x", canary, statErr)
	}
}

// Sweep derives each per-id path from directory entry names; an entry whose
// name fails the sandbox-id charset is rejected and left alone rather than
// fed to cleanupOrphanDir.
func TestSweepSkipsInvalidSandboxIDEntries(t *testing.T) {
	td := t.TempDir()
	rootDir := filepath.Join(td, "mounts")
	m := newIDTestManager(t, rootDir)

	bad := filepath.Join(rootDir, "bad id!")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	badSentinel := filepath.Join(bad, "keep")
	if err := os.WriteFile(badSentinel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(rootDir, "sb-good")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}

	m.Sweep(map[string]struct{}{})

	if _, err := os.Stat(badSentinel); err != nil {
		t.Fatalf("sweep touched invalid-id dir %q: %v", "bad id!", err)
	}
	if _, err := os.Stat(good); !os.IsNotExist(err) {
		t.Fatalf("sweep did not remove valid orphan sb-good (stat err %v)", err)
	}
}
