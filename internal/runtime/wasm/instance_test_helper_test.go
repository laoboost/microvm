package wasm

import "fmt"

// instance returns the live driver record for a sandbox id. Test-only: it lives
// in a _test.go file so production code cannot read mutable sandboxInstance
// fields outside d.mu. Tests that inspect the record are single-goroutine;
// production readers must use snapshotInstance / snapshotOfLocked.
func (d *Driver) instance(sandboxID string) (*sandboxInstance, error) {
	d.mu.Lock()
	inst := d.byID[sandboxID]
	d.mu.Unlock()
	if inst == nil {
		return nil, fmt.Errorf("wasm sandbox %q not found", sandboxID)
	}
	return inst, nil
}
