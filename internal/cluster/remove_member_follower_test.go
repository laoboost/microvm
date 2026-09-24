package cluster

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRemoveMemberRejectsSelfRemovalFromFollower pins F2f: the ErrSelfRemoval
// guard must run before the leadership branch, so it applies on both the local
// leader path and the follower-forwarded path. Before the fix, a follower
// removing itself forwarded to the leader, which compared the target against the
// LEADER's id — the guard passed and the live follower (and everything it owned)
// was force-removed.
func TestRemoveMemberRejectsSelfRemovalFromFollower(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires real raft socket")
	}
	// The leader's advertised API URL points at a recording server so we can
	// observe whether the follower forwarded (attempted a RemoveServer) or
	// short-circuited on the self guard.
	var forwarded atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/members/fol-selfrm") {
			forwarded.Store(true)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer api.Close()

	leader, cleanupLeader := newTestClusterWithAPI(t, "ldr-selfrm", true, nil, api.URL)
	defer cleanupLeader()
	waitForLeader(t, leader, 10*time.Second)

	follower, cleanupFollower := newTestCluster(t, "fol-selfrm", false, []string{leader.gossip.ml.LocalNode().Address()})
	defer cleanupFollower()
	waitForVoter(t, leader, follower.nodeID, 20*time.Second)
	waitForLeaderAPIURL(t, follower, 10*time.Second)

	err := follower.RemoveMember(context.Background(), follower.nodeID, true)
	if !errors.Is(err, ErrSelfRemoval) {
		t.Fatalf("follower RemoveMember(self) = %v, want ErrSelfRemoval", err)
	}
	if forwarded.Load() {
		t.Fatal("follower forwarded a self-removal to the leader (would force-remove a live node)")
	}
	if _, ok := leader.configuredServer(follower.nodeID); !ok {
		t.Fatal("live follower disappeared from the raft configuration")
	}
}

func waitForLeaderAPIURL(t *testing.T, c *Cluster, max time.Duration) {
	t.Helper()
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if c.Leader() != "" && c.LeaderAPIURL() != "" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("leader API URL never became resolvable (leader=%q url=%q)", c.Leader(), c.LeaderAPIURL())
}
