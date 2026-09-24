package daytona

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The gateway carries the caller's PAT in
// Sec-WebSocket-Protocol: "sandbox.bearer, <PAT>" on WebSocket handshakes
// (browsers cannot set Authorization). Whatever path a toolbox request takes
// through this facade — proxyToolbox (WS-capable reverse proxy) or
// forwardToolbox (plain HTTP) — the sandbox must only ever see the per-sandbox
// toolbox token, never the caller PAT (docs/exec-streaming.md).
func TestToolboxProxy_StripsCallerPATFromSecWebSocketProtocol(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"proxy_path_session", "/process/session"},
		{"forward_path_files", "/files"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newToolboxProxyTestEnv(t)

			req, err := http.NewRequest(http.MethodGet, env.facade.URL+ToolboxPrefix+"/"+env.sandboxID+tc.path, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Sec-WebSocket-Protocol", "sandbox.bearer, caller-pat-token")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", resp.StatusCode)
			}

			select {
			case up := <-env.toolboxRequests:
				if got := up.Header.Get("Authorization"); got != "Bearer tok-proxy" {
					t.Fatalf("upstream Authorization = %q, want %q", got, "Bearer tok-proxy")
				}
				for _, v := range up.Header.Values("Sec-WebSocket-Protocol") {
					if strings.Contains(v, "caller-pat-token") {
						t.Fatalf("caller PAT leaked upstream via Sec-WebSocket-Protocol: %q", v)
					}
				}
				if vals := up.Header.Values("Sec-WebSocket-Protocol"); len(vals) != 0 {
					t.Fatalf("Sec-WebSocket-Protocol forwarded upstream: %q, want none", vals)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for upstream toolbox request")
			}
		})
	}
}
