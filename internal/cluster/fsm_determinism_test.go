package cluster

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// fsmStateDigest returns a stable serialization of the FSM's replicated
// state (hot rows + every index). Two FSMs that applied the same log entries
// must produce byte-identical digests — any wall-clock read inside Apply
// shows up as a divergence here.
func fsmStateDigest(t *testing.T, fsm *placementFSM) string {
	t.Helper()
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	state := struct {
		Placements    map[string]Placement               `json:"placements"`
		NameIndex     map[string]string                  `json:"name_index"`
		OwnerIndex    map[string]map[string]struct{}     `json:"owner_index"`
		ReservedIndex map[string]struct{}                `json:"reserved_index"`
		Pending       map[string]pendingReservationClaim `json:"pending"`
		Drained       map[string]bool                    `json:"drained"`
		Hostnames     map[string]string                  `json:"hostnames"`
	}{
		Placements:    fsm.snapshotLockedForTest(),
		NameIndex:     fsm.nameIndex,
		OwnerIndex:    fsm.ownerIndex,
		ReservedIndex: fsm.reservedIndex,
		Pending:       fsm.pendingReservationClaims,
		Drained:       fsm.drainedNodes,
		Hostnames:     fsm.customHostnameIndex,
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state digest: %v", err)
	}
	return string(raw)
}

func (f *placementFSM) snapshotLockedForTest() map[string]Placement {
	out := make(map[string]Placement, len(f.placements))
	for k := range f.placements {
		p, _ := f.fullPlacementLocked(k)
		out[k] = clonePlacement(p)
	}
	return out
}

// TestFSMReserveExpiredOverwriteRequiresExplicitFlag pins the C5 contract:
// Apply is a pure function of (prior state, command). The decision to take
// over an expired reservation is stamped on the command by the proposer as
// AllowExpiredOverwrite — Apply never consults the wall clock to decide.
// With the flag false, an expired row held by a different owner still yields
// ErrReservationConflict; with the flag true the row is taken over. Both
// sequences run on two fresh FSMs and must converge to identical state.
func TestFSMReserveExpiredOverwriteRequiresExplicitFlag(t *testing.T) {
	const (
		now          = int64(1_700_000_000)
		expiredAt    = now - 60 // expired relative to the proposer's stamp
		freshExpires = now + 120
	)

	reserveA := command{
		Op: opReserve, SandboxID: "sb-det", OwnerNodeID: "owner-a",
		Spec:        &models.CreateSandboxRequest{Name: "det", Image: "alpine"},
		NowUnix:     now,
		ExpiresUnix: expiredAt,
	}
	reserveBNoSteal := command{
		Op: opReserve, SandboxID: "sb-det", OwnerNodeID: "owner-b",
		Spec:                  &models.CreateSandboxRequest{Name: "det", Image: "alpine"},
		NowUnix:               now,
		ExpiresUnix:           freshExpires,
		AllowExpiredOverwrite: false,
	}
	reserveBSteal := command{
		Op: opReserve, SandboxID: "sb-det", OwnerNodeID: "owner-b",
		Spec:                  &models.CreateSandboxRequest{Name: "det", Image: "alpine"},
		NowUnix:               now,
		ExpiresUnix:           freshExpires,
		AllowExpiredOverwrite: true,
	}

	// Flag=false: the proposer did NOT authorize an expired-overwrite, so the
	// second reserve must conflict even though the existing row is expired.
	f1, f2 := newPlacementFSM(), newPlacementFSM()
	for i, fsm := range []*placementFSM{f1, f2} {
		if got := applyOp(t, fsm, reserveA); got != nil {
			t.Fatalf("fsm%d initial reserve: %v", i, got)
		}
		got := applyOp(t, fsm, reserveBNoSteal)
		err, ok := got.(error)
		if !ok || !errors.Is(err, ErrReservationConflict) {
			t.Fatalf("fsm%d flag=false over expired foreign reservation = %v, want ErrReservationConflict", i, got)
		}
		p, ok := fsm.get("sb-det")
		if !ok || p.OwnerNodeID != "owner-a" {
			t.Fatalf("fsm%d flag=false mutated owner to %q, want owner-a", i, p.OwnerNodeID)
		}
	}
	if d1, d2 := fsmStateDigest(t, f1), fsmStateDigest(t, f2); d1 != d2 {
		t.Fatalf("flag=false state diverged across replicas:\n%s\n%s", d1, d2)
	}

	// Flag=true: the proposer evaluated expiry and authorized the takeover.
	g1, g2 := newPlacementFSM(), newPlacementFSM()
	for i, fsm := range []*placementFSM{g1, g2} {
		if got := applyOp(t, fsm, reserveA); got != nil {
			t.Fatalf("fsm%d initial reserve: %v", i, got)
		}
		if got := applyOp(t, fsm, reserveBSteal); got != nil {
			t.Fatalf("fsm%d flag=true over expired foreign reservation = %v, want takeover (nil)", i, got)
		}
		p, ok := fsm.get("sb-det")
		if !ok || p.OwnerNodeID != "owner-b" {
			t.Fatalf("fsm%d flag=true did not take over: %+v ok=%v", i, p, ok)
		}
	}
	if d1, d2 := fsmStateDigest(t, g1), fsmStateDigest(t, g2); d1 != d2 {
		t.Fatalf("flag=true state diverged across replicas:\n%s\n%s", d1, d2)
	}
}

