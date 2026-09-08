package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/internal/pool/dockerpool"
	"github.com/aerol-ai/microvm/pkg/models"
)

// it applies the same limit to warm pool parked containers
func TestParkContainer_AppliesSamePidsLimitToParkedContainers(t *testing.T) {
	d := &poolFakeDaemon{t: t}
	dir, err := os.MkdirTemp("", "rd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	c := newPoolClient(t, d, func(c *Client) {
		c.pulls = make(map[string]*imagePull)
		c.readyDir = dir
		c.pidsLimit = 1024
	})

	// Parked bootstrap creates containers with zero CPU/memory requests;
	// fail at create so we only need the captured request body.
	d.create = func() *http.Response {
		return textResponse(http.StatusInternalServerError, `{"message":"boom"}`)
	}

	_, err = c.parkContainer(context.Background(), "park-pids1", dockerpool.Key{Image: "alpine:3.20", Runtime: models.RuntimeDocker})
	if err == nil || !strings.Contains(err.Error(), "park create") {
		t.Fatalf("err = %v, want park create failure", err)
	}
	if len(d.createBodies) != 1 {
		t.Fatalf("create bodies = %d", len(d.createBodies))
	}
	var body struct {
		HostConfig map[string]any `json:"HostConfig"`
	}
	if err := json.Unmarshal(d.createBodies[0], &body); err != nil {
		t.Fatalf("create body: %v", err)
	}
	if body.HostConfig["PidsLimit"] != float64(1024) {
		t.Fatalf("parked create body PidsLimit = %v, want 1024 (limit must not depend on resource requests)", body.HostConfig)
	}
}
