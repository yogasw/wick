package trigger

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// withFreshSourceDedup isolates the package-level dedupe so cases don't
// leak keys into each other.
func withFreshSourceDedup(t *testing.T) {
	t.Helper()
	prev := sourceDedup
	sourceDedup = NewDedup(64, 5*time.Minute)
	t.Cleanup(func() { sourceDedup = prev })
}

func slackEvt(subtype, channelID, ts, bot string) workflow.Event {
	return workflow.Event{
		Type:    string(workflow.TriggerChannel),
		Subtype: subtype,
		Channel: "slack",
		Payload: map[string]any{
			"channel_id":  channelID,
			"ts":          ts,
			"bot_user_id": bot,
			"event_key":   channelID + "/" + ts,
		},
	}
}

// A channel message is delivered to EVERY app that is a member, and one
// process runs one Slack instance per owning user — so two wick bots in
// #integration-and-ops both hand the router the same human message. It
// must run the workflow once, not once per bot.
// Observed live 2026-09-09: runs 69184a4f (bot Ygsw) and db2c9bb3 (bot
// Atraxa), same trigger-gft9a2, same ts 1788921511.134279.
func TestRouter_FirstDelivery_CollapsesSameEventFromTwoBots(t *testing.T) {
	withFreshSourceDedup(t)
	r := &Router{}

	first := slackEvt("thread_started", "C0123ABCD", "1788921511.134279", "U0AAA0001")
	second := slackEvt("thread_started", "C0123ABCD", "1788921511.134279", "U0AAA0002")

	if !r.firstDelivery("wf#0", first) {
		t.Fatal("first delivery must be accepted")
	}
	if r.firstDelivery("wf#0", second) {
		t.Error("second bot's copy of the SAME message must be skipped")
	}
}

// message and thread_started are two deliberate events for one top-level
// post. Keying on the event alone would wrongly collapse them.
func TestRouter_FirstDelivery_KeepsDistinctSubtypes(t *testing.T) {
	withFreshSourceDedup(t)
	r := &Router{}

	if !r.firstDelivery("wf#0", slackEvt("message", "C1", "111.1", "B1")) {
		t.Fatal("message should pass")
	}
	if !r.firstDelivery("wf#0", slackEvt("thread_started", "C1", "111.1", "B1")) {
		t.Error("thread_started for the same post is a DIFFERENT event and must pass")
	}
}

func TestRouter_FirstDelivery_DistinctMessagesAllPass(t *testing.T) {
	withFreshSourceDedup(t)
	r := &Router{}

	cases := []workflow.Event{
		slackEvt("message", "C1", "111.1", "B1"),
		slackEvt("message", "C1", "111.2", "B1"), // later message, same channel
		slackEvt("message", "C2", "111.1", "B1"), // same ts, other channel
	}
	for i, evt := range cases {
		if !r.firstDelivery("wf#0", evt) {
			t.Errorf("case %d: distinct event must pass", i)
		}
	}
}

// Cron / manual / webhook events carry no event_key. The dedupe must
// never become an accidental filter on them — two identical cron fires
// are two real runs.
func TestRouter_FirstDelivery_NoKeyAlwaysPasses(t *testing.T) {
	withFreshSourceDedup(t)
	r := &Router{}

	bare := workflow.Event{Type: string(workflow.TriggerCron)}
	for i := 0; i < 3; i++ {
		if !r.firstDelivery("wf#0", bare) {
			t.Fatalf("fire %d: an event with no event_key must always pass", i)
		}
	}

	// Present-but-empty and wrong-typed keys behave the same way.
	empty := workflow.Event{Type: string(workflow.TriggerChannel), Channel: "slack",
		Payload: map[string]any{"event_key": ""}}
	nonString := workflow.Event{Type: string(workflow.TriggerChannel), Channel: "slack",
		Payload: map[string]any{"event_key": 42}}
	for i := 0; i < 2; i++ {
		if !r.firstDelivery("wf#0", empty) {
			t.Error("empty event_key must not deduplicate")
		}
		if !r.firstDelivery("wf#0", nonString) {
			t.Error("non-string event_key must not deduplicate")
		}
	}
}

// dedupTestRouter indexes workflows and gives each a queue but spawns no
// worker, so every accepted dispatch stays visible as one queued item.
func dedupTestRouter(t *testing.T, wfs ...workflow.Workflow) *Router {
	t.Helper()
	r := NewRouter(nil, nil)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range wfs {
		r.defs[w.ID] = w
		r.queues[w.ID] = NewQueue(20, workflow.OverflowDropOldest)
		r.reindexLocked(w)
	}
	return r
}

func slackMessageWF(id, instance string) workflow.Workflow {
	return workflow.Workflow{
		ID:      id,
		Enabled: true,
		Triggers: []workflow.Trigger{{
			Type:            workflow.TriggerChannel,
			ChannelName:     "slack",
			Event:           "message",
			ChannelInstance: instance,
		}},
	}
}

func fromInstance(evt workflow.Event, instance string) workflow.Event {
	evt.Payload["channel_instance"] = instance
	return evt
}

// A trigger pinned to bot B must fire when B's copy arrives AFTER another
// bot's copy of the same message. Collapsing before the instance filter
// let bot A's copy claim the event and get rejected, then dropped B's.
func TestRouter_Dispatch_PinnedInstanceFiresOnLaterDelivery(t *testing.T) {
	withFreshSourceDedup(t)
	r := dedupTestRouter(t, slackMessageWF("wf-pinned", "slack:B"))

	fromA := fromInstance(slackEvt("message", "C1", "111.1", "UA"), "slack:A")
	fromB := fromInstance(slackEvt("message", "C1", "111.1", "UB"), "slack:B")

	if n := r.Dispatch(context.Background(), fromA); n != 0 {
		t.Errorf("bot A's copy must not fire a trigger pinned to B, matched %d", n)
	}
	if n := r.Dispatch(context.Background(), fromB); n != 1 {
		t.Errorf("bot B's copy must fire the pinned trigger, matched %d", n)
	}
	if got := r.queues["wf-pinned"].Len(); got != 1 {
		t.Errorf("pinned workflow queued %d runs, want 1", got)
	}
}

// Without a pin, every bot's copy passes the router checks — the second
// copy must still be collapsed.
func TestRouter_Dispatch_UnpinnedRunsOncePerEvent(t *testing.T) {
	withFreshSourceDedup(t)
	r := dedupTestRouter(t, slackMessageWF("wf-any", ""))

	r.Dispatch(context.Background(), fromInstance(slackEvt("message", "C1", "111.1", "UA"), "slack:A"))
	r.Dispatch(context.Background(), fromInstance(slackEvt("message", "C1", "111.1", "UB"), "slack:B"))

	if got := r.queues["wf-any"].Len(); got != 1 {
		t.Errorf("unpinned workflow queued %d runs for one message, want 1", got)
	}
}

// The collapse is per workflow: one workflow taking the event must not
// starve another that matches the same message.
func TestRouter_Dispatch_EachWorkflowRunsOnce(t *testing.T) {
	withFreshSourceDedup(t)
	r := dedupTestRouter(t, slackMessageWF("wf-1", ""), slackMessageWF("wf-2", ""))

	r.Dispatch(context.Background(), fromInstance(slackEvt("message", "C1", "111.1", "UA"), "slack:A"))
	r.Dispatch(context.Background(), fromInstance(slackEvt("message", "C1", "111.1", "UB"), "slack:B"))

	for _, id := range []string{"wf-1", "wf-2"} {
		if got := r.queues[id].Len(); got != 1 {
			t.Errorf("%s queued %d runs, want 1", id, got)
		}
	}
}
