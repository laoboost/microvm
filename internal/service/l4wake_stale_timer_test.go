package service

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/internal/config"
)

// scheduleTLSWakeListenerClose's AfterFunc callback must check TIMER
// IDENTITY, not mere presence of pendingTLSClose[key]: a stale timer
// whose callback runs late (after a stop-and-replace) would otherwise
// delete the NEWER timer and close the listener the new grace window is
// protecting.

// it keeps the replacement timer and listener when a stale timer fires late
func TestScheduleTLSWakeCloseStaleTimerMustNotCancelReplacement(t *testing.T) {
	svc := &Service{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		cfg:    config.Config{InternalL4WakeDir: shortSockDir(t)},
	}
	if _, err := svc.ensureTLSWakeListener("tls-stale", 443); err != nil {
		t.Fatalf("ensureTLSWakeListener: %v", err)
	}
	key := tlsWakeKey("tls-stale", 443)

	// T1: schedule a close with a long grace window.
	svc.scheduleTLSWakeListenerClose("tls-stale", 443, time.Hour)
	svc.l4WakeMu.Lock()
	t1 := svc.pendingTLSClose[key]
	svc.l4WakeMu.Unlock()
	if t1 == nil {
		t.Fatal("expected T1 registered")
	}

	// Stop-and-replace with T2 (also long) — the most recent transition now
	// owns the key.
	svc.scheduleTLSWakeListenerClose("tls-stale", 443, time.Hour)
	svc.l4WakeMu.Lock()
	t2 := svc.pendingTLSClose[key]
	svc.l4WakeMu.Unlock()
	if t2 == nil || t2 == t1 {
		t.Fatalf("expected a distinct replacement timer, t1=%v t2=%v", t1, t2)
	}

	// Force T1's stale callback to run late (Reset re-arms the AfterFunc
	// closure with the old identity), well after T2 took ownership.
	t1.Reset(time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	svc.l4WakeMu.Lock()
	pending := svc.pendingTLSClose[key]
	_, listenerAlive := svc.l4WakeTLS[key]
	svc.l4WakeMu.Unlock()
	if pending != t2 {
		t.Fatalf("stale T1 callback deleted the replacement timer (pending=%v, want T2)", pending)
	}
	if !listenerAlive {
		t.Fatal("stale T1 callback closed the listener early; T2's grace window is still pending")
	}
}
