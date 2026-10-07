package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// root ── child, plus "theirs" (another user's) and unknown ids.
func activityWorld() (parentOf func(string) (string, bool), visible func(string) bool) {
	parentOf = func(id string) (string, bool) {
		switch id {
		case "root", "theirs":
			return "", true
		case "child":
			return "root", true
		}
		return "", false
	}
	visible = func(id string) bool { return id == "root" }
	return
}

// The roster follows a turn step by step: thinking, a tool (with its short
// label), back to thinking (with the failure flag when the tool failed),
// and nothing once the turn ends.
func TestActivityTrackerFollowsATurn(t *testing.T) {
	parentOf, visible := activityWorld()
	tr := newActivityTracker(parentOf, visible, func(string) bool { return false })

	steps := []struct {
		ev   Event
		want sessionActivity
	}{
		{Event{SessionID: "root", Type: "lifecycle", Lifecycle: "working"}, sessionActivity{Work: "thinking"}},
		{Event{SessionID: "root", Type: "tool_use", ToolName: "mcp__wick__wick_execute", ToolInput: `{"tool_id":"conn:abc/query_range@x"}`},
			sessionActivity{Work: "tool", Action: "query_range"}},
		{Event{SessionID: "root", Type: "tool_result", IsError: true}, sessionActivity{Work: "thinking", ToolError: true}},
		{Event{SessionID: "root", Type: "tool_use", ToolName: "Bash"}, sessionActivity{Work: "tool", Action: "Bash"}},
		{Event{SessionID: "root", Type: "tool_result"}, sessionActivity{Work: "thinking"}},
		{Event{SessionID: "root", Type: "lifecycle", Lifecycle: "idle"}, sessionActivity{}},
	}
	for i, s := range steps {
		got, ok := tr.apply(s.ev)
		if !ok {
			t.Fatalf("step %d: no event for %+v", i, s.ev)
		}
		s.want.SessionID = "root"
		if got != s.want {
			t.Fatalf("step %d: got %+v, want %+v", i, got, s.want)
		}
	}
	// Forgotten once idle: the tracker only holds live conversations.
	if len(tr.last) != 0 {
		t.Fatalf("idle conversation still tracked: %+v", tr.last)
	}
}

// A repeat of the same state is not re-sent, and events that say nothing
// about a turn's step never produce one.
func TestActivityTrackerSendsOnlyChanges(t *testing.T) {
	parentOf, visible := activityWorld()
	tr := newActivityTracker(parentOf, visible, func(string) bool { return false })
	if _, ok := tr.apply(Event{SessionID: "root", Type: "lifecycle", Lifecycle: "working"}); !ok {
		t.Fatal("first working must send")
	}
	if _, ok := tr.apply(Event{SessionID: "root", Type: "lifecycle", Lifecycle: "working"}); ok {
		t.Fatal("repeated working must not send")
	}
	for _, typ := range []string{"text_delta", "thinking", "user_message", "session_meta", "git_status"} {
		if _, ok := tr.apply(Event{SessionID: "root", Type: typ, Data: "x"}); ok {
			t.Fatalf("%s must not produce an activity event", typ)
		}
	}
	// A "working" while a tool runs keeps the tool.
	tr.apply(Event{SessionID: "root", Type: "tool_use", ToolName: "Read"})
	if _, ok := tr.apply(Event{SessionID: "root", Type: "lifecycle", Lifecycle: "working"}); ok {
		t.Fatal("working during a tool must not reset it to thinking")
	}
	// An idle conversation going idle again says nothing.
	tr2 := newActivityTracker(parentOf, visible, func(string) bool { return false })
	if _, ok := tr2.apply(Event{SessionID: "root", Type: "lifecycle", Lifecycle: "idle"}); ok {
		t.Fatal("idle with no turn tracked must not send")
	}
}

