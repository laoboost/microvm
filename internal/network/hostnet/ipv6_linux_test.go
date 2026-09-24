//go:build linux

package hostnet

import (
	"strings"
	"testing"
)

// TestEnsureSandboxIPv6DisabledWritesBridgeSysctl pins the egress fail-open
// fix: netrules policy is IPv4-only, so a sandbox bridge (or container veth)
// that comes up with IPv6 lets egress-policy bypass traffic out over v6.
// The fix hard-disables IPv6 via sysctl on the named bridge plus all/default
// (all covers existing ifaces, default covers the bridge and container veths
// created later). TDD: the sysctl write helper must be invoked for the bridge.
func TestEnsureSandboxIPv6DisabledWritesBridgeSysctl(t *testing.T) {
	var written [][2]string
	origW, origR := writeSysctl, readSysctl
	writeSysctl = func(path, value string) error {
		written = append(written, [2]string{path, value})
		return nil
	}
	readSysctl = func(path string) (string, error) { return "1", nil }
	t.Cleanup(func() { writeSysctl, readSysctl = origW, origR })

	if err := EnsureSandboxIPv6Disabled("br-test"); err != nil {
		t.Fatalf("EnsureSandboxIPv6Disabled: %v", err)
	}

	want := map[string]bool{
		"/proc/sys/net/ipv6/conf/br-test/disable_ipv6": false,
		"/proc/sys/net/ipv6/conf/all/disable_ipv6":     false,
		"/proc/sys/net/ipv6/conf/default/disable_ipv6": false,
	}
	for _, kv := range written {
		if seen, ok := want[kv[0]]; ok && !seen {
			if kv[1] != "1" {
				t.Fatalf("%s written with %q, want 1", kv[0], kv[1])
			}
			want[kv[0]] = true
		}
	}
	for path, seen := range want {
		if !seen {
			t.Fatalf("sysctl write helper never invoked for %s (writes=%v)", path, written)
		}
	}
}

// TestEnsureSandboxIPv6DisabledVerifiesWrite ensures the disable actually
// took: a read-back of 0 means IPv6 is still live on the bridge and the
// IPv4-only egress policy would fail open — must be a hard error, mirroring
// ensureForwardingSysctls' fail-loud verification.
func TestEnsureSandboxIPv6DisabledVerifiesWrite(t *testing.T) {
	origW, origR := writeSysctl, readSysctl
	writeSysctl = func(string, string) error { return nil }
	readSysctl = func(path string) (string, error) {
		if strings.Contains(path, "/all/") {
			return "0", nil // write silently did not take
		}
		return "1", nil
	}
	t.Cleanup(func() { writeSysctl, readSysctl = origW, origR })

	if err := EnsureSandboxIPv6Disabled("br-test"); err == nil {
		t.Fatal("want error when disable_ipv6 reads back 0 — IPv6 would fail the egress policy open")
	}
}
