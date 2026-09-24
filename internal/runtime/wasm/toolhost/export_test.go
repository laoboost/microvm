package toolhost

import "testing"

// EnableHostExecForTest flips the process-wide host-exec default on for one
// test and restores it when the test ends. Production never calls this, so the
// routes stay fail-closed there; the helper lets the enabled-path tests run
// alongside the fail-closed ones instead of being permanently skipped.
func EnableHostExecForTest(t *testing.T) {
	t.Helper()
	hostExecDefault = true
	t.Cleanup(func() { hostExecDefault = false })
}
