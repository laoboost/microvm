package netrules

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBlockAllEgressRefusesWhenIPv6Live is the regression for the IPv6
// fail-open gap: this package installs IPv4-only rules (manager_linux.go
// builds iptables.New(); parseAddrOrCIDR rejects IPv6), so on a host with IPv6
// live the per-IP DROP/ACCEPT rules (and the link-local IMDS DROP) never reach
// v6 traffic. The rule-install entry points are the sandbox-creation choke
// point, so they must verify the precondition and refuse instead of reporting
// an isolation that only half-holds.
func TestBlockAllEgressRefusesWhenIPv6Live(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the IPv4-only egress policy precondition is linux-specific")
	}
	if !hostIPv6Enabled(t) {
		t.Skip("host already has IPv6 hard-disabled; nothing can fail open over v6")
	}
	mgr, err := NewWithOptions(true, BackendExec, ChainDockerUser)
	if err != nil {
		t.Skipf("no usable iptables backend on this host: %v", err)
	}
	mgr.ipt = &memBackend{} // only the precondition is under test
	if _, err := mgr.BlockAllEgressReport("10.0.0.5"); err == nil {
		t.Fatal("BlockAllEgressReport succeeded while host IPv6 is live: the IPv4-only egress policy fails open over IPv6")
	}
}

func hostIPv6Enabled(t *testing.T) bool {
	t.Helper()
	b, err := os.ReadFile("/proc/sys/net/ipv6/conf/all/disable_ipv6")
	if err != nil {
		t.Skipf("cannot read IPv6 sysctl (%v); host IPv6 state unknown", err)
	}
	return strings.TrimSpace(string(b)) != "1"
}

// TestEgressRuleInstallsRefuseWhenIPv6Live pins the refusal deterministically
// (independent of the host's sysctl state): a manager whose IPv6 precondition
// reports IPv6 live must refuse the per-sandbox rule installs, because that is
// the only netrules path a sandbox creation goes through (the docker path never
// calls EnsureChain — dockerd pre-creates DOCKER-USER).
func TestEgressRuleInstallsRefuseWhenIPv6Live(t *testing.T) {
	live := func() error { return errors.New("ipv6 live") }

	block := &Manager{enabled: true, ipt: &memBackend{}, userChain: ChainDockerUser, ipv6Disabled: live}
	if _, err := block.BlockAllEgressReport("10.0.0.5"); err == nil {
		t.Fatal("BlockAllEgressReport must refuse when the IPv6 precondition is unmet")
	}

	pol := &Manager{enabled: true, ipt: &memBackend{}, userChain: ChainDockerUser, ipv6Disabled: live}
	if err := pol.ApplyEgressPolicy("10.0.0.5", nil, []string{"10.1.0.0/16"}); err == nil {
		t.Fatal("ApplyEgressPolicy must refuse when the IPv6 precondition is unmet")
	}
}

// TestEgressRuleInstallsProceedWhenIPv6Disabled guards against the check
// becoming a blanket refusal: a satisfied precondition must leave the rule
// installs working unchanged.
func TestEgressRuleInstallsProceedWhenIPv6Disabled(t *testing.T) {
	ok := func() error { return nil }

	be := &memBackend{}
	block := &Manager{enabled: true, ipt: be, userChain: ChainDockerUser, ipv6Disabled: ok}
	if _, err := block.BlockAllEgressReport("10.0.0.5"); err != nil {
		t.Fatalf("BlockAllEgressReport with IPv6 disabled: %v", err)
	}
	if be.countMatching("|DOCKER-USER|-s|10.0.0.5|-j|DROP") != 1 {
		t.Fatalf("block-all rule not installed: %v", be.rules)
	}

	pol := &Manager{enabled: true, ipt: &memBackend{}, userChain: ChainDockerUser, ipv6Disabled: ok}
	if err := pol.ApplyEgressPolicy("10.0.0.5", nil, []string{"10.1.0.0/16"}); err != nil {
		t.Fatalf("ApplyEgressPolicy with IPv6 disabled: %v", err)
	}
}

