package netrules

import (
	"strings"
	"testing"
)

// indexOfRule returns the position of the first rule containing substr, or -1.
func (m *memBackend) indexOfRule(substr string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.rules {
		if strings.Contains(r, substr) {
			return i
		}
	}
	return -1
}

// deleteMatching removes every rule containing substr — models the piece of a
// chain flush that drops the bridge ACCEPTs while leaving per-IP rules alone.
func (m *memBackend) deleteMatching(substr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.rules[:0]
	for _, r := range m.rules {
		if !strings.Contains(r, substr) {
			kept = append(kept, r)
		}
	}
	m.rules = kept
}

// TestReassertChainBridgeAcceptStaysBelowPerIPDrop pins the egress-block
// bypass: bridge ACCEPTs used to Insert at position 1, so a ReassertChain that
// re-inserted missing ACCEPTs LEAPFROGGED the per-IP DROPs already in the
// chain — an egress-blocked sandbox suddenly had its traffic ACCEPTed. The
// ACCEPTs must land at the END of the user chain: below every per-IP DROP.
func TestReassertChainBridgeAcceptStaysBelowPerIPDrop(t *testing.T) {
	be := &memBackend{}
	mgr := &Manager{enabled: true, ipt: be, userChain: ChainAerolvmUser}
	mgr.SetBridgeSubnet("10.88.0.0/16")
	if err := mgr.EnsureChain(); err != nil {
		t.Fatalf("EnsureChain: %v", err)
	}

	// A sandbox is egress-blocked: its per-IP DROP sits at the top.
	if err := mgr.BlockAllEgress("10.0.0.5"); err != nil {
		t.Fatalf("BlockAllEgress: %v", err)
	}
	dropIdx := be.indexOfRule("|-s|10.0.0.5|-j|DROP")
	if dropIdx < 0 {
		t.Fatalf("per-IP DROP missing: %v", be.rules)
	}

	// The bridge ACCEPTs go missing (partial chain flush) while the DROP stays.
	be.deleteMatching("|-s|10.88.0.0/16|-j|ACCEPT")
	be.deleteMatching("|-d|10.88.0.0/16|-j|ACCEPT")

	if err := mgr.ReassertChain(); err != nil {
		t.Fatalf("ReassertChain: %v", err)
	}

	for _, want := range []string{"|-s|10.88.0.0/16|-j|ACCEPT", "|-d|10.88.0.0/16|-j|ACCEPT"} {
		accIdx := be.indexOfRule(want)
		if accIdx < 0 {
			t.Fatalf("bridge ACCEPT %s not restored: %v", want, be.rules)
		}
		dropIdx := be.indexOfRule("|-s|10.0.0.5|-j|DROP")
		if accIdx < dropIdx {
			t.Fatalf("bridge ACCEPT %s at %d leapfrogged the per-IP DROP at %d — egress block bypassed: %v",
				want, accIdx, dropIdx, be.rules)
		}
	}
}
