package netrules

import (
	"errors"
	"testing"
)

// TestEnsureChainDropsLinkLocalEgress pins the IMDS guard: every sandbox's
// egress ruleset must include a DROP for 169.254.0.0/16 (RFC3927 link-local,
// which hosts the EC2 instance metadata service at 169.254.169.254). Without
// it, a sandbox reaches IMDS whenever the instance hop-limit is misconfigured,
// and user-data carries every cluster secret. The isolate egress path already
// blocks link-local (pkg/isolate/egress.go); netrules must too.
func TestEnsureChainDropsLinkLocalEgress(t *testing.T) {
	const want = "|-d|169.254.0.0/16|-j|DROP"

	t.Run("with_bridge_subnet", func(t *testing.T) {
		be := &memBackend{}
		mgr := &Manager{enabled: true, ipt: be, userChain: ChainAerolvmUser}
		mgr.SetBridgeSubnet("10.88.0.0/16")
		if err := mgr.EnsureChain(); err != nil {
			t.Fatalf("EnsureChain: %v", err)
		}
		if got := be.countMatching(want); got != 1 {
			t.Fatalf("link-local DROP count = %d, want 1; rules = %v", got, be.rules)
		}
		// Reassert (reconcile path) must stay idempotent.
		if err := mgr.ReassertChain(); err != nil {
			t.Fatalf("ReassertChain: %v", err)
		}
		if got := be.countMatching(want); got != 1 {
			t.Fatalf("link-local DROP duplicated on reassert: %v", be.rules)
		}
	})

	t.Run("docker_chain_without_bridge_subnet", func(t *testing.T) {
		be := &memBackend{}
		mgr := &Manager{enabled: true, ipt: be, userChain: ChainDockerUser}
		if err := mgr.EnsureChain(); err != nil {
			t.Fatalf("EnsureChain: %v", err)
		}
		if got := be.countMatching(want); got != 1 {
			t.Fatalf("link-local DROP count = %d, want 1; rules = %v", got, be.rules)
		}
	})

	t.Run("restored_after_chain_flush", func(t *testing.T) {
		be := &memBackend{}
		mgr := &Manager{enabled: true, ipt: be, userChain: ChainAerolvmUser}
		if err := mgr.EnsureChain(); err != nil {
			t.Fatalf("EnsureChain: %v", err)
		}
		be.reset()
		if err := mgr.ReassertChain(); err != nil {
			t.Fatalf("ReassertChain: %v", err)
		}
		if got := be.countMatching(want); got != 1 {
			t.Fatalf("link-local DROP not restored after flush: %v", be.rules)
		}
	})
}

// TestEnsureLinkLocalDropDockerPath pins the Docker-engine install added for the
// no-egress-policy gap: it must add the global link-local DROP to the manager's
// own chain (DOCKER-USER on a Docker node) WITHOUT bootstrapping the chain or a
// FORWARD jump — dockerd owns those, so a Docker node must not get them from us.
func TestEnsureLinkLocalDropDockerPath(t *testing.T) {
	be := &memBackend{}
	mgr := &Manager{enabled: true, ipt: be, userChain: ChainDockerUser}

	if err := mgr.EnsureLinkLocalDrop(); err != nil {
		t.Fatalf("EnsureLinkLocalDrop: %v", err)
	}
	if got := be.countMatching("|" + ChainDockerUser + "|-d|" + LinkLocalEgressCIDR + "|-j|DROP"); got != 1 {
		t.Fatalf("link-local DROP in %s count = %d, want 1; rules = %v", ChainDockerUser, got, be.rules)
	}
	// Idempotent: the same install repeated (e.g. a future re-assert) must not
	// duplicate the rule.
	if err := mgr.EnsureLinkLocalDrop(); err != nil {
		t.Fatalf("EnsureLinkLocalDrop (2nd): %v", err)
	}
	if got := be.countMatching("|-d|" + LinkLocalEgressCIDR + "|-j|DROP"); got != 1 {
		t.Fatalf("link-local DROP duplicated: %v", be.rules)
	}
	if be.hasChain(ChainDockerUser) || be.countMatching("|FORWARD|") != 0 {
		t.Fatalf("EnsureLinkLocalDrop bootstrapped a chain/jump dockerd owns: chains=%v rules=%v", be.chains, be.rules)
	}
}

// TestEnsureLinkLocalDropGating pins the two guard rails: a disabled or nil
// manager installs nothing (the SB_NETWORK_RULES gate that keeps the DOCKER-USER
// change on the operator's egress-enforcement switch), and an unmet IPv6
// precondition refuses the IPv4-only rule without touching the backend.
func TestEnsureLinkLocalDropGating(t *testing.T) {
	if err := (&Manager{enabled: false, ipt: &memBackend{}}).EnsureLinkLocalDrop(); err != nil {
		t.Fatalf("disabled manager: %v", err)
	}
	if err := (*Manager)(nil).EnsureLinkLocalDrop(); err != nil {
		t.Fatalf("nil manager: %v", err)
	}

	be := &memBackend{}
	live := &Manager{enabled: true, ipt: be, userChain: ChainDockerUser, ipv6Disabled: func() error { return errors.New("ipv6 live") }}
	if err := live.EnsureLinkLocalDrop(); err == nil {
		t.Fatal("EnsureLinkLocalDrop must refuse when the IPv6 precondition is unmet")
	}
	if len(be.rules) != 0 {
		t.Fatalf("touched the backend despite the unmet precondition: %v", be.rules)
	}
}