// TestVerifyIPv6DisabledAt exercises the sysctl probe directly against temp
// files: only "1" on every present sysctl is a pass. A missing sysctl means
// IPv6 is compiled out of the kernel — nothing can fail open over v6, so that
// is a pass (hostnet.EnsureSandboxIPv6Disabled makes the same call).
func TestVerifyIPv6DisabledAt(t *testing.T) {
	dir := t.TempDir()
	write := func(name, value string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	allOn := write("all", "1")
	defOn := write("default", "1")
	if err := verifyIPv6DisabledAt(allOn, defOn); err != nil {
		t.Fatalf("disabled sysctls should pass: %v", err)
	}

	missing := filepath.Join(dir, "absent")
	if err := verifyIPv6DisabledAt(allOn, missing, defOn); err != nil {
		t.Fatalf("absent sysctl (IPv6 compiled out) should pass: %v", err)
	}

	live := write("live", "0")
	if err := verifyIPv6DisabledAt(allOn, live); err == nil {
		t.Fatal("a sysctl reading 0 must fail the precondition")
	}
}

// TestEnsureChainRefusesWhenIPv6PreconditionUnmet pins the boot-ordering guard:
// the chain bootstrap is where netrules starts installing IPv4-only rules, so
// an unmet IPv6 precondition must refuse there too — not only on the per-sandbox
// installs. Without it, a daemon that hard-disabled IPv6 AFTER the chain work
// (the ordering the wiring used to have) would boot a chain whose isolation
// silently fails open over v6.
func TestEnsureChainRefusesWhenIPv6PreconditionUnmet(t *testing.T) {
	be := &memBackend{}
	mgr := &Manager{enabled: true, ipt: be, userChain: ChainDockerUser, ipv6Disabled: func() error { return errors.New("ipv6 live") }}
	if err := mgr.EnsureChain(); err == nil {
		t.Fatal("EnsureChain bootstrapped the chain while the IPv6 precondition is unmet; the IPv4-only rules would fail open over IPv6")
	}
	if len(be.rules) != 0 || len(be.chains) != 0 {
		t.Fatalf("chain bootstrap touched the backend despite the unmet precondition: rules=%v chains=%v", be.rules, be.chains)
	}
}

// TestIPv6PreconditionProbesNamedSandboxBridge is the regression for limitation
// #1 of the first fix: the precondition read only the host-wide all/default
// sysctls, so a sandbox bridge whose own disable_ipv6 reads 0 was treated as
// disabled. all/default cannot see that case — a write to an interface's own
// sysctl wins over "all", and an operator can re-enable IPv6 on the bridge
// alone after boot. The manager must probe the bridge it installs rules for,
// and the refusal must name it.
func TestIPv6PreconditionProbesNamedSandboxBridge(t *testing.T) {
	withIPv6SysctlFixture(t, map[string]string{
		"all/disable_ipv6":     "1",
		"default/disable_ipv6": "1",
		"docker0/disable_ipv6": "0",
	})

	mgr := &Manager{enabled: true, ipt: &memBackend{}, userChain: ChainDockerUser, ipv6Disabled: probeSandboxIPv6Disabled}
	mgr.SetBridgeName("docker0")
	if _, err := mgr.BlockAllEgressReport("10.0.0.5"); err == nil {
		t.Fatal("BlockAllEgressReport succeeded while the sandbox bridge itself still carries IPv6")
	} else if !strings.Contains(err.Error(), "docker0") {
		t.Fatalf("refusal %q does not name the sandbox bridge that fails the precondition", err)
	}

	// The bridge's own sysctl reading 1 is what matters: the install proceeds.
	writeIPv6SysctlFixture(t, "docker0/disable_ipv6", "1")
	if _, err := mgr.BlockAllEgressReport("10.0.0.5"); err != nil {
		t.Fatalf("BlockAllEgressReport with the bridge disabled: %v", err)
	}
}

// TestIPv6PreconditionDerivesSandboxBridgeFromSubnet: the containerd engine
// builds its AEROLVM-USER manager inside the engine wiring and only tells it the
// bridge SUBNET (SetBridgeSubnet), so the precondition derives the interface
// from that subnet. Ambiguity must stay host-wide-only: probing a guessed
// interface could refuse every rule install on a host whose uplink overlaps the
// sandbox subnet.
func TestIPv6PreconditionDerivesSandboxBridgeFromSubnet(t *testing.T) {
	withIPv6SysctlFixture(t, map[string]string{
		"all/disable_ipv6":      "1",
		"default/disable_ipv6":  "1",
		"aerolvm0/disable_ipv6": "0",
	})
	withSandboxInterfaceAddrs(t, []ifaceAddr{{Iface: "aerolvm0", Addr: mustCIDR(t, "10.88.0.1/16")}})

	mgr := &Manager{enabled: true, ipt: &memBackend{}, userChain: ChainAerolvmUser, ipv6Disabled: probeSandboxIPv6Disabled}
	mgr.SetBridgeSubnet("10.88.0.0/16")
	if _, err := mgr.BlockAllEgressReport("10.88.0.5"); err == nil {
		t.Fatal("BlockAllEgressReport succeeded while the subnet's bridge still carries IPv6")
	} else if !strings.Contains(err.Error(), "aerolvm0") {
		t.Fatalf("refusal %q does not name the derived bridge interface", err)
	}
}

// TestBridgeIfaceForSubnet pins the derivation rules: exactly one interface
// owning the subnet wins, everything else falls back to host-wide probing.
func TestBridgeIfaceForSubnet(t *testing.T) {
	cases := []struct {
		name  string
		addrs []ifaceAddr
		want  string
	}{
		{name: "unique", addrs: []ifaceAddr{{Iface: "aerolvm0", Addr: mustCIDR(t, "10.88.0.1/16")}}, want: "aerolvm0"},
		{name: "no_match", addrs: []ifaceAddr{{Iface: "eth0", Addr: mustCIDR(t, "10.0.0.5/24")}}, want: ""},
		{name: "ambiguous", addrs: []ifaceAddr{
			{Iface: "aerolvm0", Addr: mustCIDR(t, "10.88.0.1/16")},
			{Iface: "eth1", Addr: mustCIDR(t, "10.88.4.1/16")},
		}, want: ""},
		{name: "same_iface_two_addrs", addrs: []ifaceAddr{
			{Iface: "aerolvm0", Addr: mustCIDR(t, "10.88.0.1/16")},
			{Iface: "aerolvm0", Addr: mustCIDR(t, "10.88.9.1/16")},
		}, want: "aerolvm0"},
		{name: "bad_subnet", addrs: []ifaceAddr{{Iface: "aerolvm0", Addr: mustCIDR(t, "10.88.0.1/16")}}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withSandboxInterfaceAddrs(t, tc.addrs)
			subnet := "10.88.0.0/16"
			if tc.name == "bad_subnet" {
				subnet = "not-a-subnet"
			}
			if got := bridgeIfaceForSubnet(subnet); got != tc.want {
				t.Fatalf("bridgeIfaceForSubnet(%q) = %q, want %q", subnet, got, tc.want)
			}
		})
	}
}

