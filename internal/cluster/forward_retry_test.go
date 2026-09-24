package cluster

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// countingTransport counts RoundTripper attempts (including failed dials) so
// the bounded-retry test can assert how many times the forwarder actually
// tried a hard-down leader.
type countingTransport struct {
	base     http.RoundTripper
	attempts atomic.Int32
}

func (t *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.attempts.Add(1)
	return t.base.RoundTrip(r)
}

// TestForwardApplyToLeaderRetriesOn503ThenSucceeds pins the comment/behavior
// mismatch in forwardApplyToLeader: the docs promise the forwarder retries a
// stale-leader 503 against a refreshed leader, but the code made exactly one
// attempt. A fake leader that503s once (leadership moved) and then accepts
// must end with a successful forward.
func TestForwardApplyToLeaderRetriesOn503ThenSucceeds(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "not leader", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, cleanup := newTestClusterWithAPI(t, "n-retry", true, nil, srv.URL)
	defer cleanup()
	waitForLeader(t, c, 5*time.Second)

	payload := []byte("cluster-test-forward-payload")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.forwardApplyToLeader(ctx, payload); err != nil {
		t.Fatalf("forwardApplyToLeader after one transient 503: %v (want success)", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("leader saw %d requests, want 2 (one 503 + one success)", got)
	}
}

// TestForwardApplyToLeaderHardDownBounded pins that a hard-down leader fails
// after a bounded number of attempts — not one-and-done (comment promised
// retries) and not forever (a wedged forwarder would stall every write).
func TestForwardApplyToLeaderHardDownBounded(t *testing.T) {
	// Reserve then release a port so the first dial is connection-refused.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := l.Addr().String()
	_ = l.Close()

	c, cleanup := newTestClusterWithAPI(t, "n-dead", true, nil, "http://"+deadAddr)
	defer cleanup()
	waitForLeader(t, c, 5*time.Second)

	transport := &countingTransport{base: http.DefaultTransport}
	c.httpClient = &http.Client{Transport: transport, Timeout: 2 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = c.forwardApplyToLeader(ctx, []byte("payload"))
	if err == nil {
		t.Fatal("forwardApplyToLeader to a hard-down leader succeeded, want bounded failure")
	}
	if got := transport.attempts.Load(); got != int32(forwardApplyMaxAttempts) {
		t.Fatalf("attempts = %d, want %d (bounded retry)", got, forwardApplyMaxAttempts)
	}
}
