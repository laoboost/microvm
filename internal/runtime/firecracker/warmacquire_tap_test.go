package firecracker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// Regression (SA4009): tryAcquireWarm's tapSlot parameter was overwritten
// with the pool-transferred slot before any read of it — a caller-allocated
// TAP was silently dropped and the warm slot's TAP could be left orphaned
// under the pool's slot id. A warm acquire must end with exactly the
// transferred warm TAP owned by the sandbox and nothing else outstanding.
func TestWarmAcquireDoesNotLeakTapSlot(t *testing.T) {
	f := newDriverFixture(t)
	_, _ = stageWarmFixture(t, f)

	tplDir := t.TempDir()
	rootfs := filepath.Join(tplDir, "rootfs.ext4")
	if err := os.WriteFile(rootfs, []byte("ROOTFS"), 0o644); err != nil {
		t.Fatalf("write rootfs: %v", err)
	}
	snap := &TemplateResolution{HasSnapshot: true, RootfsPath: rootfs}

	state, hit, err := f.driver.tryAcquireWarm(context.Background(),
		models.CreateSandboxRequest{TemplateID: "tpl-warm"}, "sb-warm-leak", snap, "")
	if err != nil || !hit {
		t.Fatalf("tryAcquireWarm hit=%v err=%v, want hit", hit, err)
	}
	// The warm path must surface the pool-transferred TAP (stageWarmFixture's
	// warm slot TAP), never a caller-allocated one.
	if state.ContainerIP != "172.16.0.6" {
		t.Fatalf("state.ContainerIP = %q, want the transferred warm TAP guest IP 172.16.0.6", state.ContainerIP)
	}
	if f.pool.alloc != 0 {
		t.Fatalf("warm acquire allocated %d TAP slots, want 0 (nothing to leak)", f.pool.alloc)
	}

	f.pool.mu.Lock()
	defer f.pool.mu.Unlock()
	if len(f.pool.slots) != 1 {
		t.Fatalf("TAP slots after warm acquire = %d (%v), want exactly 1 (transferred warm TAP)",
			len(f.pool.slots), f.pool.slots)
	}
	if f.pool.slots["sb-warm-leak"] == nil {
		t.Fatal("transferred warm TAP is not owned by the sandbox")
	}
}
