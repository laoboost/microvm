package toolhost

import (
	"sync"
	"testing"

	"github.com/aerol-ai/microvm/cmd/toolboxd/sessions"
)

// Regression: broadcast() copied subscriber channels under c.mu but sent to
// them AFTER unlocking, while finish() closed those same channels after
// unlocking — a /logs?follow=true subscriber present while a command ended
// could panic the daemon with "send on closed channel".
func TestDaytonaCommandStreamBroadcastFinishRace(t *testing.T) {
	for trial := 0; trial < 200; trial++ {
		s := newDaytonaCommandStream()
		_, ch, _ := s.subscribe()
		// Fill the subscriber buffer so later sends take the non-blocking
		// default path — a slow /logs follower must never stall the runner.
		for i := 0; i < 300; i++ {
			s.broadcast(sessions.StreamStdout, []byte("fill"))
		}

		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					s.broadcast(sessions.StreamStdout, []byte("x"))
				}
			}()
		}
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.finish()
			}()
		}
		wg.Wait()

		// Drain the frames channel so the subscriber can't leak into the
		// next trial's window.
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					goto next
				}
			default:
				goto next
			}
		}
	next:
	}
}
