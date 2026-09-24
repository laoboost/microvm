package toolhost

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/cmd/toolboxd/sessions"
	"github.com/gorilla/websocket"
)

func newTestSessionManager(t *testing.T) *sessions.Manager {
	t.Helper()
	mgr, err := sessions.New(slog.Default(), sessions.Config{
		SandboxID:    "sb-failclosed",
		RecordingDir: t.TempDir(),
		BufferBytes:  64 * 1024,
	})
	if err != nil {
		t.Fatalf("sessions.New: %v", err)
	}
	t.Cleanup(func() { mgr.Close() })
	return mgr
}

// requireHostExec enables the live host-exec routes for a test. The gate is off
// by default in production (fail-closed), but the enabled code path must still
// be exercised, so this flips the injectable default for the duration of the
// test rather than skipping it.
func requireHostExec(t *testing.T) {
	t.Helper()
	EnableHostExecForTest(t)
}

// The wasm runtime has no container/jail: any route that spawns a host
// process hands host root to the toolbox token. These tests pin the
// fail-closed contract (501) for every such surface.

func TestExecStreamFailClosed(t *testing.T) {
	work := t.TempDir()
	side := filepath.Join(work, "execstream-spawned")
	h := &Host{workDir: work}
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	conn, resp, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(srv.URL, "http")+"/process/exec/stream", nil)
	if err == nil {
		defer conn.Close()
		_ = conn.WriteJSON(execStreamStartMsg{Command: "touch " + side})
		time.Sleep(500 * time.Millisecond)
	}
	if _, statErr := os.Stat(side); statErr == nil {
		t.Fatalf("host process spawned: side-effect file %s exists", side)
	}
	if err == nil {
		t.Fatal("expected exec-stream upgrade to fail closed with 501, got live websocket")
	}
	if resp == nil || resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501 Not Implemented, got resp=%v err=%v", resp, err)
	}
}

func TestCodeRunFailClosed(t *testing.T) {
	work := t.TempDir()
	side := filepath.Join(work, "coderun-spawned")
	h := &Host{workDir: work}
	handler := h.Handler()

	payload, _ := json.Marshal(codeRunRequest{
		Language: "bash",
		Code:     "touch " + side,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/process/code-run", bytes.NewReader(payload))
	handler.ServeHTTP(rec, req)

	if _, statErr := os.Stat(side); statErr == nil {
		t.Fatalf("host process spawned: side-effect file %s exists", side)
	}
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 Not Implemented, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSessionsCreateFailClosed(t *testing.T) {
	work := t.TempDir()
	side := filepath.Join(work, "sessions-spawned")
	mgr := newTestSessionManager(t)
	h := New(Config{WorkDir: work, Sessions: mgr})
	handler := h.Handler()

	payload, _ := json.Marshal(map[string]any{"command": "touch " + side})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewReader(payload))
	handler.ServeHTTP(rec, req)

	if _, statErr := os.Stat(side); statErr == nil {
		t.Fatalf("host process spawned: side-effect file %s exists", side)
	}
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 Not Implemented, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDaytonaSessionExecFailClosed(t *testing.T) {
	work := t.TempDir()
	side := filepath.Join(work, "daytona-spawned")
	mgr := newTestSessionManager(t)
	h := New(Config{WorkDir: work, Sessions: mgr})
	handler := h.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/process/session", strings.NewReader(`{"sessionId":"failclosed"}`))
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusCreated || rec.Code == http.StatusOK {
		t.Fatalf("daytona session create spawned a host shell: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/process/session/failclosed/exec",
		strings.NewReader(`{"command":"touch `+side+`"}`))
	handler.ServeHTTP(rec, req)

	if _, statErr := os.Stat(side); statErr == nil {
		t.Fatalf("host process spawned: side-effect file %s exists", side)
	}
}
