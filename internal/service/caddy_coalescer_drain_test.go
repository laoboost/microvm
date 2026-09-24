package service

import (
	"sync/atomic"
	"testing"
	"time"
)

// Flush spawns `go c.drain()` while Run's tick also drains; two drain
// bodies running concurrently break the documented invariant that Caddy
// admin writes are sequential (Caddy's admin path is single-threaded).
// Drain bodies must be serialized.

// it serializes concurrent drain bodies so admin writes never overlap
func TestCoalescerConcurrentDrainsSerializeOps(t *testing.T) {
	c := newCaddyCoalescer(quietLogger(), time.Hour)

	var (
		inCS       atomic.Bool
		overlapped atomic.Bool
		ranA       atomic.Bool
		ranB       atomic.Bool
	)
	enter := func() {
		if !inCS.CompareAndSwap(false, true) {
			overlapped.Store(true)
		}
	}

	started := make(chan struct{})
	release := make(chan struct{})
	c.Enqueue("sb-a", 8080, func() error {
		enter()
		close(started)
		<-release // simulate a slow Caddy admin write
		inCS.Store(false)
		ranA.Store(true)
		return nil
	})
	go c.drain() // first drain picks up A and blocks inside it
	<-started

	// B lands mid-drain (as it would after a Flush enqueue) and a second
	// drain runs — exactly Flush's `go c.drain()` racing the tick drain.
	c.Enqueue("sb-b", 8080, func() error {
		enter()
		inCS.Store(false)
		ranB.Store(true)
		return nil
	})
	done := make(chan struct{})
	go func() { c.drain(); close(done) }()

	select {
	case <-done:
		if !ranB.Load() {
			t.Fatal("second drain finished without running its op")
		}
		t.Fatal("second drain completed while the first op was still in flight — Caddy admin writes overlapped")
	case <-time.After(50 * time.Millisecond):
		// serialized: B must still be waiting on the first drain body
	}

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second drain never completed after the first released")
	}
	if !ranA.Load() || !ranB.Load() {
		t.Fatalf("both ops must run exactly once: ranA=%v ranB=%v", ranA.Load(), ranB.Load())
	}
	if overlapped.Load() {
		t.Fatal("ops overlapped; drain bodies must be serialized")
	}
}
