package service

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/docker"
	"github.com/aerol-ai/microvm/pkg/models"
)

// blockingTemplatePushDocker extends the blocking PushImage seam with the
// extra TemplateArtifactPushDocker methods (no-op pass-through).
type blockingTemplatePushDocker struct {
	blockingPushDocker
}

func (b *blockingTemplatePushDocker) ImportImage(_ context.Context, req docker.ImportImageRequest) error {
	// Must drain the tar pipe — the pusher's writer goroutine blocks on it,
	// and the worker waits for that goroutine before reaching PushImage.
	_, _ = io.ReadAll(req.Tar)
	return nil
}

func (b *blockingTemplatePushDocker) RemoveImage(_ context.Context, _ string) error {
	return nil
}

// Same H16 cancel-path contract as the snapshot push reconciler: RunOnce must
// not return while workers are live — they mutate stats under mu.
func TestTemplatePushRetry_RunOnceWaitsForLiveWorkersOnCancel(t *testing.T) {
	store := newFakeTemplatePushStore()
	blocker := &blockingTemplatePushDocker{blockingPushDocker{entered: make(chan struct{}, 8), release: make(chan struct{})}}
	defer blocker.unblock()
	rec, templatesDir := newTestTemplateReconciler(t, store, blocker) // maxInFlight = 2

	for _, id := range []string{"tpl-1", "tpl-2", "tpl-3", "tpl-4"} {
		rootfs, mem, state := seedArtifactsOnDisk(t, templatesDir, id)
		store.seed(&models.Template{
			ID: id, Image: "img:1", Status: models.TemplateStatusReady,
			PushState:          models.TemplatePushStatePending,
			RootfsPath:         rootfs,
			SnapshotMemoryPath: mem,
			SnapshotStatePath:  state,
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		stats TemplateArtifactPushStats
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
	// Free exactly one worker: the loop admits item 3 (which blocks again),
	// then hits its ctx check at item 4 while two workers are live.
	blocker.release <- struct{}{}
	waitWorkersEntered(t, blocker.entered, 1)

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