// TestSetBridgeNameRejectsPathTraversal: the name is interpolated into a /proc
// path, so anything with a separator must be ignored rather than probed.
func TestSetBridgeNameRejectsPathTraversal(t *testing.T) {
	mgr := &Manager{}
	mgr.SetBridgeName("../../etc/passwd")
	if got := mgr.sandboxBridgeIface(); got != "" {
		t.Fatalf("sandboxBridgeIface() = %q after a traversal attempt, want empty", got)
	}
	mgr.SetBridgeName(" docker0 ")
	if got := mgr.sandboxBridgeIface(); got != "docker0" {
		t.Fatalf("sandboxBridgeIface() = %q, want docker0", got)
	}
}

// withIPv6SysctlFixture points the probe's sysctl root at a temp tree holding
// the given "<iface>/disable_ipv6" files, so the precondition is exercised
// without touching (or depending on) the host's IPv6 state.
func withIPv6SysctlFixture(t *testing.T, files map[string]string) {
	t.Helper()
	orig := ipv6SysctlRoot
	ipv6SysctlRoot = t.TempDir()
	t.Cleanup(func() { ipv6SysctlRoot = orig })
	for name, value := range files {
		writeIPv6SysctlFixture(t, name, value)
	}
}

func writeIPv6SysctlFixture(t *testing.T, name, value string) {
	t.Helper()
	path := filepath.Join(ipv6SysctlRoot, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func withSandboxInterfaceAddrs(t *testing.T, addrs []ifaceAddr) {
	t.Helper()
	orig := sandboxInterfaceAddrs
	sandboxInterfaceAddrs = func() []ifaceAddr { return addrs }
	t.Cleanup(func() { sandboxInterfaceAddrs = orig })
}

func mustCIDR(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("parse %q: %v", cidr, err)
	}
	return ipnet
}
