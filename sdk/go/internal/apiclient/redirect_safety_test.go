package apiclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

type recordedRequest struct {
	authorization string
	registryToken string
	registryUser  string
	body          string
	contentLength int64
}

// TestCrossOriginRedirectStripsRegistryCredentials pins the redirect policy:
// following a cross-origin 3xx must never forward Authorization or the
// X-Registry-* headers to the redirect target.
func TestCrossOriginRedirectStripsRegistryCredentials(t *testing.T) {
	var mu sync.Mutex
	var hits []recordedRequest

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits = append(hits, recordedRequest{
			authorization: r.Header.Get("Authorization"),
			registryToken: r.Header.Get("X-Registry-Token"),
			registryUser:  r.Header.Get("X-Registry-Username"),
			body:          string(body),
		})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"module_ref":"oci://x/mod:latest","digest":"sha256:1","size_bytes":3}`))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/wasm-modules/push", http.StatusFound)
	}))
	defer redirector.Close()

	client := NewClient(redirector.URL, ClientOptions{PATToken: "pat-token", HTTPClient: &http.Client{}})
	_, err := client.PushWasmModule(context.Background(), models.PushWasmModuleOptions{
		Name:             "mod",
		Tag:              "latest",
		Module:           []byte("wasm"),
		RegistryUsername: "registry-user",
		RegistryToken:    "registry-token-secret",
	})
	if err != nil {
		t.Fatalf("PushWasmModule() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 1 {
		t.Fatalf("target hits = %d, want 1", len(hits))
	}
	hit := hits[0]
	if hit.registryToken != "" {
		t.Errorf("cross-origin redirect leaked X-Registry-Token: %q", hit.registryToken)
	}
	if hit.registryUser != "" {
		t.Errorf("cross-origin redirect leaked X-Registry-Username: %q", hit.registryUser)
	}
	if hit.authorization != "" {
		t.Errorf("cross-origin redirect leaked Authorization: %q", hit.authorization)
	}
}

// TestCrossOriginRedirectRefusesBodyReplay pins the second half of the policy:
// a 307/308 that would replay a credential-bearing body to a different origin
// must be refused, not followed.
func TestCrossOriginRedirectRefusesBodyReplay(t *testing.T) {
	const secret = "super-secret-push-password"

	var mu sync.Mutex
	var hitCount int
	var bodies []string

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		hitCount++
		bodies = append(bodies, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"image":"built:latest"}`))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL+"/v1/images/build")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	client := NewClient(redirector.URL, ClientOptions{PATToken: "pat-token", HTTPClient: &http.Client{}})
	_, err := client.BuildImageWithPush(context.Background(), "FROM alpine", &BuildImagePushSpec{
		Registry: "ghcr.io/acme/app",
		Username: "acme",
		Password: secret,
	})

	mu.Lock()
	defer mu.Unlock()
	for i, body := range bodies {
		if strings.Contains(body, secret) {
			t.Errorf("cross-origin redirect replayed request body %d containing credentials", i)
		}
	}
	if hitCount > 0 && err == nil {
		t.Errorf("cross-origin 307 with a credential-bearing body was followed (hitCount=%d); want refusal", hitCount)
	}
	if hitCount == 0 && err == nil {
		t.Errorf("cross-origin 307 with a credential-bearing body neither followed nor refused")
	}
}

// TestDefaultPortIsNotCrossOrigin pins that a redirect which only spells out a
// default port (https://h/x -> https://h:443/x) is same-origin, so credentials
// are not stripped unnecessarily.
func TestDefaultPortIsNotCrossOrigin(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"https://h/x", "https://h:443/x", false},
		{"http://h/x", "http://h:80/x", false},
		{"https://h/x", "https://h:8443/x", true},
		{"https://h/x", "http://h/x", true},
		{"https://h:443/x", "https://other:443/x", true},
	}
	for _, tc := range cases {
		a, err := url.Parse(tc.a)
		if err != nil {
			t.Fatalf("url.Parse(%q) error = %v", tc.a, err)
		}
		b, err := url.Parse(tc.b)
		if err != nil {
			t.Fatalf("url.Parse(%q) error = %v", tc.b, err)
		}
		if got := isCrossOrigin(a, b); got != tc.want {
			t.Errorf("isCrossOrigin(%s, %s) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestSameOriginRedirectKeepsAuthorization is the regression guard for the
// redirect policy: same-origin redirects keep working and keep auth.
func TestSameOriginRedirectKeepsAuthorization(t *testing.T) {
	var sawAuthOnSecondHit string
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/health" {
			http.Redirect(w, r, "/health2", http.StatusFound)
			return
		}
		sawAuthOnSecondHit = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, ClientOptions{PATToken: "pat-token", HTTPClient: server.Client()})
	if _, err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if hits != 2 {
		t.Fatalf("hits = %d, want 2 (redirect should be followed same-origin)", hits)
	}
	if sawAuthOnSecondHit != "Bearer pat-token" {
		t.Errorf("same-origin redirect dropped Authorization: %q", sawAuthOnSecondHit)
	}
}
