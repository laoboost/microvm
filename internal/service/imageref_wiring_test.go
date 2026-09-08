package service

import (
	"context"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// TestCreateSandboxRejectsTransportPrefixedImage wires the task-004
// ValidateImageRef into the tenant create path: any container transport
// prefix (other than docker://, which is stripped) must be rejected before
// the runtime is touched.
func TestCreateSandboxRejectsTransportPrefixedImage(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name  string
		image string
	}{
		{"it rejects a docker archive transport prefix on the service create path", "docker-archive:alpine.tar"},
		{"rejects oci archive prefix", "oci-archive:rootfs.tar"},
		{"rejects oci prefix", "oci:registry.example/repo:tag"},
		{"rejects dir prefix", "dir:/var/lib/images/alpine"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := newServiceRuntimeHarness(t, &recordingRuntime{})
			svc.admitter = nil

			_, err := svc.CreateSandbox(ctx, models.CreateSandboxRequest{Image: tc.image})
			if err == nil {
				t.Fatalf("CreateSandbox(%q) = nil error, want transport-prefix rejection", tc.image)
			}
			if !strings.Contains(err.Error(), "transport prefix") {
				t.Fatalf("CreateSandbox(%q) error = %v, want a transport-prefix rejection", tc.image, err)
			}
		})
	}
}

// TestCreateSandboxAcceptsProductionSandboxImage pins the exact production
// image ref from subchat (admin-set default) so the validator stays
// compatible with the one image the control plane actually sends.
func TestCreateSandboxAcceptsProductionSandboxImage(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newServiceRuntimeHarness(t, &recordingRuntime{})
	svc.admitter = nil

	const productionRef = "cr.selcloud.ru/speshu/agent-sandbox:v1"
	resp, err := svc.CreateSandbox(ctx, models.CreateSandboxRequest{Image: productionRef})
	if err != nil {
		t.Fatalf("CreateSandbox(%q) error = %v, want the production image accepted", productionRef, err)
	}
	if resp == nil || resp.ID == "" {
		t.Fatalf("CreateSandbox(%q) = %+v, want a created sandbox", productionRef, resp)
	}
}
