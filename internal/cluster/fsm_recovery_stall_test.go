package cluster

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/models"
)

// blockingResolverSetup restores a snapshot whose row carries a RecoveryRef
// but no local payload into a sink FSM, then installs a recoveryResolver that
// blocks on a channel — the shape of a slow peer fetch.
func blockingResolverSetup(t *testing.T) (sink *placementFSM, started chan struct{}, release chan struct{}) {
	t.Helper()
	src := newPlacementFSM()
	if res := applyOp(t, src, command{
		Op: opPlace, SandboxID: "sb-stall", OwnerNodeID: "A", OwnerAPIURL: "http://a",
		Spec:      &models.CreateSandboxRequest{Image: "alpine:3.20", Name: "stall-me", CPU: 1, MemoryMB: 512},
		SecretRef: "cluster-secret://sandbox/sb-stall/v1", SecretVersion: 1,
	}); res != nil {
		t.Fatalf("place: %v", res)
	}
	snap, err := src.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	sk := &fakeSnapshotSink{Buffer: &bytes.Buffer{}}
	if err := snap.Persist(sk); err != nil {
		t.Fatalf("persist: %v", err)
	}

	sink = newPlacementFSMWithRecoveryStore(newPlacementRecoveryMemoryStore())
	// Restore with no resolver so nothing hydrates (and nothing blocks) yet.
	if err := sink.Restore(io.NopCloser(bytes.NewReader(sk.Buffer.Bytes()))); err != nil {
		t.Fatalf("restore: %v", err)
	}

	started = make(chan struct{})
	release = make(chan struct{})
	sink.recoveryResolver = func(ctx context.Context, ref string) (RecoveryBlob, bool, error) {
		close(started)
		<-release
		record, ok, err := src.recoveryStore.GetRecord(ref)
		if err != nil || !ok {
			return RecoveryBlob{}, ok, err
		}
		return recoveryBlobFromRecord(ref, record), true, nil
	}
	return sink, started, release
}

// TestFSMRecoveryFetchDoesNotBlockApply pins the lock-holding bug in
// resolveRecoveryRef: get()/snapshot() ran the remote fetch-on-miss UNDER
// f.mu, so a slow peer stalled every Apply behind an RWMutex write lock for
// up to the fetch timeout. Reads must hydrate OUTSIDE f.mu: while a fetch is
// in flight, Apply must still complete.
func TestFSMRecoveryFetchDoesNotBlockApply(t *testing.T) {
	for _, read := range []struct {
		name string
		fn   func(f *placementFSM) error
	}{
		{"get", func(f *placementFSM) error {
			p, ok := f.get("sb-stall")
			if !ok {
				return errTest("placement missing")
			}
			if p.Spec == nil || p.Spec.Image != "alpine:3.20" {
				return errTest("spec not hydrated outside the lock")
			}
			return nil
		}},
		{"snapshot", func(f *placementFSM) error {
			m := f.snapshot()
			p := m["sb-stall"]
			if p.Spec == nil {
				return errTest("spec not hydrated outside the lock")
			}
			return nil
		}},
	} {
		t.Run(read.name, func(t *testing.T) {
			sink, started, release := blockingResolverSetup(t)

			readDone := make(chan error, 1)
			go func() { readDone <- read.fn(sink) }()

			// Wait until the read is inside the (blocking) remote fetch.
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("recovery resolver never fired during read")
			}

			// Apply must complete while that fetch is still pending.
			applied := make(chan interface{}, 1)
			go func() {
				applied <- applyOp(t, sink, command{
					Op: opPlace, SandboxID: "sb-during-fetch", OwnerNodeID: "B", OwnerAPIURL: "http://b",
					Spec: &models.CreateSandboxRequest{Image: "busybox", Name: "during-fetch"},
				})
			}()
			select {
			case res := <-applied:
				if res != nil {
					t.Fatalf("apply during in-flight recovery fetch: %v", res)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Apply blocked behind an in-flight recovery fetch (resolve ran under f.mu)")
			}

			close(release)
			select {
			case err := <-readDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("read did not finish after the fetch was released")
			}
		})
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
