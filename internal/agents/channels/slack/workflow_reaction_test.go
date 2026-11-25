package slack

import (
	"context"
	"testing"
	"time"
)

func TestReactionReachesWorkflowOncePerReactor(t *testing.T) {
	s := &Channel{botUserID: "UBOT"}
	type got struct {
		event   string
		payload map[string]any
	}
	var calls []got
	s.SetWorkflowEventSink(func(_ context.Context, event string, payload map[string]any) {
		calls = append(calls, got{event, payload})
	})
	ctx := context.Background()
	s.emitReactionWorkflow(ctx, "reaction", "U1", "UBOT", "white_check_mark", "C01TESTCHAN", "1.2", true)
	s.emitReactionWorkflow(ctx, "reaction", "U2", "UBOT", "white_check_mark", "C01TESTCHAN", "1.2", true)
	s.emitReactionWorkflow(ctx, "reaction_removed", "U1", "UBOT", "white_check_mark", "C01TESTCHAN", "1.2", false)
	// The bot's own reaction never fires a workflow (loop guard).
	s.emitReactionWorkflow(ctx, "reaction", "UBOT", "U1", "eyes", "C01TESTCHAN", "1.2", true)
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(calls))
	}
	p := calls[0].payload
	if calls[0].event != "reaction" || p["reaction"] != "white_check_mark" || p["user"] != "U1" || p["item_user"] != "UBOT" || p["ts"] != "1.2" || p["added"] != true {
		t.Fatalf("payload = %+v", p)
	}
	keys := map[string]bool{}
	for _, c := range calls {
		keys[c.payload["event_key"].(string)] = true
	}
	if len(keys) != 3 {
		t.Fatalf("event keys must differ per reactor and add/remove: %v", keys)
	}
	if calls[2].event != "reaction_removed" || calls[2].payload["added"] != false {
		t.Fatalf("removed = %+v", calls[2])
	}
}

func TestReactionPayloadCarriesMessageOnlyWhenWanted(t *testing.T) {
	s := &Channel{botUserID: "UBOT"}
	var last map[string]any
	s.SetWorkflowEventSink(func(_ context.Context, _ string, payload map[string]any) { last = payload })
	reactedCache.Store("C01TESTCHAN/5.6", reactedMessage{text: "the answer", thread: "5.0", botID: "B1", at: time.Now()})
	t.Cleanup(func() { reactedCache.Delete("C01TESTCHAN/5.6") })
	ctx := context.Background()
	// Nobody listens: nothing is read, the payload has no text.
	s.emitReactionWorkflow(ctx, "reaction", "U1", "UBOT", "white_check_mark", "C01TESTCHAN", "5.6", true)
	if last["text"] != "" {
		t.Fatalf("text without a listener = %v", last["text"])
	}
	s.SetWorkflowWants(func(ev string) bool { return ev == "reaction" })
	s.emitReactionWorkflow(ctx, "reaction", "U1", "UBOT", "white_check_mark", "C01TESTCHAN", "5.6", true)
	if last["text"] != "the answer" || last["thread"] != "5.0" || last["is_reply"] != true || last["item_bot_id"] != "B1" {
		t.Fatalf("payload = %+v", last)
	}
}
