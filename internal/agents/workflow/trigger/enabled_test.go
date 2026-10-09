package trigger

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// A disabled workflow must not keep a cron schedule: the cron fires through
// RunNow, which deliberately skips the Enabled check.
func TestCronSync_DisabledWorkflowHasNoSchedule(t *testing.T) {
	c := NewCronScheduler(nil)
	w := workflow.Workflow{
		Enabled:  true,
		Triggers: []workflow.Trigger{{Type: workflow.TriggerCron, Schedule: "*/5 * * * *", EntryNode: "n"}},
	}
	c.Sync("wf", w)
	if len(c.cron["wf"]) != 1 {
		t.Fatalf("enabled workflow should be scheduled, got %d entries", len(c.cron["wf"]))
	}
	w.Enabled = false
	c.Sync("wf", w)
	if len(c.cron["wf"]) != 0 {
		t.Fatalf("disabled workflow must drop its schedule, got %d entries", len(c.cron["wf"]))
	}
}
