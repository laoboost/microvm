package wasm

import (
	"maps"
	"testing"
)

// The advertised AOCR layer media types must carry the same version as the
// artifact schema: a peer distinguishes artifacts by these strings, so a v2
// config inside a v1-typed artifact is a producer/consumer mismatch.
func TestSnapshotMediaTypesAdvertiseCurrentSchema(t *testing.T) {
	want := map[string]string{
		configFileName:    "application/vnd.aerolvm.wasm-snapshot.v2+json",
		memoryFileName:    "application/vnd.aerolvm.wasm-snapshot.v2.memory.zstd",
		globalsFileName:   "application/vnd.aerolvm.wasm-snapshot.v2.globals.cbor",
		wasiStateFileName: "application/vnd.aerolvm.wasm-snapshot.v2.wasi-state.cbor",
	}
	if got := SnapshotMediaTypes(); !maps.Equal(got, want) {
		t.Fatalf("SnapshotMediaTypes() = %v, want %v", got, want)
	}
}
