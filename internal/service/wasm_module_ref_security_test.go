package service

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/internal/config"
	"github.com/aerol-ai/microvm/internal/store"
	"github.com/aerol-ai/microvm/pkg/controlplane"
	"github.com/aerol-ai/microvm/pkg/models"
	"github.com/aerol-ai/microvm/pkg/wasmmod"
)

func scopedCtx(owner string) context.Context {
	return controlplane.ContextWithAccess(context.Background(), controlplane.Access{
		Identity: controlplane.Identity{OwnerRef: owner},
	})
}

func newWasmModuleAPIHarness(t *testing.T, resolver WasmModuleResolver) *Service {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := New(config.Config{EnableWasm: true, WasmModulesDir: dir}, slog.New(slog.NewTextHandler(io.Discard, nil)), st, wasmModuleAPINoopRuntime{}, nil, nil, nil, nil, nil)
	if resolver != nil {
		svc.SetWasmModuleResolver(resolver)
	}
	return svc
}

// A scoped (user-token) caller must not be able to point module_ref at the
// host filesystem — contrast internal/service/isolate.go's jsbundle.IsFileRef
// gate. file:// / absolute refs are operator-only.
func TestCreateWasmModule_ScopedCallerRejectsHostPathRef(t *testing.T) {
	svc := newWasmModuleAPIHarness(t, stubWasmModuleResolver{path: "/etc/passwd", digest: "d1"})

	for _, ref := range []string{"file:///etc/passwd", "/etc/passwd"} {
		mod, err := svc.CreateWasmModule(scopedCtx("acc-1"), models.CreateWasmModuleRequest{ModuleRef: ref})
		if err == nil {
			t.Fatalf("CreateWasmModule(%q) = %+v, want error", ref, mod)
		}
		if !strings.Contains(err.Error(), "operator") {
			t.Fatalf("err = %q, want operator-only message", err.Error())
		}
	}
}

// ".."-bearing refs must not escape the modules dir. The probe file sits one
// level ABOVE ModulesDir so a missing escape check resolves successfully.
func TestCreateWasmModule_RejectsEscapingModuleRef(t *testing.T) {
	dir := t.TempDir()
	modules := filepath.Join(dir, "modules")
	if err := os.MkdirAll(modules, 0o755); err != nil {
		t.Fatal(err)
	}
	wasmmod.WriteMinimalWasm(t, dir, "escape.wasm") // outside ModulesDir
	resolver := wasmmod.NewResolver(modules)

	svc := newWasmModuleAPIHarness(t, resolver)
	_, err := svc.CreateWasmModule(scopedCtx("acc-1"), models.CreateWasmModuleRequest{ModuleRef: "../escape.wasm"})
	if err == nil {
		t.Fatal("scoped CreateWasmModule(../escape.wasm) succeeded, want error")
	}

	svc2 := newWasmModuleAPIHarness(t, resolver)
	_, err = svc2.CreateWasmModule(context.Background(), models.CreateWasmModuleRequest{ModuleRef: "../escape.wasm"})
	if err == nil {
		t.Fatal("unscoped CreateWasmModule(../escape.wasm) succeeded, want error")
	}

	svc3 := newWasmModuleAPIHarness(t, resolver)
	_, err = svc3.CreateWasmModule(context.Background(), models.CreateWasmModuleRequest{ModuleRef: "../../../etc/passwd"})
	if err == nil {
		t.Fatal("unscoped CreateWasmModule(../../../etc/passwd) succeeded, want error")
	}
}

// Relative refs that stay inside the modules dir keep working for scoped
// callers — the gate must not break the normal registration flow.
func TestCreateWasmModule_RelativeInModulesDirWorks(t *testing.T) {
	dir := t.TempDir()
	modules := filepath.Join(dir, "modules")
	if err := os.MkdirAll(filepath.Join(modules, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	wasmmod.WriteMinimalWasm(t, modules, "sub/mod.wasm")
	resolver := wasmmod.NewResolver(modules)
	svc := newWasmModuleAPIHarness(t, resolver)

	mod, err := svc.CreateWasmModule(scopedCtx("acc-1"), models.CreateWasmModuleRequest{ModuleRef: "sub/mod.wasm"})
	if err != nil {
		t.Fatalf("CreateWasmModule(sub/mod.wasm): %v", err)
	}
	if mod.Status != models.WasmModuleStatusReady {
		t.Fatalf("status = %s, want ready", mod.Status)
	}
}

// ModulePath is a host filesystem location — it must not leave the service.
func TestCreateWasmModule_ModulePathStrippedFromResponse(t *testing.T) {
	svc := newWasmModuleAPIHarness(t, stubWasmModuleResolver{path: "/host/secrets/mod.wasm", digest: "d-strip"})

	mod, err := svc.CreateWasmModule(scopedCtx("acc-1"), models.CreateWasmModuleRequest{ModuleRef: "mod.wasm"})
	if err != nil {
		t.Fatalf("CreateWasmModule: %v", err)
	}
	if mod.ModulePath != "" {
		t.Fatalf("ModulePath = %q, want empty (host path must be stripped from responses)", mod.ModulePath)
	}
	got, err := svc.GetWasmModule(scopedCtx("acc-1"), mod.ID)
	if err != nil {
		t.Fatalf("GetWasmModule: %v", err)
	}
	if got.ModulePath != "" {
		t.Fatalf("GetWasmModule ModulePath = %q, want empty", got.ModulePath)
	}
}

// Explicit catalogue ids are caller-supplied and must not carry traversal /
// separators.
func TestCreateWasmModule_RejectsTraversalExplicitID(t *testing.T) {
	svc := newWasmModuleAPIHarness(t, stubWasmModuleResolver{path: "/host/mod.wasm", digest: "d-id"})

	for _, id := range []string{"../evil", "a/b", strings.Repeat("m", 200)} {
		_, err := svc.CreateWasmModule(scopedCtx("acc-1"), models.CreateWasmModuleRequest{ID: id, ModuleRef: "mod.wasm"})
		if err == nil {
			t.Fatalf("CreateWasmModule(id=%q) succeeded, want error", id)
		}
		if !strings.Contains(err.Error(), "invalid module id") {
			t.Fatalf("err = %q, want message containing %q", err.Error(), "invalid module id")
		}
	}
}
