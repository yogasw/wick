package trigger

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

func TestSubscribedNeedsAnEnabledExactTrigger(t *testing.T) {
	r := newTestRouter()
	add := func(id, ch, ev string, enabled bool) {
		w := workflow.Workflow{ID: id, Enabled: enabled, Triggers: []workflow.Trigger{{Type: workflow.TriggerChannel, ChannelName: ch, Event: ev}}}
		r.mu.Lock()
		r.defs[id] = w
		r.reindexLocked(w)
		r.mu.Unlock()
	}
	if r.Subscribed("slack", "reaction") {
		t.Fatal("empty router subscribed")
	}
	add("wf-any", "slack", "", true) // wildcard: does not count
	add("wf-off", "slack", "reaction", false)
	if r.Subscribed("slack", "reaction") {
		t.Fatal("a wildcard or disabled workflow counted")
	}
	add("wf-on", "slack", "reaction", true)
	if !r.Subscribed("slack", "reaction") || r.Subscribed("slack", "reaction_removed") {
		t.Fatal("exact enabled trigger not found, or leaked to another event")
	}
}
