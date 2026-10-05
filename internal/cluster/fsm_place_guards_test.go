package cluster

import (
	"errors"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// spyRecoveryStore wraps a real store and records what the FSM looked like at
// Put time, so a test can pin the apply ordering (recovery Put BEFORE any
// index mutation). putErr, when set, makes Put fail like failPutRecoveryStore.
type spyRecoveryStore struct {
	inner     placementRecoveryStore
	fsm       *placementFSM
	putErr    error
	putCalls  int
	nameAtPut map[string]string // copy of nameIndex at the last Put
}

func newSpyRecoveryStore(putErr error) *spyRecoveryStore {
	return &spyRecoveryStore{
		inner:     newPlacementRecoveryMemoryStore(),
		putErr:    putErr,
		nameAtPut: make(map[string]string),
	}
}

func (s *spyRecoveryStore) Put(id string, rec placementRecovery) (string, error) {
	s.putCalls++
	if s.fsm != nil {
		s.nameAtPut = make(map[string]string, len(s.fsm.nameIndex))
		for k, v := range s.fsm.nameIndex {
			s.nameAtPut[k] = v
		}
	}
	if s.putErr != nil {
		return "", s.putErr
	}
	return s.inner.Put(id, rec)
}
func (s *spyRecoveryStore) Get(ref string) (placementRecovery, bool, error) {
	return s.inner.Get(ref)
}
func (s *spyRecoveryStore) GetRecord(ref string) (placementRecoveryStoreRecord, bool, error) {
	return s.inner.GetRecord(ref)
}
func (s *spyRecoveryStore) Delete(ref string) error { return s.inner.Delete(ref) }
func (s *spyRecoveryStore) RetainSnapshotRefs(refs []string) error {
	return s.inner.RetainSnapshotRefs(refs)
}

// TestFSMOpPlaceWithFailingRecoveryStoreFallsBackToInlineAndAppliesAllOrNothing
// documents the chosen C6a semantics: a failing recoveryStore.Put must NOT
// fail or half-apply the entry. The FSM falls back to the in-memory recovery
// map (same deterministic content-addressed RecoveryRef) and applies the
// placement + indexes completely, so every replica converges regardless of
// which nodes had local I/O errors.
func TestFSMOpPlaceWithFailingRecoveryStoreFallsBackToInlineAndAppliesAllOrNothing(t *testing.T) {
	spy := newSpyRecoveryStore(nil)
	fsm := newPlacementFSMWithRecoveryStore(spy)
	spy.fsm = fsm

	if got := applyOp(t, fsm, command{
		Op: opPlace, SandboxID: "sb-atomic", OwnerNodeID: "node-a", OwnerAPIURL: "http://a",
		IncarnationID: "inc-atomic",
		Spec:          &models.CreateSandboxRequest{Name: "old-name", Image: "alpine:1"},
	}); got != nil {
		t.Fatalf("initial place: %v", got)
	}

	// Now the local recovery store starts failing (disk full, permissions…).
	spy.putErr = errors.New("forced put failure")

	if got := applyOp(t, fsm, command{
		Op: opPlace, SandboxID: "sb-atomic", OwnerNodeID: "node-a", OwnerAPIURL: "http://a",
		ExpectedIncarnationID: "inc-atomic",
		Spec:                  &models.CreateSandboxRequest{Name: "new-name", Image: "alpine:2"},
	}); got != nil {
		t.Fatalf("re-place with failing recovery store = %v, want nil (in-memory fallback must keep apply deterministic)", got)
	}

	if owner, ok := fsm.sandboxIDByName("new-name"); !ok || owner != "sb-atomic" {
		t.Fatalf("name index after fallback = (%q, %v), want (sb-atomic, true)", owner, ok)
	}
	if owner, ok := fsm.sandboxIDByName("old-name"); ok {
		t.Fatalf("stale name index still maps old-name -> %q; rename must not half-apply", owner)
	}
	p, ok := fsm.get("sb-atomic")
	if !ok {
		t.Fatal("placement missing after fallback apply")
	}
	if p.Name != "new-name" || p.Spec == nil || p.Spec.Image != "alpine:2" {
		t.Fatalf("placement after fallback apply = %+v, want renamed row with hydrated spec", p)
	}
	if ids := fsm.idsOwnedBy("node-a"); len(ids) != 1 || ids[0] != "sb-atomic" {
		t.Fatalf("owner index after fallback = %v, want [sb-atomic]", ids)
	}
}

// TestFSMOpPlaceRecoversInlineWhenRecoveryPutFails pins C6a as it applies to
// opPlace: the recovery Put is still attempted, and when it fails the apply
// must not error or half-apply — the payload is kept inline and the placement
// is stored like any other. The index ordering that used to matter (Put before
// index mutation, so a failed Put could not leave a half-released index) is
// subsumed: the invariant now is that the indexes end up fully consistent
// with the applied command.
func TestFSMOpPlaceRecoversInlineWhenRecoveryPutFails(t *testing.T) {
	spy := newSpyRecoveryStore(nil)
	fsm := newPlacementFSMWithRecoveryStore(spy)
	spy.fsm = fsm

	if got := applyOp(t, fsm, command{
		Op: opPlace, SandboxID: "sb-order", OwnerNodeID: "node-a",
		IncarnationID: "inc-order",
		Spec:          &models.CreateSandboxRequest{Name: "old-name", Image: "alpine:1"},
	}); got != nil {
		t.Fatalf("initial place: %v", got)
	}

	spy.putErr = errors.New("forced put failure")
	spy.putCalls = 0
	if got := applyOp(t, fsm, command{
		Op: opPlace, SandboxID: "sb-order", OwnerNodeID: "node-a",
		ExpectedIncarnationID: "inc-order",
		Spec:                  &models.CreateSandboxRequest{Name: "new-name", Image: "alpine:2"},
	}); got != nil {
		t.Fatalf("re-place with failing recovery store = %v, want nil (inline fallback)", got)
	}

	if spy.putCalls == 0 {
		t.Fatal("recovery Put was never invoked")
	}
	if owner, ok := fsm.sandboxIDByName("new-name"); !ok || owner != "sb-order" {
		t.Fatalf("name index after fallback = new-name -> (%q, %v), want (sb-order, true)", owner, ok)
	}
	if owner, ok := fsm.sandboxIDByName("old-name"); ok {
		t.Fatalf("old name still claimed after fallback: %q", owner)
	}
}

// TestFSMOpPlaceRejectsReservedRowHeldByDifferentOwner pins the C6c fix: a
// RESERVED row is an ownership claim like any other — opPlace from a
// different owner must not steal it (only opReserve with an explicit
// AllowExpiredOverwrite may take over). The same owner's promote stays the
// happy path.
func TestFSMOpPlaceRejectsReservedRowHeldByDifferentOwner(t *testing.T) {
	fsm := newPlacementFSM()
	if got := applyOp(t, fsm, command{
		Op: opReserve, SandboxID: "sb-claim", OwnerNodeID: "owner-a", OwnerAPIURL: "http://a",
		IncarnationID: "inc-claim",
		Spec:          &models.CreateSandboxRequest{Name: "claim", Image: "alpine"},
		ExpiresUnix:   4_000_000_000,
		NowUnix:       1_700_000_000,
	}); got != nil {
		t.Fatalf("reserve by owner-a: %v", got)
	}

	got := applyOp(t, fsm, command{
		Op: opPlace, SandboxID: "sb-claim", OwnerNodeID: "owner-b", OwnerAPIURL: "http://b",
		ExpectedIncarnationID: "inc-claim",
		NowUnix:               1_700_000_001,
	})
	err, ok := got.(error)
	if !ok || !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("opPlace by owner-b over owner-a reservation = %v, want ErrReservationConflict", got)
	}
	p, ok := fsm.get("sb-claim")
	if !ok || p.OwnerNodeID != "owner-a" || !p.IsReserved() {
		t.Fatalf("reservation mutated by rejected steal: %+v ok=%v", p, ok)
	}

	if got := applyOp(t, fsm, command{
		Op: opPlace, SandboxID: "sb-claim", OwnerNodeID: "owner-a", OwnerAPIURL: "http://a",
		ExpectedIncarnationID: "inc-claim",
		NowUnix:               1_700_000_002,
	}); got != nil {
		t.Fatalf("opPlace by owner-a (promotion) = %v, want nil", got)
	}
	p, _ = fsm.get("sb-claim")
	if p.OwnerNodeID != "owner-a" || p.IsReserved() {
		t.Fatalf("promotion by owner-a failed: %+v", p)
	}
}
