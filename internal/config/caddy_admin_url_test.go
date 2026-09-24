package config

import (
	"strings"
	"testing"
)

// The generated Caddyfile binds the admin endpoint to a unix socket
// (packaging/Caddyfile.template) and nothing listens on the old TCP port, so a
// deployment that does not run install.sh must default to the socket.
func TestLoadDefaultCaddyAdminURLIsUnixSocket(t *testing.T) {
	t.Setenv("SB_CADDY_ADMIN_URL", "")
	t.Setenv("SB_PAT_TOKEN", "token")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CaddyAdminURL != "unix:///run/caddy/caddy-admin.sock" {
		t.Fatalf("default CaddyAdminURL = %q, want unix:///run/caddy/caddy-admin.sock", cfg.CaddyAdminURL)
	}
}

// The Caddy admin API is unauthenticated, so a public/non-loopback TCP admin
// URL (or a scheme/path the client cannot dial) must fail config load.
func TestLoadRejectsUnsafeCaddyAdminURL(t *testing.T) {
	for _, u := range []string{
		"http://0.0.0.0:2019",
		"http://10.1.2.3:2019",
		"https://caddy.internal:2019",
		"unix://",
		"unix:///",
		"tcp://127.0.0.1:2019",
	} {
		t.Run(u, func(t *testing.T) {
			t.Setenv("SB_CADDY_ADMIN_URL", u)
			t.Setenv("SB_PAT_TOKEN", "token")
			_, err := Load()
			if err == nil {
				t.Fatalf("Load accepted unsafe SB_CADDY_ADMIN_URL=%q", u)
			}
			if !strings.Contains(err.Error(), "SB_CADDY_ADMIN_URL") {
				t.Fatalf("Load(%q) err = %v, want it to name SB_CADDY_ADMIN_URL", u, err)
			}
		})
	}
}

func TestLoadAcceptsLoopbackCaddyAdminURL(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1:2019",
		"http://localhost:2019",
		"http://[::1]:2019",
		"unix:///run/caddy/caddy-admin.sock",
	} {
		t.Run(u, func(t *testing.T) {
			t.Setenv("SB_CADDY_ADMIN_URL", u)
			t.Setenv("SB_PAT_TOKEN", "token")
			if _, err := Load(); err != nil {
				t.Fatalf("Load(%q): %v", u, err)
			}
		})
	}
}