// Anti-leak: another user's conversation, a sub-agent's own session and an
// unknown session never produce an event — and a child's tool is not
// re-addressed to its parent either, since it is not the parent's work.
func TestActivityTrackerFiltersByAccessAndSubAgent(t *testing.T) {
	parentOf, visible := activityWorld()
	tr := newActivityTracker(parentOf, visible, func(string) bool { return true })
	for _, sid := range []string{"theirs", "child", "ghost", ""} {
		for _, ev := range []Event{
			{SessionID: sid, Type: "lifecycle", Lifecycle: "working"},
			{SessionID: sid, Type: "tool_use", ToolName: "Bash"},
			{SessionID: sid, Type: "tool_result", IsError: true},
			{SessionID: sid, Type: "ask_user"},
			{SessionID: sid, Type: "approval_request"},
		} {
			if got, ok := tr.apply(ev); ok {
				t.Fatalf("%q %s leaked as %+v", sid, ev.Type, got)
			}
		}
	}
	if len(tr.last) != 0 {
		t.Fatalf("filtered sessions tracked: %+v", tr.last)
	}
}

// needs_attention follows ask_user / approval events, read from the
// pending stores rather than the event (a resolve may leave another one).
func TestActivityTrackerAttention(t *testing.T) {
	parentOf, visible := activityWorld()
	pending := false
	tr := newActivityTracker(parentOf, visible, func(string) bool { return pending })
	tr.apply(Event{SessionID: "root", Type: "lifecycle", Lifecycle: "working"})
	pending = true
	got, ok := tr.apply(Event{SessionID: "root", Type: "ask_user"})
	if !ok || !got.NeedsAttention || got.Work != "thinking" {
		t.Fatalf("ask_user: %+v ok=%v", got, ok)
	}
	if _, ok := tr.apply(Event{SessionID: "root", Type: "approval_request"}); ok {
		t.Fatal("already waiting: no change to send")
	}
	pending = false
	got, ok = tr.apply(Event{SessionID: "root", Type: "ask_user_resolved"})
	if !ok || got.NeedsAttention {
		t.Fatalf("resolved: %+v ok=%v", got, ok)
	}
}

// Replay paints a running turn's step for a fresh subscriber: the open
// tool, or thinking with the failure flag; idle, foreign and child
// sessions are skipped.
func TestActivityTrackerReplay(t *testing.T) {
	parentOf, _ := activityWorld()
	visible := func(id string) bool { return id == "root" || id == "root2" }
	parentOf2 := func(id string) (string, bool) {
		if id == "root2" || id == "idle" {
			return "", true
		}
		return parentOf(id)
	}
	tr := newActivityTracker(parentOf2, visible, func(id string) bool { return id == "root2" })
	got := tr.replay([]pool.ActiveEntry{
		{SessionID: "root", Lifecycle: "working", InFlightEvents: []store.TurnEvent{{Type: "tool_use", ToolUseID: "t1", ToolName: "Bash"}}},
		{SessionID: "root2", Lifecycle: "working", InFlightEvents: []store.TurnEvent{
			{Type: "tool_use", ToolUseID: "t1", ToolName: "Read"}, {Type: "tool_result", ToolUseID: "t1", IsError: true}}},
		{SessionID: "idle", Lifecycle: "idle"},
		{SessionID: "theirs", Lifecycle: "working"},
		{SessionID: "child", Lifecycle: "working"},
	})
	want := []sessionActivity{
		{SessionID: "root", Work: "tool", Action: "Bash"},
		{SessionID: "root2", Work: "thinking", ToolError: true, NeedsAttention: true},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("replay = %+v, want %+v", got, want)
	}
	// The replayed state is the baseline: the same tool again is no news.
	if _, ok := tr.apply(Event{SessionID: "root", Type: "tool_use", ToolName: "Bash"}); ok {
		t.Fatal("replayed state re-sent")
	}
}

// The event carries the roster's fields only — never the tool input, its
// output or the agent's text.
func TestSessionActivityShipsNoExtraFields(t *testing.T) {
	parentOf, visible := activityWorld()
	tr := newActivityTracker(parentOf, visible, func(string) bool { return true })
	tr.apply(Event{SessionID: "root", Type: "tool_result", IsError: true, Data: "secret output"})
	a, ok := tr.apply(Event{SessionID: "root", Type: "tool_use", ToolName: "Bash", ToolInput: `{"command":"cat secret"}`, Data: "Bash"})
	if !ok {
		t.Fatal("expected an event")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(a.JSON()), &out); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"session_id": true, "work": true, "action": true, "tool_error": true, "needs_attention": true}
	for k := range out {
		if !allowed[k] {
			t.Errorf("unexpected field %q", k)
		}
	}
	if strings.Contains(a.JSON(), "secret") {
		t.Fatalf("tool input/output leaked: %s", a.JSON())
	}
}

