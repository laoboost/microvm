package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/aerol-ai/microvm/internal/config"
	"github.com/aerol-ai/microvm/internal/service"
	pkgisolate "github.com/aerol-ai/microvm/pkg/isolate"
)

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

// The boot log must report the ACTUAL jail coverage (pkgisolate.JailCoverage),
// not just jail_requested/jail_realizable — otherwise operators read
// "jail_realizable=true" and believe seccomp is applied when it is not.
func TestWireIsolateRuntimeLogsActualJailCoverage(t *testing.T) {
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))

	st := openTestStore(t)
	svc := service.New(config.Config{EnableIsolate: true}, testLogger(), st, nil, nil, nil, nil, nil, nil)

	_, err := wireIsolateRuntime(context.Background(), config.Config{
		IsolateWorkerdPath:      "/usr/local/bin/workerd",
		IsolateRunDir:           t.TempDir(),
		IsolateGroupGranularity: config.IsolateGroupPerTenant,
	}, logger, svc)
	if err != nil {
		t.Fatalf("wireIsolateRuntime: %v", err)
	}

	want := pkgisolate.JailCoverage()
	if want == "" {
		t.Fatal("pkgisolate.JailCoverage() is empty; the boot log cannot report coverage")
	}
	if got := logs.String(); !strings.Contains(got, want) {
		t.Fatalf("isolate boot log does not report actual jail coverage %q; log:\n%s", want, got)
	}
}
