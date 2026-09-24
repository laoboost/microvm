// Snapshot artifact layout and validation per plans/wasm-runtime.md §4.8.1.
package wasm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aerol-ai/microvm/pkg/models"
	"github.com/klauspost/compress/zstd"
)

const (
	// snapshotSchemaVersion is the current artifact schema. v2 added the
	// globals_checksum field.
	snapshotSchemaVersion = 2
	// legacySnapshotSchemaVersion is the pre-globals-checksum schema. Artifacts
	// written by an older build carry no globals_checksum; they are still
	// accepted so a rolling upgrade does not reject every durable checkpoint as
	// corrupt. The checksum is verified only when present (see ReadSnapshotDir).
	legacySnapshotSchemaVersion = 1
	configFileName              = "config.json"
	memoryFileName              = "memory.zstd"
	globalsFileName             = "globals.cbor"
	wasiStateFileName           = "wasi-state.cbor"

	// Media-type version tracks snapshotSchemaVersion: a peer tells artifacts
	// apart by these strings, so a v2 config must not ride inside a v1-typed
	// artifact. The bump to v2 is a producer-side change only — the OCI pull
	// path unpacks layers by name and does not filter on media type, so
	// in-flight v1 artifacts still restore (and ReadSnapshotDir accepts both
	// schema versions).
	mediaConfig    = "application/vnd.aerolvm.wasm-snapshot.v2+json"
	mediaMemory    = "application/vnd.aerolvm.wasm-snapshot.v2.memory.zstd"
	mediaGlobals   = "application/vnd.aerolvm.wasm-snapshot.v2.globals.cbor"
	mediaWASIState = "application/vnd.aerolvm.wasm-snapshot.v2.wasi-state.cbor"
	engineWazero   = "wazero"
	wasiPreview1   = "preview1"
)

// legacyGlobalsChecksumWarnOnce ensures the "legacy v1 artifact without a
// globals checksum" warning is emitted at most once per process, so a fleet
// re-hydrating many old checkpoints does not spam the log.
var legacyGlobalsChecksumWarnOnce sync.Once

// SnapshotConfig is the snapshot config descriptor (§4.8.1). Schema v1 predates
// GlobalsChecksum; v2 requires it.
type SnapshotConfig struct {
	SchemaVersion     int                `json:"schema_version"`
	Engine            string             `json:"engine"`
	EngineVersion     string             `json:"engine_version"`
	WASIVersion       string             `json:"wasi_version"`
	BaseModule        SnapshotBaseModule `json:"base_module"`
	Entrypoint        string             `json:"entrypoint"`
	Durability        string             `json:"durability"`
	CapturedAt        string             `json:"captured_at"`
	CloneGeneration   string             `json:"clone_generation"`
	MemoryChecksum    string             `json:"memory_checksum"`
	WASIStateChecksum string             `json:"wasi_state_checksum"`
	GlobalsChecksum   string             `json:"globals_checksum"`
	GlobalsCount      int                `json:"globals_count"`
}

// SnapshotBaseModule references the compiled module by digest.
type SnapshotBaseModule struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// SnapshotCapture holds the host-boundary state written into mem.snap.
type SnapshotCapture struct {
	Config    SnapshotConfig
	Memory    []byte
	Globals   []byte
	WASIState []byte
}

// SnapshotRestoreInput is validated artifact content for engine reconstruction.
type SnapshotRestoreInput struct {
	Config    SnapshotConfig
	Memory    []byte
	Globals   []byte
	WASIState []byte
}

