package isolate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// TestInvokeHTTPConcurrentWithGroupTeardownHasNoRace pins F2g: InvokeHTTP read
// rec.groupKey after releasing d.mu, while markGroupMembersStopped writes it
// under the lock. Run with -race.
func TestInvokeHTTPConcurrentWithGroupTeardownHasNoRace(t *testing.T) {
	d := newCreateDriver(t, GroupPerTenant, &fakeSupervisor{})
	ctx := context.Background()
	if _, err := d.Create(ctx, models.CreateSandboxRequest{ModuleRef: "a.js", TenantID: "acme"}, "sb-1", "", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	d.mu.Lock()
	groupKey := d.byID["sb-1"].groupKey
	d.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			req := httptest.NewRequest(http.MethodGet, "http://isolate/", nil)
			resp, err := d.InvokeHTTP(ctx, "sb-1", req)
			if err == nil && resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			d.markGroupMembersStopped(groupKey)
			// Re-pin the key under the lock so the next iteration's teardown has
			// something to clear (the real reaper reloads a fresh group each
			// cycle); this keeps the write/read pair live across iterations.
			d.mu.Lock()
			if rec := d.byID["sb-1"]; rec != nil {
				rec.groupKey = groupKey
			}
			d.mu.Unlock()
		}
	}()
	wg.Wait()
}

// TestGuestHTTPProxyConcurrentWithGroupTeardownHasNoRace is the guestHTTPProxy
// half of F2g: same unlocked rec.groupKey read.
func TestGuestHTTPProxyConcurrentWithGroupTeardownHasNoRace(t *testing.T) {
	d := newCreateDriver(t, GroupPerTenant, &fakeSupervisor{})
	ctx := context.Background()
	if _, err := d.Create(ctx, models.CreateSandboxRequest{ModuleRef: "a.js", TenantID: "acme"}, "sb-1", "", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	d.mu.Lock()
	groupKey := d.byID["sb-1"].groupKey
	d.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "http://isolate/", nil)
			_ = d.guestHTTPProxy("sb-1", 0, rec, req)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			d.markGroupMembersStopped(groupKey)
			// Re-pin the key under the lock so the next iteration's teardown has
			// something to clear (the real reaper reloads a fresh group each
			// cycle); this keeps the write/read pair live across iterations.
			d.mu.Lock()
			if rec := d.byID["sb-1"]; rec != nil {
				rec.groupKey = groupKey
			}
			d.mu.Unlock()
		}
	}()
	wg.Wait()
}
