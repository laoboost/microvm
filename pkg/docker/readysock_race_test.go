package docker

import (
	"context"
	"sync"
	"testing"
	"time"
)

// ParkedListener's lifecycle fields (closed/listener/conn/dead) were guarded by
// two different mutexes — closed was read under connMu but written under
// closeMu — and Close() nil-ed listener while WaitParked still held it, so the
// type-assert in WaitParked could nil-deref mid-close.
func TestParkedListenerLifecycleRace(t *testing.T) {
	pl, err := NewParkedListener(t.TempDir(), "slot-race", "boot-token", "park-nonce")
	if err != nil {
		t.Fatalf("NewParkedListener: %v", err)
	}

	const iters = 200
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
			_ = pl.WaitParked(ctx)
			cancel()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = pl.Alive()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = pl.Close()
		}
	}()
	wg.Wait()
}

// ReadyListener.Wait reads l.listener unlocked while Close() nils it under
// closeMu — same class of lifecycle race.
func TestReadyListenerWaitCloseRace(t *testing.T) {
	rl, err := NewReadyListener(t.TempDir(), "sb-race", "tok", "ready-nonce")
	if err != nil {
		t.Fatalf("NewReadyListener: %v", err)
	}

	const iters = 200
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
			_ = rl.Wait(ctx)
			cancel()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = rl.Close()
		}
	}()
	wg.Wait()
}