// WriteSnapshotDir atomically writes a mem.snap directory at dst.
func WriteSnapshotDir(dst string, cap SnapshotCapture) error {
	if cap.Config.SchemaVersion == 0 {
		cap.Config.SchemaVersion = snapshotSchemaVersion
	}
	if cap.Config.Engine == "" {
		cap.Config.Engine = engineWazero
	}
	if cap.Config.WASIVersion == "" {
		cap.Config.WASIVersion = wasiPreview1
	}
	if cap.Config.CapturedAt == "" {
		cap.Config.CapturedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if cap.Globals == nil {
		cap.Globals = []byte("[]")
	}
	if cap.WASIState == nil {
		cap.WASIState = []byte("{}")
	}
	memZ, err := zstdCompress(cap.Memory)
	if err != nil {
		return err
	}
	cap.Config.MemoryChecksum = checksumPrefixed(cap.Memory)
	cap.Config.WASIStateChecksum = checksumPrefixed(cap.WASIState)
	cap.Config.GlobalsChecksum = checksumPrefixed(cap.Globals)
	if cap.Config.GlobalsCount == 0 {
		cap.Config.GlobalsCount = countGlobals(cap.Globals)
	}

	tmpParent := filepath.Dir(dst)
	if err := os.MkdirAll(tmpParent, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(tmpParent, ".mem.snap-*")
	if err != nil {
		return err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }

	cfgBytes, err := json.MarshalIndent(cap.Config, "", "  ")
	if err != nil {
		cleanup()
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, configFileName), cfgBytes, 0o600); err != nil {
		cleanup()
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, memoryFileName), memZ, 0o600); err != nil {
		cleanup()
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, globalsFileName), cap.Globals, 0o600); err != nil {
		cleanup()
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, wasiStateFileName), cap.WASIState, 0o600); err != nil {
		cleanup()
		return err
	}
	_ = os.RemoveAll(dst)
	if err := os.Rename(tmp, dst); err != nil {
		cleanup()
		return err
	}
	return nil
}

// ReadSnapshotDir loads and validates a mem.snap directory.
func ReadSnapshotDir(dir string, runningEngine string) (SnapshotRestoreInput, error) {
	cfgBytes, err := os.ReadFile(filepath.Join(dir, configFileName))
	if err != nil {
		return SnapshotRestoreInput{}, snapshotCorrupt(fmt.Errorf("read config: %w", err))
	}
	var cfg SnapshotConfig
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		return SnapshotRestoreInput{}, snapshotCorrupt(fmt.Errorf("decode config: %w", err))
	}
	if cfg.SchemaVersion != snapshotSchemaVersion && cfg.SchemaVersion != legacySnapshotSchemaVersion {
		return SnapshotRestoreInput{}, snapshotCorruptf("unsupported schema_version %d", cfg.SchemaVersion)
	}
	if runningEngine == "" {
		runningEngine = engineWazero
	}
	if cfg.Engine != runningEngine {
		return SnapshotRestoreInput{}, snapshotCorruptf("engine mismatch: artifact=%s running=%s", cfg.Engine, runningEngine)
	}
	memZ, err := os.ReadFile(filepath.Join(dir, memoryFileName))
	if err != nil {
		return SnapshotRestoreInput{}, snapshotCorrupt(fmt.Errorf("read memory layer: %w", err))
	}
	mem, err := zstdDecompress(memZ)
	if err != nil {
		return SnapshotRestoreInput{}, snapshotCorrupt(fmt.Errorf("decompress memory: %w", err))
	}
	if got := checksumPrefixed(mem); got != strings.TrimSpace(cfg.MemoryChecksum) {
		return SnapshotRestoreInput{}, snapshotCorruptf("memory checksum mismatch")
	}
	globals, err := os.ReadFile(filepath.Join(dir, globalsFileName))
	if err != nil {
		return SnapshotRestoreInput{}, snapshotCorrupt(fmt.Errorf("read globals: %w", err))
	}
	// Globals are integrity-checked like memory/WASI state. v2 requires the
	// checksum: a missing field there is a corrupt artifact, not a legacy one
	// (snapshots arrive from the untrusted push path). A v1 artifact predates
	// the field, so its checksum is verified only when present — otherwise we
	// warn once and fall back to the item-count check below.
	switch want := strings.TrimSpace(cfg.GlobalsChecksum); {
	case want != "":
		if got := checksumPrefixed(globals); got != want {
			return SnapshotRestoreInput{}, snapshotCorruptf("globals checksum mismatch")
		}
	case cfg.SchemaVersion >= snapshotSchemaVersion:
		return SnapshotRestoreInput{}, snapshotCorruptf("globals checksum missing")
	default:
		legacyGlobalsChecksumWarnOnce.Do(func() {
			log.Printf("wasm snapshot: accepting legacy schema v1 artifact without globals_checksum; globals integrity is unverifiable")
		})
	}
	wasi, err := os.ReadFile(filepath.Join(dir, wasiStateFileName))
	if err != nil {
		return SnapshotRestoreInput{}, snapshotCorrupt(fmt.Errorf("read wasi state: %w", err))
	}
	if got := checksumPrefixed(wasi); got != strings.TrimSpace(cfg.WASIStateChecksum) {
		return SnapshotRestoreInput{}, snapshotCorruptf("wasi state checksum mismatch")
	}
	if cfg.GlobalsCount != countGlobals(globals) {
		return SnapshotRestoreInput{}, snapshotCorruptf("globals_count mismatch")
	}
	return SnapshotRestoreInput{
		Config:    cfg,
		Memory:    mem,
		Globals:   globals,
		WASIState: wasi,
	}, nil
}