// End to end over GET /stream/sessions: a non-admin user gets activity
// for their own conversation only — never another user's, never a
// sub-agent's own session id.
func TestSessionsStreamActivityIsAccessChecked(t *testing.T) {
	layout := agentconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	for _, s := range []session.CreateOptions{
		{ID: "MINE", UserID: "u1"},
		{ID: "MYCHILD", UserID: "u1", ParentSessionID: "MINE"},
		{ID: "THEIRS", UserID: "u2"},
	} {
		s.Origin = session.OriginUI
		if _, err := session.Create(context.Background(), layout, s); err != nil {
			t.Fatal(err)
		}
	}
	reg := registry.New(layout)
	if err := reg.Reload(); err != nil {
		t.Fatal(err)
	}
	prevMgr, prevBcast, prevPool := globalMgr, globalBcast, globalPool
	globalMgr, globalBcast = registry.NewManager(reg), NewBroadcaster()
	globalPool = pool.New(pool.PoolConfig{Layout: layout})
	t.Cleanup(func() { globalMgr, globalBcast, globalPool = prevMgr, prevBcast, prevPool })

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodGet, "/stream/sessions", nil).WithContext(ctx)
	r = r.WithContext(login.WithUser(r.Context(), &entity.User{ID: "u1"}, nil))
	w := httptest.NewRecorder()
	c := tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); sessionsLifecycleSSE(c) }()
	time.Sleep(50 * time.Millisecond)

	ctxB := context.Background()
	globalBcast.PublishLifecycle(ctxB, "MINE", "a", "working", "claude", 1)
	globalBcast.Publish("MINE", "a", event.AgentEvent{Type: event.ToolUse, ToolName: "Bash", ToolUseID: "t1", ToolInput: `{"command":"ls"}`})
	globalBcast.Publish("THEIRS", "a", event.AgentEvent{Type: event.ToolUse, ToolName: "LeakedTool", ToolUseID: "t2"})
	globalBcast.Publish("MYCHILD", "a", event.AgentEvent{Type: event.ToolUse, ToolName: "ChildTool", ToolUseID: "t3"})
	globalBcast.Publish("MINE", "a", event.AgentEvent{Type: event.ToolResult, ToolUseID: "t1", IsError: true})
	globalBcast.PublishLifecycle(ctxB, "MINE", "a", "idle", "claude", 1)
	time.Sleep(100 * time.Millisecond)
	cancel()
	wg.Wait()

	body := w.Body.String()
	var acts []sessionActivity
	for _, block := range strings.Split(body, "\n\n") {
		if data, ok := strings.CutPrefix(block, "event: activity\ndata: "); ok {
			var a sessionActivity
			if err := json.Unmarshal([]byte(data), &a); err != nil {
				t.Fatalf("bad activity %q: %v", data, err)
			}
			acts = append(acts, a)
		}
	}
	want := []sessionActivity{
		{SessionID: "MINE", Work: "thinking"},
		{SessionID: "MINE", Work: "tool", Action: "Bash"},
		{SessionID: "MINE", Work: "thinking", ToolError: true},
		{SessionID: "MINE"},
	}
	if len(acts) != len(want) {
		t.Fatalf("activity = %+v, want %+v\nbody:\n%s", acts, want, body)
	}
	for i := range want {
		if acts[i] != want[i] {
			t.Fatalf("activity[%d] = %+v, want %+v", i, acts[i], want[i])
		}
	}
	for _, leak := range []string{"THEIRS", "LeakedTool", "MYCHILD", "ChildTool", `"command"`} {
		if strings.Contains(body, leak) {
			t.Fatalf("%s leaked onto the stream:\n%s", leak, body)
		}
	}
	// The sidebar's own session events still flow on the same stream.
	if !strings.Contains(body, "event: session\ndata: ") {
		t.Fatalf("session events missing:\n%s", body)
	}
}
