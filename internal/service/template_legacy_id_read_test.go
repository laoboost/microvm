package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/models"
)

// TestLegacyTemplateIDRemainsReachableAndDeletable pins F2j: TemplateIDValid is
// a create/write boundary check, not a read gate. A row that predates the
// validation (e.g. an id containing '.') must stay readable and deletable —
// otherwise it becomes an unreachable, undeletable orphan.
func TestLegacyTemplateIDRemainsReachableAndDeletable(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newTemplateHarness(t)

	const legacyID = "legacy.tpl" // contains '.', rejected by templateIDPattern
	if err := st.CreateTemplate(ctx, &models.Template{
		ID: legacyID, Image: "x", Status: models.TemplateStatusReady,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	got, err := svc.GetTemplate(ctx, legacyID)
	if err != nil || got == nil || got.ID != legacyID {
		t.Fatalf("GetTemplate(legacy id) = %+v err=%v; a pre-existing row must stay readable", got, err)
	}

	if _, err := svc.RequestTemplateRebuild(ctx, legacyID); err != nil && strings.Contains(err.Error(), "invalid template id") {
		t.Fatalf("RequestTemplateRebuild(legacy id) = %v; validation must not gate reads of pre-existing rows", err)
	}

	if err := svc.DeleteTemplate(ctx, legacyID); err != nil {
		t.Fatalf("DeleteTemplate(legacy id) = %v; a pre-existing row must stay deletable", err)
	}
}
