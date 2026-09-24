package netrules

import (
	"fmt"
	"strings"
)

// EnsureChain bootstraps the filter user chain and its FORWARD jump once per
// Manager lifetime. Idempotent and latched like EnsureLayer4Ready: concurrent
// callers single-flight through chainMu; success sets chainReady.
//
// Fails closed on the IPv6 precondition before touching the backend: this is
// where the first rules land, and the daemon hard-disables sandbox IPv6 before
// any chain work (see daemon.bootSandboxNetworkIsolation). A reordering that
// let a chain bootstrap run first is refused here rather than installing
// isolation that fails open over v6.
func (m *Manager) EnsureChain() error {
	if !m.Enabled() {
		return nil
	}
	if err := m.verifyIPv6Disabled(); err != nil {
		return fmt.Errorf("netrules: refusing to bootstrap the %s chain: %w", m.filterChain(), err)
	}
	if m.chainReady.Load() {
		return nil
	}
	m.chainMu.Lock()
	defer m.chainMu.Unlock()
	if m.chainReady.Load() {
		return nil
	}
	boot, ok := m.ipt.(bootstrapBackend)
	if !ok {
		// Fail loud, never silently latch success. A backend that cannot create
		// the user chain + FORWARD jump means every per-IP egress rule the driver
		// later inserts is either rejected (missing chain) or never traversed
		// (missing jump) — i.e. requested isolation silently fails. Both shipped
		// backends (exec, netlink) implement bootstrapBackend; this guards against
		// a future backend regressing the contract.
		return fmt.Errorf("netrules: backend %T cannot bootstrap the %s chain; containerd egress isolation would be silently ineffective", m.ipt, m.filterChain())
	}
	chain := m.filterChain()
	if err := boot.EnsureUserChain(chain); err != nil {
		return err
	}
	if err := boot.EnsureForwardJump(chain); err != nil {
		return err
	}
	if err := m.ensureBridgeForwardAccept(); err != nil {
		return err
	}
	// After the bridge ACCEPTs so the insert-at-1 lands ABOVE them — an
	// ACCEPT on top would let subnet traffic through to IMDS.
	if err := m.ensureLinkLocalDrop(); err != nil {
		return err
	}
	m.chainReady.Store(true)
	return nil
}

// LinkLocalEgressCIDR is RFC3927 link-local (169.254.0.0/16), home of the EC2
// instance metadata service at 169.254.169.254.
const LinkLocalEgressCIDR = "169.254.0.0/16"

// linkLocalDropSpec generates the sandbox-egress DROP for link-local: sandboxes
// must never reach IMDS (instance role credentials, user-data secrets) even if
// the instance hop-limit is misconfigured. pkg/isolate/egress.go blocks the same
// range on the isolate path. Only -d/-j so both backends express it.
func linkLocalDropSpec() []string {
	return []string{"-d", LinkLocalEgressCIDR, "-j", "DROP"}
}

// ensureLinkLocalDrop installs the link-local (IMDS) DROP into the user chain,
// idempotently. Must run after ensureBridgeForwardAccept (see EnsureChain).
func (m *Manager) ensureLinkLocalDrop() error {
	if err := m.ensurePolicyRule(linkLocalDropSpec()...); err != nil {
		return fmt.Errorf("insert link-local (IMDS) drop: %w", err)
	}
	return nil
}

