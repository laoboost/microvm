package mounts

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/models"
	"github.com/aerol-ai/microvm/pkg/mounts/adapters"
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

// shCredFailAdapter mimics a mount-s3 whose credentials are rejected: it
// prints the warning to stderr and then keeps serving (sleeping), so the
// mount becomes ready and the match only ever arrives on the live stream.
type shCredFailAdapter struct{}

func (shCredFailAdapter) Build(_ string, _ int, _ models.MountSpec, _, _ string) (adapters.Plan, error) {
	return adapters.Plan{
		Argv: []string{"sh", "-c", "echo '<Warning>: ClientError(NoSigningCredentials) from request' >&2; exec sleep 30"},
	}, nil
}

// TestCredFailureLogsStructuredWarningOnMatch requires a structured WARN on
// both mount spawn sites: the initial mount and the supervisor restart path.
func TestCredFailureLogsStructuredWarningOnMatch(t *testing.T) {
	t.Run("initial mount", func(t *testing.T) {
		logger, buf := newCapturingLogger(t)
		m := newTestManager(t, map[models.MountType]adapters.Adapter{
			models.MountTypeS3: shCredFailAdapter{},
		})
		m.logger = logger
		stubProbe(t, func(string, time.Duration) error { return nil })

		if _, err := m.MountAll(context.Background(), "sb-log-init", []models.MountSpec{
			{Type: models.MountTypeS3, Source: "s3://bucket", Target: "/data"},
		}); err != nil {
			t.Fatalf("MountAll: %v", err)
		}

		waitForLog(t, buf, "mount credentials rejected")
		if !strings.Contains(buf.String(), `sandbox_id=sb-log-init`) {
			t.Errorf("log missing sandbox_id: %q", buf.String())
		}
	})

	t.Run("supervisor restart path", func(t *testing.T) {
		logger, buf := newCapturingLogger(t)
		credPath := filepath.Join(t.TempDir(), "cred")
		plan := adapters.Plan{
			Argv:     []string{"sh", "-c", "echo '<Warning>: ClientError(NoSigningCredentials) from request' >&2; exec sleep 30"},
			CredFile: credPath, CredBody: []byte("secret"), UnlinkCred: true,
		}
		m, state, _ := restartTestManager(t, plan)
		m.logger = logger
		stubProbe(t, func(string, time.Duration) error { return nil })

		m.superviseExit(state)

		waitForLog(t, buf, "mount credentials rejected")
	})
}

// waitForLog polls until buf contains want; output arrives asynchronously
// from the mount process's stderr.
func waitForLog(t *testing.T, buf *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("log %q never appeared; got: %q", want, buf.String())
}
