package cluster

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/models"
)

func testRecovery(image string) placementRecovery {
	return placementRecovery{Spec: &models.CreateSandboxRequest{Image: image}}
}

func blobPath(t *testing.T, s *placementRecoveryFileStore, ref string) string {
	t.Helper()
	p, err := s.pathForRef(ref)
	if err != nil {
		t.Fatalf("pathForRef(%s): %v", ref, err)
	}
	return p
}

// TestRetainSnapshotRefs_SkipsFilesWrittenAfterGCStart pins the GC/PUT race
// fix: RetainSnapshotRefs may not delete a blob whose mtime is after GC-start
// (a Put that landed while GC was running — its ref cannot be in any retain
// set yet). Older unreferenced blobs are still collected.
func TestRetainSnapshotRefs_SkipsFilesWrittenAfterGCStart(t *testing.T) {
	s, err := newPlacementRecoveryFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	oldRef, err := s.Put("sb-old", testRecovery("old"))
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(blobPath(t, s, oldRef), past, past); err != nil {
		t.Fatal(err)
	}

	newRef, err := s.Put("sb-new", testRecovery("new"))
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(blobPath(t, s, newRef), future, future); err != nil {
		t.Fatal(err)
	}

	// Retain nothing that references either blob.
	if err := s.RetainSnapshotRefs([]string{placementRecoveryRefPrefix + "00"}); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := s.GetRecord(oldRef); ok {
		t.Fatal("old unreferenced blob survived GC, want collected")
	}
	if _, ok, _ := s.GetRecord(newRef); !ok {
		t.Fatal("blob written after GC-start was deleted (GC raced a just-written Put)")
	}
}

// TestPutDuringRetainSnapshotRefs_SerializesAndSurvives pins both halves of
// the fix: a store mutex serializes Put against GC (a Put issued while GC runs
// must not complete mid-scan), and the blob survives the GC pass.
func TestPutDuringRetainSnapshotRefs_SerializesAndSurvives(t *testing.T) {
	s, err := newPlacementRecoveryFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	gcInLock := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	s.now = func() time.Time {
		once.Do(func() { close(gcInLock) })
		<-proceed
		return time.Now()
	}

	gcDone := make(chan struct{})
	go func() {
		defer close(gcDone)
		_ = s.RetainSnapshotRefs(nil)
	}()

	<-gcInLock
	putDone := make(chan struct{})
	var newRef string
	go func() {
		defer close(putDone)
		newRef, _ = s.Put("sb-racing", testRecovery("racing"))
	}()

	select {
	case <-putDone:
		t.Fatal("Put completed while GC held the store (no mutual exclusion between Put and GC)")
	case <-time.After(100 * time.Millisecond):
	}
	close(proceed)
	<-putDone
	<-gcDone

	if newRef == "" {
		t.Fatal("concurrent Put produced no ref")
	}
	if _, ok, _ := s.GetRecord(newRef); !ok {
		t.Fatal("concurrent Put during RetainSnapshotRefs: the new blob was GC-deleted")
	}
}

// TestRecoveryStoreSyncsDirAfterRename pins the durability fix: a temp-write
// rename is lost on power failure unless the containing directory is also
// fsynced. Both Put and the GC manifest writer must sync s.dir after rename.
func TestRecoveryStoreSyncsDirAfterRename(t *testing.T) {
	s, err := newPlacementRecoveryFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var synced []string
	orig := syncDir
	syncDir = func(dir string) error {
		mu.Lock()
		synced = append(synced, dir)
		mu.Unlock()
		return orig(dir)
	}
	t.Cleanup(func() { syncDir = orig })

	if _, err := s.Put("sb-sync", testRecovery("sync")); err != nil {
		t.Fatal(err)
	}
	if err := s.RetainSnapshotRefs(nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	wantDir := s.dir
	count := 0
	for _, d := range synced {
		if filepath.Clean(d) == filepath.Clean(wantDir) {
			count++
		}
	}
	if count < 2 {
		t.Fatalf("syncDir(%s) calls = %d, want >= 2 (Put rename + GC manifest rename)", wantDir, count)
	}
}
