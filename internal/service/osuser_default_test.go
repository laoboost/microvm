package service

import (
	"context"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// normalizeCreateRequest used to force OSUser="root" on every create. The
// docker create path then sent HostConfig.User=root, so an image shipping a
// non-root USER was silently run as root — and the cold path diverged from
// warm-pool park slots (which never set a User). An unspecified OSUser must
// stay unspecified so the image's own USER applies.
func TestCreateSandbox_UnspecifiedOSUserStaysUnset(t *testing.T) {
	ctx := context.Background()
	rt := &recordingRuntime{}
	svc, _, _ := newServiceRuntimeHarness(t, rt)

	if _, err := svc.CreateSandbox(ctx, models.CreateSandboxRequest{Image: "alpine:3.20"}); err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if rt.lastCreateReq.OSUser != "" {
		t.Fatalf("runtime create OSUser = %q, want empty (unspecified must not be forced to root)", rt.lastCreateReq.OSUser)
	}
}

// An explicit OSUser is still passed through untouched.
func TestNormalizeCreateRequest_PreservesExplicitOSUser(t *testing.T) {
	got := normalizeCreateRequest(models.CreateSandboxRequest{OSUser: "ubuntu"})
	if got.OSUser != "ubuntu" {
		t.Fatalf("OSUser = %q, want ubuntu", got.OSUser)
	}
	got = normalizeCreateRequest(models.CreateSandboxRequest{OSUser: "root"})
	if got.OSUser != "root" {
		t.Fatalf("explicit root OSUser = %q, want root", got.OSUser)
	}
}
