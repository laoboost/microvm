package readyproto

import (
	"io"
	"strings"
	"testing"
)

// endlessReader serves one byte forever and counts how many bytes the parser
// actually pulled — the proxy for "did it buffer the whole stream".
type endlessReader struct {
	n int64
}

func (e *endlessReader) Read(p []byte) (int, error) {
	e.n += int64(len(p))
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

// Regression: readLine used br.ReadBytes('\n'), which allocates BEFORE the
// MaxLineBytes check — a newline-free stream from the world-writable ready
// socket could OOM sandboxd. The parser must reject at the cap without
// buffering the stream.
func TestReadLineRejectsHugeStreamWithoutBufferingIt(t *testing.T) {
	src := &endlessReader{}
	// 64 MiB ceiling on the wire; the parser must bail far earlier.
	r := io.LimitReader(src, 64<<20)

	_, err := readLine(r)
	if err == nil {
		t.Fatal("expected limit error for newline-free stream")
	}
	if !strings.Contains(err.Error(), "line exceeds") {
		t.Fatalf("expected line-exceeds limit error, got %v", err)
	}
	// Allow a couple of bufio buffers of look-ahead, but nowhere near the
	// stream: the old ReadBytes path pulled all 64 MiB before rejecting.
	const maxConsumed = 256 << 10
	if src.n > maxConsumed {
		t.Fatalf("parser consumed %d bytes before rejecting, want <= %d (unbounded buffering)", src.n, maxConsumed)
	}
}
