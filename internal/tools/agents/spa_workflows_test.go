package agents

import (
	"strings"
	"testing"
)

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
