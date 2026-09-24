package config

import (
	"net"
	"testing"
)

// TestLoadDefaultAPIHostIsLoopback pins the safe-by-default bind address: the
// plaintext HTTP API carries bearer PATs, so an unset SB_API_HOST must bind
// loopback (127.0.0.1), not every interface (0.0.0.0) — Caddy reverse-proxies
// the public path and nothing else should see the PATs on the wire.
func TestLoadDefaultAPIHostIsLoopback(t *testing.T) {
	t.Setenv("SB_API_HOST", "")
	t.Setenv("SB_PAT_TOKEN", "token")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.APIHost != "127.0.0.1" {
		t.Fatalf("default APIHost = %q, want 127.0.0.1", cfg.APIHost)
	}
	host, _, err := net.SplitHostPort(cfg.ListenAddr())
	if err != nil {
		t.Fatalf("ListenAddr() %q: %v", cfg.ListenAddr(), err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("default ListenAddr host = %q, want 127.0.0.1 (addr %q)", host, cfg.ListenAddr())
	}
}
