package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestResourceIDsArePercentEscapedInURLPaths guards against path traversal via
// dynamic IDs: an id like "x/../admin" must be percent-escaped as a single
// path segment, never concatenated raw into the URL.
func TestResourceIDsArePercentEscapedInURLPaths(t *testing.T) {
	var gotPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.EscapedPath())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, ClientOptions{PATToken: "pat-token", HTTPClient: server.Client()})
	ctx := context.Background()
	hostile := "x/../admin"

	if _, err := client.Get(ctx, hostile); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if err := client.Destroy(ctx, hostile); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if _, err := client.GetTemplate(ctx, hostile); err != nil {
		t.Fatalf("GetTemplate() error = %v", err)
	}
	if err := client.DeleteTemplate(ctx, hostile); err != nil {
		t.Fatalf("DeleteTemplate() error = %v", err)
	}
	if _, err := client.GetWasmModule(ctx, hostile); err != nil {
		t.Fatalf("GetWasmModule() error = %v", err)
	}
	if _, err := client.GetSession(ctx, hostile, hostile); err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if _, err := client.ExposePort(ctx, hostile, 8080, ""); err != nil {
		t.Fatalf("ExposePort() error = %v", err)
	}
	if _, err := client.SessionLog(ctx, hostile, hostile); err != nil {
		t.Fatalf("SessionLog() error = %v", err)
	}

	escaped := "x%2F..%2Fadmin"
	want := []string{
		"/v1/sandboxes/" + escaped,
		"/v1/sandboxes/" + escaped,
		"/v1/templates/" + escaped,
		"/v1/templates/" + escaped,
		"/v1/wasm-modules/" + escaped,
		"/v1/sandboxes/" + escaped + "/sessions/" + escaped,
		"/v1/sandboxes/" + escaped + "/ports/8080",
		"/v1/sandboxes/" + escaped + "/sessions/" + escaped + "/log",
	}
	if len(gotPaths) != len(want) {
		t.Fatalf("got %d requests, want %d: %v", len(gotPaths), len(want), gotPaths)
	}
	for i := range want {
		if gotPaths[i] != want[i] {
			t.Errorf("request %d path = %q, want %q", i, gotPaths[i], want[i])
		}
	}
}
