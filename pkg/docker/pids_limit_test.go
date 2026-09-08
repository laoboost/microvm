package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func captureCreateBody(t *testing.T, c *Client) func() []byte {
	t.Helper()
	var createBody []byte
	base := c.httpClient.Transport
	c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && r.URL.Path == "/containers/create" {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read create body: %v", err)
			}
			createBody = b
			r.Body = io.NopCloser(bytesReader(b))
		}
		return base.RoundTrip(r)
	})
	return func() []byte { return createBody }
}

func decodeHostConfig(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var decoded struct {
		HostConfig map[string]any `json:"HostConfig"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("create body: %v", err)
	}
	return decoded.HostConfig
}

// it sets a pids limit on the docker host config when enabled
func TestCreate_SetsPidsLimitOnHostConfigWhenEnabled(t *testing.T) {
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
	c := newCreateClient(t, d, true, func(c *Client) { c.pidsLimit = 1024 })
	getCreateBody := captureCreateBody(t, c)

	req := models.CreateSandboxRequest{Image: "registry.example/app:v1", CPU: 2, MemoryMB: 1024}
	if _, err := c.Create(context.Background(), req, "sb", "tok", nil); err == nil {
		t.Fatal("Create() expected injected create failure")
	}

	hostConfig := decodeHostConfig(t, getCreateBody())
	if got, ok := hostConfig["PidsLimit"]; !ok {
		t.Fatalf("PidsLimit missing from HostConfig: %v", hostConfig)
	} else if got != float64(1024) {
		t.Fatalf("PidsLimit = %v, want 1024", got)
	}
}

// it omits the pids limit when disabled by config
func TestCreate_OmitsPidsLimitWhenDisabledByConfig(t *testing.T) {
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
	c := newCreateClient(t, d, true, func(c *Client) { c.pidsLimit = 0 })
	getCreateBody := captureCreateBody(t, c)

	req := models.CreateSandboxRequest{Image: "registry.example/app:v1", CPU: 2, MemoryMB: 1024}
	if _, err := c.Create(context.Background(), req, "sb", "tok", nil); err == nil {
		t.Fatal("Create() expected injected create failure")
	}

	hostConfig := decodeHostConfig(t, getCreateBody())
	if _, ok := hostConfig["PidsLimit"]; ok {
		t.Fatalf("PidsLimit must be absent when disabled: %v", hostConfig)
	}
}

// it sets the pids limit even when cpu and memory requests are zero
func TestCreate_SetsPidsLimitEvenWhenCPUAndMemoryRequestsAreZero(t *testing.T) {
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
	c := newCreateClient(t, d, true, func(c *Client) { c.pidsLimit = 1024 })
	getCreateBody := captureCreateBody(t, c)

	req := models.CreateSandboxRequest{Image: "registry.example/app:v1"}
	if _, err := c.Create(context.Background(), req, "sb", "tok", nil); err == nil {
		t.Fatal("Create() expected injected create failure")
	}

	hostConfig := decodeHostConfig(t, getCreateBody())
	if hostConfig["PidsLimit"] != float64(1024) {
		t.Fatalf("PidsLimit = %v, want 1024 (must not be nested under cpu/memory conditionals)", hostConfig)
	}
	if hostConfig["CpuQuota"] != nil || hostConfig["Memory"] != nil {
		t.Fatalf("zero requests must not produce cpu/memory limits: %v", hostConfig)
	}
}
