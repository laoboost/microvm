package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/internal/pool/dockerpool"
	"github.com/aerol-ai/microvm/pkg/models"
)

// Parked (warm-pool) containers are what every default create actually boots,
// so their HostConfig must carry the same no-new-privileges hardening as a
// cold create. parkContainer used to build HostConfig inline and omitted it.
func TestParkContainer_HostConfigCarriesSameSecurityOptAsColdCreate(t *testing.T) {
	d := &poolFakeDaemon{t: t}
	d.create = func() *http.Response {
		return textResponse(http.StatusInternalServerError, `{"message":"stop here"}`)
	}
	c := newPoolClient(t, d, func(c *Client) { c.readyDir = t.TempDir() })

	_, err := c.parkContainer(context.Background(), "park-sec", dockerpool.Key{
		Image: "alpine:3.20", Runtime: models.RuntimeDocker,
	})
	if err == nil || !strings.Contains(err.Error(), "park create") {
		t.Fatalf("err = %v, want park create failure", err)
	}
	if len(d.createBodies) != 1 {
		t.Fatalf("create bodies = %d, want 1", len(d.createBodies))
	}
	var body struct {
		HostConfig map[string]any `json:"HostConfig"`
	}
	if err := json.Unmarshal(d.createBodies[0], &body); err != nil {
		t.Fatalf("create body: %v", err)
	}
	secOpt, ok := body.HostConfig["SecurityOpt"].([]any)
	if !ok {
		t.Fatalf("parked HostConfig missing SecurityOpt: %v", body.HostConfig)
	}
	found := false
	for _, o := range secOpt {
		if o == "no-new-privileges=true" {
			found = true
		}
	}
	if !found {
		t.Fatalf("parked SecurityOpt = %v, want no-new-privileges=true", secOpt)
	}
}
