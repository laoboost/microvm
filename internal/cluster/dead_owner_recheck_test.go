package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/internal/config"
)

// TestEvictDeadOwnerSkipsWhenNodeAlreadyBack pins the C6e re-check: eviction
// decisions are made from a live-map snapshot taken at tick start, but the
// eviction itself must re-check before acting. A node that rejoined (gossip
// reports it alive again) must not have its placements reassigned or
// orphaned by a stale eviction.
func TestEvictDeadOwnerSkipsWhenNodeAlreadyBack(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires real raft socket")
	}
	c, cleanup := newTestCluster(t, "leader", true, nil)
	defer cleanup()
	waitForLeader(t, c, 10*time.Second)
	seedSelfFailoverCapacity(c)

	// Placement with failover opt-in owned by a phantom node — the eviction
	// would normally reassign it to self/leader.
	cmd := command{
		Op: opPlace, SandboxID: "sb-back", OwnerNodeID: "dead-node", OwnerAPIURL: "http://gone",
		Spec: failoverRecreateSpec(),
	}
	payload, _ := encodeCommand(cmd)
	if err := c.raft.raft.Apply(payload, 2*time.Second).Error(); err != nil {
		t.Fatalf("raft Apply: %v", err)
	}
	c.deadOwners.markDead("dead-node", time.Now())

	// The node comes back before the eviction acts: gossip reports it alive.
	c.gossip.memberIndex.replace([]Member{
		{NodeID: c.nodeID, Alive: true, Role: config.NodeRoleServer, APIURL: c.apiURL},
		{NodeID: "dead-node", Alive: true, Role: config.NodeRoleServer, APIURL: "http://back"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.evictDeadOwner(ctx, "dead-node")

	owner, err := c.OwnerOf("sb-back")
	if err != nil || owner.NodeID != "dead-node" {
		t.Fatalf("stale eviction moved a placement whose owner came back: owner=%+v err=%v", owner, err)
	}
}

// TestEvictDeadOwnerAbortsWhenNodeReturnsAfterSnapshot is the exact C6e race:
// the live-map snapshot says the node is dead, but it rejoins (join callback
// clears the dead-owner tracker) before the eviction acts. No reassign or
// orphan op may be issued for it.
func TestEvictDeadOwnerAbortsWhenNodeReturnsAfterSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires real raft socket")
	}
	c, cleanup := newTestCluster(t, "leader", true, nil)
	defer cleanup()
	waitForLeader(t, c, 10*time.Second)
	seedSelfFailoverCapacity(c)

	cmd := command{
		Op: opPlace, SandboxID: "sb-snap", OwnerNodeID: "dead-node", OwnerAPIURL: "http://gone",
		Spec: failoverRecreateSpec(),
	}
	payload, _ := encodeCommand(cmd)
	if err := c.raft.raft.Apply(payload, 2*time.Second).Error(); err != nil {
		t.Fatalf("raft Apply: %v", err)
	}
	c.deadOwners.markDead("dead-node", time.Now())

	prev := afterEvictOwnedSnapshot
	afterEvictOwnedSnapshot = func(nodeID string) {
		if nodeID == "dead-node" {
			// The memberlist join callback fires mid-eviction: the tracker
			// mark is cleared and the node is back.
			c.cancelDeadOwnerWatch("dead-node")
		}
	}
	defer func() { afterEvictOwnedSnapshot = prev }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.evictDeadOwner(ctx, "dead-node")

	owner, err := c.OwnerOf("sb-snap")
	if err != nil || owner.NodeID != "dead-node" {
		t.Fatalf("eviction issued ops after mid-eviction rejoin: owner=%+v err=%v", owner, err)
	}
}