// TestFSMApplyHasNoWallClockReads is the static half of the determinism fix:
// parse fsm.go and assert that no function on the Apply path calls time.Now.
// The only tolerated occurrences are the snapshot Persist/Restore metric
// timers, which never touch replicated state.
func TestFSMApplyHasNoWallClockReads(t *testing.T) {
	src, err := os.ReadFile("fsm.go")
	if err != nil {
		t.Fatalf("read fsm.go: %v", err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fsm.go", src, 0)
	if err != nil {
		t.Fatalf("parse fsm.go: %v", err)
	}
	allowed := map[string]bool{
		"Restore": true, // snapshot restore metric timer
		"Persist": true, // snapshot persist metric timer
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		name := fn.Name.Name
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if pkgIdent.Name == "time" && sel.Sel.Name == "Now" {
				if !allowed[name] {
					t.Errorf("fsm.go: function %s calls time.Now; Apply-path state must come from the command only", name)
				}
			}
			return true
		})
	}
}

// TestFSMApplyStoresOnlyCommandSuppliedTimes pins that every timestamp written
// by Apply comes from the command (NowUnix), never from the local clock.
// A replica replaying the same log must store the same CreatedUnix /
// UpdatedUnix / OrphanedUnix / volume CreatedAt as the proposer recorded.
func TestFSMApplyStoresOnlyCommandSuppliedTimes(t *testing.T) {
	fsm := newPlacementFSM()

	if got := applyOp(t, fsm, command{
		Op: opPlace, SandboxID: "sb-times", OwnerNodeID: "node-a",
		Spec:    &models.CreateSandboxRequest{Name: "times", Image: "alpine"},
		NowUnix: 111,
	}); got != nil {
		t.Fatalf("opPlace: %v", got)
	}
	p, ok := fsm.get("sb-times")
	if !ok {
		t.Fatal("placement missing")
	}
	if p.CreatedUnix != 111 || p.UpdatedUnix != 111 {
		t.Fatalf("opPlace times = created %d updated %d, want 111/111 from command", p.CreatedUnix, p.UpdatedUnix)
	}

	if got := applyOp(t, fsm, command{
		Op: opUpsertSpec, SandboxID: "sb-times", NowUnix: 222,
		Spec: &models.CreateSandboxRequest{Name: "times", Image: "alpine:3.20"},
	}); got != nil {
		t.Fatalf("opUpsertSpec: %v", got)
	}
	p, _ = fsm.get("sb-times")
	if p.UpdatedUnix != 222 {
		t.Fatalf("opUpsertSpec updated = %d, want 222 from command", p.UpdatedUnix)
	}

	if got := applyOp(t, fsm, command{
		Op: opReserve, SandboxID: "sb-rsv", OwnerNodeID: "node-a",
		NowUnix: 333, ExpiresUnix: 999,
	}); got != nil {
		t.Fatalf("opReserve: %v", got)
	}
	r, ok := fsm.get("sb-rsv")
	if !ok {
		t.Fatal("reservation missing")
	}
	if r.CreatedUnix != 333 || r.UpdatedUnix != 333 {
		t.Fatalf("opReserve times = created %d updated %d, want 333/333 from command", r.CreatedUnix, r.UpdatedUnix)
	}

	if got := applyOp(t, fsm, command{Op: opReassign, SandboxID: "sb-times", OwnerNodeID: "", NowUnix: 444}); got != nil {
		t.Fatalf("opReassign orphan: %v", got)
	}
	p, _ = fsm.get("sb-times")
	if p.OrphanedUnix != 444 || p.UpdatedUnix != 444 {
		t.Fatalf("opReassign orphan times = orphaned %d updated %d, want 444/444 from command", p.OrphanedUnix, p.UpdatedUnix)
	}

	vol := &models.Volume{Tenant: "t1", Name: "vol", ID: "v-1", Backend: "nfs"}
	if got := applyOp(t, fsm, command{Op: opUpsertVolume, Volume: vol, NowUnix: 555}); got != nil {
		t.Fatalf("opUpsertVolume: %v", got)
	}
	got := fsm.volumes[volumeKey("t1", "v-1")]
	if got.CreatedAt.Unix() != 555 {
		t.Fatalf("opUpsertVolume CreatedAt = %d, want 555 from command", got.CreatedAt.Unix())
	}
}

// TestCommandReservationsCarryDecisionFields pins the wire plumbing: the
// single-reserve and batch-reserve encodings must round-trip the proposer
// decision fields so a replayed log entry applies identically on every node.
func TestCommandReservationsCarryDecisionFields(t *testing.T) {
	cmd := command{
		Op: opReserveBatch,
		Reservations: []reservationCommand{{
			SandboxID: "sb-wire", OwnerNodeID: "node-a",
			NowUnix: 42, ExpiresUnix: 99, AllowExpiredOverwrite: true,
		}},
	}
	payload, err := encodeCommand(cmd)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := decodeCommand(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Reservations) != 1 {
		t.Fatalf("decoded reservations = %d, want 1", len(decoded.Reservations))
	}
	r := decoded.Reservations[0]
	if r.NowUnix != 42 || r.ExpiresUnix != 99 || !r.AllowExpiredOverwrite {
		t.Fatalf("reservation decision fields lost in round trip: %+v", r)
	}
	single := reservationFromCommand(command{
		Op: opReserve, SandboxID: "sb-wire", OwnerNodeID: "node-a",
		NowUnix: 7, ExpiresUnix: 8, AllowExpiredOverwrite: true,
	})
	if single.NowUnix != 7 || !single.AllowExpiredOverwrite {
		t.Fatalf("reservationFromCommand dropped decision fields: %+v", single)
	}
	back := commandFromReservation(reservationCommand{
		SandboxID: "sb-wire", OwnerNodeID: "node-a",
		NowUnix: 7, ExpiresUnix: 8, AllowExpiredOverwrite: true,
	})
	if back.NowUnix != 7 || !back.AllowExpiredOverwrite {
		t.Fatalf("commandFromReservation dropped decision fields: %+v", back)
	}
}
