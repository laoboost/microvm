package caddy

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/internal/config"
)

// The Caddy admin endpoint is bound to a unix socket (packaging/Caddyfile.template)
// so no local TCP port offers unauthenticated route/cert-key control. The admin
// client must therefore dial unix:// URLs over the socket — a plain http.Client
// rejects the scheme entirely.
func TestNew_AdminUnixSocketURLDialsSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "caddy-admin.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config/" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	c := New(config.Config{
		EnableCaddy:       true,
		CaddyAdminURL:     "unix://" + sock,
		HTTPClientTimeout: 2 * time.Second,
	})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping over unix socket: %v", err)
	}
}

func TestNew_HttpAdminURLKeepsTCPRemainingSupported(t *testing.T) {
	c := New(config.Config{EnableCaddy: true, CaddyAdminURL: "http://127.0.0.1:2019"})
	if c.baseURL != "http://127.0.0.1:2019" {
		t.Fatalf("baseURL = %q, want the http URL unchanged", c.baseURL)
	}
}

// A malformed unix admin URL must fail loudly instead of silently dialing a
// truncated path ("unix://run/x.sock" → "/x.sock") or "/" ("unix://"). New
// keeps its signature, so the error surfaces on the first admin call.
func TestNew_RejectsMalformedUnixAdminURL(t *testing.T) {
	for _, raw := range []string{"unix://run/x.sock", "unix://", "unix:///"} {
		c := New(config.Config{EnableCaddy: true, CaddyAdminURL: raw, HTTPClientTimeout: 2 * time.Second})
		err := c.Ping(context.Background())
		if err == nil {
			t.Fatalf("Ping(%q) succeeded, want a config error", raw)
		}
		if !strings.Contains(err.Error(), "invalid caddy admin url") {
			t.Fatalf("Ping(%q) err = %v, want it to report an invalid admin URL", raw, err)
		}
	}
}
