//go:build !linux

package hostnet

func ensureForwardingSysctls() error { return nil }

func ensureSandboxIPv6Disabled(string) error { return nil }

func flushConntrackForIP(string) error { return nil }
