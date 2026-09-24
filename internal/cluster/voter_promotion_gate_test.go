package cluster

import "testing"

// Gossip claims are self-reported. With encrypted gossip the peer at least
// proved possession of the fleet key; without it any host that can reach the
// gossip port can announce itself and must never be auto-promoted to a raft
// voter (suffrage grants it a quorum vote — cluster takeover).
func TestMayAutoPromoteToVoter(t *testing.T) {
	t.Run("encrypted gossip allows auto-promotion", func(t *testing.T) {
		c := &Cluster{gossipEncrypted: true}
		if !c.mayAutoPromoteToVoter() {
			t.Fatal("expected auto-promotion allowed when gossip is encrypted")
		}
	})

	t.Run("unencrypted gossip refuses auto-promotion without explicit opt-in", func(t *testing.T) {
		c := &Cluster{gossipEncrypted: false}
		if c.mayAutoPromoteToVoter() {
			t.Fatal("expected auto-promotion refused when gossip is unencrypted")
		}
	})

	t.Run("unencrypted gossip with explicit insecure opt-in allows auto-promotion", func(t *testing.T) {
		c := &Cluster{gossipEncrypted: false}
		c.cfg.ClusterInsecureGossip = true
		if !c.mayAutoPromoteToVoter() {
			t.Fatal("expected auto-promotion allowed under explicit ClusterInsecureGossip opt-in")
		}
	})
}
