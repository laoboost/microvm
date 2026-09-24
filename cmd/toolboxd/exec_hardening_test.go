package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newExecTestServer() *server {
	return &server{
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		sandboxID:    "sb-exec-hard",
		authToken:    "token-123",
		allowedPorts: map[int]struct{}{},
		envd:         newEnvdCompat(),
	}
}

// A multi-MiB JSON body must be rejected with413 instead of buffered and
// decoded (memory DoS on the shared bridge).
func TestHandleExecRejectsOversizedBody(t *testing.T) {
	s := newExecTestServer()
	body := `{"command":"true","env":{"PAD":"` + strings.Repeat("x", 2<<20) + `"}}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/process/execute", strings.NewReader(body))
	s.handleExec(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized exec body: status = %d, want 413", rr.Code)
	}
}

func TestHandleSetAllowedPortsRejectsOversizedBody(t *testing.T) {
	s := newExecTestServer()
	body := `{"ports":[1` + strings.Repeat(",1", 1<<20) + `]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/allowed-ports", strings.NewReader(body))
	s.handleSetAllowedPorts(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized allowed-ports body: status = %d, want 413", rr.Code)
	}
}

func TestEnvdJSONHandlerRejectsOversizedBody(t *testing.T) {
	s := newExecTestServer()
	body := `{"process":{"tag":"` + strings.Repeat("x", 2<<20) + `"}}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/envd/process.Process/Update", strings.NewReader(body))
	s.handleEnvdProcessUpdate(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized envd body: status = %d, want 413 body=%s", rr.Code, rr.Body.String())
	}
}

// Caller-supplied env keys that exec_stream.go already rejects (LD_*,
// BASH_ENV, PATH) must not reach handleExec children either.
func TestHandleExecFiltersPrivilegedEnv(t *testing.T) {
	s := newExecTestServer()
	body := `{"command":"echo LD=$LD_PRELOAD BASH=$BASH_ENV SAFE=$SAFE_VAR","env":{"LD_PRELOAD":"/evil.so","BASH_ENV":"/evil.sh","SAFE_VAR":"ok"}}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/process/execute", strings.NewReader(body))
	s.handleExec(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("exec status = %d body=%s", rr.Code, rr.Body.String())
	}
	var res struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode exec result: %v", err)
	}
	if strings.Contains(res.Stdout, "/evil.so") || strings.Contains(res.Stdout, "/evil.sh") {
		t.Fatalf("privileged env leaked into child: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "SAFE=ok") {
		t.Fatalf("ordinary env was dropped: %q", res.Stdout)
	}
}

// Captured stdout/stderr must be capped: a chatty command can't balloon
// toolboxd memory. Overflow is truncated with a marker, and the pipe is still
// drained so the child never blocks.
func TestHandleExecCapsCapturedOutput(t *testing.T) {
	s := newExecTestServer()
	body := `{"command":"yes x | head -c 2097152"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/process/execute", strings.NewReader(body))
	s.handleExec(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("exec status = %d body=%s", rr.Code, rr.Body.String())
	}
	var res struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode exec result: %v", err)
	}
	if len(res.Stdout) > (1<<20)+64 {
		t.Fatalf("captured stdout len = %d, want <= %d+marker", len(res.Stdout), 1<<20)
	}
	if !strings.Contains(res.Stdout, "[output truncated]") {
		t.Fatalf("captured stdout missing truncation marker (len=%d)", len(res.Stdout))
	}
}

// /process/code-run merges caller env too; same filtering contract.
func TestDaytonaCodeRunFiltersPrivilegedEnv(t *testing.T) {
	s := newExecTestServer()
	reqBody, _ := json.Marshal(map[string]any{
		"code":     "echo LD=$LD_PRELOAD SAFE=$SAFE_VAR",
		"language": "sh",
		"envs":     map[string]string{"LD_PRELOAD": "/evil.so", "SAFE_VAR": "ok"},
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/process/code-run", bytes.NewReader(reqBody))
	s.handleDaytonaCodeRun(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code-run status = %d body=%s", rr.Code, rr.Body.String())
	}
	var res struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode code-run result: %v", err)
	}
	if strings.Contains(res.Result, "/evil.so") {
		t.Fatalf("privileged env leaked into code-run child: %q", res.Result)
	}
	if !strings.Contains(res.Result, "SAFE=ok") {
		t.Fatalf("ordinary env was dropped: %q", res.Result)
	}
}

func TestDaytonaCodeRunRejectsOversizedBody(t *testing.T) {
	s := newExecTestServer()
	body := `{"code":"` + strings.Repeat("x", 2<<20) + `","language":"sh"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/process/code-run", strings.NewReader(body))
	s.handleDaytonaCodeRun(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized code-run body: status = %d, want 413", rr.Code)
	}
}