// FenceCloneGeneration rejects snapshots older than the store row token.
func FenceCloneGeneration(rowGen, snapshotGen string) error {
	rowGen = strings.TrimSpace(rowGen)
	snapshotGen = strings.TrimSpace(snapshotGen)
	if rowGen == "" || snapshotGen == "" || rowGen == snapshotGen {
		return nil
	}
	return fmt.Errorf("clone generation fenced (row=%s snapshot=%s): %w", rowGen, snapshotGen, models.ErrSnapshotFenced)
}

func checksumPrefixed(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func zstdCompress(src []byte) ([]byte, error) {
	var enc *zstd.Encoder
	var err error
	if len(src) == 0 {
		return []byte{}, nil
	}
	enc, err = zstd.NewWriter(nil)
	if err != nil {
		return nil, err
	}
	defer enc.Close()
	return enc.EncodeAll(src, make([]byte, 0, len(src)/2)), nil
}

func zstdDecompress(src []byte) ([]byte, error) {
	return zstdDecompressLimit(src, maxSnapshotDecodedBytes)
}

// maxSnapshotCompressedBytes / maxSnapshotDecodedBytes bound snapshot layer
// decompression. Snapshots arrive over the untrusted push path; a crafted
// frame can be a zstd bomb (a few KB expanding to gigabytes) and would OOM
// the daemon without a hard ceiling. 1 GiB covers a 1-GiB wasm linear memory
// with headroom, well below the host's OOM threshold when the daemon also
// hosts other sandboxes.
const (
	maxSnapshotCompressedBytes = 1 << 30
	maxSnapshotDecodedBytes    = 1 << 30
)

func zstdDecompressLimit(src []byte, maxOut int64) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	if len(src) > maxSnapshotCompressedBytes {
		return nil, fmt.Errorf("zstd: compressed input of %d bytes exceeds limit %d", len(src), maxSnapshotCompressedBytes)
	}
	dec, err := zstd.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(dec, maxOut+1))
	if err != nil {
		return nil, err
	}
	if n > maxOut {
		return nil, fmt.Errorf("zstd: decompressed output exceeds limit %d bytes", maxOut)
	}
	return buf.Bytes(), nil
}

func countGlobals(b []byte) int {
	var items []json.RawMessage
	if err := json.Unmarshal(b, &items); err != nil {
		return 0
	}
	return len(items)
}

func snapshotCorrupt(err error) error {
	return fmt.Errorf("%w: %w", err, models.ErrSnapshotCorrupt)
}

func snapshotCorruptf(format string, args ...any) error {
	return snapshotCorrupt(fmt.Errorf(format, args...))
}

// SnapshotMediaTypes documents the current (v2) layer media types for AOCR
// push. v1-typed artifacts written by an older build are still accepted on
// read: the pull path unpacks layers by filename and never inspects the media
// type, and ReadSnapshotDir accepts both schema versions.
func SnapshotMediaTypes() map[string]string {
	return map[string]string{
		configFileName:    mediaConfig,
		memoryFileName:    mediaMemory,
		globalsFileName:   mediaGlobals,
		wasiStateFileName: mediaWASIState,
	}
}

// ErrEmptySnapshotDir is returned when a checkpoint path is missing on disk.
var ErrEmptySnapshotDir = errors.New("snapshot directory missing")

// DirExists reports whether dir looks like a mem.snap artifact.
func DirExists(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	st, err := os.Stat(filepath.Join(dir, configFileName))
	return err == nil && !st.IsDir()
}
