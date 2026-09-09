package mounts

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aerol-ai/microvm/pkg/mounts/adapters"
)

// A mount crash under a running VM is not healable in place: the container's
// 9p/bind channel to the dead FUSE vfsmount stays broken (persistent EIO, prod
// 2026-09-09) no matter how many times the host re-spawns the tool on the same
// path. The manager must therefore tell the service about every crash so the
// sandbox itself can be restarted (fresh container, fresh binds).

func TestManagerNotifiesOnMountCrash(t *testing.T) {
	type crashEvent struct {
		sandboxID string
		index     string
	}
	events := make(chan crashEvent, 4)
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m, err := New(logger, Config{
		RootDir:     root,
		CredDir:     t.TempDir(),
		WaitTimeout: 200 * time.Millisecond,
		OnMountCrash: func(sandboxID string, index int) {
			events <- crashEvent{sandboxID, strconv.Itoa(index)}
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(m.Close)

	hostPath := filepath.Join(root, "sb-hook", "0")
	if err := os.MkdirAll(hostPath, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	state := &mountState{
		sandboxID:  "sb-hook",
		index:      0,
		hostPath:   hostPath,
		plan:       adapters.Plan{Argv: []string{"sleep", "0.5"}},
		cmd:        cmd,
		startedAt:  time.Now().UTC(),
		supervised: true,
	}
	m.mu.Lock()
	m.state["sb-hook"] = []*mountState{state}
	m.mu.Unlock()

	stubProbe(t, func(string, time.Duration) error { return nil })
	m.superviseExit(state)

	select {
	case ev := <-events:
		if ev.sandboxID != "sb-hook" || ev.index != "0" {
			t.Fatalf("crash event = %+v, want sb-hook/0", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnMountCrash never fired on mount crash")
	}
}
