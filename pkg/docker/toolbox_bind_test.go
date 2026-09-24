package docker

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/internal/pool/dockerpool"
	"github.com/aerol-ai/microvm/pkg/models"
)

// The toolbox bind is colon-joined from operator config; a ':' or ',' in the
// mount path would inject bind options (e.g. ":rshared"). Like the tenant
// mount loop, Create must reject it before any engine round-trip.
func TestCreate_RejectsToolboxBindInjection(t *testing.T) {
	d := &fakeDaemon{
		t: t,
		imageInspect: func() *http.Response {
			return jsonResponse(http.StatusOK, map[string]any{
				"Config": map[string]any{"WorkingDir": "/", "Entrypoint": []string{}, "Cmd": []string{"/bin/sh"}},
			})
		},
	}
	c := newCreateClient(t, d, true, func(c *Client) {
		c.toolboxMountPath = "/usr/local/bin/toolboxd:rshared"
	})
	getCreateBody := captureCreateBody(t, c)

	_, err := c.Create(context.Background(), models.CreateSandboxRequest{Image: "registry.example/app:v1"}, "sb", "tok", nil)
	if err == nil || !strings.Contains(err.Error(), "bind path") {
		t.Fatalf("Create() err = %v, want a bind-path rejection", err)
	}
	if body := getCreateBody(); body != nil {
		t.Fatalf("Create sent a container create request despite an injectable toolbox bind: %s", body)
	}
}

// The park path builds the same toolbox bind; it must reject injection too.
func TestParkContainer_RejectsToolboxBindInjection(t *testing.T) {
	d := &poolFakeDaemon{t: t}
	c := newPoolClient(t, d, func(c *Client) {
		c.toolboxMountPath = "/usr/local/bin/toolboxd,suid"
		c.readyDir = shortParkTestDir(t)
	})

	_, err := c.parkContainer(context.Background(), "park-inject", dockerpool.Key{
		Image: "alpine:3.20", Runtime: models.RuntimeDocker,
	})
	if err == nil || !strings.Contains(err.Error(), "bind path") {
		t.Fatalf("parkContainer() err = %v, want a bind-path rejection", err)
	}
	if len(d.createBodies) != 0 {
		t.Fatalf("parkContainer sent a create request despite an injectable toolbox bind: %s", d.createBodies)
	}
}
