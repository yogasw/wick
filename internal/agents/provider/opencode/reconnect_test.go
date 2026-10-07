package opencode

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func fastReconnect(t *testing.T) {
	t.Helper()
	pb, pa := reconnectBackoff, reconnectAttempts
	reconnectBackoff, reconnectAttempts = 10*time.Millisecond, 4
	t.Cleanup(func() { reconnectBackoff, reconnectAttempts = pb, pa })
}

func textPart(sid, id, text string) map[string]any {
	return map[string]any{"id": id, "type": "text", "text": text, "sessionID": sid, "messageID": "msg_a1", "time": map[string]any{"end": 1}}
}

func toolPart(sid, id string) map[string]any {
	return map[string]any{"id": id, "type": "tool", "callID": "call_" + id, "tool": "task", "sessionID": sid, "messageID": "msg_a1",
		"state": map[string]any{"status": "completed", "output": "sub-agent done"}}
}

func stopPart(sid, id string) map[string]any {
	return map[string]any{"id": id, "type": "step-finish", "reason": "stop", "sessionID": sid, "messageID": "msg_a1"}
}

// storedTurn is GET /session/{id}/message for an earlier turn plus this
// one: history before this turn's prompt must not be replayed.
func storedTurn(sid string, parts ...map[string]any) string {
	b, _ := json.Marshal([]map[string]any{
		{"info": map[string]any{"id": "msg_old", "role": "user"}, "parts": []any{}},
		{"info": map[string]any{"id": "msg_olda", "role": "assistant"}, "parts": []any{textPart(sid, "prt_old", "earlier answer")}},
		{"info": map[string]any{"id": "msg_user1", "role": "user"}, "parts": []any{}},
		{"info": map[string]any{"id": "msg_a1", "role": "assistant"}, "parts": parts},
	})
	return string(b)
}

func countPart(lines []string, id string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, `"id":"`+id+`"`) {
			n++
		}
	}
	return n
}

// The event stream drops while the server is still running the turn (a
// sub-agent that takes minutes): wick resubscribes, replays what it missed
// from the stored messages, and the turn ends on its own idle — each part
// passed on exactly once, nothing from earlier turns.
func TestRemoteStreamDropWhileBusyReconnectsAndResyncs(t *testing.T) {
	fastReconnect(t)
	f := &fakeOpencode{}
	f.midTurn = func(sid string) {
		f.publish("message.part.updated", map[string]any{"part": textPart(sid, "prt_a", "looking")})
		f.mu.Lock()
		f.status = "busy"
		f.messages = storedTurn(sid, textPart(sid, "prt_a", "looking"), toolPart(sid, "prt_b"), textPart(sid, "prt_c", "found it"))
		f.mu.Unlock()
		f.dropStreams()
	}
	f.onSubscribe = func(n int) {
		if n != 2 {
			return
		}
		sid := "ses_new1"
		f.mu.Lock()
		f.messages = storedTurn(sid, textPart(sid, "prt_a", "looking"), toolPart(sid, "prt_b"), textPart(sid, "prt_c", "found it"), stopPart(sid, "prt_d"))
		f.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		// The live stream repeats a part the resync already passed on.
		f.publish("message.part.updated", map[string]any{"part": textPart(sid, "prt_c", "found it")})
		f.publish("message.part.updated", map[string]any{"part": stopPart(sid, "prt_d")})
		f.setStatus("idle")
		f.publish("session.idle", map[string]any{"sessionID": sid})
	}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "a/b", prompt: "hi"})
	lines := readAll(t, p)
	if err := p.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	for _, id := range []string{"prt_a", "prt_b", "prt_c", "prt_d"} {
		if n := countPart(lines, id); n != 1 {
			t.Fatalf("part %s passed on %d times:\n%s", id, n, strings.Join(lines, "\n"))
		}
	}
	all := strings.Join(lines, "\n")
	if strings.Contains(all, "prt_old") || strings.Contains(all, `"type":"error"`) {
		t.Fatalf("history replayed or turn failed:\n%s", all)
	}
	if !strings.Contains(all, `"type":"tool_use"`) {
		t.Fatalf("tool finished during the gap was not passed on:\n%s", all)
	}
	f.mu.Lock()
	subs := f.subscribed
	f.mu.Unlock()
	if subs != 2 {
		t.Fatalf("subscriptions = %d, want 2", subs)
	}
}

// The stream drops and the session is no longer running: the stored parts
// show a finished answer, so the turn ends normally with what it missed.
func TestRemoteStreamDropAfterAnswerEndsTurnFromResync(t *testing.T) {
	fastReconnect(t)
	f := &fakeOpencode{}
	f.midTurn = func(sid string) {
		f.publish("message.part.updated", map[string]any{"part": textPart(sid, "prt_a", "looking")})
		f.mu.Lock()
		f.messages = storedTurn(sid, textPart(sid, "prt_a", "looking"), textPart(sid, "prt_c", "the answer"), stopPart(sid, "prt_d"))
		f.mu.Unlock()
		f.dropStreams()
	}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "a/b", prompt: "hi"})
	lines := readAll(t, p)
	if err := p.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	all := strings.Join(lines, "\n")
	if countPart(lines, "prt_a") != 1 || countPart(lines, "prt_c") != 1 || countPart(lines, "prt_d") != 1 || strings.Contains(all, `"type":"error"`) {
		t.Fatalf("lines:\n%s", all)
	}
	f.mu.Lock()
	subs := f.subscribed
	f.mu.Unlock()
	if subs != 1 {
		t.Fatalf("resubscribed to a session that is not running: %d", subs)
	}
}

// The stream drops, the session is not running and its answer never
// finished: the turn ends with an error that says so, not a spinner and
// not an empty success.
func TestRemoteStreamDropWithoutAnswerIsTurnError(t *testing.T) {
	fastReconnect(t)
	f := &fakeOpencode{}
	f.midTurn = func(sid string) {
		f.publish("message.part.updated", map[string]any{"part": textPart(sid, "prt_a", "looking")})
		f.mu.Lock()
		f.messages = storedTurn(sid, textPart(sid, "prt_a", "looking"))
		f.mu.Unlock()
		f.dropStreams()
	}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "a/b", prompt: "hi"})
	lines := readAll(t, p)
	if err := p.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	all := strings.Join(lines, "\n")
	if countPart(lines, "prt_a") != 1 || !strings.Contains(all, "closed the event stream mid-turn") {
		t.Fatalf("lines:\n%s", all)
	}
}
