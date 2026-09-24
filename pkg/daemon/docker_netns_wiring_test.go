package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/aerol-ai/microvm/internal/config"
	"github.com/aerol-ai/microvm/internal/service"
	"github.com/aerol-ai/microvm/pkg/docker/netrules"
	"github.com/aerol-ai/microvm/pkg/models"
)

// TestMain neutralises the host-mutating sandbox-IPv6 sysctl seam for the whole
// package. The daemon boot path invokes it on every Run, and the unit tests run
// as an unprivileged user on a developer/CI host whose own IPv6 must not be
// touched — a real /proc/sys write either fails with EPERM or, when the suite
// runs as root, silently disables IPv6 host-wide. Tests that need to observe the
// invocation swap the seam locally.
func TestMain(m *testing.M) {
	ensureSandboxIPv6Disabled = func(string) error { return nil }
	os.Exit(m.Run())
}

// TestSandboxBridgeInterfaces pins which bridges a host of each shape carries
// sandbox traffic on. docker0 is the default docker network; a custom network's
// br-<id> is only named once dockerd has created it, so that case lists "" (the
// host-wide all/default sysctls still get written, and new interfaces inherit
// them). aerolvm0 is the containerd engine's CNI bridge; a host migrating off
// docker can have both.
func TestSandboxBridgeInterfaces(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Config
		want []string
	}{
		{name: "docker_default_network", cfg: config.Config{DockerNetwork: "bridge"}, want: []string{"docker0"}},
		{name: "docker_zero_value_network", cfg: config.Config{}, want: []string{"docker0"}},
		{name: "docker_custom_network", cfg: config.Config{DockerNetwork: "aerolvm-net"}, want: []string{""}},
		{name: "containerd", cfg: config.Config{ContainerEngine: models.ContainerEngineContainerd, DockerNetwork: "bridge"},
			want: []string{"aerolvm0", "docker0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sandboxBridgeInterfaces(tc.cfg); strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("sandboxBridgeInterfaces(%+v) = %v, want %v", tc.cfg, got, tc.want)
			}
		})
	}
}

// TestBootSandboxNetworkIsolationDisablesEverySandboxBridge pins the docker and
// containerd halves of the IPv6 wiring: netrules enforces an IPv4-only egress
// policy, so every bridge that can carry sandbox traffic must have IPv6
// hard-disabled, and netrules must be told which interface to probe per sandbox.
func TestBootSandboxNetworkIsolationDisablesEverySandboxBridge(t *testing.T) {
	orig := ensureSandboxIPv6Disabled
	var got []string
	ensureSandboxIPv6Disabled = func(bridge string) error {
		got = append(got, bridge)
		return nil
	}
	t.Cleanup(func() { ensureSandboxIPv6Disabled = orig })

	rules := netrules.NewWithBackend(&chainRecorder{})
	cfg := config.Config{EnableNetworkRules: true, ContainerEngine: models.ContainerEngineContainerd, DockerNetwork: "bridge"}
	if err := bootSandboxNetworkIsolation(cfg, rules, nil); err != nil {
		t.Fatalf("bootSandboxNetworkIsolation: %v", err)
	}
	if strings.Join(got, ",") != "aerolvm0,docker0" {
		t.Fatalf("IPv6 disable invoked with %v, want [aerolvm0 docker0]", got)
	}
	if name := rules.BridgeName(); name != dockerDefaultBridge {
		t.Fatalf("netrules bridge name = %q, want %q", name, dockerDefaultBridge)
	}
}

