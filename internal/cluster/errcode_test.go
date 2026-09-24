package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLeaderForwardApplyPreservesSentinelIdentity pins the C6f bug at the
// wire boundary: an FSM sentinel (e.g. ErrReservationConflict) that crossed a
// node boundary must still satisfy errors.Is on the receiving side. Plain
// string matching on err.Error() is what this replaces — a machine-readable
// code in the internal error envelope keeps identity across hops.
func TestLeaderForwardApplyPreservesSentinelIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"cluster: sandbox already placed or reserved","code":"reservation_conflict"}`)
	}))
	defer srv.Close()

	c := &Cluster{}
	err := c.doLeaderApply(context.Background(), srv.Client(), srv.URL+"/internal/apply", []byte("payload"))
	if !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("doLeaderApply lost sentinel identity across the wire: %v", err)
	}
}

// TestInternalErrorRoundTripPreservesSentinelIdentity is the review's C6f
// acceptance test: every sentinel must survive write (error envelope) + read
// (classify) with errors.Is identity intact, including under wrapping like
// the real "cluster: fsm apply: …" envelope.
func TestInternalErrorRoundTripPreservesSentinelIdentity(t *testing.T) {
	for _, e := range sentinelErrorCodes {
		t.Run(e.code, func(t *testing.T) {
			wrapped := fmt.Errorf("cluster: fsm apply: %w", e.err)
			rec := httptest.NewRecorder()
			writeInternalError(rec, http.StatusInternalServerError, wrapped)
			if body := rec.Body.String(); !strings.Contains(body, `"code":"`+e.code+`"`) {
				t.Fatalf("envelope body %q missing code %q", body, e.code)
			}
			got := classifyInternalError(rec.Code, rec.Body.Bytes())
			if !errors.Is(got, e.err) {
				t.Fatalf("round trip lost identity for %s: got %v", e.code, got)
			}
		})
	}
}

// TestClassifyInternalErrorCodeBeatsMessage pins that the machine-readable
// code is authoritative: a tampered/confused message that string-matches a
// different sentinel must not override the code. The string-match fallback
// only applies to code-less bodies.
func TestClassifyInternalErrorCodeBeatsMessage(t *testing.T) {
	got := classifyInternalErrorPayload(http.StatusInternalServerError, "reservation_conflict", "cluster: not raft leader")
	if !errors.Is(got, ErrReservationConflict) || errors.Is(got, ErrNotLeader) {
		t.Fatalf("code must beat message: got %v", got)
	}

	got = classifyInternalErrorPayload(http.StatusInternalServerError, "", "cluster: sandbox already placed or reserved")
	if !errors.Is(got, ErrReservationConflict) {
		t.Fatalf("string-match fallback must classify code-less bodies: got %v", got)
	}

	if got := classifyInternalErrorPayload(http.StatusInternalServerError, "", "unmapped gibberish"); got != nil {
		t.Fatalf("unknown body must classify as nil, got %v", got)
	}
	if got := classifyInternalErrorPayload(http.StatusInternalServerError, "unknown_future_code", "x"); got != nil {
		t.Fatalf("unknown code without a message match must classify as nil, got %v", got)
	}
}

// TestWriteInternalErrorOmitsCodeForUnsentinelErrors pins the envelope shape
// for non-sentinel errors: plain {"error": …} with no code, so readers don't
// mis-classify arbitrary failures.
func TestWriteInternalErrorOmitsCodeForUnsentinelErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	writeInternalError(rec, http.StatusInternalServerError, errors.New("something else entirely"))
	var env internalErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("body is not the error envelope: %v (%q)", err, rec.Body.String())
	}
	if env.Code != "" || env.Error != "something else entirely" {
		t.Fatalf("envelope = %+v, want error text with empty code", env)
	}
}
