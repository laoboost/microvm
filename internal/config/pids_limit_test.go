package config

import (
	"os"
	"strings"
	"testing"
)

func loadWithPidsEnv(t *testing.T, value string) (Config, error) {
	t.Helper()
	t.Setenv("SB_PAT_TOKEN", "token")
	t.Setenv("SB_DB_PATH", "/tmp/test.db")
	if value != "" {
		t.Setenv("SB_SANDBOX_PIDS_LIMIT", value)
	} else {
		_ = os.Unsetenv("SB_SANDBOX_PIDS_LIMIT")
	}
	return Load()
}

// it defaults the pids limit to 1024 when env is unset
func TestLoad_DefaultsPidsLimitTo1024WhenEnvUnset(t *testing.T) {
	cfg, err := loadWithPidsEnv(t, "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SandboxPidsLimit != 1024 {
		t.Fatalf("SandboxPidsLimit = %d, want default 1024", cfg.SandboxPidsLimit)
	}
}

// it rejects a negative pids limit env value
func TestLoad_RejectsNegativePidsLimitEnv(t *testing.T) {
	_, err := loadWithPidsEnv(t, "-5")
	if err == nil || !strings.Contains(err.Error(), "SB_SANDBOX_PIDS_LIMIT") {
		t.Fatalf("Load() error = %v, want SB_SANDBOX_PIDS_LIMIT rejection", err)
	}
}

func TestLoad_ParsesPidsLimitOverride(t *testing.T) {
	cfg, err := loadWithPidsEnv(t, "2048")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SandboxPidsLimit != 2048 {
		t.Fatalf("SandboxPidsLimit = %d, want 2048", cfg.SandboxPidsLimit)
	}
}
