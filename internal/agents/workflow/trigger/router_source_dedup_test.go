package trigger

import (
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

	first := slackEvt("thread_started", "C030CBY48KF", "1788921511.134279", "U0BAF9T1PFF")
	second := slackEvt("thread_started", "C030CBY48KF", "1788921511.134279", "U0BU65EH9PE")

	if !r.firstDelivery(first) {
		t.Fatal("first delivery must be accepted")
	}
	if r.firstDelivery(second) {
		t.Error("second bot's copy of the SAME message must be skipped")
	}
}

// message and thread_started are two deliberate events for one top-level
// post. Keying on the event alone would wrongly collapse them.
func TestRouter_FirstDelivery_KeepsDistinctSubtypes(t *testing.T) {
	withFreshSourceDedup(t)
	r := &Router{}

	if !r.firstDelivery(slackEvt("message", "C1", "111.1", "B1")) {
		t.Fatal("message should pass")
	}
	if !r.firstDelivery(slackEvt("thread_started", "C1", "111.1", "B1")) {
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
		if !r.firstDelivery(evt) {
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
		if !r.firstDelivery(bare) {
			t.Fatalf("fire %d: an event with no event_key must always pass", i)
		}
	}

	// Present-but-empty and wrong-typed keys behave the same way.
	empty := workflow.Event{Type: string(workflow.TriggerChannel), Channel: "slack",
		Payload: map[string]any{"event_key": ""}}
	nonString := workflow.Event{Type: string(workflow.TriggerChannel), Channel: "slack",
		Payload: map[string]any{"event_key": 42}}
	for i := 0; i < 2; i++ {
		if !r.firstDelivery(empty) {
			t.Error("empty event_key must not deduplicate")
		}
		if !r.firstDelivery(nonString) {
			t.Error("non-string event_key must not deduplicate")
		}
	}
}
