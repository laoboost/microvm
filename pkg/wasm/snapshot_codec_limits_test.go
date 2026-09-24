package wasm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A crafted mem.snap can be a zstd bomb: a few KB of compressed input that
// expands to gigabytes. The decompressor must enforce an output ceiling so a
// hostile snapshot cannot OOM the daemon. (RED before the fix: the same bomb
// through zstdDecompress was accepted in full — no bound existed.)
func TestZstdDecompressRejectsOversizeOutput(t *testing.T) {
	bomb, err := zstdCompress(bytes.Repeat([]byte{0}, 64<<20))
	if err != nil {
		t.Fatalf("zstdCompress: %v", err)
	}
	if len(bomb) > 1<<20 {
		t.Skipf("bomb compressed to %d bytes; not a useful bomb test", len(bomb))
	}
	out, err := zstdDecompressLimit(bomb, 1<<20)
	if err == nil {
		t.Fatalf("zstdDecompressLimit accepted a %dMiB expansion (got %d bytes) — want a size-limit error", 64, len(out))
	}
	if !strings.Contains(err.Error(), "limit") && !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error should mention the size limit, got: %v", err)
	}
}

func TestZstdDecompressRoundTripsSmallPayload(t *testing.T) {
	in := []byte("snapshot-memory-bytes")
	z, err := zstdCompress(in)
	if err != nil {
		t.Fatalf("zstdCompress: %v", err)
	}
	got, err := zstdDecompress(z)
	if err != nil {
		t.Fatalf("zstdDecompress: %v", err)
	}
	if !bytes.Equal(got, in) {
		t.Fatalf("round trip mismatch: got %q want %q", got, in)
	}
}

// globals.cbor is currently validated only by item COUNT. Two arrays with the
// same length but different contents must be rejected once a checksum is
// recorded and verified — a swapped/corrupted globals blob would otherwise be
// restored into the engine.
func TestReadSnapshotDirRejectsGlobalsContentMismatch(t *testing.T) {
	dir := t.TempDir()
	cap := SnapshotCapture{
		Memory:    []byte("memory-bytes"),
		Globals:   []byte(`[1,2,3]`),
		WASIState: []byte("{}"),
	}
	if err := WriteSnapshotDir(dir, cap); err != nil {
		t.Fatalf("WriteSnapshotDir: %v", err)
	}
	// Same item count (3), different contents.
	if err := os.WriteFile(filepath.Join(dir, globalsFileName), []byte(`["a","b","c"]`), 0o600); err != nil {
		t.Fatalf("rewrite globals: %v", err)
	}
	_, err := ReadSnapshotDir(dir, engineWazero)
	if err == nil {
		t.Fatal("ReadSnapshotDir accepted swapped globals contents — want a checksum/corrupt error")
	}
	if !strings.Contains(err.Error(), "corrupt") && !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("error should be a corruption error, got: %v", err)
	}
}