// TestBootSandboxNetworkIsolationIPv6DisableGating pins the two gates: the
// disable must not run with the egress policy off (local-mode dev hosts keep
// their IPv6), and a custom docker network falls back to the host-wide
// all/default sysctls (its br-<id> name isn't known before dockerd runs).
func TestBootSandboxNetworkIsolationIPv6DisableGating(t *testing.T) {
	orig := ensureSandboxIPv6Disabled
	var got []string
	ensureSandboxIPv6Disabled = func(bridge string) error {
		got = append(got, bridge)
		return nil
	}
	t.Cleanup(func() { ensureSandboxIPv6Disabled = orig })

	t.Run("rules_off", func(t *testing.T) {
		got = nil
		cfg := config.Config{EnableNetworkRules: false, DockerNetwork: "bridge"}
		if err := bootSandboxNetworkIsolation(cfg, netrules.NewWithBackend(&chainRecorder{}), nil); err != nil {
			t.Fatalf("bootSandboxNetworkIsolation: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("IPv6 disable ran with network rules off: %v", got)
		}
	})

	t.Run("custom_network_uses_host_wide_pair", func(t *testing.T) {
		got = nil
		cfg := config.Config{EnableNetworkRules: true, DockerNetwork: "aerolvm-net"}
		rules := netrules.NewWithBackend(&chainRecorder{})
		if err := bootSandboxNetworkIsolation(cfg, rules, nil); err != nil {
			t.Fatalf("bootSandboxNetworkIsolation: %v", err)
		}
		if len(got) != 1 || got[0] != "" {
			t.Fatalf("IPv6 disable invoked with %v, want [\"\"] for a custom network", got)
		}
		if name := rules.BridgeName(); name != "" {
			t.Fatalf("netrules bridge name = %q, want empty (br-<id> is unknown pre-dockerd)", name)
		}
	})
}

// TestBootSandboxNetworkIsolationIPv6DisableFailsBoot: a disable that does not
// take must abort boot rather than ship sandboxes whose egress policy fails open
// — and must never reach the chain-installing callback.
func TestBootSandboxNetworkIsolationIPv6DisableFailsBoot(t *testing.T) {
	orig := ensureSandboxIPv6Disabled
	ensureSandboxIPv6Disabled = func(string) error { return errors.New("ipv6 still live") }
	t.Cleanup(func() { ensureSandboxIPv6Disabled = orig })

	installed := false
	cfg := config.Config{EnableNetworkRules: true, DockerNetwork: "bridge"}
	err := bootSandboxNetworkIsolation(cfg, netrules.NewWithBackend(&chainRecorder{}), func() error {
		installed = true
		return nil
	})
	if err == nil {
		t.Fatal("want boot failure when the IPv6 disable does not take")
	}
	if installed {
		t.Fatal("chain install ran after a failed IPv6 disable")
	}
}

// TestBootSandboxNetworkIsolationDisablesIPv6BeforeChainWork is the ordering
// regression: the boot-time IPv6 hard-disable must be recorded BEFORE any chain
// work. The install callback stands in for wireContainerEngine, whose containerd
// path bootstraps the AEROLVM-USER chain + FORWARD jump, and both the sysctl
// writer and the iptables backend record into one sequence. Moving the disable
// after the install (the defect this fixes) turns this test red.
func TestBootSandboxNetworkIsolationDisablesIPv6BeforeChainWork(t *testing.T) {
	var (
		mu  sync.Mutex
		seq []string
	)
	record := func(entry string) {
		mu.Lock()
		defer mu.Unlock()
		seq = append(seq, entry)
	}

	orig := ensureSandboxIPv6Disabled
	ensureSandboxIPv6Disabled = func(bridge string) error {
		record("ipv6-disable " + bridge)
		return nil
	}
	t.Cleanup(func() { ensureSandboxIPv6Disabled = orig })

	rules := netrules.NewWithBackend(&chainRecorder{record: record})
	cfg := config.Config{EnableNetworkRules: true, DockerNetwork: "bridge"}
	if err := bootSandboxNetworkIsolation(cfg, rules, rules.EnsureChain); err != nil {
		t.Fatalf("bootSandboxNetworkIsolation: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seq) < 2 {
		t.Fatalf("expected an IPv6 disable and chain work, recorded %v", seq)
	}
	if seq[0] != "ipv6-disable "+dockerDefaultBridge {
		t.Fatalf("first boot-time action = %q, want the IPv6 hard-disable", seq[0])
	}
	for _, entry := range seq[1:] {
		if strings.HasPrefix(entry, "ipv6-disable") {
			t.Fatalf("IPv6 disable ran after chain work: %v", seq)
		}
	}
}

// TestDockerBootPathInstallsLinkLocalDrop is the regression for the Docker
// engine's missing IMDS guard. dockerd owns DOCKER-USER and its FORWARD jump, so
// the Docker path never calls EnsureChain — and with it never installed the
// global "169.254.0.0/16 DROP" that EnsureChain puts in the user chain. A sandbox
// created with no egress policy (the default) installs no per-IP rule at all, so
// nothing else carried the guard: it reached the link-local IMDS at
// 169.254.169.254 over IPv4 whenever the instance hop-limit was misconfigured.
// The Docker boot path must install the link-local DROP into the chain it
// actually uses (DOCKER-USER), not bootstrap AEROLVM-USER.
func TestDockerBootPathInstallsLinkLocalDrop(t *testing.T) {
	be := &chainRecorder{}
	rules := netrules.NewWithBackend(be)
	cfg := config.Config{
		EnableNetworkRules: true,
		ContainerEngine:    models.ContainerEngineDocker,
		DockerNetwork:      "bridge",
	}
	err := bootSandboxNetworkIsolation(cfg, rules, func() error {
		_, werr := wireContainerEngine(context.Background(), cfg, testLogger(), &service.Service{}, nil, nil, rules, nil)
		return werr
	})
	if err != nil {
		t.Fatalf("docker boot: %v", err)
	}
	if !be.sawRule(netrules.ChainDockerUser, "-d "+netrules.LinkLocalEgressCIDR+" -j DROP") {
		t.Fatalf("Docker boot path did not install the link-local (IMDS) DROP into %s; recorded calls:\n%s",
			netrules.ChainDockerUser, strings.Join(be.calls(), "\n"))
	}
}

// chainRecorder is a netrules.RuleBackend that records every call it sees, so a
// test can assert what happened in which order relative to the IPv6 disable.
type chainRecorder struct {
	record func(string)
	mu     sync.Mutex
	seen   []string
}

func (r *chainRecorder) log(format string, args ...any) {
	entry := fmt.Sprintf(format, args...)
	r.mu.Lock()
	r.seen = append(r.seen, entry)
	r.mu.Unlock()
	if r.record != nil {
		r.record(entry)
	}
}

// calls returns a copy of every logged entry, in order.
func (r *chainRecorder) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

func (r *chainRecorder) Exists(table, chain string, spec ...string) (bool, error) {
	r.log("rule-exists %s", ruleKey(table, chain, spec))
	return false, nil
}

func (r *chainRecorder) Insert(table, chain string, pos int, spec ...string) error {
	r.log("rule-insert %s", ruleKey(table, chain, spec))
	return nil
}

func (r *chainRecorder) Append(table, chain string, spec ...string) error {
	r.log("rule-append %s", ruleKey(table, chain, spec))
	return nil
}

func (r *chainRecorder) Delete(table, chain string, spec ...string) error {
	r.log("rule-delete %s", ruleKey(table, chain, spec))
	return nil
}

// ruleKey renders a recorded call as "table chain spec..." so a test can assert
// which chain a rulespec landed in, not just that some call happened.
func ruleKey(table, chain string, spec []string) string {
	return strings.Join(append([]string{table, chain}, spec...), " ")
}

// sawRule reports whether any recorded call targeted chain with a rulespec whose
// rendered form contains want.
func (r *chainRecorder) sawRule(chain, want string) bool {
	for _, c := range r.calls() {
		if strings.Contains(c, " "+chain+" ") && strings.Contains(c, want) {
			return true
		}
	}
	return false
}

// EnsureUserChain / EnsureForwardJump are the chain bootstrap the containerd
// engine runs at boot. netrules' bootstrapBackend is unexported, but the method
// set is satisfiable from another package.
func (r *chainRecorder) EnsureUserChain(chain string) error {
	r.log("chain-create %s", chain)
	return nil
}

func (r *chainRecorder) EnsureForwardJump(chain string) error {
	r.log("chain-jump %s", chain)
	return nil
}
