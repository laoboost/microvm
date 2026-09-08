package config

import (
	"os"
	"strings"
	"testing"
)

func loadWithL4Range(t *testing.T, start, end string) (Config, error) {
	t.Helper()
	t.Setenv("SB_PAT_TOKEN", "token")
	t.Setenv("SB_DB_PATH", "/tmp/test.db")
	if start != "" {
		t.Setenv("SB_L4_PORT_RANGE_START", start)
	} else {
		_ = os.Unsetenv("SB_L4_PORT_RANGE_START")
	}
	if end != "" {
		t.Setenv("SB_L4_PORT_RANGE_END", end)
	} else {
		_ = os.Unsetenv("SB_L4_PORT_RANGE_END")
	}
	return Load()
}

// it rejects an l4 port range overlapping the api port
func TestLoad_RejectsL4RangeOverlappingAPIPort(t *testing.T) {
	_, err := loadWithL4Range(t, "21210", "21250")
	if err == nil || !strings.Contains(err.Error(), "SB_L4_PORT_RANGE_START/END") {
		t.Fatalf("Load() error = %v, want L4 range collision rejection", err)
	}
}

// it rejects an l4 port range overlapping the toolbox port
func TestLoad_RejectsL4RangeOverlappingToolboxPort(t *testing.T) {
	_, err := loadWithL4Range(t, "2200", "2300")
	if err == nil || !strings.Contains(err.Error(), "SB_L4_PORT_RANGE_START/END") {
		t.Fatalf("Load() error = %v, want L4 range collision rejection", err)
	}
}

// it rejects an l4 port range overlapping the ssh listen port
func TestLoad_RejectsL4RangeOverlappingSSHListenPort(t *testing.T) {
	_, err := loadWithL4Range(t, "2100", "2300")
	if err == nil || !strings.Contains(err.Error(), "SB_L4_PORT_RANGE_START/END") {
		t.Fatalf("Load() error = %v, want L4 range collision rejection", err)
	}
}

// it rejects an l4 port range overlapping the ingress or wake ports
func TestLoad_RejectsL4RangeOverlappingIngressOrWakePorts(t *testing.T) {
	for _, r := range [][2]string{{"21210", "21213"}, {"21210", "21214"}} {
		_, err := loadWithL4Range(t, r[0], r[1])
		if err == nil || !strings.Contains(err.Error(), "SB_L4_PORT_RANGE_START/END") {
			t.Fatalf("Load() error = %v for range %v, want L4 range collision rejection", err, r)
		}
	}
}

// it accepts a range disjoint from all daemon ports
func TestLoad_AcceptsL4RangeDisjointFromDaemonPorts(t *testing.T) {
	cfg, err := loadWithL4Range(t, "40000", "50000")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.L4PortRangeStart != 40000 || cfg.L4PortRangeEnd != 50000 {
		t.Fatalf("L4 range = %d-%d, want 40000-50000", cfg.L4PortRangeStart, cfg.L4PortRangeEnd)
	}
}

// it keeps accepting the existing default range — the production values set
// on Lorentz (Terraform-managed 40000-50000) against the full daemon
// exclusion set (21212, 2280, 2220, 21213, 21214, 2019), disjoint today.
func TestLoad_KeepsAcceptingExistingDefaultRange(t *testing.T) {
	cfg, err := loadWithL4Range(t, "40000", "50000")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.APIPort != 21212 || cfg.ToolboxPort != 2280 {
		t.Fatalf("daemon defaults changed: APIPort=%d ToolboxPort=%d", cfg.APIPort, cfg.ToolboxPort)
	}
}

// it reports all colliding ports in the error message
func TestLoad_L4RangeErrorReportsAllCollidingPorts(t *testing.T) {
	_, err := loadWithL4Range(t, "2100", "23000")
	if err == nil {
		t.Fatal("Load() error = nil, want collision rejection")
	}
	for _, port := range []string{"21212", "2280", "2220", "21213", "21214"} {
		if !strings.Contains(err.Error(), port) {
			t.Fatalf("error %q missing colliding port %s", err, port)
		}
	}
}
