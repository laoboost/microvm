package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newAuthTestServer(token string, authOptional bool, logBuf *bytes.Buffer) *server {
	var logger *slog.Logger
	if logBuf != nil {
		logger = slog.New(slog.NewTextHandler(logBuf, nil))
	} else {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &server{
		logger:       logger,
		authToken:    token,
		authOptional: authOptional,
		allowedPorts: map[int]struct{}{},
	}
}

func authRequest() (*httptest.ResponseRecorder, *http.Request) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	return rr, req
}

func TestRequireAuthRejects401WhenTokenEmptyAndAuthRequired(t *testing.T) {
	s := newAuthTestServer("", false, nil)
	rr, req := authRequest()
	if s.requireAuth(rr, req) {
		t.Fatal("requireAuth with empty token and auth required should return false")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestRequireAuthAcceptsWhenTokenEmptyAndAuthOptional(t *testing.T) {
	s := newAuthTestServer("", true, nil)
	rr, req := authRequest()
	if !s.requireAuth(rr, req) {
		t.Fatal("requireAuth with empty token and auth optional should return true")
	}
}

func TestRequireAuthAcceptsCorrectBearerToken(t *testing.T) {
	s := newAuthTestServer("secret", false, nil)
	rr, req := authRequest()
	req.Header.Set("Authorization", "Bearer secret")
	if !s.requireAuth(rr, req) {
		t.Fatal("requireAuth with correct bearer token should return true")
	}
}

func TestRequireAuthRejectsWrongBearerToken(t *testing.T) {
	s := newAuthTestServer("secret", false, nil)
	rr, req := authRequest()
	req.Header.Set("Authorization", "Bearer wrongtoken")
	if s.requireAuth(rr, req) {
		t.Fatal("requireAuth with wrong bearer token should return false")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestWarnsWhenRunningWithAuthOptionalAndEmptyToken(t *testing.T) {
	var buf bytes.Buffer
	s := newAuthTestServer("", true, &buf)
	s.warnAuthOptional()
	out := buf.String()
	if !strings.Contains(out, "auth optional") {
		t.Fatalf("expected auth-optional warning in log output, got: %s", out)
	}
}

func TestAuthOptionalFromEnv(t *testing.T) {
	cases := map[string]bool{
		"":     false,
		"0":    false,
		"no":   false,
		"1":    true,
		"true": true,
		"TRUE": true,
		" 1 ":  true,
	}
	for val, want := range cases {
		t.Setenv("SB_TOOLBOX_AUTH_OPTIONAL", val)
		if got := authOptionalFromEnv(); got != want {
			t.Errorf("authOptionalFromEnv(%q) = %v, want %v", val, got, want)
		}
	}
}
