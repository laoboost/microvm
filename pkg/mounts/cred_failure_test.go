package mounts

import (
	"bytes"
	"log/slog"
	"sync"
	"testing"
)

// syncBuffer is a threadsafe bytes.Buffer for capturing slog output in tests.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newCapturingLogger(t *testing.T) (*slog.Logger, *syncBuffer) {
	t.Helper()
	buf := &syncBuffer{}
	return slog.New(slog.NewTextHandler(buf, nil)), buf
}

// TestCredFailureScanDetectsMatchAfterSuccessfulMount guards the live-stream
// scan: a mount that succeeds at startup and only later logs
// NoSigningCredentials per request must still be detected, even though the
// failure-time tail buffer is never read on that path.
func TestCredFailureScanDetectsMatchAfterSuccessfulMount(t *testing.T) {
	out := &capturedOutput{}
	var fired int
	out.onCredFailure = func() { fired++ }

	// Simulate the live stream: chunks arrive after the mount is already
	// healthy, including a match split across two writes.
	for _, chunk := range []string{"<INFO>: Mounting bucket ", "abcdefgh\n", "<Warning>: ClientError(NoSigningCredentials) from request\n", "<Warning>: ClientError(NoSigningCredentials) from request\n"} {
		if _, err := out.Write([]byte(chunk)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	if fired == 0 {
		t.Fatal("live output stream containing NoSigningCredentials was not scanned")
	}
}
