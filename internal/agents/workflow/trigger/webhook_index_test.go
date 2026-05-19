package trigger

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
)

func newTestRouter() *Router {
	return &Router{
		defs:         map[string]workflow.Workflow{},
		queues:       map[string]*Queue{},
		dedups:       map[string]*Dedup{},
		workers:      map[string]context.CancelFunc{},
		index:        map[string][]triggerRef{},
		webhookIndex: map[string]webhookEntry{},
		clock:        time.Now,
	}
}

func wfWithWebhook(id, path, secretRef string) workflow.Workflow {
	return workflow.Workflow{
		ID:      id,
		Enabled: true,
		Triggers: []workflow.Trigger{
			{Type: workflow.TriggerWebhook, Path: path, SecretRef: secretRef},
		},
	}
}

// TestWebhookIndex_ExactMatch verifies a registered path is found O(1).
func TestWebhookIndex_ExactMatch(t *testing.T) {
	r := newTestRouter()
	r.mu.Lock()
	r.reindexLocked(wfWithWebhook("wf1", "/hooks/pay", "sec-pay"))
	r.mu.Unlock()

	secret, found := r.WebhookSecretFor("/hooks/pay")
	if !found {
		t.Fatal("expected to find secret for /hooks/pay")
	}
	if secret != "sec-pay" {
		t.Errorf("expected sec-pay, got %s", secret)
	}
}

// TestWebhookIndex_WildcardFallback verifies empty path registers as "*"
// and matches any incoming path.
func TestWebhookIndex_WildcardFallback(t *testing.T) {
	r := newTestRouter()
	r.mu.Lock()
	r.reindexLocked(wfWithWebhook("wf1", "", "sec-wildcard"))
	r.mu.Unlock()

	secret, found := r.WebhookSecretFor("/any/path/at/all")
	if !found {
		t.Fatal("expected wildcard to match any path")
	}
	if secret != "sec-wildcard" {
		t.Errorf("expected sec-wildcard, got %s", secret)
	}
}

// TestWebhookIndex_ExactBeforeWildcard verifies exact match wins over wildcard.
func TestWebhookIndex_ExactBeforeWildcard(t *testing.T) {
	r := newTestRouter()
	r.mu.Lock()
	r.reindexLocked(wfWithWebhook("wf-specific", "/hooks/pay", "sec-specific"))
	r.reindexLocked(wfWithWebhook("wf-wild", "", "sec-wildcard"))
	r.mu.Unlock()

	secret, found := r.WebhookSecretFor("/hooks/pay")
	if !found {
		t.Fatal("expected to find secret")
	}
	if secret != "sec-specific" {
		t.Errorf("exact match should win; expected sec-specific, got %s", secret)
	}
}

// TestWebhookIndex_MissingPath returns not found for unregistered path.
func TestWebhookIndex_MissingPath(t *testing.T) {
	r := newTestRouter()
	r.mu.Lock()
	r.reindexLocked(wfWithWebhook("wf1", "/hooks/pay", "sec-pay"))
	r.mu.Unlock()

	_, found := r.WebhookSecretFor("/hooks/other")
	if found {
		t.Fatal("expected not found for unregistered path")
	}
}

// TestWebhookIndex_NoSecretRefSkipped verifies triggers without SecretRef
// are not added to webhookIndex.
func TestWebhookIndex_NoSecretRefSkipped(t *testing.T) {
	r := newTestRouter()
	r.mu.Lock()
	r.reindexLocked(wfWithWebhook("wf1", "/hooks/pay", ""))
	r.mu.Unlock()

	_, found := r.WebhookSecretFor("/hooks/pay")
	if found {
		t.Fatal("trigger without SecretRef should not appear in index")
	}
}

// TestWebhookIndex_UnregisterCleansUp verifies removeFromIndexLocked
// removes webhook entries when a workflow is unregistered.
func TestWebhookIndex_UnregisterCleansUp(t *testing.T) {
	r := newTestRouter()
	r.mu.Lock()
	r.reindexLocked(wfWithWebhook("wf1", "/hooks/pay", "sec-pay"))
	r.mu.Unlock()

	r.mu.Lock()
	r.removeFromIndexLocked("wf1")
	r.mu.Unlock()

	_, found := r.WebhookSecretFor("/hooks/pay")
	if found {
		t.Fatal("secret should be gone after unregister")
	}
}

// TestWebhookIndex_MultipleWorkflows verifies each path maps to its own secret.
func TestWebhookIndex_MultipleWorkflows(t *testing.T) {
	r := newTestRouter()
	r.mu.Lock()
	r.reindexLocked(wfWithWebhook("wf1", "/hooks/pay", "sec-pay"))
	r.reindexLocked(wfWithWebhook("wf2", "/hooks/ship", "sec-ship"))
	r.mu.Unlock()

	cases := []struct{ path, want string }{
		{"/hooks/pay", "sec-pay"},
		{"/hooks/ship", "sec-ship"},
	}
	for _, c := range cases {
		secret, found := r.WebhookSecretFor(c.path)
		if !found {
			t.Errorf("expected to find secret for %s", c.path)
			continue
		}
		if secret != c.want {
			t.Errorf("path %s: want %s, got %s", c.path, c.want, secret)
		}
	}
}
