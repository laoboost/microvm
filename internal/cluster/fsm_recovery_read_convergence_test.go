package cluster

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// readErrorRecoveryStore wraps a working memory store but fails every read, the
// way a replica with a failing disk behaves: Put still succeeds (the blob lands
// on disk) but Get/GetRecord error out.
type readErrorRecoveryStore struct {
	*placementRecoveryMemoryStore
}

func (s readErrorRecoveryStore) Get(string) (placementRecovery, bool, error) {
	return placementRecovery{}, false, errors.New("injected recovery-store read failure")
}

func (s readErrorRecoveryStore) GetRecord(string) (placementRecoveryStoreRecord, bool, error) {
	return placementRecoveryStoreRecord{}, false, errors.New("injected recovery-store read failure")
}

// fsmReplicatedStateDigest serializes the *replicated* hot state — the rows and
// indexes raft actually replicates and snapshots — without hydrating the
// per-replica recovery payload from the local store. Hydration is intentionally
// excluded: a replica with a failing disk cannot resolve a ref it still holds,
// so comparing hydrated views would always differ. What must converge is the
// replicated fact (the content-addressed RecoveryRef), not the derived payload.
func fsmReplicatedStateDigest(t *testing.T, fsm *placementFSM) string {
	t.Helper()
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	// RecoveryRef is json:"-" on Placement (it is not part of the row's wire
	// shape), so surface it explicitly — it is exactly the replicated fact that
	// must stay convergent.
	refs := make(map[string]string, len(fsm.placements))
	for id, p := range fsm.placements {
		refs[id] = p.RecoveryRef
	}
	state := struct {
		Placements map[string]Placement           `json:"placements"`
		Refs       map[string]string              `json:"refs"`
		NameIndex  map[string]string              `json:"name_index"`
		OwnerIndex map[string]map[string]struct{} `json:"owner_index"`
		Reserved   map[string]struct{}            `json:"reserved_index"`
	}{
		Placements: fsm.placements,
		Refs:       refs,
		NameIndex:  fsm.nameIndex,
		OwnerIndex: fsm.ownerIndex,
		Reserved:   fsm.reservedIndex,
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal replicated state digest: %v", err)
	}
	return string(raw)
}

// TestFSMRecoveryReadFailureConverges pins F2b: a local recovery-store READ
// failure must not let a replica permanently drop the RecoveryRef a healthy
// replica keeps. Before the fix, opPlace rebuilt the row from the nil
// (unresolved) spec, cleared RecoveryRef and deleted the in-memory payload —
// diverging from a healthy replica and then propagating that loss to new
// voters via snapshot.
func TestFSMRecoveryReadFailureConverges(t *testing.T) {
	broken := newPlacementFSMWithRecoveryStore(readErrorRecoveryStore{newPlacementRecoveryMemoryStore()})
	healthy := newPlacementFSM()

	reserve := command{
		Op: opReserve, SandboxID: "sb-recv", OwnerNodeID: "owner-a",
		Spec:        &models.CreateSandboxRequest{Name: "recv", Image: "alpine"},
		NowUnix:     100,
		ExpiresUnix: 1000,
	}
	promote := command{
		Op: opPlace, SandboxID: "sb-recv", OwnerNodeID: "owner-a", NowUnix: 200,
	}

	for i, fsm := range []*placementFSM{broken, healthy} {
		if got := applyOp(t, fsm, reserve); got != nil {
			t.Fatalf("fsm%d opReserve = %v, want nil", i, got)
		}
		if got := applyOp(t, fsm, promote); got != nil {
			t.Fatalf("fsm%d opPlace = %v, want nil", i, got)
		}
	}

	if d1, d2 := fsmReplicatedStateDigest(t, broken), fsmReplicatedStateDigest(t, healthy); d1 != d2 {
		t.Fatalf("replicas diverged after a local recovery-store read failure:\nbroken:  %s\nhealthy: %s", d1, d2)
	}
}
