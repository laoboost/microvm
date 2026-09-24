package hostnet

// EnsureForwardingSysctls enables host forwarding/filter sysctls required for
// containerd native CNI networking (Phase 2). No-op on platforms without them.
func EnsureForwardingSysctls() error {
	return ensureForwardingSysctls()
}

// EnsureSandboxIPv6Disabled hard-disables IPv6 on the sandbox bridge and
// container interfaces so the IPv4-only egress policy cannot be bypassed
// over v6. No-op on platforms without the sysctls.
func EnsureSandboxIPv6Disabled(bridge string) error {
	return ensureSandboxIPv6Disabled(bridge)
}

// FlushConntrackForIP drops conntrack entries for an IP on slot release (Phase 2).
func FlushConntrackForIP(ip string) error {
	return flushConntrackForIP(ip)
}
