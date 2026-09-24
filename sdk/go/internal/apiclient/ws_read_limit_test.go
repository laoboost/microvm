package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

// oversizedWSMessage is 32 MiB + 1 KiB — just past the 32 MiB read limit the
// SDK must enforce on WebSocket connections.
var oversizedWSMessage = append([]byte{streamPrefixStdout}, make([]byte, 32<<20+1024)...)

// TestExecStream_OversizedMessageIsRejected pins the WebSocket read limit on
// exec streams: a peer message beyond 32 MiB must fail the stream rather than
// being buffered and delivered without bound.
func TestExecStream_OversizedMessageIsRejected(t *testing.T) {
	var upgrader websocket.Upgrader
	delivered := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var start map[string]any
		if err := conn.ReadJSON(&start); err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.BinaryMessage, oversizedWSMessage)
		_ = conn.WriteJSON(map[string]any{"type": "exit", "code": 0})
	}))
	defer server.Close()

	client := NewClient(server.URL, ClientOptions{PATToken: "tok", HTTPClient: server.Client()})
	handle, err := client.ExecStream(context.Background(), "sb-1", ExecStreamOptions{
		Command: "bash",
		OnStdout: func(chunk []byte) {
			delivered++
		},
	})
	if err != nil {
		t.Fatalf("ExecStream: %v", err)
	}

	_, err = handle.Wait()
	if err == nil {
		t.Errorf("oversized message was accepted (delivered=%d); want the stream to fail on the 32MiB read limit", delivered)
	}
	if delivered != 0 {
		t.Errorf("oversized payload delivered to OnStdout %d times; want 0", delivered)
	}
}

// TestAttachSession_OversizedMessageIsRejected pins the same WebSocket read
// limit on session attach streams.
func TestAttachSession_OversizedMessageIsRejected(t *testing.T) {
	var upgrader websocket.Upgrader
	delivered := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.BinaryMessage, oversizedWSMessage)
		_ = conn.WriteJSON(map[string]any{"type": "exit", "code": 0})
	}))
	defer server.Close()

	client := NewClient(server.URL, ClientOptions{PATToken: "tok", HTTPClient: server.Client()})
	handle, err := client.AttachSession(context.Background(), "sb-1", "ses-1", SessionAttachOptions{
		OnStdout: func(chunk []byte) {
			delivered++
		},
	})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	_, _, err = handle.Wait()
	if err == nil {
		t.Errorf("oversized message was accepted (delivered=%d); want the stream to fail on the 32MiB read limit", delivered)
	}
	if delivered != 0 {
		t.Errorf("oversized payload delivered to OnStdout %d times; want 0", delivered)
	}
}
