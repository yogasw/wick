package trigger

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
)

func botThreadEvt(channelID, thread string, isBot bool) workflow.Event {
	return workflow.Event{
		Type:    string(workflow.TriggerChannel),
		Subtype: "message",
		Channel: "slack",
		Payload: map[string]any{
			"channel_id": channelID,
			"thread":     thread,
			"is_bot":     isBot,
		},
	}
}

// A workflow whose reply still matches its own trigger, posted by another
// bot of the same host, re-fires on every reply. The guard must stop it
// after botRunLimit runs in one thread.
func TestRouter_BotRunGuard_StopsLoopInOneThread(t *testing.T) {
	now := time.Date(2026, 1, 3, 6, 42, 0, 0, time.UTC)
	r := &Router{clock: func() time.Time { return now }}

	evt := botThreadEvt("C01TESTCHAN", "1000.0001", true)
	for i := 0; i < botRunLimit; i++ {
		if !r.allowBotRun("wf-a", evt) {
			t.Fatalf("run %d must pass while under the limit", i+1)
		}
		now = now.Add(15 * time.Second)
	}
	if r.allowBotRun("wf-a", evt) {
		t.Fatal("run past the limit must be skipped")
	}
}

// People are never counted, and the budget is per workflow and per thread.
func TestRouter_BotRunGuard_ScopedToBotsWorkflowAndThread(t *testing.T) {
	now := time.Date(2026, 1, 3, 6, 42, 0, 0, time.UTC)
	r := &Router{clock: func() time.Time { return now }}

	bot := botThreadEvt("C01TESTCHAN", "1000.0001", true)
	for i := 0; i < botRunLimit; i++ {
		r.allowBotRun("wf-a", bot)
	}
	if r.allowBotRun("wf-a", bot) {
		t.Fatal("precondition: wf-a budget for this thread is spent")
	}

	human := botThreadEvt("C01TESTCHAN", "1000.0001", false)
	for i := 0; i < botRunLimit*2; i++ {
		if !r.allowBotRun("wf-a", human) {
			t.Fatalf("human message %d must never be limited", i+1)
		}
	}
	if !r.allowBotRun("wf-b", bot) {
		t.Error("another workflow has its own budget")
	}
	if !r.allowBotRun("wf-a", botThreadEvt("C01TESTCHAN", "2000.0002", true)) {
		t.Error("another thread has its own budget")
	}
	if !r.allowBotRun("wf-a", botThreadEvt("C02TESTCHAN", "1000.0001", true)) {
		t.Error("the same ts in another channel is another thread")
	}
}

// The window slides: once the oldest run is botRunWindow old, one more fits.
func TestRouter_BotRunGuard_WindowSlides(t *testing.T) {
	start := time.Date(2026, 1, 3, 6, 42, 0, 0, time.UTC)
	now := start
	r := &Router{clock: func() time.Time { return now }}

	evt := botThreadEvt("C01TESTCHAN", "1000.0001", true)
	for i := 0; i < botRunLimit; i++ {
		r.allowBotRun("wf-a", evt)
	}
	now = start.Add(botRunWindow - time.Second)
	if r.allowBotRun("wf-a", evt) {
		t.Fatal("still inside the window: must be skipped")
	}
	now = start.Add(botRunWindow)
	if !r.allowBotRun("wf-a", evt) {
		t.Error("oldest run left the window: one more run must fit")
	}
}

// Bot events with no thread key (cron-like, DMs without one) are not counted.
func TestRouter_BotRunGuard_NoThreadPasses(t *testing.T) {
	r := &Router{}
	evt := botThreadEvt("C01TESTCHAN", "", true)
	for i := 0; i < botRunLimit*2; i++ {
		if !r.allowBotRun("wf-a", evt) {
			t.Fatalf("event %d without a thread key must pass", i+1)
		}
	}
}
