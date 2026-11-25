package trigger

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// A process that handed trigger intake over keeps no timer, and a workflow
// edited afterwards arms none: the successor fires them.
func TestScheduleAtStopDisarmsAndStaysStopped(t *testing.T) {
	s := NewScheduleAtScheduler(nil)
	wf := workflow.Workflow{Enabled: true, Triggers: []workflow.Trigger{{
		ID: "t1", Type: workflow.TriggerScheduleAt, At: time.Now().Add(time.Hour),
	}}}
	s.Sync("wf-a", wf)
	if len(s.timers) != 1 {
		t.Fatalf("armed %d timers before Stop, want 1", len(s.timers))
	}
	s.Stop()
	if len(s.timers) != 0 {
		t.Fatalf("%d timers left after Stop", len(s.timers))
	}
	s.Sync("wf-a", wf)
	if len(s.timers) != 0 {
		t.Fatalf("Sync after Stop armed %d timers", len(s.timers))
	}
	s.Stop() // a second Stop is harmless
}
