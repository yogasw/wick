package template

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// TestRenderMissingKeyDoesNotFail reproduces the github_pr webhook run
// that failed under the old strict policy: node check_action used
// {{.Event.Payload.action}} but the test payload was {"test":"test"} —
// no "action" key. The whole point of the fix: the run must NOT fail.
// Under missingkey=zero the lookup yields the zero value of the map's
// element type. For map[string]any that zero value is nil, which Go's
// text/template prints as the literal "<no value>" — so authors who want
// a clean empty string should wrap optional fields in `default`
// (see TestRenderMissingKeyWithDefault). The key assertion here is that
// err is nil — no more "map has no entry for key".
func TestRenderMissingKeyDoesNotFail(t *testing.T) {
	ctx := workflow.RenderCtx{
		Event: workflow.Event{
			Type:    "webhook",
			Payload: map[string]any{"test": "test"}, // exactly the user's run body
		},
	}

	got, err := Render("action={{.Event.Payload.action}}", ctx)
	if err != nil {
		t.Fatalf("missing key should not error under missingkey=zero, got: %v", err)
	}
	if got != "action=<no value>" {
		t.Fatalf("missing key on map[string]any renders <no value>, got %q", got)
	}
}

// TestRenderPresentKeyStillResolves guards that the lenient policy didn't
// break the normal case — a real GitHub PR webhook carries "action".
func TestRenderPresentKeyStillResolves(t *testing.T) {
	ctx := workflow.RenderCtx{
		Event: workflow.Event{
			Type:    "webhook",
			Payload: map[string]any{"action": "opened"},
		},
	}

	got, err := Render("action={{.Event.Payload.action}}", ctx)
	if err != nil {
		t.Fatalf("present key render errored: %v", err)
	}
	if got != "action=opened" {
		t.Fatalf("present key render = %q, want action=opened", got)
	}
}

// TestRenderMissingKeyWithDefault documents the explicit-default form an
// author can still use — `default` coerces the empty zero-value to a
// fallback, which is the clean way to express an optional field.
func TestRenderMissingKeyWithDefault(t *testing.T) {
	ctx := workflow.RenderCtx{
		Event: workflow.Event{Payload: map[string]any{"test": "test"}},
	}
	got, err := Render(`{{ .Event.Payload.action | default "none" }}`, ctx)
	if err != nil {
		t.Fatalf("default render errored: %v", err)
	}
	if got != "none" {
		t.Fatalf("default render = %q, want none", got)
	}
}
