package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// TestEvictDeadOwnerSkipsRemovalWhenNodeRejoins pins F2e: the rejoin guard must
// also cover the final RemoveServer step, not just the reassign/orphan loop. A
// node that comes back in the window between the orphan apply and the removal
// would otherwise be stripped from the raft configuration while alive — and,
// with the voter gate, silently never re-promoted, shrinking the cluster.
func TestEvictDeadOwnerSkipsRemovalWhenNodeRejoins(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires real raft socket")
	}
	c, cleanup := newTestCluster(t, "ldr-rejoin", true, nil)
	defer cleanup()
	waitForLeader(t, c, 10*time.Second)

	// A configured nonvoter gives the removal step a real server to drop.
	f := c.raft.raft.AddNonvoter(raft.ServerID("victim-rejoin"), raft.ServerAddress("127.0.0.1:1"), 0, c.commitTimeout)
	if err := f.Error(); err != nil {
		t.Fatalf("AddNonvoter: %v", err)
	}
	c.deadOwners.markDead("victim-rejoin", time.Now())

	// Simulate the node rejoining in the window between the orphan step and the
	// removal: the memberlist join callback clears its dead-owner tracking entry.
	beforeRemoveDeadOwnerServer = func(nodeID string) {
		if nodeID == "victim-rejoin" {
			c.deadOwners.clear(nodeID)
		}
	}
	defer func() { beforeRemoveDeadOwnerServer = nil }()

	c.evictDeadOwner(context.Background(), "victim-rejoin")

	if _, ok := c.configuredServer("victim-rejoin"); !ok {
		t.Fatal("rejoined node was removed from the raft configuration while alive")
	}
}
