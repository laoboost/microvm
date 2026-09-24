package service

import (
	"context"
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// A scoped (user-token) caller must not be able to point a wasm sandbox's
// module_ref at the host filesystem. The resolver returns absolute/file://
// refs verbatim and its error text distinguishes missing/unreadable/empty/
// too-large/bad-magic, so an ungated create is a host-path existence and size
// oracle. This mirrors internal/service/isolate.go's jsbundle.IsFileRef gate.
func TestCreateWasmSandbox_ScopedCallerRejectsHostPathRef(t *testing.T) {
	for _, ref := range []string{"file:///etc/hostname", "/etc/hostname"} {
		rt := &wasmRecordingRuntime{}
		svc, _, _ := newServiceRuntimeHarness(t, &recordingRuntime{})
		svc.cfg.EnableWasm = true
		svc.admitter = nil
		svc.SetWasmRuntime(rt)

		_, err := svc.createWasmSandbox(scopedCtx("acc-1"), models.CreateSandboxRequest{
			Runtime:   models.RuntimeWasm,
			ModuleRef: ref,
		}, "")
		if err == nil {
			t.Fatalf("scoped createWasmSandbox(module_ref=%q) succeeded, want operator-only rejection", ref)
		}
		if !strings.Contains(err.Error(), "operator") {
			t.Fatalf("err = %q, want an operator-only message", err.Error())
		}
		if rt.createCalls != 0 {
			t.Fatalf("driver Create called %d times before the gate", rt.createCalls)
		}
	}
}

// An unscoped/operator caller still gets the host-path convenience.
func TestCreateWasmSandbox_OperatorAllowsHostPathRef(t *testing.T) {
	rt := &wasmRecordingRuntime{}
	svc, _, _ := newServiceRuntimeHarness(t, &recordingRuntime{})
	svc.cfg.EnableWasm = true
	svc.admitter = nil
	svc.SetWasmRuntime(rt)

	if _, err := svc.createWasmSandbox(context.Background(), models.CreateSandboxRequest{
		Runtime:   models.RuntimeWasm,
		ModuleRef: "/etc/hostname",
	}, ""); err != nil {
		t.Fatalf("operator createWasmSandbox: %v", err)
	}
	if rt.createCalls != 1 {
		t.Fatalf("driver Create calls = %d, want 1", rt.createCalls)
	}
}
