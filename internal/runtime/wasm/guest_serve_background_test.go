package wasm

import (
	"context"
	"testing"
	"time"
)

// waitBackgroundInvoke waits for the serve invoke, which is fired from a
// goroutine, and returns the export it carried.
func waitBackgroundInvoke(t *testing.T, client *recordingWorkerClient) string {
	t.Helper()
	select {
	case export := <-client.backgroundInvokeCh:
		return export
	case <-time.After(2 * time.Second):
		t.Fatal("serve path never issued a background (long-lived) invoke")
		return ""
	}
}

// The exposed-HTTP serve is the long-lived guest entry: it must be invoked as a
// background call, or the worker bounds it with the per-request wall timeout and
// kills the server at that budget.
func TestSyncGuestListenPortInvokesServeAsBackground(t *testing.T) {
	d := New(Config{ModulesDir: t.TempDir()}, nil)
	d.waitListenReady = func(string, int) error { return nil }
	client := &recordingWorkerClient{resolvedPort: 19081, backgroundInvokeCh: make(chan string, 4)}
	inst := &sandboxInstance{sandboxID: "sb-serve-bg", entryExport: "_start"}

	if err := d.syncGuestListenPort(context.Background(), inst, client, 0); err != nil {
		t.Fatalf("syncGuestListenPort: %v", err)
	}
	if got := waitBackgroundInvoke(t, client); got != "_start" {
		t.Fatalf("background serve invoke export = %q, want _start", got)
	}
}

// startGuestEntryAsync is the other serve site: it runs the entry export as the
// long-lived guest entry, so it uses the background invoke too.
func TestStartGuestEntryAsyncInvokesBackground(t *testing.T) {
	d := New(Config{ModulesDir: t.TempDir()}, nil)
	client := &recordingWorkerClient{backgroundInvokeCh: make(chan string, 4)}
	inst := &sandboxInstance{sandboxID: "sb-entry-bg", entryExport: "serve"}
	inst.bumpRunGeneration()

	d.startGuestEntryAsync(inst, client)
	if got := waitBackgroundInvoke(t, client); got != "serve" {
		t.Fatalf("background entry invoke export = %q, want serve", got)
	}
}
