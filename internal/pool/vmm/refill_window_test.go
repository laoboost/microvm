package vmm

import (
	"context"
	"testing"
	"time"
)

// TestSpawnOne_AcquireWithHandleDuringPublishWindow pins the AcquireWithHandle
// TOCTOU: the refill loop used to call RecordLoaded (row visible as 'loaded')
// and only afterwards registerHandle. An AcquireWithHandle landing in between
// claimed the row, missed the handle, treated the slot as a daemon-restart
// orphan and fell back to cold spawn — wasting the warm VMM. The seam holds
// RecordLoaded after its store write commits so the test can claim the row
// while the publish sequence is still in flight; the acquire must hit the
// handle, not the orphan path.
func TestSpawnOne_AcquireWithHandleDuringPublishWindow(t *testing.T) {
	p, _ := newTestPool(t)
	p.SetDepth("tpl-a", 1)

	inWindow := make(chan struct{})
	release := make(chan struct{})
	p.afterRecordLoaded = func() {
		close(inWindow)
		<-release
	}

	spawner := &fakeSpawner{}
	tpl := TemplateWarmInput{TemplateID: "tpl-a", VsockCID: 3}
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.spawnOne(context.Background(), tpl, spawner, 5*time.Second)
	}()

	<-inWindow
	slot, handle, err := p.AcquireWithHandle(context.Background(), "tpl-a", "sb-window", time.Now().UTC())
	close(release)
	<-done

	if err != nil {
		t.Fatalf("AcquireWithHandle during publish window: %v (want handle hit, not ErrNoLoadedSlot/orphan)", err)
	}
	if handle == nil {
		t.Fatal("AcquireWithHandle returned nil handle during publish window (orphan path taken)")
	}
	if slot == nil || slot.SandboxID != "sb-window" {
		t.Fatalf("slot = %+v, want allocated to sb-window", slot)
	}
}
