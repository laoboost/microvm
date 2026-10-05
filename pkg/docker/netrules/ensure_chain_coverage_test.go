package netrules

import (
	"errors"
	"sync"
	"testing"
)

func TestEnsureChainConcurrentLatchFastPath(t *testing.T) {
	be := &memBackend{}
	mgr := &Manager{enabled: true, ipt: be, userChain: ChainAerolvmUser}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := mgr.EnsureChain(); err != nil {
				t.Errorf("EnsureChain: %v", err)
			}
		}()
	}
	wg.Wait()
	if !mgr.chainReady.Load() {
		t.Fatal("chainReady should latch")
	}
}

func TestEnsureBridgeForwardAcceptSkipsExistingRules(t *testing.T) {
	be := &memBackend{}
	mgr := &Manager{enabled: true, ipt: be, userChain: ChainAerolvmUser}
	mgr.SetBridgeSubnet("10.88.0.0/16")
	if err := be.Insert("filter", ChainAerolvmUser, 1, "-s", "10.88.0.0/16", "-j", "ACCEPT"); err != nil {
		t.Fatal(err)
	}
	if err := be.Insert("filter", ChainAerolvmUser, 1, "-d", "10.88.0.0/16", "-j", "ACCEPT"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ensureBridgeForwardAccept(); err != nil {
		t.Fatalf("ensureBridgeForwardAccept: %v", err)
	}
	if be.countMatching("ACCEPT") != 2 {
		t.Fatalf("rules = %v", be.rules)
	}
}

type bridgeAppendFailBackend struct {
	memBackend
	fail bool
}

// Append is the call the bridge-forward install path actually makes; failing
// Insert instead left this test green against a path that never ran.
func (b *bridgeAppendFailBackend) Append(table, chain string, spec ...string) error {
	if b.fail {
		return errors.New("bridge append boom")
	}
	return b.memBackend.Append(table, chain, spec...)
}

func TestEnsureBridgeForwardAcceptAppendError(t *testing.T) {
	be := &bridgeAppendFailBackend{fail: true}
	mgr := &Manager{enabled: true, ipt: be, userChain: ChainAerolvmUser}
	mgr.SetBridgeSubnet("10.88.0.0/16")
	if err := mgr.ensureBridgeForwardAccept(); err == nil {
		t.Fatal("expected append error")
	}
}

func TestReassertChainPropagatesBootstrapErrors(t *testing.T) {
	mgr := &Manager{enabled: true, ipt: &failingBootstrap{failChain: true}, userChain: ChainAerolvmUser}
	if err := mgr.ReassertChain(); err == nil {
		t.Fatal("want chain error")
	}
	mgr = &Manager{enabled: true, ipt: &failingBootstrap{failJump: true}, userChain: ChainAerolvmUser}
	if err := mgr.ReassertChain(); err == nil {
		t.Fatal("want jump error")
	}
}
