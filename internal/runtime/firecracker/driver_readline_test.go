package firecracker

import (
	"context"
	"strings"
	"testing"
)

// The vsock handshake response is attacker-influenced (guest-controlled). A
// newline-free stream must be rejected at the line cap instead of buffered in
// full by ReadBytes (OOM hardening, same class as readyproto readLine).
func TestVsockHandshake_RejectsOversizedLineWithoutBuffering(t *testing.T) {
	huge := []byte(strings.Repeat("x", 10<<20)) // 10 MiB, no newline
	d := &Driver{vsockDial: &stubVsockDialer{
		conns: []*errVsockConn{{reply: huge}},
	}}
	err := d.vsockHandshake(context.Background(), "/tmp/vsock.sock", 3)
	if err == nil {
		t.Fatal("expected oversized handshake line to fail")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v, want a line-limit error (got an unbounded read path)", err)
	}
}
