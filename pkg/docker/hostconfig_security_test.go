package docker

import (
	"context"
	"net/http"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// HostConfig is built as {Privileged, Binds} only and never carries
// no-new-privileges, so a setuid binary inside the container can re-escalate.
func TestCreate_SetsNoNewPrivilegesSecurityOpt(t *testing.T) {
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

	_, err := c.Create(context.Background(), models.CreateSandboxRequest{
		Image:  "registry.example/app:v1",
		OSUser: "ubuntu",
	}, "sb", "tok", nil)
	if err == nil {
		t.Fatal("Create() expected injected create failure")
	}

	hostConfig := decodeHostConfig(t, getCreateBody())
	secOpt, ok := hostConfig["SecurityOpt"].([]any)
	if !ok {
		t.Fatalf("SecurityOpt missing from HostConfig: %v", hostConfig)
	}
	found := false
	for _, o := range secOpt {
		if o == "no-new-privileges=true" {
			found = true
		}
	}
	if !found {
		t.Fatalf("SecurityOpt = %v, want no-new-privileges=true", secOpt)
	}
}

// The create path ignores req.OSUser: containers run as whatever USER the
// image ships (often root = host uid 0).
func TestCreate_SetsUserFromOSUser(t *testing.T) {
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

	_, err := c.Create(context.Background(), models.CreateSandboxRequest{
		Image:  "registry.example/app:v1",
		OSUser: "ubuntu",
	}, "sb", "tok", nil)
	if err == nil {
		t.Fatal("Create() expected injected create failure")
	}

	hostConfig := decodeHostConfig(t, getCreateBody())
	if hostConfig["User"] != "ubuntu" {
		t.Fatalf("User = %v, want ubuntu", hostConfig["User"])
	}
}

// Privileged is the operator's explicit full-access opt-in and stays opt-in;
// it exempts the container from no-new-privileges (which would defeat it).
func TestCreate_PrivilegedExemptsNoNewPrivileges(t *testing.T) {
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
	c := newCreateClient(t, d, true, func(c *Client) { c.privileged = true })
	getCreateBody := captureCreateBody(t, c)

	_, err := c.Create(context.Background(), models.CreateSandboxRequest{
		Image:  "registry.example/app:v1",
		OSUser: "root",
	}, "sb", "tok", nil)
	if err == nil {
		t.Fatal("Create() expected injected create failure")
	}

	hostConfig := decodeHostConfig(t, getCreateBody())
	if _, ok := hostConfig["SecurityOpt"]; ok {
		t.Fatalf("SecurityOpt must be absent for privileged containers: %v", hostConfig)
	}
	if hostConfig["Privileged"] != true {
		t.Fatalf("Privileged = %v, want true", hostConfig["Privileged"])
	}
}

// An empty OSUser leaves the image's own USER in charge (no User override).
func TestCreate_OmitsUserWhenOSUserEmpty(t *testing.T) {
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

	_, err := c.Create(context.Background(), models.CreateSandboxRequest{Image: "registry.example/app:v1"}, "sb", "tok", nil)
	if err == nil {
		t.Fatal("Create() expected injected create failure")
	}

	hostConfig := decodeHostConfig(t, getCreateBody())
	if _, ok := hostConfig["User"]; ok {
		t.Fatalf("User must be absent when OSUser is empty: %v", hostConfig)
	}
}
