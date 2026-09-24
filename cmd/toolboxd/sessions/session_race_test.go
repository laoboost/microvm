package sessions

import (
	"sync"
	"testing"
	"time"
)

// finish() publishes exit state while HTTP handlers read it through
// Snapshot/ExitInfo. The exit fields must be guarded by the session mutex
// (the exited atomic alone does not order the post-CAS writes). Run with -race:
// without the lock the detector reports a data race between finish and
// Snapshot/ExitInfo.
func TestSessionFinishConcurrentWithSnapshotAndExitInfo(t *testing.T) {
	for i := 0; i < 50; i++ {
		s := &Session{
			buf:    newRing(8),
			doneCh: make(chan struct{}),
		}
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			s.finish(3, "", false)
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = s.Snapshot()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = s.ExitInfo()
			}
		}()
		wg.Wait()
		snap := s.Snapshot()
		if snap.ExitCode != 3 {
			t.Fatalf("Snapshot().ExitCode = %d, want 3", snap.ExitCode)
		}
		if code, _ := s.ExitInfo(); code != 3 {
			t.Fatalf("ExitInfo() = %d, want 3", code)
		}
		select {
		case <-s.Done():
		case <-time.After(time.Second):
			t.Fatal("done channel not closed after finish")
		}
	}
}