// EnsureLinkLocalDrop installs the global link-local (IMDS) DROP into this
// manager's user chain, idempotently, WITHOUT bootstrapping the chain or its
// FORWARD jump.
//
// It exists for the Docker engine path. dockerd creates and owns DOCKER-USER and
// the FORWARD jump it is called from, so the Docker path must not call
// EnsureChain (it would create a chain and a jump dockerd is the authority for).
// But EnsureChain's link-local DROP is a global rule attached to no sandbox: a
// Docker sandbox created with no egress policy (the default) installs no per-IP
// rule of its own, so without a boot-time install nothing on the host blocks
// 169.254.0.0/16 and that sandbox reaches the EC2 instance metadata service over
// IPv4 whenever the instance's IMDSv2 hop-limit is misconfigured. This is the
// Docker-engine half of the same defense as the containerd path's
// EnsureChain-installed drop.
//
// Gated on the manager being enabled (SB_NETWORK_RULES), matching the containerd
// path: DOCKER-USER is the operator's chain, so the decision to add a global
// link-local DROP there rides the operator's existing egress-enforcement switch
// (see wireContainerEngine for the tradeoff). Fail-closed on the IPv6
// precondition like EnsureChain: the rule is IPv4-only.
func (m *Manager) EnsureLinkLocalDrop() error {
	if m == nil || !m.Enabled() {
		return nil
	}
	if err := m.verifyIPv6Disabled(); err != nil {
		return fmt.Errorf("netrules: refusing to install the link-local (IMDS) drop in %s: %w", m.filterChain(), err)
	}
	return m.ensureLinkLocalDrop()
}

// ensureBridgeForwardAccept installs subnet-scoped FORWARD ACCEPT rules for the
// sandbox bridge so its traffic survives dockerd's FORWARD DROP policy. They
// are APPENDED at the END of the user chain — never Inserted at position 1 —
// so a blocked/egress-policied sandbox is still dropped (its per-IP DROP sits
// above) while an unrestricted sandbox gets egress + sandbox↔sandbox
// connectivity. Inserting at position 1 let a ReassertChain re-insert leapfrog
// the per-IP DROPs and silently un-block an egress-blocked sandbox. Idempotent;
// no-op when no bridge subnet is set (dockerd path). Uses only -s/-d matches
// so it is expressible on both the exec and netlink backends.
func (m *Manager) ensureBridgeForwardAccept() error {
	if m == nil || strings.TrimSpace(m.bridgeSubnet) == "" {
		return nil
	}
	chain := m.filterChain()
	for _, spec := range [][]string{
		{"-s", m.bridgeSubnet, "-j", "ACCEPT"},
		{"-d", m.bridgeSubnet, "-j", "ACCEPT"},
	} {
		exists, err := m.ipt.Exists("filter", chain, spec...)
		if err != nil {
			return fmt.Errorf("check bridge forward accept: %w", err)
		}
		if exists {
			continue
		}
		if err := m.ipt.Append("filter", chain, spec...); err != nil {
			return fmt.Errorf("append bridge forward accept: %w", err)
		}
	}
	return nil
}

// ReassertChain re-runs the chain + FORWARD-jump bootstrap WITHOUT the latch, so
// a caller (e.g. the containerd netns reconcile ticker) can re-assert the jump
// after a dockerd restart flushes/reorders FORWARD and drops it. Both steps are
// idempotent; a no-op when disabled or the backend cannot bootstrap.
//
// Deliberately not gated on the IPv6 precondition like EnsureChain: healing the
// FORWARD jump must not stop because IPv6 drifted back on — that state already
// refuses every per-sandbox install and every new chain bootstrap, which is
// where the fail-closed signal belongs.
func (m *Manager) ReassertChain() error {
	if m == nil || !m.Enabled() {
		return nil
	}
	boot, ok := m.ipt.(bootstrapBackend)
	if !ok {
		return nil
	}
	chain := m.filterChain()
	if err := boot.EnsureUserChain(chain); err != nil {
		return err
	}
	if err := boot.EnsureForwardJump(chain); err != nil {
		return err
	}
	if err := m.ensureBridgeForwardAccept(); err != nil {
		return err
	}
	return m.ensureLinkLocalDrop()
}

// ResetChainLatch clears the bootstrap latch. Test-only seam.
func (m *Manager) ResetChainLatch() {
	if m == nil {
		return
	}
	m.chainReady.Store(false)
}
