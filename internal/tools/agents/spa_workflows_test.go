package agents

import (
	"strings"
	"testing"
	"time"

	wf "github.com/yogasw/wick/internal/agents/workflow"
)

// TestRerunEvent confirms a re-run reuses the original run's trigger
// event (payload + routing) but with a fresh timestamp.
func TestRerunEvent(t *testing.T) {
	orig := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	st := wf.RunState{
		Event: wf.Event{
			Type:      "webhook",
			Channel:   "bitbucket",
			TriggerID: "trg-1",
			Payload:   map[string]any{"foo": "bar"},
			At:        orig,
		},
	}
	got := rerunEvent(st, now)
	if got.Type != "webhook" || got.Channel != "bitbucket" || got.TriggerID != "trg-1" {
		t.Fatalf("event identity not preserved: %+v", got)
	}
	if got.Payload["foo"] != "bar" {
		t.Fatalf("payload not preserved: %+v", got.Payload)
	}
	if !got.At.Equal(now) {
		t.Fatalf("At not refreshed: got %v want %v", got.At, now)
	}
}

// TestNormaliseWorkflowBody_RawWorkflowJSON confirms the FE can post
// the Workflow object directly and the normaliser round-trips it back
// to JSON the parser can consume.
func TestNormaliseWorkflowBody_RawWorkflowJSON(t *testing.T) {
	in := []byte(`{"id":"xyz","name":"raw json","enabled":false,"graph":{"entry":"n1","nodes":[],"edges":[]}}`)
	out, err := normaliseWorkflowBody("xyz", in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `"name":"raw json"`) {
		t.Errorf("workflow JSON not round-tripped; got: %s", s)
	}
	if !strings.Contains(s, `"id":"xyz"`) {
		t.Errorf("id missing in round-tripped JSON; got: %s", s)
	}
}

// TestNormaliseWorkflowBody_EmptyRejected — the parser needs a body.
func TestNormaliseWorkflowBody_EmptyRejected(t *testing.T) {
	_, err := normaliseWorkflowBody("x", []byte(""))
	if err == nil {
		t.Fatal("expected error for empty body")
	}
	_, err = normaliseWorkflowBody("x", []byte("   "))
	if err == nil {
		t.Fatal("expected error for whitespace body")
	}
}

// TestNormaliseWorkflowBody_IDFallback fills the ID from the URL path
// when the FE didn't include one in the workflow object.
func TestNormaliseWorkflowBody_IDFallback(t *testing.T) {
	in := []byte(`{"name":"missing id","enabled":true,"graph":{"entry":"n1","nodes":[],"edges":[]}}`)
	out, err := normaliseWorkflowBody("backfilled", in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), `"id":"backfilled"`) {
		t.Errorf("ID not back-filled from path; got: %s", string(out))
	}
}

// TestNormaliseWorkflowBody_InvalidJSON returns a useful error rather
// than panicking.
func TestNormaliseWorkflowBody_InvalidJSON(t *testing.T) {
	_, err := normaliseWorkflowBody("x", []byte(`not json`))
	if err == nil {
		t.Fatal("expected error for non-JSON body")
	}
}
