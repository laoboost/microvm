package isolate

import (
	"context"
	"sync"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// Inspect/Start/ListManaged/Create must never hand out the live
// *SandboxRuntimeState: Stop and markGroupMembersStopped write rec.state.Status
// under d.mu, so a caller reading Status from a shared pointer races with those
// writers even though the writer holds the lock.
func TestInspectAndStopDoNotRaceOnRuntimeState(t *testing.T) {
	d := New(Config{}, nil)
	d.mu.Lock()
	d.byID["sb-1"] = &sandboxRecord{state: &models.SandboxRuntimeState{SandboxID: "sb-1", Status: models.SandboxStatusStarted}}
	d.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 5000; i++ {
			st, err := d.Inspect(context.Background(), "sb-1")
			if err != nil || st == nil {
				t.Errorf("Inspect: %v %+v", err, st)
				return
			}
			_ = st.Status // caller-side read; no lock is available to the caller
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 5000; i++ {
			if err := d.Stop(context.Background(), "sb-1"); err != nil {
				t.Errorf("Stop: %v", err)
				return
			}
		}
	}()
	wg.Wait()
}

// Same contract in testable form (fails without -race too): every read path
// returns a detached copy, so a caller mutating its result can never corrupt
// the driver's record.
func TestLifecycleReturnsDetachedStateCopies(t *testing.T) {
	sup := &fakeSupervisor{}
	d := newCreateDriver(t, GroupPerTenant, sup)
	ctx := context.Background()

	created, err := d.Create(ctx, req("acme"), "sb-copy", "", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	inspected, err := d.Inspect(ctx, "sb-copy")
	if err != nil || inspected == nil {
		t.Fatalf("Inspect: %v %+v", err, inspected)
	}
	started, err := d.Start(ctx, "sb-copy")
	if err != nil || started == nil {
		t.Fatalf("Start: %v %+v", err, started)
	}
	managed, err := d.ListManaged(ctx)
	if err != nil {
		t.Fatalf("ListManaged: %v", err)
	}

	d.mu.Lock()
	live := d.byID["sb-copy"].state
	d.mu.Unlock()

	for name, got := range map[string]*models.SandboxRuntimeState{
		"Create":      created,
		"Inspect":     inspected,
		"Start":       started,
		"ListManaged": managed["sb-copy"],
	} {
		if got == nil {
			t.Fatalf("%s returned nil", name)
		}
		if got == live {
			t.Fatalf("%s returned the live record (callers race with Stop)", name)
		}
	}

	// Mutating a returned copy must not corrupt the record.
	created.Status = models.SandboxStatusError
	d.mu.Lock()
	gotStatus := live.Status
	d.mu.Unlock()
	if gotStatus != models.SandboxStatusStarted {
		t.Fatalf("mutating Create result changed live status to %s", gotStatus)
	}
}
