package mounts

import (
	"bytes"
	"context"
	"expvar"
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

func expvarSnapshot(t *testing.T, name string) int64 {
	t.Helper()
	v := expvar.Get(name)
	if v == nil {
		return 0
	}
	i, ok := v.(*expvar.Int)
	if !ok {
		t.Fatalf("expvar %s is not an Int", name)
	}
	n := i.Value()
	return n
}

// TestCredFailureIncrementsMetricOnMatch requires the expvar counter
// aerolvm_mount_cred_failure_total (surfaced as Prometheus text at
// /v1/metrics) to advance on each credential-failure match.
func TestCredFailureIncrementsMetricOnMatch(t *testing.T) {
	logger, _ := newCapturingLogger(t)
	m := newTestManager(t, map[models.MountType]adapters.Adapter{
		models.MountTypeS3: shCredFailAdapter{},
	})
	m.logger = logger
	stubProbe(t, func(string, time.Duration) error { return nil })

	before := expvarSnapshot(t, "aerolvm_mount_cred_failure_total")

	if _, err := m.MountAll(context.Background(), "sb-metric", []models.MountSpec{
		{Type: models.MountTypeS3, Source: "s3://bucket", Target: "/data"},
	}); err != nil {
		t.Fatalf("MountAll: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if expvarSnapshot(t, "aerolvm_mount_cred_failure_total") == before+1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("metric aerolvm_mount_cred_failure_total did not advance: before=%d after=%d",
		before, expvarSnapshot(t, "aerolvm_mount_cred_failure_total"))
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

// TestCredFailureDeduplicatesRepeatedMatches requires a per-mount-process
// one-shot latch: the pattern recurs per request, so without a latch every
// stderr line would produce a log storm and unbounded metric growth.
func TestCredFailureDeduplicatesRepeatedMatches(t *testing.T) {
	out := &capturedOutput{}
	var fired int
	out.onCredFailure = func() { fired++ }

	for i := 0; i < 5; i++ {
		if _, err := out.Write([]byte("<Warning>: ClientError(NoSigningCredentials) from request\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	if fired != 1 {
		t.Fatalf("credential-failure callback fired %d times for 5 matches, want 1 (one-shot latch)", fired)
	}
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
