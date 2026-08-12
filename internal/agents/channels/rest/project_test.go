package rest

import (
	"context"
	"net/http"
	"sync"
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
)

// newProjectProbeChannel builds a REST channel whose sendFn records the
// project values riding on each dispatch ctx, so a test can assert what
// the pool closure would resolve without standing up a pool.
func newProjectProbeChannel(t *testing.T, cfg agentconfig.RestChannelConfig) (*Channel, func() (instance, override string)) {
	t.Helper()
	ch := New(cfg, &fakeAuth{wantToken: "good", userID: "user-1"})
	ch.SetSessionChecker(fakeSessions{exists: true}) // skip the inject send

	var mu sync.Mutex
	var gotInstance, gotOverride string

	ch.SetSendFunc(func(ctx context.Context, sessionID, _, _, role, _ string) error {
		if role == "user" {
			mu.Lock()
			gotInstance = agentchannels.ChannelProject(ctx)
			gotOverride = agentchannels.ProjectOverride(ctx)
			mu.Unlock()
			go func() {
				ch.OnAgentEvent(sessionID, event.AgentEvent{Type: event.TextDelta, Text: "ok"})
				ch.OnAgentEvent(sessionID, event.AgentEvent{Type: event.Done})
			}()
		}
		return nil
	})

	return ch, func() (string, string) {
		mu.Lock()
		defer mu.Unlock()
		return gotInstance, gotOverride
	}
}

// TestDispatchStampsInstanceProject proves a REST instance puts its OWN
// configured project on the dispatch ctx. Several REST instances (one per
// owning user) share a single SendFunc closure, so the closure cannot
// infer the origin — resolving by channel type alone returned an
// arbitrary agent_channels row and collapsed every bot into one project.
func TestDispatchStampsInstanceProject(t *testing.T) {
	stubModels(t, "claude")
	ch, probe := newProjectProbeChannel(t, agentconfig.RestChannelConfig{
		Enabled:   "true",
		ProjectID: "proj-instance",
	})

	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
		"model":    "claude",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
		"user":     "sess-project-1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	instance, override := probe()
	if instance != "proj-instance" {
		t.Errorf("channel project on ctx = %q, want proj-instance", instance)
	}
	if override != "" {
		t.Errorf("override = %q, want empty when the request names no project", override)
	}
}

// TestDispatchRequestProjectOutranksInstance keeps the documented REST
// contract intact: a `project` field in the body overrides the instance
// default for that one call, and both values reach the resolver so it can
// fall back if the named project doesn't exist.
func TestDispatchRequestProjectOutranksInstance(t *testing.T) {
	stubModels(t, "claude")
	ch, probe := newProjectProbeChannel(t, agentconfig.RestChannelConfig{
		Enabled:   "true",
		ProjectID: "proj-instance",
	})

	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", map[string]any{
		"model":    "claude",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
		"user":     "sess-project-2",
		"project":  "proj-request",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	instance, override := probe()
	if override != "proj-request" {
		t.Errorf("override = %q, want proj-request", override)
	}
	if instance != "proj-instance" {
		t.Errorf("channel project = %q, want proj-instance to survive alongside the override", instance)
	}
}
