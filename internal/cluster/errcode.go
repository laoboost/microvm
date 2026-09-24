package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Machine-readable error codes for cross-node error identity (C6f). The
// cluster-internal API error envelope is {"error": "<message>", "code":
// "<code>"} so a receiving node restores the exact sentinel with errors.Is
// instead of sniffing message text (which breaks under wrapping, truncation,
// and localization of the transport layer). Peers that only send plain text
// or a code-less {"error"} body (older nodes, the public fallback path) still
// classify via a string-match fallback.

// internalErrorEnvelope is the wire shape of every cluster-internal error
// response. Code is omitted when the error is not one of the known sentinels.
type internalErrorEnvelope struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// sentinelErrorCodes maps every cluster sentinel that can cross a node
// boundary to a stable wire code. The code strings are part of the internal
// API contract — never rename them without a compatibility fallback.
var sentinelErrorCodes = []struct {
	err  error
	code string
}{
	{ErrNotLeader, "not_leader"},
	{ErrCreateBackpressure, "create_backpressure"},
	{ErrCapacityExceeded, "capacity_exceeded"},
	{ErrNoPlacementTarget, "no_placement_target"},
	{ErrUnknownMember, "unknown_member"},
	{ErrMemberStillAlive, "member_still_alive"},
	{ErrLastVoter, "last_voter"},
	{ErrSelfRemoval, "self_removal"},
	{ErrLeaderRemoval, "leader_removal"},
	{ErrReservationConflict, "reservation_conflict"},
	{ErrNameConflict, "name_conflict"},
	{ErrCustomHostnameConflict, "custom_hostname_conflict"},
	{ErrUnknownSandbox, "unknown_sandbox"},
	{ErrOrphaned, "orphaned"},
	{ErrOrphanClaimConflict, "orphan_claim_conflict"},
	{ErrHostPortReserved, "host_port_reserved"},
	{ErrInvalidTopology, "invalid_topology"},
	{ErrUnknownVolume, "unknown_volume"},
	{ErrVolumeInUse, "volume_in_use"},
	{ErrVolumeQuotaExceeded, "volume_quota_exceeded"},
}

// errorCodeFor returns the wire code for err's sentinel identity (walking
// wraps), or "" when err is not one of the known sentinels.
func errorCodeFor(err error) string {
	for _, e := range sentinelErrorCodes {
		if errors.Is(err, e.err) {
			return e.code
		}
	}
	return ""
}

// sentinelForCode returns the sentinel for a wire code, or nil when unknown.
func sentinelForCode(code string) error {
	for _, e := range sentinelErrorCodes {
		if e.code == code {
			return e.err
		}
	}
	return nil
}

// writeInternalError writes the cluster-internal JSON error envelope with the
// sentinel-derived code, so the receiving node can restore error identity.
func writeInternalError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(internalErrorEnvelope{Error: err.Error(), Code: errorCodeFor(err)})
}

// classifyInternalError reconstructs the sentinel for a cross-node failure
// body. Classification is code-first (identity) with a string-match fallback
// for plain-text and code-less bodies from older peers. Returns nil when the
// body maps to no known sentinel — the caller then applies its own
// status-based handling.
func classifyInternalError(status int, body []byte) error {
	message := strings.TrimSpace(string(body))
	code := ""
	var env internalErrorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && (env.Error != "" || env.Code != "") {
		if env.Error != "" {
			message = env.Error
		}
		code = env.Code
	}
	return classifyInternalErrorPayload(status, code, message)
}

// classifyInternalErrorPayload is classifyInternalError for an already-parsed
// (code, message) pair. Split out so tests can pin the code-vs-message
// precedence directly.
func classifyInternalErrorPayload(status int, code, message string) error {
	if code != "" {
		if s := sentinelForCode(code); s != nil {
			if message == "" || message == s.Error() {
				return s
			}
			return fmt.Errorf("%w: %s", s, message)
		}
	}
	// String-match fallback for legacy peers and plain-text bodies.
	if strings.Contains(message, ErrNotLeader.Error()) || strings.Contains(message, "not leader") {
		return ErrNotLeader
	}
	for _, e := range sentinelErrorCodes {
		if e.err == ErrNotLeader {
			continue
		}
		if strings.Contains(message, e.err.Error()) {
			if message == e.err.Error() {
				return e.err
			}
			return fmt.Errorf("%w: %s", e.err, message)
		}
	}
	_ = status // status-scoped quirks stay at the call sites
	return nil
}
