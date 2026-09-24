package service

import (
	"context"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// The cluster create-id override (X-Cluster-Create-ID → CreateSandboxWithID)
// must be validated at the service boundary. The firecracker path never calls
// mounts.MountAll, so a traversal id used to sail straight into the runtime
// and host-path layers there (createFirecrackerSandbox, wasm.go, isolate.go
// all consume idOverride unchecked).
func TestCreateSandboxWithID_RejectsPathTraversalID(t *testing.T) {
	ctx := context.Background()
	rt := &recordingRuntime{}
	svc, st, _ := newServiceRuntimeHarness(t, rt)
	svc.cfg.EnableFirecracker = true
	svc.SetFirecrackerRuntime(rt)

	resp, err := svc.CreateSandboxWithID(ctx, models.CreateSandboxRequest{
		Image:   "alpine:3.20",
		Runtime: models.RuntimeFirecracker,
	}, "../../etc")
	if err == nil {
		t.Fatalf("CreateSandboxWithID(%q) = %+v, want validation error", "../../etc", resp)
	}
	if !strings.Contains(err.Error(), "invalid sandbox id") {
		t.Fatalf("err = %q, want message containing %q", err.Error(), "invalid sandbox id")
	}
	if rt.createCalls != 0 {
		t.Fatalf("runtime Create calls = %d, want 0 (id must be rejected before the runtime)", rt.createCalls)
	}
	rows, listErr := st.List(ctx)
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	if len(rows) != 0 {
		t.Fatalf("store rows = %d, want 0", len(rows))
	}
}

func TestCreateSandbox_RejectsPathTraversalIDOverride(t *testing.T) {
	ctx := context.Background()
	rt := &recordingRuntime{}
	svc, _, _ := newServiceRuntimeHarness(t, rt)
	svc.cfg.EnableFirecracker = true
	svc.SetFirecrackerRuntime(rt)

	resp, err := svc.createSandbox(ctx, models.CreateSandboxRequest{
		Image:   "alpine:3.20",
		Runtime: models.RuntimeFirecracker,
	}, "../../../../etc/passwd")
	if err == nil {
		t.Fatalf("createSandbox(idOverride=%q) = %+v, want validation error", "../../../../etc/passwd", resp)
	}
	if !strings.Contains(err.Error(), "invalid sandbox id") {
		t.Fatalf("err = %q, want message containing %q", err.Error(), "invalid sandbox id")
	}
	if rt.createCalls != 0 {
		t.Fatalf("runtime Create calls = %d, want 0", rt.createCalls)
	}
}

func TestCreateSandboxWithID_RejectsSeparatorAndOverlongID(t *testing.T) {
	ctx := context.Background()
	rt := &recordingRuntime{}
	svc, _, _ := newServiceRuntimeHarness(t, rt)
	svc.cfg.EnableFirecracker = true
	svc.SetFirecrackerRuntime(rt)

	for _, id := range []string{"a/b", `a\b`, "a b", "a\x00b", strings.Repeat("a", 200)} {
		if _, err := svc.CreateSandboxWithID(ctx, models.CreateSandboxRequest{
			Image:   "alpine:3.20",
			Runtime: models.RuntimeFirecracker,
		}, id); err == nil {
			t.Fatalf("CreateSandboxWithID(%q) succeeded, want validation error", id)
		}
	}
	if rt.createCalls != 0 {
		t.Fatalf("runtime Create calls = %d, want 0", rt.createCalls)
	}
}

func TestCreateSandboxWithID_AcceptsValidID(t *testing.T) {
	ctx := context.Background()
	rt := &recordingRuntime{}
	svc, _, _ := newServiceRuntimeHarness(t, rt)
	svc.cfg.EnableFirecracker = true
	svc.SetFirecrackerRuntime(rt)

	resp, err := svc.CreateSandboxWithID(ctx, models.CreateSandboxRequest{
		Image:   "alpine:3.20",
		Runtime: models.RuntimeFirecracker,
	}, "sb-valid_ID-01")
	if err != nil {
		t.Fatalf("CreateSandboxWithID(valid): %v", err)
	}
	if resp.Sandbox.ID != "sb-valid_ID-01" {
		t.Fatalf("sandbox id = %q, want sb-valid_ID-01", resp.Sandbox.ID)
	}
}
