package wasm

import (
	"context"
	"time"
)

// DefaultWallTimeout is the invocation budget when Capabilities.WallTimeoutNs is zero.
const DefaultWallTimeout = 5 * time.Minute

// WallTimeoutFromCaps returns the wall-clock budget for one guest invocation.
func WallTimeoutFromCaps(caps Capabilities) time.Duration {
	if caps.WallTimeoutNs > 0 {
		return time.Duration(caps.WallTimeoutNs)
	}
	return DefaultWallTimeout
}

// WithInvocationDeadline wraps ctx with the caps wall timeout (epoch-style on wazero).
// It bounds a ONE-SHOT invocation; see InvocationContext for the long-lived serve
// path, which is deliberately left without a deadline.
func WithInvocationDeadline(ctx context.Context, caps Capabilities) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, WallTimeoutFromCaps(caps))
}

// InvocationContext returns the context a guest invocation must run under.
//
// background=false is the one-shot path (MsgExec, and a plain MsgInvoke from
// Exec-style API calls): the call is bounded by the caps wall timeout. That
// bound is deliberate protection against a CPU-bound guest that never returns —
// on wazero the deadline is real, because the runtime is configured with
// WithCloseOnContextDone, so expiry tears the module down.
//
// background=true is the long-lived serve path: a guest _start that never
// returns because it blocks in the HTTP accept loop for the sandbox's whole
// lifetime. No deadline is applied — the serve is not a request, it is the
// server, so the per-request wall budget must not kill it. Such a call is
// bounded by the sandbox lifecycle instead: StopInstance/Close cancel it
// through the engine's in-flight call registry and wait for the guest to unwind
// (see wazeroEngine.stopInFlight).
func InvocationContext(ctx context.Context, caps Capabilities, background bool) (context.Context, context.CancelFunc) {
	if background {
		return context.WithCancel(ctx)
	}
	return WithInvocationDeadline(ctx, caps)
}

// MemoryLimitPages converts a MiB cap to WASM pages (64KiB each). Zero means no limit.
func MemoryLimitPages(memoryMB int) uint32 {
	if memoryMB <= 0 {
		return 0
	}
	const pageSize = 64 * 1024
	bytes := uint64(memoryMB) * 1024 * 1024
	pages := bytes / pageSize
	if pages == 0 {
		return 1
	}
	if pages > uint64(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(pages)
}

// CapsFromResourceLimits projects host limits into engine capabilities.
func CapsFromResourceLimits(base Capabilities, memoryMB int, wallTimeout time.Duration) Capabilities {
	if memoryMB > 0 {
		base.MemoryMB = memoryMB
	}
	if wallTimeout > 0 {
		base.WallTimeoutNs = wallTimeout.Nanoseconds()
	}
	return base
}
