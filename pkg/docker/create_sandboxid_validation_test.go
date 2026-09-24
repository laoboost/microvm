package docker

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// sandboxID is joined into host paths (ready-socket files) and used as the
// Docker name, so Create must reject an invalid ID up front — before any engine
// round-trip — matching pkg/mounts' per-id path guard.
func TestCreate_RejectsInvalidSandboxIDBeforeEngineCalls(t *testing.T) {
	inspectCalls := 0
	d := &fakeDaemon{
		t: t,
		imageInspect: func() *http.Response {
			inspectCalls++
			return jsonResponse(http.StatusOK, map[string]any{
				"Config": map[string]any{"WorkingDir": "/", "Entrypoint": []string{}, "Cmd": []string{"/bin/sh"}},
			})
		},
		create: func() *http.Response {
			return textResponse(http.StatusInternalServerError, `{"message":"stop here"}`)
		},
	}
	c := newCreateClient(t, d, true, nil)

	for _, id := range []string{"../evil", "a/b", "has space"} {
		_, err := c.Create(context.Background(), models.CreateSandboxRequest{Image: "registry.example/app:v1"}, id, "tok", nil)
		if err == nil {
			t.Fatalf("Create(sandboxID=%q) succeeded, want rejection", id)
		}
		if !strings.Contains(err.Error(), "invalid sandbox id") {
			t.Fatalf("Create(sandboxID=%q) err = %v, want invalid sandbox id", id, err)
		}
	}
	if inspectCalls != 0 {
		t.Fatalf("image inspect calls = %d, want 0 (ID must be validated before any engine call)", inspectCalls)
	}
}
