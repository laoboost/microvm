package docker

import (
	"context"
	"net/http"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// poolEligible treats "ROOT" as eligible (case-insensitive), but the cold path
// used to pass the raw string through as HostConfig.User — dockerd resolves
// user names case-sensitively against the image's /etc/passwd, so "ROOT" is
// unresolvable. Eligibility and the request must agree on the normalized name.
func TestCreate_NormalizesRootOSUserForColdPath(t *testing.T) {
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
		OSUser: "ROOT",
	}, "sb", "tok", nil)
	if err == nil {
		t.Fatal("Create() expected injected create failure")
	}

	hostConfig := decodeHostConfig(t, getCreateBody())
	if hostConfig["User"] != "root" {
		t.Fatalf("User = %v, want normalized %q (dockerd cannot resolve %q)", hostConfig["User"], "root", "ROOT")
	}
}

// The eligibility decision and the cold-path User must not diverge: whichever
// root spelling a caller sends, poolEligible and the container's HostConfig
// agree.
func TestPoolEligibleRootSpellingsAgree(t *testing.T) {
	for _, user := range []string{"root", "ROOT", "Root"} {
		req := models.CreateSandboxRequest{Image: "alpine:3.20", OSUser: user}
		if !poolEligible(req, nil, 0) {
			t.Fatalf("OSUser %q: expected pool-eligible", user)
		}
		if got := normalizeOSUser(req.OSUser); got != "root" {
			t.Fatalf("normalizeOSUser(%q) = %q, want root", user, got)
		}
	}
	if poolEligible(models.CreateSandboxRequest{Image: "alpine:3.20", OSUser: "ubuntu"}, nil, 0) {
		t.Fatal("non-root OSUser must not be pool-eligible")
	}
}
