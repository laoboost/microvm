package wasm

import (
	"context"
	"sync"
	"testing"
)

// noopSpawner is safe for concurrent use (unlike fakeSpawner's append).
type noopSpawner struct {
	mu    sync.Mutex
	calls int
}

func (s *noopSpawner) Warm(context.Context, string, string, string, int) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return nil
}

func (s *noopSpawner) Shutdown(string) error { return nil }

// TestRaceSetSpawnerAndMemoryVsWarmOne pins the unlocked spawner and
// defaultMemoryMB reads in WarmOne against their setters (SetSpawner is also
// called from the refill goroutine via Run). Under -race, concurrent
// SetSpawner/SetDefaultMemoryMB + WarmOne is a data race.
func TestRaceSetSpawnerAndMemoryVsWarmOne(t *testing.T) {
	p := New(t.TempDir(), nil)
	p.NoteModule("digest-race", "/mod.wasm")

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				p.SetSpawner(&noopSpawner{})
				p.SetDefaultMemoryMB(j%512 + 1)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				_, _ = p.WarmOne(context.Background(), "digest-race", "/mod.wasm")
			}
		}()
	}
	wg.Wait()
}
