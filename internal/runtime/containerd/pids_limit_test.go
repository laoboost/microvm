package containerd

import (
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// it sets a pids limit in the containerd resource spec opts
func TestResourceSpecOpts_SetsPidsLimit(t *testing.T) {
	d := &Driver{cfg: Config{PidsLimit: 1024}}
	spec := applyOpts(t, d.resourceSpecOpts(models.CreateSandboxRequest{MemoryMB: 256, CPU: 0.5}))

	if spec.Linux.Resources == nil || spec.Linux.Resources.Pids == nil {
		t.Fatal("pids limit not set in Linux resources")
	}
	if got := spec.Linux.Resources.Pids.Limit; got != 1024 {
		t.Fatalf("Pids.Limit = %d, want 1024", got)
	}
}

// it sets a pids limit in the containerd resource spec opts (zero requests)
func TestResourceSpecOpts_SetsPidsLimitWithZeroRequests(t *testing.T) {
	d := &Driver{cfg: Config{PidsLimit: 1024}}
	spec := applyOpts(t, d.resourceSpecOpts(models.CreateSandboxRequest{}))

	if spec.Linux.Resources == nil || spec.Linux.Resources.Pids == nil {
		t.Fatal("pids limit not set for zero-resource request (warm-pool parked path)")
	}
}

// it preserves the pids limit on a containerd resource update
func TestResize_PreservesPidsLimitOnResourceUpdate(t *testing.T) {
	res := resizeLinuxResources(models.ResizeSandboxRequest{MemoryMB: 512}, 1024)
	if res == nil {
		t.Fatal("resize resources nil")
	}
	if res.Pids == nil {
		t.Fatal("resize resources missing Pids limit")
	}
	if res.Pids.Limit != 1024 {
		t.Fatalf("Pids.Limit = %d, want 1024", res.Pids.Limit)
	}

	// Pids limit alone must still produce an update (no memory/cpu change).
	resOnlyPids := resizeLinuxResources(models.ResizeSandboxRequest{}, 1024)
	if resOnlyPids == nil || resOnlyPids.Pids == nil || resOnlyPids.Pids.Limit != 1024 {
		t.Fatalf("pids-only resize lost: %+v", resOnlyPids)
	}
}

func TestResize_NoPidsLimitOmitsPids(t *testing.T) {
	if res := resizeLinuxResources(models.ResizeSandboxRequest{}, 0); res != nil {
		t.Fatalf("no limits set must produce nil resources, got %+v", res)
	}
}
