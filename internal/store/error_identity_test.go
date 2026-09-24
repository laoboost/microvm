package store

import (
	"errors"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// opaqueWrap hides the wrapped error's text so only errors.Is (via Unwrap)
// can see through it — the shape a machine-readable error code decodes into
// when the original sentinel rides behind a transport wrapper.
type opaqueWrap struct{ err error }

func (e opaqueWrap) Error() string {
	return "constraint failed: UNIQUE constraint failed: sandboxes(name)"
}
func (e opaqueWrap) Unwrap() error { return e.err }

// TestSandboxConflictClassificationUsesErrorIdentity pins the error-identity
// fix at the store boundary: conflict classification must use errors.Is
// against the sentinel (so wrapped/transported sentinels still classify), not
// substring sniffing of the message (which false-positives on lookalike text).
func TestSandboxConflictClassificationUsesErrorIdentity(t *testing.T) {
	if !isSandboxNameConflict(opaqueWrap{ErrSandboxNameConflict}, "n") {
		t.Fatal("isSandboxNameConflict missed a wrapped ErrSandboxNameConflict")
	}
	if isSandboxNameConflict(errors.New("sandboxes.name: sandbox name already in use"), "n") {
		t.Fatal("isSandboxNameConflict matched on message text alone")
	}
	if !isSandboxIDConflict(opaqueWrap{models.ErrSandboxExists}, "id-1") {
		t.Fatal("isSandboxIDConflict missed a wrapped models.ErrSandboxExists")
	}
	if isSandboxIDConflict(errors.New("hint: sandbox already exists"), "id-1") {
		t.Fatal("isSandboxIDConflict matched on message text alone")
	}
	// Raw SQLite unique-constraint errors keep classifying via the driver
	// error shape (covered elsewhere); a plain unrelated error stays false.
	if isSandboxNameConflict(errors.New("disk I/O error"), "n") {
		t.Fatal("unrelated error classified as name conflict")
	}
}
