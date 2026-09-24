package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// errOnlyReader fails immediately with err.
type errOnlyReader struct{ err error }

func (e errOnlyReader) Read([]byte) (int, error) { return 0, e.err }

// pumpReaderThrough drives pumpReader server-side over a real websocket and
// returns its verdict.
func pumpReaderThrough(t *testing.T, r io.Reader) error {
	t.Helper()
	errCh := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := up.Upgrade(w, req, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		errCh <- pumpReader(conn, r, streamFramePrefixStdout)
	}))
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	select {
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("pumpReader did not return")
		return nil
	}
}

// Regression: pumpReader had no nil-return path (only return err / return
// werr), so a reader that ended in a clean io.EOF leaked that EOF as an
// "error" and the error contract lied to every caller.
func TestPumpReaderCleanEOFReturnsNil(t *testing.T) {
	if err := pumpReaderThrough(t, strings.NewReader("hello")); err != nil {
		t.Fatalf("pumpReader on clean EOF = %v, want nil", err)
	}
}

func TestPumpReaderReturnsReaderError(t *testing.T) {
	want := errors.New("boom")
	got := pumpReaderThrough(t, errOnlyReader{err: want})
	if !errors.Is(got, want) {
		t.Fatalf("pumpReader on reader error = %v, want %v", got, want)
	}
}
