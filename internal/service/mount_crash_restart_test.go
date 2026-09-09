package service

import (
	"testing"
)

// A mount crash under a running sandbox triggers one auto-restart per cooldown
// window: further crashes inside the window must be suppressed so a crash loop
// cannot produce stop/start churn on a billable sandbox.
func TestHandleMountCrash_RestartGateCooldown(t *testing.T) {
	svc, _, _ := newServiceRuntimeHarness(t, &recordingRuntime{})

	if !svc.mountCrashRestartAllowed("sb-gate") {
		t.Fatal("first crash must be allowed to restart")
	}
	if svc.mountCrashRestartAllowed("sb-gate") {
		t.Fatal("second crash within the cooldown must be suppressed")
	}
	// A different sandbox is gated independently.
	if !svc.mountCrashRestartAllowed("sb-other") {
		t.Fatal("cooldown must be per sandbox")
	}
}
