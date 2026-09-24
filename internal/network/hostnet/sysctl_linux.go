//go:build linux

package hostnet

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var sysctlPaths = []string{
	"/proc/sys/net/ipv4/ip_forward",
	"/proc/sys/net/bridge/bridge-nf-call-iptables",
	"/proc/sys/net/bridge/bridge-nf-call-ip6tables",
}

// execModprobe is a test seam over `modprobe`.
var execModprobe = func(module string) error {
	return exec.Command("modprobe", module).Run()
}

func ensureForwardingSysctls() error {
	// The bridge-nf-call-* sysctls only exist once br_netfilter is loaded.
	// Without them the bridge sysctl writes silently no-op and sandbox↔sandbox
	// bridge traffic bypasses iptables — neighbor isolation (a §8 exit gate)
	// fails open. Load the module first, then write, then VERIFY the bridge
	// hooks actually took: fail loud rather than ship sandboxes with no
	// east-west isolation.
	needBridge := false
	for _, path := range sysctlPaths {
		if strings.Contains(path, "bridge-nf-call") {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				needBridge = true
			}
		}
	}
	if needBridge {
		_ = execModprobe("br_netfilter")
	}
	for _, path := range sysctlPaths {
		if err := writeSysctl(path, "1"); err != nil {
			return err
		}
	}
	for _, path := range sysctlPaths {
		if !strings.Contains(path, "bridge-nf-call") {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("bridge netfilter sysctl %s absent after modprobe br_netfilter; sandbox east-west isolation would fail open: %w", path, err)
		}
	}
	return nil
}

func flushConntrackForIP(ip string) error {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return nil
	}
	// Best-effort: conntrack may be absent on minimal hosts.
	_ = runConntrackDelete(ip)
	return nil
}

// writeSysctl is a test seam over raw sysctl writes.
var writeSysctl = func(path, value string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("sysctl %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// readSysctl is a test seam over raw sysctl reads (verification half of the
// write+read-back pair ensureForwardingSysctls established).
var readSysctl = func(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// ensureSandboxIPv6Disabled hard-disables IPv6 on the named sandbox bridge and
// on every container interface (all = existing, default = created later) via
// net.ipv6.conf.*.disable_ipv6=1. The egress policy netrules enforces is
// IPv4-only, so any interface that comes up with IPv6 live fails that policy
// OPEN — v6 traffic would bypass every per-IP DROP. disable_ipv6 is the cheap
// correct fix; ip6tables parity is deliberately out of scope. Verified by
// read-back like ensureForwardingSysctls: a write that silently did not take
// must not be treated as success.
func ensureSandboxIPv6Disabled(bridge string) error {
	bridge = strings.TrimSpace(bridge)
	if strings.ContainsAny(bridge, "/\\ \t\n") {
		return fmt.Errorf("bridge name %q contains path separators", bridge)
	}
	targets := []string{
		"/proc/sys/net/ipv6/conf/all/disable_ipv6",
		"/proc/sys/net/ipv6/conf/default/disable_ipv6",
	}
	if bridge != "" {
		// Per-iface first: it must take effect even if all/default are
		// already 1 from a previous boot.
		targets = append([]string{"/proc/sys/net/ipv6/conf/" + bridge + "/disable_ipv6"}, targets...)
	}
	for _, path := range targets {
		if err := writeSysctl(path, "1"); err != nil {
			return fmt.Errorf("disable ipv6 via %s: %w", path, err)
		}
	}
	// Verify like ensureForwardingSysctls: fail loud rather than ship
	// sandboxes whose egress policy silently fails open over IPv6. An
	// absent path means IPv6 is compiled out — there is nothing to fail
	// open over, so that is a pass (writeSysctl already no-ops on it).
	for _, path := range targets {
		got, err := readSysctl(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("verify %s: %w", path, err)
		}
		if got != "1" {
			return fmt.Errorf("sysctl %s = %q after write, want 1; IPv6 would fail the egress policy open", path, got)
		}
	}
	return nil
}

func runConntrackDelete(ip string) error {
	// Flush BOTH directions on IP release. -s catches flows the released IP
	// originated; -d catches inbound/DNAT flows (caddy→containerIP:port) where
	// it was the original destination. Without -d, a reused IP inherits stale
	// ingress conntrack and its first inbound connection is misrouted/dropped
	// ("blackhole", plan §4 item #8). Both are best-effort — conntrack -D exits
	// non-zero when nothing matched, which the caller ignores.
	_ = execConntrack("-D", "-s", ip)
	return execConntrack("-D", "-d", ip)
}
