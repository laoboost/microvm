package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/models"
)

// captureUpstream records the request headers that arrive at the "toolbox".
type captureUpstream struct {
	auth    string
	swsProt []string
}

func (c *captureUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.auth = r.Header.Get("Authorization")
	c.swsProt = r.Header.Values("Sec-WebSocket-Protocol")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

// toolboxProxySecurityHarness wires a recording upstream "toolbox daemon" and
// a docker-runtime sandbox whose ToolboxTarget points at it.
func toolboxProxySecurityHarness(t *testing.T) (*Service, *captureUpstream) {
	t.Helper()
	cap := &captureUpstream{}
	ts := httptest.NewServer(cap)
	t.Cleanup(ts.Close)
	u := strings.TrimPrefix(ts.URL, "http://")
	port, err := strconv.Atoi(u[strings.LastIndex(u, ":")+1:])
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	svc, st, _ := newServiceRuntimeHarness(t, &recordingRuntime{})
	svc.cfg.ToolboxPort = port
	now := time.Now().UTC()
	if err := st.Create(context.Background(), &models.Sandbox{
		ID:           "sb-proxy-sec",
		Runtime:      models.RuntimeDocker,
		Status:       models.SandboxStatusStarted,
		ToolboxToken: "toolbox-token-123",
		ContainerIP:  "127.0.0.1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("Create sandbox: %v", err)
	}
	return svc, cap
}

func assertNoPATArrived(t *testing.T, cap *captureUpstream) {
	t.Helper()
	if cap.auth != "Bearer toolbox-token-123" {
		t.Fatalf("upstream Authorization = %q, want %q", cap.auth, "Bearer toolbox-token-123")
	}
	for _, v := range cap.swsProt {
		if strings.Contains(v, "caller-pat-token") {
			t.Fatalf("Sec-WebSocket-Protocol leaked to upstream: %q", cap.swsProt)
		}
	}
	if len(cap.swsProt) != 0 {
		t.Fatalf("Sec-WebSocket-Protocol forwarded to upstream: %q, want none", cap.swsProt)
	}
}

// The gateway extracts the caller's PAT from
// Sec-WebSocket-Protocol: "sandbox.bearer, <PAT>" (browsers cannot set
// Authorization on a WS handshake). The toolbox proxy must strip that header
// before forwarding: only the per-sandbox toolbox token may reach the sandbox
// (docs/exec-streaming.md).
func TestServeToolboxReverseProxy_StripsSecWebSocketProtocol(t *testing.T) {
	svc, cap := toolboxProxySecurityHarness(t)

	req := httptest.NewRequest("GET", "http://example.com/pty", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "sandbox.bearer, caller-pat-token")
	rec := httptest.NewRecorder()

	if err := svc.ServeToolboxReverseProxy(context.Background(), "sb-proxy-sec", rec, req, "/pty"); err != nil {
		t.Fatalf("ServeToolboxReverseProxy: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	assertNoPATArrived(t, cap)
}

// RoundTripToolbox (the forwardToolbox / daytona plain-HTTP path) copies
// caller headers into the upstream request — same leak, same fix.
func TestRoundTripToolbox_StripsSecWebSocketProtocol(t *testing.T) {
	svc, cap := toolboxProxySecurityHarness(t)

	headers := http.Header{}
	headers.Set("Sec-WebSocket-Protocol", "sandbox.bearer, caller-pat-token")
	resp, err := svc.RoundTripToolbox(context.Background(), "sb-proxy-sec", "GET", "/session", nil, nil, headers)
	if err != nil {
		t.Fatalf("RoundTripToolbox: %v", err)
	}
	defer resp.Body.Close()
	assertNoPATArrived(t, cap)
}
