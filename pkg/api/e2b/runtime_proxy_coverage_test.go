package e2b

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/internal/config"
)

func TestRuntimeProxyWakeErrorWithClosedDB(t *testing.T) {
	_, st, handler := newE2BHandlerTestEnv(t)
	id := createE2BSandbox(t, handler)
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/e2b/runtime/health", nil)
	req.Header.Set("E2b-Sandbox-Id", id)
	handler.ServeHTTP(rr, req)
	if rr.Code == http.StatusOK {
		t.Fatal("expected wake/proxy error with closed DB")
	}
}

func TestRuntimeProxyBasicAuthHeaderBranches(t *testing.T) {
	toolboxRequests := make(chan *http.Request, 1)
	toolboxServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		toolboxRequests <- r.Clone(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	defer toolboxServer.Close()

	parsed, err := url.Parse(toolboxServer.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("split host: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	runtime := newFakeE2BRuntime()
	runtime.containerIP = host
	_, _, handler := newE2BHandlerTestEnvWithRuntime(t, runtime, config.Config{
		PublicHost:  "sandbox.test",
		EnableCaddy: false,
		ToolboxPort: port,
	})

	createReq := httptest.NewRequest(http.MethodPost, "/e2b/sandboxes", strings.NewReader(`{"templateID":"base","secure":true}`))
	createResp := httptest.NewRecorder()
	handler.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d", createResp.Code)
	}
	var created sandboxResponse
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	runtimeReq := httptest.NewRequest(http.MethodGet, "/e2b/runtime/health", nil)
	runtimeReq.Header.Set("E2b-Sandbox-Id", created.SandboxID)
	runtimeReq.Header.Set("X-Access-Token", created.EnvdAccessToken)
	runtimeReq.Header.Set("Authorization", "Basic dXNlcjo=")
	runtimeResp := httptest.NewRecorder()
	handler.ServeHTTP(runtimeResp, runtimeReq)
	if runtimeResp.Code != http.StatusOK {
		t.Fatalf("basic auth proxy status = %d", runtimeResp.Code)
	}

	select {
	case forwarded := <-toolboxRequests:
		if forwarded.Header.Get("X-E2B-User-Authorization") != "Basic dXNlcjo=" {
			t.Fatalf("user auth = %q", forwarded.Header.Get("X-E2B-User-Authorization"))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected forwarded toolbox request")
	}

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/e2b/runtime/health", nil)
	req2.Header.Set("E2b-Sandbox-Id", created.SandboxID)
	req2.Header.Set("X-Access-Token", created.EnvdAccessToken)
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("non-basic auth proxy status = %d", rr2.Code)
	}
}
