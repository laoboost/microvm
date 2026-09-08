package v1

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/internal/config"
	"github.com/aerol-ai/microvm/internal/service"
)

// TestCreateSandboxAPIRejectsTransportPrefixedImage pins the tenant API
// boundary: the v1 create endpoint must refuse container transport prefixes
// (docker-archive:, oci-archive:, oci:, dir:) with a 400 before the request
// reaches the runtime layer.
func TestCreateSandboxAPIRejectsTransportPrefixedImage(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(config.Config{}, logger, nil, nil, nil, nil, nil, nil, nil)
	h := &handlers{deps: Deps{Service: svc, Logger: logger}}

	for _, image := range []string{
		"docker-archive:alpine.tar",
		"oci-archive:rootfs.tar",
		"oci:registry.example/repo:tag",
		"dir:/var/lib/images/alpine",
	} {
		t.Run(image, func(t *testing.T) {
			body := `{"image": "` + image + `"}`
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", strings.NewReader(body))
			h.createSandbox(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("create with %q: status = %d, body = %s, want 400", image, rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "transport prefix") {
				t.Fatalf("create with %q: body = %s, want a transport-prefix rejection", image, rr.Body.String())
			}
		})
	}
}
