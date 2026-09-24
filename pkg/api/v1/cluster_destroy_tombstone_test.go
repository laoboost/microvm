package v1

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/internal/cluster"
	storepkg "github.com/aerol-ai/microvm/internal/store"
	"github.com/aerol-ai/microvm/pkg/models"
)

// tombstoneStubCluster layers a MarkDeliberatelyDeleted recorder onto the
// promoteStubCluster seam.
type tombstoneStubCluster struct {
	*promoteStubCluster
	marked []string
}

func (c *tombstoneStubCluster) MarkDeliberatelyDeleted(id string) {
	c.marked = append(c.marked, id)
}

func seedDestroySandbox(t *testing.T, st *storepkg.Store, id string) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.Create(context.Background(), &models.Sandbox{
		ID: id, Image: "alpine:3.20", Status: models.SandboxStatusStarted,
		ContainerID: "ctr-" + id, ContainerIP: "10.0.0.9",
		CPU: 1, MemoryMB: 256, DiskGB: 1, OSUser: "root", ToolboxEnabled: true,
		CreatedAt: now, UpdatedAt: now, LastActiveAt: now,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// When the local destroy succeeds but DeletePlacement fails, the FSM can keep
// a Placed row that would let the owner watcher resurrect the sandbox. The
// handler must tombstone the id (MarkDeliberatelyDeleted) so the watcher
// leaves it dead — reconcile does NOT catch these ghost rows.
func TestClusterDestroyWrap_TombstonesOnDeletePlacementFailure(t *testing.T) {
	rt := &apiRecordingRuntime{}
	stub := &tombstoneStubCluster{promoteStubCluster: &promoteStubCluster{
		Noop:      cluster.NewNoop("node-a", "http://node-a", ""),
		deleteErr: errors.New("fsm delete failed"),
	}}
	h, st := newClusterCreateHarness(t, rt, stub)
	seedDestroySandbox(t, st, "sb-destroy")

	req := httptest.NewRequest(http.MethodDelete, "/v1/sandboxes/sb-destroy", nil)
	req.SetPathValue("id", "sb-destroy")
	rr := httptest.NewRecorder()
	h.clusterDestroyWrap(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if len(rt.destroyIDs) != 1 || rt.destroyIDs[0] != "sb-destroy" {
		t.Fatalf("destroy ids = %+v, want [sb-destroy]", rt.destroyIDs)
	}
	if len(stub.deleteCalls) != 1 || stub.deleteCalls[0] != "sb-destroy" {
		t.Fatalf("DeletePlacement calls = %+v, want [sb-destroy]", stub.deleteCalls)
	}
	if len(stub.marked) != 1 || stub.marked[0] != "sb-destroy" {
		t.Fatalf("MarkDeliberatelyDeleted calls = %+v, want [sb-destroy]", stub.marked)
	}
}

// A successful DeletePlacement already plants its own tombstone inside the
// cluster layer — the handler must not double-mark.
func TestClusterDestroyWrap_NoTombstoneOnDeletePlacementSuccess(t *testing.T) {
	rt := &apiRecordingRuntime{}
	stub := &tombstoneStubCluster{promoteStubCluster: &promoteStubCluster{
		Noop: cluster.NewNoop("node-a", "http://node-a", ""),
	}}
	h, st := newClusterCreateHarness(t, rt, stub)
	seedDestroySandbox(t, st, "sb-destroy-ok")

	req := httptest.NewRequest(http.MethodDelete, "/v1/sandboxes/sb-destroy-ok", nil)
	req.SetPathValue("id", "sb-destroy-ok")
	rr := httptest.NewRecorder()
	h.clusterDestroyWrap(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if len(stub.marked) != 0 {
		t.Fatalf("MarkDeliberatelyDeleted calls = %+v, want none on DeletePlacement success", stub.marked)
	}
}
