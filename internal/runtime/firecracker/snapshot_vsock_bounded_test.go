package firecracker

import (
	"context"
	"strings"
	"testing"
)

// The post_resume ack is guest-controlled. A newline-free stream must be
// rejected at the line cap instead of being buffered in full by
// bufio.Reader.ReadString (OOM hardening; same class as the sibling
// vsockHandshake/readFirecrackerVsockAck bound). The dial deadline bounds the
// read in TIME, not bytes.
func TestSendVsockOpRejectsOversizedAckLine(t *testing.T) {
	huge := []byte(strings.Repeat("x", 1<<20)) // 1 MiB, no newline
	d := &Driver{vsockDial: &stubVsockDialer{
		conns: []*errVsockConn{{reply: huge}},
	}}
	err := d.sendVsockOp(context.Background(), "/tmp/vsock.sock", 3, "post_resume", nil)
	if err == nil {
		t.Fatal("sendVsockOp accepted a newline-free 1MiB ack stream")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v, want a line-limit error", err)
	}
}
