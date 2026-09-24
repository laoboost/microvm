package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/models"
)

// countingStore counts Get calls so the test can observe when the fan-out
// loop admits another retryOne worker.
type countingStore struct {
	*fakePendingStore
	getCalls atomic.Int64
}

func (s *countingStore) Get(ctx context.Context, id string) (*models.Sandbox, error) {
	s.getCalls.Add(1)
	return s.fakePendingStore.Get(ctx, id)
}

// blockingMutator gates MarkImported — the last hop of a successful retryOne —
// on a channel. Blocking is ctx-unaware so cancel does not free the worker.
type blockingMutator struct {
	entered chan struct{}
	release chan struct{}
}

func (m *blockingMutator) MarkImported(_ context.Context, _, _ string) {
	m.entered <- struct{}{}
	<-m.release
}

// Same cancel-path contract as the snapshot push reconciler: RunOnce must not
// return while workers are live — they mutate stats under mu.
func TestAutoImportRetry_RunOnceWaitsForLiveWorkersOnCancel(t *testing.T) {
	// Import target answers immediately so successful workers reach the
	// MarkImported gate and park there.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(importResponse{
			Status:      "imported",
			RegistryRef: "aocr.aerol.ai/cluster/cl/_imported/ghcr.io/aerol-ai/sandbox:v1--idle-90d",
		})
	}))
	defer srv.Close()

	store := &countingStore{fakePendingStore: newFakeStore()}
	specs := map[string]*models.CreateSandboxRequest{}
	for _, id := range []string{"sb-1", "sb-2", "sb-3", "sb-4"} {
		store.seed(id, true)
		specs[id] = eligibleSpec()
	}
	imp, err := NewAutoImporter(validImportCfg(srv.URL))
	if err != nil {
		t.Fatalf("NewAutoImporter: %v", err)
	}
	rec := NewAutoImportReconciler(imp, store, &fakeSpecResolver{specs: specs}, slog.Default(), 2)
	gate := &blockingMutator{entered: make(chan struct{}, 8), release: make(chan struct{})}
	rec.SetSpecMutator(gate)
	var gateOnce sync.Once
	unblockGate := func() { gateOnce.Do(func() { close(gate.release) }) }
	defer unblockGate()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		stats AutoImportReconcileStats
		err   error
	}
	resultCh := make(chan result, 1)
	go func() {
		stats, err := rec.RunOnce(ctx)
		resultCh <- result{stats, err}
	}()

	// Two workers complete their import and park in MarkImported
	// (maxInFlight=2); the fan-out loop is parked on its semaphore acquiring
	// the slot for item 3.
	waitWorkersEntered(t, gate.entered, 2)
	cancel()
	// Free exactly one parked worker. The loop then admits item 3 (its Import
	// fails fast on the cancelled ctx) and hits its ctx check at item 4 while
	// the other parked worker is still live in MarkImported.
	gate.release <- struct{}{}
	deadline := time.After(2 * time.Second)
	for store.getCalls.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for item-3 worker to start (Get calls = %d)", store.getCalls.Load())
		case <-time.After(time.Millisecond):
		}
	}

	select {
	case res := <-resultCh:
		t.Fatalf("RunOnce returned while a worker was still live in MarkImported: stats=%+v err=%v", res.stats, res.err)
	case <-time.After(150 * time.Millisecond):
	}

	unblockGate()
	res := <-resultCh
	if !errors.Is(res.err, context.Canceled) {
		t.Fatalf("RunOnce err = %v, want context.Canceled", res.err)
	}
	if res.stats.Scanned != 4 {
		t.Fatalf("Scanned = %d, want 4", res.stats.Scanned)
	}
	completed := res.stats.Succeeded + res.stats.Failed + res.stats.Skipped
	if completed != 3 {
		t.Fatalf("completed outcomes = %d (stats=%+v), want 3 workers' worth", completed, res.stats)
	}
}
