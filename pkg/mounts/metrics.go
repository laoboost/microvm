package mounts

import (
	"context"
	"expvar"
	"log/slog"
)

// mountCredFailureTotal counts mount-tool output matches of
// credFailurePattern (NoSigningCredentials). Surfaced as Prometheus text at
// /v1/metrics via the expvar idiom (see pkg/docker/netstats/metrics.go).
// With the credential-lifetime invariant this should stay at zero; operators
// alert on it as a backstop for node restores, manual restarts, config drift.
var mountCredFailureTotal = expvar.NewInt("aerolvm_mount_cred_failure_total")

// credFailureHandler returns the one-shot callback wired into a mount
// process's capturedOutput: a structured WARN plus a counter increment on the
// first credential-failure match from that process. It deliberately does NOT
// fire OnMountCrash — a credential rejection is not a process crash, and
// restarting the sandbox cannot heal expired credentials (logging + metric
// only).
func (m *Manager) credFailureHandler(sandboxID string, index int) func() {
	return func() {
		mountCredFailureTotal.Add(1)
		m.logger.LogAttrs(context.Background(), slog.LevelWarn, "mount credentials rejected",
			slog.String("sandbox_id", sandboxID),
			slog.Int("index", index),
			slog.String("pattern", credFailurePattern),
		)
	}
}
