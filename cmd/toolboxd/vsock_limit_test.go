package main

import (
	"context"
	"io"
	"testing"
	"time"
)

// countingEndless serves one byte forever and counts bytes pulled upstream.
type countingEndless struct{ n int64 }

func (c *countingEndless) Read(p []byte) (int, error) {
	c.n += int64(len(p))
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

// Regression: handleVsockConn used reader.ReadBytes('\n'), which buffers a
// newline-free stream in full before any size check (OOM the guest agent).
// The loop must reject at a hard line cap without consuming the stream.
func TestVsockConnRejectsHugeLineWithoutBuffering(t *testing.T) {
	src := &countingEndless{}
	rw := struct {
		io.Reader
		io.Writer
	}{Reader: io.LimitReader(src, 64<<20), Writer: io.Discard}

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleVsockConn(context.Background(), rw, &recordingHandler{}, nil)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleVsockConn did not return on a newline-free stream")
	}
	const maxConsumed = 2 << 20
	if src.n > maxConsumed {
		t.Fatalf("vsock parser consumed %d bytes before rejecting, want <= %d (unbounded buffering)", src.n, maxConsumed)
	}
}
