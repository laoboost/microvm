package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/models"
)

// TestDeletedTombstonesExpireAfterTTL pins the tombstone set semantics: a
// deliberately-deleted sandbox id is remembered for deliberatelyDeletedTombstoneTTL
// and forgotten afterwards (so a later re-create with the same id works).
func TestDeletedTombstonesExpireAfterTTL(t *testing.T) {
	ts := newDeletedTombstones()
	now := time.Unix(1_700_000_000, 0)
	ts.mark("sb-gone", now)

	if !ts.isDeliberatelyDeleted("sb-gone", now.Add(time.Second)) {
		t.Fatal("fresh tombstone not seen")
	}
	if !ts.isDeliberatelyDeleted("sb-gone", now.Add(deliberatelyDeletedTombstoneTTL-time.Second)) {
		t.Fatal("tombstone expired too early")
	}
	if ts.isDeliberatelyDeleted("sb-gone", now.Add(deliberatelyDeletedTombstoneTTL+time.Second)) {
		t.Fatal("tombstone must be cleared after the TTL")
	}
	if ts.isDeliberatelyDeleted("sb-other", now) {
		t.Fatal("unknown id must not be tombstoned")
	}
}

// TestOwnerWatcherSkipsDeliberatelyDeletedUntilTombstoneExpires pins the C6b
// mechanism against the recreate decision path: a leftover Placed row for a
// sandbox whose local destroy already succeeded (but whose DeletePlacement
// failed) must NOT be re-materialized by the owner watcher until the
// tombstone expires.
func TestOwnerWatcherSkipsDeliberatelyDeletedUntilTombstoneExpires(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires real raft socket")
	}
	c, cleanup := newTestCluster(t, "leader", true, nil)
	defer cleanup()
	waitForLeader(t, c, 10*time.Second)
	seedSelfFailoverCapacity(c)

	rec := newRecordingRecreator()
	c.AttachRecreator(rec)

	spec := failoverRecreateSpec()
	cmd := command{Op: opPlace, SandboxID: "sb-tomb", OwnerNodeID: "leader", Spec: spec}
	payload, _ := encodeCommand(cmd)
	if err := c.raft.raft.Apply(payload, 2*time.Second).Error(); err != nil {
		t.Fatalf("raft Apply: %v", err)
	}

	// Local destroy succeeded but DeletePlacement failed: the caller uses
	// the exported helper to say "this deletion was deliberate".
	c.MarkDeliberatelyDeleted("sb-tomb")

	c.recreateOwnedSandboxes(context.Background())
	if _, ok := rec.get("sb-tomb"); ok {
		t.Fatal("owner watcher recreated a deliberately deleted sandbox")
	}

	// After the TTL the tombstone is gone and the stale row becomes
	// recreatable again (the normal failover path).
	c.deliberatelyDeleted.mark("sb-tomb", time.Now().Add(-deliberatelyDeletedTombstoneTTL-time.Second))

	c.recreateOwnedSandboxes(context.Background())
	got, ok := rec.get("sb-tomb")
	if !ok {
		t.Fatal("owner watcher did not recreate after tombstone expiry")
	}
	if got.spec.Image != "alpine" {
		t.Fatalf("recreated with wrong spec: %+v", got.spec)
	}
}

// TestDeletePlacementMarksDeliberatelyDeleted pins that a SUCCESSFUL
// DeletePlacement also plants the tombstone, covering the other half of the
// destroy race (destroy + delete both succeeded, but a stale watcher tick
// still sees the pre-delete row).
func TestDeletePlacementMarksDeliberatelyDeleted(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires real raft socket")
	}
	c, cleanup := newTestCluster(t, "leader", true, nil)
	defer cleanup()
	waitForLeader(t, c, 10*time.Second)

	cmd := command{
		Op: opPlace, SandboxID: "sb-del", OwnerNodeID: "leader",
		Spec: &models.CreateSandboxRequest{Name: "del-me", Image: "alpine"},
	}
	payload, _ := encodeCommand(cmd)
	if err := c.raft.raft.Apply(payload, 2*time.Second).Error(); err != nil {
		t.Fatalf("raft Apply: %v", err)
	}

	if err := c.DeletePlacement(context.Background(), "sb-del"); err != nil {
		t.Fatalf("DeletePlacement: %v", err)
	}
	if !c.deliberatelyDeleted.isDeliberatelyDeleted("sb-del", time.Now()) {
		t.Fatal("successful DeletePlacement must plant a deliberately-deleted tombstone")
	}
}
