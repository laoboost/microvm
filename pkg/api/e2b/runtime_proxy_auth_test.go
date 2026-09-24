package e2b

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// The /e2b/runtime surface is not wrapped in d.Auth (E2B clients
// authenticate envd calls with the per-sandbox X-Access-Token), so the
// proxy must enforce that token on every request — otherwise a
// meta.Secure=false sandbox accepted unauthenticated envd read/write/exec
// through a proxy that injects the real toolbox token upstream. The old
// Secure=false skip is gated behind SB_E2B_ALLOW_UNAUTHENTICATED_RUNTIME
// (default off).

// it requires the access token even when meta.Secure is false
func TestRuntimeProxyRequiresTokenWhenMetaInsecure(t *testing.T) {
	_, st, handler := newE2BHandlerTestEnv(t)
	id := createE2BSandbox(t, handler)

	stateBlob := compatBlob{Secure: false, OnTimeout: "kill"}
	b, err := json.Marshal(stateBlob)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCompatState(context.Background(), id, "e2b", string(b)); err != nil {
		t.Fatal(err)
	}
	st.UpdateStatus(context.Background(), id, models.SandboxStatusStarted, "")
	st.UpdateRuntime(context.Background(), id, "cid", "127.0.0.1", "")

	req := httptest.NewRequest(http.MethodGet, "/e2b/runtime/", nil)
	req.Header.Set("E2b-Sandbox-Id", id)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no X-Access-Token with meta.Secure=false = %d, want 401 (token is mandatory unless the operator flag is on)", rr.Code)
	}
}

// it restores the unauthenticated Secure=false behavior only behind the
// operator flag
func TestRuntimeProxyInsecureOptOutBehindOperatorFlag(t *testing.T) {
	t.Setenv("SB_E2B_ALLOW_UNAUTHENTICATED_RUNTIME", "1")

	_, st, handler := newE2BHandlerTestEnv(t)
	id := createE2BSandbox(t, handler)

	stateBlob := compatBlob{Secure: false, OnTimeout: "kill"}
	b, err := json.Marshal(stateBlob)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCompatState(context.Background(), id, "e2b", string(b)); err != nil {
		t.Fatal(err)
	}
	st.UpdateStatus(context.Background(), id, models.SandboxStatusStarted, "")
	st.UpdateRuntime(context.Background(), id, "cid", "127.0.0.1", "")

	req := httptest.NewRequest(http.MethodGet, "/e2b/runtime/", nil)
	req.Header.Set("E2b-Sandbox-Id", id)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	// Old behavior: no token check → the request proceeds to the proxy
	// stage and fails to dial the fake sandbox (502/503), not 401.
	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("operator flag must restore unauthenticated Secure=false access; got 401")
	}
}

// it rejects a wrong-length access token (constant-time compare contract)
func TestRuntimeProxyRejectsWrongLengthAccessToken(t *testing.T) {
	_, _, handler := newE2BHandlerTestEnv(t)
	id := createE2BSandbox(t, handler)

	req := httptest.NewRequest(http.MethodGet, "/e2b/runtime/", nil)
	req.Header.Set("E2b-Sandbox-Id", id)
	req.Header.Set("X-Access-Token", "short")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-length X-Access-Token = %d, want 401", rr.Code)
	}
}

// it validates access tokens with a length-safe comparison
func TestAccessTokenValid(t *testing.T) {
	if accessTokenValid("", "0123456789abcdef") {
		t.Fatal("empty candidate must not validate")
	}
	if accessTokenValid("0123456789abcdef", "") {
		t.Fatal("empty want must not validate")
	}
	if accessTokenValid("short", "0123456789abcdef") {
		t.Fatal("wrong-length token must not validate")
	}
	if accessTokenValid("0123456789abcdef0", "0123456789abcdef") {
		t.Fatal("longer wrong-length token must not validate")
	}
	if !accessTokenValid("0123456789abcdef", "0123456789abcdef") {
		t.Fatal("matching token must validate")
	}
}
