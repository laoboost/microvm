package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/internal/store"
	"github.com/aerol-ai/microvm/pkg/models"
)

// Template IDs are joined into FirecrackerTemplatesDir (kickTemplateBuild,
// DeleteTemplate's RemoveAll). A caller-supplied id like "../victim" must be
// rejected at the service boundary before any MkdirAll/RemoveAll runs.
func TestCreateTemplate_RejectsTraversalID(t *testing.T) {
	ctx := context.Background()
	svc, st, templatesDir := newTemplateHarness(t)
	svc.SetTemplateBuilder(&fakeTemplateBuilder{})

	base := filepath.Dir(templatesDir) // t.TempDir()
	outside := filepath.Join(base, "victim")

	tpl, err := svc.CreateTemplate(ctx, models.CreateTemplateRequest{
		ID:    "../victim",
		Image: "docker://alpine:3.20",
	})
	if err == nil {
		t.Fatalf("CreateTemplate(id=%q) = %+v, want validation error", "../victim", tpl)
	}
	if !strings.Contains(err.Error(), "invalid template id") {
		t.Fatalf("err = %q, want message containing %q", err.Error(), "invalid template id")
	}
	if _, statErr := os.Stat(outside); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("path %q was created outside FirecrackerTemplatesDir (stat err = %v)", outside, statErr)
	}
	rows, listErr := st.ListTemplates(ctx)
	if listErr != nil {
		t.Fatalf("ListTemplates: %v", listErr)
	}
	if len(rows) != 0 {
		t.Fatalf("template rows = %d, want 0", len(rows))
	}
}

func TestCreateTemplate_RejectsSeparatorIDs(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTemplateHarness(t)
	svc.SetTemplateBuilder(&fakeTemplateBuilder{})

	for _, id := range []string{"../../../../etc", "a/b", `a\b`, "a b", strings.Repeat("t", 200)} {
		if _, err := svc.CreateTemplate(ctx, models.CreateTemplateRequest{ID: id, Image: "docker://alpine:3.20"}); err == nil {
			t.Fatalf("CreateTemplate(id=%q) succeeded, want validation error", id)
		}
	}
}

// Reads deliberately skip TemplateIDValid (F2j): the id only reaches the store,
// so an unknown/traversal id is a plain not-found — not a validation error — and
// nothing on disk is touched. Validation is a create/write-only boundary
// (TestCreateTemplate_* above).
func TestGetTemplate_TraversalIDFallsThroughToStore(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTemplateHarness(t)

	_, err := svc.GetTemplate(ctx, "../../../../etc")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetTemplate(traversal) = %v, want store.ErrNotFound", err)
	}
}

func TestDeleteTemplate_TraversalIDFallsThroughToStore(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTemplateHarness(t)

	err := svc.DeleteTemplate(ctx, "../../../../etc")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DeleteTemplate(traversal) = %v, want store.ErrNotFound", err)
	}
}

func TestRequestTemplateRebuild_TraversalIDFallsThroughToStore(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTemplateHarness(t)

	_, err := svc.RequestTemplateRebuild(ctx, "../victim")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RequestTemplateRebuild(traversal) = %v, want store.ErrNotFound", err)
	}
}

func TestCreateTemplate_AcceptsValidID(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTemplateHarness(t)
	done := make(chan struct{}, 1)
	builder := &fakeTemplateBuilder{done: done}
	svc.SetTemplateBuilder(builder)

	tpl, err := svc.CreateTemplate(ctx, models.CreateTemplateRequest{
		ID:    "tpl-Custom_01",
		Image: "docker://alpine:3.20",
	})
	if err != nil {
		t.Fatalf("CreateTemplate(valid): %v", err)
	}
	if tpl.ID != "tpl-Custom_01" {
		t.Fatalf("template id = %q", tpl.ID)
	}
	<-done
}

func TestTemplateIDValid(t *testing.T) {
	for _, id := range []string{"tpl-abc123", "a", "A_b-9", strings.Repeat("x", 128)} {
		if !TemplateIDValid(id) {
			t.Errorf("TemplateIDValid(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", "..", "../x", "a/b", `a\b`, "a b", "a\x00b", ".", strings.Repeat("x", 129)} {
		if TemplateIDValid(id) {
			t.Errorf("TemplateIDValid(%q) = true, want false", id)
		}
	}
}
