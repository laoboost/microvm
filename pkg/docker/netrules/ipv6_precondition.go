package netrules

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// ipv6SysctlRoot is the kernel's per-interface IPv6 sysctl directory. A seam:
// tests point it at a fixture tree instead of reading the host's real sysctls.
var ipv6SysctlRoot = "/proc/sys/net/ipv6/conf"

// ipv6DisableSysctlPaths are the host-wide sysctls that prove IPv6 is
// hard-disabled. "all" covers every interface that already exists (the sandbox
// bridge when it is up); "default" is what interfaces created later inherit,
// which is what actually covers the CNI bridge + container veths created after
// sandboxd boot.
var ipv6DisableSysctlPaths = []string{
	"all/disable_ipv6",
	"default/disable_ipv6",
}

// ipv6DisableSysctlPath is the sysctl that decides whether iface itself carries
// IPv6. It is the exact answer for a sandbox bridge: a per-interface value wins
// over "all", and an interface can be re-enabled after boot without all/default
// changing.
func ipv6DisableSysctlPath(iface string) string {
	return ipv6SysctlRoot + "/" + iface + "/disable_ipv6"
}

func hostWideIPv6DisablePaths() []string {
	paths := make([]string, 0, len(ipv6DisableSysctlPaths))
	for _, rel := range ipv6DisableSysctlPaths {
		paths = append(paths, ipv6SysctlRoot+"/"+rel)
	}
	return paths
}

// probeSandboxIPv6Disabled reads the host-wide sysctls this package's IPv4-only
// rules depend on. See verifyIPv6DisabledAt for the contract.
func probeSandboxIPv6Disabled() error {
	return verifyIPv6DisabledAt(hostWideIPv6DisablePaths()...)
}

// verifyIPv6DisabledAt reports whether every present sysctl reads 1
// (disabled). A missing sysctl means IPv6 is compiled out of the kernel —
// nothing can fail open over v6, so that is a pass, mirroring
// hostnet.EnsureSandboxIPv6Disabled. Anything else is a hard error: the rules
// this package installs are IPv4-only (manager_linux.go builds iptables.New(),
// and parseAddrOrCIDR rejects IPv6), so an interface that comes up with IPv6
// live lets traffic bypass every per-IP DROP/ACCEPT, the bridge-scoped ACCEPTs
// and the link-local (IMDS) DROP.
func verifyIPv6DisabledAt(paths ...string) error {
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("netrules: read %s: %w", path, err)
		}
		if v := strings.TrimSpace(string(b)); v != "1" {
			return fmt.Errorf("netrules: IPv6 is not disabled (%s=%q) but the egress policy is IPv4-only; sandbox egress and the IMDS block would fail open over IPv6 — disable IPv6 on the sandbox bridge/interfaces before creating sandboxes", path, v)
		}
	}
	return nil
}

// ifaceAddr is one host interface address: the input to the bridge-interface
// derivation below.
type ifaceAddr struct {
	Iface string
	Addr  *net.IPNet
}

// sandboxInterfaceAddrs lists this host's interface addresses. A seam so the
// derivation is testable without a real bridge.
var sandboxInterfaceAddrs = hostInterfaceAddrs

func hostInterfaceAddrs() []ifaceAddr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []ifaceAddr
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			out = append(out, ifaceAddr{Iface: iface.Name, Addr: ipnet})
		}
	}
	return out
}

// bridgeIfaceForSubnet returns the single interface whose address lies inside
// subnet, or "" when there is no match or more than one match. Only an
// unambiguous answer is usable: a guess could probe an unrelated interface
// (say an uplink overlapping the sandbox subnet) and refuse every rule install
// on the host, so ambiguity falls back to the host-wide pair.
func bridgeIfaceForSubnet(subnet string) string {
	_, ipnet, err := net.ParseCIDR(strings.TrimSpace(subnet))
	if err != nil {
		return ""
	}
	matches := map[string]bool{}
	for _, a := range sandboxInterfaceAddrs() {
		if a.Addr == nil || !ipnet.Contains(a.Addr.IP) {
			continue
		}
		matches[a.Iface] = true
	}
	if len(matches) != 1 {
		return ""
	}
	for name := range matches {
		return name
	}
	return ""
}
