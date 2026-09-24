package dockerpool

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestRaceSettersVsPoolOps pins the unlocked spawner/onReleasePark/idleTTL
// field accesses: SetSpawner/SetParkReleaser/SetIdleTTL write those fields
// (SetSpawner also from the refill goroutine via Run) while destroySlots,
// releasePark and ReapIdle read them without the pool mutex. Under -race a
// concurrent setter + Acquire/ReapIdle is a data race.
func TestRaceSettersVsPoolOps(t *testing.T) {
	p := New(nil)
	p.SetDefaultDepth(1)
	p.PinTarget(Key{Image: "img", Runtime: "rt"})

	var wg sync.WaitGroup
	ctx := context.Background()

	// Setters: spawner, park releaser, idle TTL.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 400; j++ {
				p.SetSpawner(&refillSpawner{})
				p.SetParkReleaser(func(string) {})
				p.SetIdleTTL(time.Duration(j) * time.Millisecond)
			}
		}(i)
	}

	// Readers/mutators that touch the same fields unlocked today.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 400; j++ {
				// Acquire → destroySlots reads p.spawner, releasePark reads
				// p.onReleasePark (dead slots force the destroy path).
				p.RecordLoaded(&ParkedSlot{ID: "park-race", Key: Key{Image: "img", Runtime: "rt"}})
				_, _ = p.Acquire(ctx, Key{Image: "img", Runtime: "rt"}, "")
				// ReapIdle reads p.idleTTL both outside and inside the lock.
				p.ReapIdle(time.Now().UTC())
			}
		}()
	}
	wg.Wait()
}

// TestRunDoesNotRewriteSpawner pins that Run must not perform a second
// SetSpawner write from the refill goroutine — the daemon wiring already
// installed the spawner before starting the loop, and the extra unlocked
// write raced with Acquire-driven destroySlots.
func TestRunDoesNotRewriteSpawner(t *testing.T) {
	p := New(nil)
	installed := &refillSpawner{}
	p.SetSpawner(installed)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Run(ctx, RefillConfig{RefillInterval: time.Hour}, &refillSpawner{}, nil)
	}()
	// Let Run reach its select loop (it used to call SetSpawner on entry).
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	p.mu.Lock()
	got := p.spawner
	p.mu.Unlock()
	if got != Spawner(installed) {
		t.Fatalf("Run replaced the pre-installed spawner (%T), want the one set via SetSpawner", got)
	}
}
