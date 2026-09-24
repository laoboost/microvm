package cluster

import (
	"context"
	"testing"
)

// TestDeletePlacementPlantsTombstoneWhenApplyFails pins the F2a contract: a call
// to DeletePlacement is a statement of intent ("destroy this on purpose"), so
// the deliberately-deleted tombstone must be planted regardless of whether the
// raft round-trip succeeds. Otherwise a transient raft failure on any of the
// destroy flows that route through DeletePlacement lets the owner watcher
// resurrect the just-deleted sandbox from the leftover Placed row.
func TestDeletePlacementPlantsTombstoneWhenApplyFails(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires real raft socket")
	}
	// A non-bootstrapped single node never elects a leader, so the
	// DeletePlacement raft round-trip fails deterministically.
	c, cleanup := newTestCluster(t, "follower", false, nil)
	defer cleanup()

	// Seed the local replicated row plus its recovery payload directly — with
	// no leader there is no way to commit an opPlace through raft.
	spec := failoverRecreateSpec()
	c.fsm.mu.Lock()
	c.fsm.placements["sb-faildel"] = Placement{
		SandboxID:   "sb-faildel",
		OwnerNodeID: "follower",
		State:       PlacementStatePlaced,
	}
	c.fsm.recovery["sb-faildel"] = placementRecovery{Spec: spec}
	if c.fsm.ownerIndex == nil {
		c.fsm.ownerIndex = map[string]map[string]struct{}{}
	}
	c.fsm.ownerIndex["follower"] = map[string]struct{}{"sb-faildel": {}}
	c.fsm.mu.Unlock()

	rec := newRecordingRecreator()
	c.AttachRecreator(rec)

	if err := c.DeletePlacement(context.Background(), "sb-faildel"); err == nil {
		t.Fatal("DeletePlacement without a leader = nil, want a transport error")
	}

	c.recreateOwnedSandboxes(context.Background())
	if _, ok := rec.get("sb-faildel"); ok {
		t.Fatal("owner watcher recreated a sandbox whose deliberate DeletePlacement failed")
	}
}
