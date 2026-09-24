package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/pkg/clonegen"
)

// /proxy/ forwards to sandbox-local ports and was reachable without
// requireAuth — cross-tenant callers on the shared bridge could hit any
// allowed port. It must fail closed like the other routes.
func TestProxyRequiresAuth(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("proxied-ok"))
	}))
	defer backend.Close()
	parts := strings.Split(strings.TrimPrefix(backend.URL, "http://"), ":")
	port, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		t.Fatalf("parse backend port: %v", err)
	}

	s := &server{
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		sandboxID:    "sb-proxy-auth",
		authToken:    "token-123",
		allowedPorts: map[int]struct{}{},
	}
	s.setAllowedPorts([]int{port})
	h := s.routes()

	without := httptest.NewRecorder()
	h.ServeHTTP(without, httptest.NewRequest(http.MethodGet, "/proxy/"+strconv.Itoa(port)+"/hello", nil))
	if without.Code != http.StatusUnauthorized {
		t.Fatalf("proxy without token: status = %d, want 401", without.Code)
	}

	wrong := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/proxy/"+strconv.Itoa(port)+"/hello", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	h.ServeHTTP(wrong, req)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("proxy with wrong token: status = %d, want 401", wrong.Code)
	}

	ok := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/proxy/"+strconv.Itoa(port)+"/hello", nil)
	req.Header.Set("Authorization", "Bearer token-123")
	h.ServeHTTP(ok, req)
	if ok.Code != http.StatusOK || ok.Body.String() != "proxied-ok" {
		t.Fatalf("proxy with token: status = %d body=%q, want 200 proxied-ok", ok.Code, ok.Body.String())
	}
}

// /version is not needed unauthenticated (nothing outside the sandbox calls
// it; /health still reports the version for probes) so it goes behind
// requireAuth like every other non-health route.
func TestVersionRequiresAuth(t *testing.T) {
	s := &server{
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		sandboxID: "sb-version-auth",
		authToken: "token-123",
	}
	h := s.routes()

	without := httptest.NewRecorder()
	h.ServeHTTP(without, httptest.NewRequest(http.MethodGet, "/version", nil))
	if without.Code != http.StatusUnauthorized {
		t.Fatalf("version without token: status = %d, want 401", without.Code)
	}

	ok := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	req.Header.Set("Authorization", "Bearer token-123")
	h.ServeHTTP(ok, req)
	if ok.Code != http.StatusOK {
		t.Fatalf("version with token: status = %d, want 200", ok.Code)
	}
}

// /clone-generation stays unauthenticated on purpose: the token is a
// non-sensitive change-detector, external access is gated by the auth'd v1
// toolbox proxy, and in-guest readers (or the well-known file written by
// cloneGeneration) depend on reading it without the bearer. This pins that
// deliberate exception so removing it is a conscious decision.
func TestCloneGenerationRemainsUnauthenticated(t *testing.T) {
	cg := clonegen.New(t.TempDir()+"/clone-generation", nil)
	s := &server{
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		sandboxID: "sb-clonegen",
		authToken: "token-123",
		cloneGen:  cg,
	}
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/clone-generation", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("clone-generation without token: status = %d, want 200 (documented unauthenticated route)", rr.Code)
	}
}

// The bearer compare in requireAuth must be constant-time so a caller on the
// shared bridge can't walk the token byte-by-byte. Timing isn't observable in
// a unit test, so this pins the implementation detail directly.
func TestRequireAuthTokenCompareIsConstantTime(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(src), "subtle.ConstantTimeCompare") {
		t.Fatal("requireAuth must compare the bearer token with crypto/subtle.ConstantTimeCompare, not !=")
	}
}
