package wasm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// writeLegacyV1Snapshot lays down a schema-1 artifact the way a pre-upgrade
// build did: no globals_checksum field. Everything else is valid.
func writeLegacyV1Snapshot(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mem := []byte("memory-bytes")
	globals := []byte(`[]`)
	wasi := []byte("{}")
	memZ, err := zstdCompress(mem)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	cfg := SnapshotConfig{
		SchemaVersion:     1,
		Engine:            engineWazero,
		EngineVersion:     "wazero",
		WASIVersion:       wasiPreview1,
		BaseModule:        SnapshotBaseModule{Digest: "sha256:abc", Size: 1},
		Entrypoint:        "_start",
		Durability:        models.DurabilityPassivatable,
		MemoryChecksum:    checksumPrefixed(mem),
		WASIStateChecksum: checksumPrefixed(wasi),
		GlobalsCount:      countGlobals(globals),
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, configFileName), raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, memoryFileName), memZ, 0o600); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, globalsFileName), globals, 0o600); err != nil {
		t.Fatalf("write globals: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, wasiStateFileName), wasi, 0o600); err != nil {
		t.Fatalf("write wasi state: %v", err)
	}
}

// TestReadSnapshotDirAcceptsLegacyV1WithoutGlobalsChecksum pins F2d: every
// durable checkpoint written by the previous build is schema 1 with no
// globals_checksum. Requiring the field while the schema stayed at 1 made a
// rolling upgrade reject all of them as corrupt.
func TestReadSnapshotDirAcceptsLegacyV1WithoutGlobalsChecksum(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mem.snap")
	writeLegacyV1Snapshot(t, dir)

	got, err := ReadSnapshotDir(dir, engineWazero)
	if err != nil {
		t.Fatalf("legacy v1 artifact rejected after upgrade: %v", err)
	}
	if string(got.Memory) != "memory-bytes" {
		t.Fatalf("memory = %q", got.Memory)
	}
	if got.Config.SchemaVersion != 1 {
		t.Fatalf("schema = %d, want the artifact's 1", got.Config.SchemaVersion)
	}
}

// TestReadSnapshotDirStillRejectsCorruptV2Globals pins the other side: a
// current (v2) artifact with a tampered globals layer or a missing checksum is
// still a corrupt artifact.
func TestReadSnapshotDirStillRejectsCorruptV2Globals(t *testing.T) {
	t.Run("corrupted_globals", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "mem.snap")
		cap := SnapshotCapture{
			Memory:    []byte("mem"),
			Globals:   []byte(`[1,2,3]`),
			WASIState: []byte("{}"),
		}
		if err := WriteSnapshotDir(dir, cap); err != nil {
			t.Fatalf("WriteSnapshotDir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, globalsFileName), []byte(`["a","b","c"]`), 0o600); err != nil {
			t.Fatalf("rewrite globals: %v", err)
		}
		if _, err := ReadSnapshotDir(dir, engineWazero); !errors.Is(err, models.ErrSnapshotCorrupt) {
			t.Fatalf("ReadSnapshotDir = %v, want ErrSnapshotCorrupt", err)
		}
	})

	t.Run("missing_checksum", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "mem.snap")
		cap := SnapshotCapture{Memory: []byte("mem"), Globals: []byte(`[]`), WASIState: []byte("{}")}
		if err := WriteSnapshotDir(dir, cap); err != nil {
			t.Fatalf("WriteSnapshotDir: %v", err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, configFileName))
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		var cfg SnapshotConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("decode config: %v", err)
		}
		if cfg.SchemaVersion != snapshotSchemaVersion {
			t.Fatalf("fresh artifact schema = %d, want %d", cfg.SchemaVersion, snapshotSchemaVersion)
		}
		cfg.GlobalsChecksum = ""
		raw, _ = json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, configFileName), raw, 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		if _, err := ReadSnapshotDir(dir, engineWazero); !errors.Is(err, models.ErrSnapshotCorrupt) {
			t.Fatalf("v2 without globals checksum = %v, want ErrSnapshotCorrupt", err)
		}
	})
}
