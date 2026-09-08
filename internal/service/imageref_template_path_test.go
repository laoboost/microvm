package service

import (
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/models"
)

// TestCreateTemplateLeavesInternalBuildImageRefPathUnvalidated pins the
// deliberate carve-out: the internal Firecracker template build pipeline
// (operator-PAT only) keeps transport refs (oci-archive:/docker-archive:)
// and must NOT go through the tenant ValidateImageRef gate. The raw ref
// reaches the builder untouched.
func TestCreateTemplateLeavesInternalBuildImageRefPathUnvalidated(t *testing.T) {
	svc, _, _ := newTemplateHarness(t)

	done := make(chan struct{}, 1)
	builder := &fakeTemplateBuilder{done: done}
	svc.SetTemplateBuilder(builder)

	const transportRef = "oci-archive:/var/lib/microvm/rootfs.tar"
	tpl, err := svc.CreateTemplate(t.Context(), models.CreateTemplateRequest{
		ID:    "tpl-transport",
		Image: transportRef,
	})
	if err != nil {
		t.Fatalf("CreateTemplate(%q) error = %v, want the internal build path to keep transport refs", transportRef, err)
	}
	if tpl.Status != models.TemplateStatusPending {
		t.Fatalf("CreateTemplate() status = %s, want pending", tpl.Status)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("template build did not run")
	}
	builder.mu.Lock()
	got := builder.lastReq.ImageRef
	builder.mu.Unlock()
	if got != transportRef {
		t.Fatalf("builder ImageRef = %q, want the raw transport ref %q passed through unvalidated", got, transportRef)
	}
}
