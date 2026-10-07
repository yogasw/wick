package api

import "testing"

// The navbar's version block polls /boot-status to decide whether to show
// "reloading now", so these two keys are a contract with the UI, not an
// implementation detail: rename one and the row silently never appears
// again — the failure mode is nothing happening, which nobody notices.
func TestBootStatusReportsHandoverState(t *testing.T) {
	body := bootStatusPayload(true, "")
	for _, key := range []string{"handover", "draining"} {
		v, ok := body[key]
		if !ok {
			t.Fatalf("/boot-status has no %q — the navbar cannot tell a reload is running", key)
		}
		busy, ok := v.(bool)
		if !ok {
			t.Fatalf("%q = %T, want a bool the page can test directly", key, v)
		}
		// A process nobody asked to hand over is not handing over.
		if busy {
			t.Errorf("%q = true in a process with no handover in flight", key)
		}
	}
}
