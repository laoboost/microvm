package cluster

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRemoveMemberLocalRefusesSelfAndLeader pins the C6d guards on
// removeMemberLocal: force-removing yourself (the live leader) orphans every
// placement the cluster owns, so it must be refused unless allowSelf is set
// explicitly — and removing the CURRENT raft leader is always refused with
// instructions to transfer leadership first.
func TestRemoveMemberLocalRefusesSelfAndLeader(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires real raft socket")
	}
	c, cleanup := newTestCluster(t, "leader", true, nil)
	defer cleanup()
	waitForLeader(t, c, 10*time.Second)

	// Self-removal without the explicit opt-in: refused up front, before any
	// drain/orphan/RemoveServer side effect.
	err := c.removeMemberLocal(context.Background(), c.nodeID, true, false)
	if !errors.Is(err, ErrSelfRemoval) {
		t.Fatalf("removeMemberLocal(self, allowSelf=false) = %v, want ErrSelfRemoval", err)
	}

	// Even WITH allowSelf, removing the node that currently holds raft
	// leadership is refused — the operator must transfer leadership first.
	err = c.removeMemberLocal(context.Background(), c.Leader(), true, true)
	if !errors.Is(err, ErrLeaderRemoval) {
		t.Fatalf("removeMemberLocal(leader, allowSelf=true) = %v, want ErrLeaderRemoval", err)
	}

	// The refused calls must not have touched the raft configuration.
	if _, ok := c.configuredServer(c.nodeID); !ok {
		t.Fatal("self disappeared from the raft configuration after refused removal")
	}
}
