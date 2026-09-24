package wasmmod

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// resolvePath joins relative refs under ModulesDir. A ref like
// "../../../etc/passwd" must not escape that root — the API path passes
// user-supplied module_ref here.
func TestResolverRejectsEscapingRelativeRef(t *testing.T) {
	modules := filepath.Join(t.TempDir(), "modules")
	r := NewResolver(modules)

	_, err := r.Resolve(context.Background(), "../../../etc/passwd")
	if err == nil {
		t.Fatal("Resolve(../../../etc/passwd) succeeded, want ErrUnsafeModuleRef")
	}
	if !errors.Is(err, ErrUnsafeModuleRef) {
		t.Fatalf("err = %v, want ErrUnsafeModuleRef", err)
	}

	_, err = r.Resolve(context.Background(), "a/../../../../etc/passwd")
	if err == nil {
		t.Fatal("Resolve(a/../../../../etc/passwd) succeeded, want ErrUnsafeModuleRef")
	}
	if !errors.Is(err, ErrUnsafeModuleRef) {
		t.Fatalf("err = %v, want ErrUnsafeModuleRef", err)
	}
}

// A ref whose cleaned join stays inside ModulesDir keeps working, even when
// it carries interior ".." segments.
func TestResolverAllowsInTreeParentCollapse(t *testing.T) {
	modules := t.TempDir()
	writeTestWasm(t, modules, "ok.wasm")
	r := NewResolver(modules)

	got, err := r.Resolve(context.Background(), "sub/../ok.wasm")
	if err != nil {
		t.Fatalf("Resolve(sub/../ok.wasm): %v", err)
	}
	if got.Path != filepath.Join(modules, "ok.wasm") {
		t.Fatalf("path = %q, want %q", got.Path, filepath.Join(modules, "ok.wasm"))
	}
}
