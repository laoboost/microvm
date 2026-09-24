package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/docker"
	"github.com/aerol-ai/microvm/pkg/models"
)

// blockingPushDocker holds every PushImage open until the test releases it,
// so a test can observe RunOnce's behavior while workers are still live.
// Blocking is deliberately ctx-unaware: cancel must NOT free the worker —
// that is what lets the test hold a worker live across the cancel edge.
type blockingPushDocker struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingPushDocker) unblock() { b.once.Do(func() { close(b.release) }) }

func (b *blockingPushDocker) PushImage(_ context.Context, req docker.PushImageRequest) (string, error) {
	b.entered <- struct{}{}
	<-b.release
	return req.DestRef, nil
}

// waitWorkersEntered consumes n per-worker entry signals.
func waitWorkersEntered(t *testing.T, ch <-chan struct{}, n int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-deadline:
			t.Fatalf("timed out waiting for worker %d to enter", i+1)
		}
	}
}

// Cancelling mid-batch must not let RunOnce return while workers are still
// running: the workers mutate stats under mu, so a caller reading the
// returned struct races them (and the workers would be orphaned).
func TestSnapshotPushRetry_RunOnceWaitsForLiveWorkersOnCancel(t *testing.T) {
	store := newFakePushStore()
	for _, name := range []string{"snap-1", "snap-2", "snap-3", "snap-4"} {
		store.seed(&models.SandboxSnapshot{
			Name:                  name,
			Image:                 "aerolvm-build/abc:latest",
			PushState:             models.SnapshotPushStatePending,
			ImageDistributionMode: models.ImageDistributionLocalOnly,
		})
	}
	blocker := &blockingPushDocker{entered: make(chan struct{}, 8), release: make(chan struct{})}
	defer blocker.unblock()
	rec := newTestReconciler(t, store, blocker) // maxInFlight = 2

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		stats SnapshotPushReconcileStats
		err   error
	}
	resultCh := make(chan result, 1)
	go func() {
		stats, err := rec.RunOnce(ctx)
		resultCh <- result{stats, err}
	}()

	// Two workers are live and blocked (maxInFlight=2); the fan-out loop is
	// parked on its semaphore acquiring the slot for item 3.
	waitWorkersEntered(t, blocker.entered, 2)
	cancel()
	// Free exactly one worker: the loop admits item 3 (which blocks again in
	// PushImage), then hits its ctx check at item 4 while two workers are live.
	blocker.release <- struct{}{}
	waitWorkersEntered(t, blocker.entered, 1) // the newly admitted item-3 worker

	select {
	case res := <-resultCh:
		t.Fatalf("RunOnce returned while workers were still live: stats=%+v err=%v", res.stats, res.err)
	case <-time.After(150 * time.Millisecond):
	}

	blocker.unblock()
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
