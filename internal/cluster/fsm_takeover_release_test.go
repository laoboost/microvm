package cluster

import (
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// TestFSMReserveExpiredOverwriteReleasesHostClaims pins F2j: taking over an
// expired reservation must release the same index claims opCancelReserve does,
// including the host-port and custom-hostname claims. Without it the old
// reservation's host port / hostname stay claimed in the FSM indexes while the
// replacement row no longer carries them — leaking the claim cluster-wide (no
// other sandbox can take the port/hostname) and leaving the indexes pointing at
// a row that no longer references them.
func TestFSMReserveExpiredOverwriteReleasesHostClaims(t *testing.T) {
	fsm := newPlacementFSM()
	spec := &models.CreateSandboxRequest{Name: "take", Image: "alpine"}

	apply := func(cmd command) {
		t.Helper()
		if got := applyOp(t, fsm, cmd); got != nil {
			t.Fatalf("apply op=%d: %v", cmd.Op, got)
		}
	}

	apply(command{Op: opReserve, SandboxID: "sb-take", OwnerNodeID: "owner-a", Spec: spec, NowUnix: 100, ExpiresUnix: 1000})
	apply(command{
		Op: opAddExposedPort, SandboxID: "sb-take", Port: 8080,
		Protocol: models.ExposedPortProtocolTCP, HostPort: 40000, NowUnix: 110,
	})
	apply(command{Op: opAddCustomDomain, SandboxID: "sb-take", Hostname: "app.example.com", NowUnix: 120})

	// The proposer authorized taking over the (expired) reservation for a
	// different owner. Apply must release every claim the old row held.
	apply(command{
		Op: opReserve, SandboxID: "sb-take", OwnerNodeID: "owner-b", Spec: spec,
		NowUnix: 200, ExpiresUnix: 2000, AllowExpiredOverwrite: true,
	})

	fsm.mu.RLock()
	defer fsm.mu.RUnlock()
	if claim, ok := fsm.hostPortIndex[40000]; ok {
		t.Fatalf("host port 40000 still claimed by %+v after expired-reservation takeover", claim)
	}
	if owner, ok := fsm.customHostnameIndex["app.example.com"]; ok {
		t.Fatalf("hostname still claimed by %q after expired-reservation takeover", owner)
	}
}
