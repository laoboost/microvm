package mounts

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/models"
	"github.com/aerol-ai/microvm/pkg/mounts/adapters"
)

// gateAdapter blocks inside Build until released, signalling entry first: it
// simulates a slow mount-s3 spawn so tests can race a Sweep against an
// in-progress MountAll.
type gateAdapter struct {
	enteredCh chan struct{}
	releaseCh chan struct{}
	entered   bool
	mu        sync.Mutex
}

func newGateAdapter() *gateAdapter {
	return &gateAdapter{enteredCh: make(chan struct{}), releaseCh: make(chan struct{})}
}

func (g *gateAdapter) Build(string, int, models.MountSpec, string, string) (adapters.Plan, error) {
	g.mu.Lock()
	if !g.entered {
		g.entered = true
		close(g.enteredCh)
	}
	g.mu.Unlock()
	<-g.releaseCh
	return adapters.Plan{Argv: []string{"true"}, IsKernelMount: true}, nil
}

// it does not sweep a sandbox whose mounts are mid-establishment: a start in
// flight registers the sandbox before any FUSE process spawns, so a reconcile
// tick running concurrently must never kill its mounts as "orphans" (prod
// incident 2026-09-09: sweep killed a starting sandbox's mounts mid-reestablish
// and the start then timed out and destroyed the sandbox).
func TestSweepSkipsSandboxesWithMountsInFlight(t *testing.T) {
	gate := newGateAdapter()
	m := newTestManager(t, map[models.MountType]adapters.Adapter{
		models.MountTypeS3: gate,
	})

	done := make(chan error, 1)
	go func() {
		_, err := m.MountAll(context.Background(), "sb-starting", []models.MountSpec{
			{Type: models.MountTypeS3, Source: "s3://bucket", Target: "/data"},
		})
		done <- err
	}()

	select {
	case <-gate.enteredCh:
	case <-time.After(3 * time.Second):
		t.Fatal("MountAll never reached the adapter")
	}

	// The sandbox dir exists (mountOne created the host path) and the sandbox
	// is NOT tracked in m.state yet (state commits after MountAll returns).
	sbDir := filepath.Join(m.rootDir, "sb-starting")
	if _, err := os.Stat(sbDir); err != nil {
		t.Fatalf("sandbox dir should exist mid-mount: %v", err)
	}

	// Sweep with an empty keep set: the starting sandbox must survive.
	m.Sweep(map[string]struct{}{})

	if _, err := os.Stat(sbDir); err != nil {
		t.Fatalf("sweep removed the dir of a sandbox with mounts in flight: %v", err)
	}

	close(gate.releaseCh)
	if err := <-done; err != nil {
		t.Fatalf("MountAll: %v", err)
	}
}

// slowAdapter sleeps before returning its plan: it makes sequential MountAll
// timing measurable.
type slowAdapter struct {
	delay time.Duration
}

func (s slowAdapter) Build(string, int, models.MountSpec, string, string) (adapters.Plan, error) {
	time.Sleep(s.delay)
	return adapters.Plan{Argv: []string{"true"}, IsKernelMount: true}, nil
}

// it establishes independent mounts concurrently: five serialized mount-s3
// spawns are why every sandbox start took ~24s in prod (5 × ~5s); mounts to
// distinct host paths must run in parallel, with binds still in spec order.
func TestMountAllEstablishesMountsConcurrentlyInSpecOrder(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := t.TempDir()
	m, err := New(logger, Config{RootDir: root, CredDir: t.TempDir(), WaitTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(m.Close)
	const delay = 300 * time.Millisecond
	m.adapters = map[models.MountType]adapters.Adapter{
		models.MountTypeS3: slowAdapter{delay: delay},
	}

	specs := make([]models.MountSpec, 5)
	for i := range specs {
		specs[i] = models.MountSpec{
			Type:   models.MountTypeS3,
			Source: "s3://bucket",
			Target: fmt.Sprintf("/data%d", i),
		}
	}

	start := time.Now()
	binds, err := m.MountAll(context.Background(), "sb-par", specs)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("MountAll: %v", err)
	}

	// Sequential would be 5×300ms = 1.5s; concurrent must beat 2×300ms.
	if elapsed >= 2*delay {
		t.Fatalf("MountAll took %v — mounts are still serialized", elapsed)
	}
	if len(binds) != 5 {
		t.Fatalf("got %d binds, want 5", len(binds))
	}
	for i, b := range binds {
		want := filepath.Join(root, "sb-par", strconv.Itoa(i))
		if b.HostPath != want {
			t.Errorf("binds[%d].HostPath = %s, want %s (spec order must be preserved)", i, b.HostPath, want)
		}
	}
}
