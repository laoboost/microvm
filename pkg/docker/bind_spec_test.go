package docker

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
	"github.com/aerol-ai/microvm/pkg/mounts"
)

// HostConfig.Binds entries are colon-joined src:dst[:opts]; a colon or comma
// in either path segment injects bind options (ContainerPath "/data:rshared"
// becomes an rshared-propagation bind). Create must reject such binds before
// any request reaches dockerd.
func TestCreate_RejectsBindPathsWithOptionInjection(t *testing.T) {
	d := &fakeDaemon{
		t: t,
		imageInspect: func() *http.Response {
			return jsonResponse(http.StatusOK, map[string]any{
				"Config": map[string]any{"WorkingDir": "/", "Entrypoint": []string{}, "Cmd": []string{"/bin/sh"}},
			})
		},
		create: func() *http.Response {
			return textResponse(http.StatusInternalServerError, `{"message":"stop here"}`)
		},
	}
	c := newCreateClient(t, d, true, nil)
	getCreateBody := captureCreateBody(t, c)

	bad := []mounts.ContainerBind{
		{HostPath: "/tmp/host", ContainerPath: "/data:rshared"},
		{HostPath: "/tmp/host", ContainerPath: "/data,suid"},
		{HostPath: "/tmp/host:evil", ContainerPath: "/data"},
		{HostPath: "/tmp/host,evil", ContainerPath: "/data"},
	}
	for _, b := range bad {
		_, err := c.Create(context.Background(), models.CreateSandboxRequest{Image: "registry.example/app:v1"}, "sb", "tok", []mounts.ContainerBind{b})
		if err == nil {
			t.Fatalf("Create(bind %+v) expected error", b)
		}
		if !strings.Contains(err.Error(), "bind") {
			t.Fatalf("Create(bind %+v) error = %v, want bind validation error", b, err)
		}
		if body := getCreateBody(); body != nil {
			t.Fatalf("Create(bind %+v) sent a container create request: %s", b, body)
		}
	}

	// A plain bind still reaches the create API.
	_, err := c.Create(context.Background(), models.CreateSandboxRequest{Image: "registry.example/app:v1"}, "sb", "tok",
		[]mounts.ContainerBind{{HostPath: "/tmp/host", ContainerPath: "/data"}})
	if err == nil {
		t.Fatal("Create() expected injected create failure for the good bind")
	}
	if body := getCreateBody(); body == nil {
		t.Fatal("good bind must produce a container create request")
	} else if !strings.Contains(string(body), "/tmp/host:/data") {
		t.Fatalf("create body missing good bind: %s", body)
	}
}
