package slack

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	slackgo "github.com/slack-go/slack"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/event"
)

// interimHooks wires the switch and the open tasks for one test.
func interimHooks(t *testing.T, hide bool, open map[string]bool) {
	t.Helper()
	prevHide, prevActive := agentchannels.HideInterim, agentchannels.TasksActive
	t.Cleanup(func() { agentchannels.HideInterim, agentchannels.TasksActive = prevHide, prevActive })
	agentchannels.HideInterim = func(string) bool { return hide }
	agentchannels.TasksActive = func(ids []string) bool {
		for _, id := range ids {
			if open[id] {
				return true
			}
		}
		return false
	}
}

const interimLine = "Sudah saya tanyakan ke Franky."

// playInterim sends text, team_message (tool), its result, then Done,
// and returns what the thread got.
func playInterim(t *testing.T, tool, text string) string {
	t.Helper()
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: text})
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolUse, ToolName: tool, ToolInput: `{"to":"@franky","message":"cek"}`, ToolUseID: "u1"})
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolResult, ToolUseID: "u1", Text: `{"task_id":"t1","state":"working"}`})
	// A live edit tick while the turn holds posts nothing.
	c.flushLiveMessage("slack-t1")
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.Done})
	return postedText(f)
}

func TestInterimHeldWhileTaskWorks(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	for _, tool := range []string{"mcp__wick__team_message", "wick.team_message"} {
		if got := playInterim(t, tool, interimLine); got != "" {
			t.Fatalf("%s: in-between line posted: %q", tool, got)
		}
	}
}

func TestInterimPostsWhenTasksSettled(t *testing.T) {
	interimHooks(t, true, map[string]bool{})
	if got := playInterim(t, "team_message", interimLine); !strings.Contains(got, interimLine) {
		t.Fatalf("posted %q", got)
	}
}

func TestInterimGuard1Posts(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	q := "Sudah saya tanyakan. Mau sekalian dicek prod?"
	if got := playInterim(t, "team_message", q); !strings.Contains(got, q) {
		t.Fatalf("posted %q", got)
	}
}

func TestInterimSwitchOffPosts(t *testing.T) {
	interimHooks(t, false, map[string]bool{"t1": true})
	if got := playInterim(t, "team_message", interimLine); !strings.Contains(got, interimLine) {
		t.Fatalf("posted %q", got)
	}
}

// A turn that did not call team_message streams as before: the first
// live edit posts its text, before the Done.
func TestInterimNoTeamMessageNotHeld(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolUse, ToolName: "Bash", ToolInput: `{}`, ToolUseID: "u0"})
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: interimLine})
	c.flushLiveMessage("slack-t1")
	if got := postedText(f); !strings.Contains(got, interimLine) {
		t.Fatalf("live edit held a turn without team_message: %q", got)
	}
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.Done})
}

// liveFake answers chat.postMessage, chat.update and chat.delete, records
// each call, and fails chat.delete when failDelete is set.
type liveFake struct {
	srv        *httptest.Server
	mu         sync.Mutex
	calls      []mdCall
	failDelete bool
}

func newLiveFake(t *testing.T, failDelete bool) *liveFake {
	t.Helper()
	f := &liveFake{failDelete: failDelete}
	record := func(method string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			vals, _ := url.ParseQuery(string(body))
			f.mu.Lock()
			f.calls = append(f.calls, mdCall{method, vals})
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if method == "delete" && f.failDelete {
				_, _ = w.Write([]byte(`{"ok":false,"error":"cant_delete_message"}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"channel":"C1","ts":"1700000000.000300"}`))
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/chat.postMessage", record("post"))
	mux.HandleFunc("/chat.update", record("update"))
	mux.HandleFunc("/chat.delete", record("delete"))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *liveFake) got() []mdCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mdCall(nil), f.calls...)
}

func (f *liveFake) channel() *Channel {
	return &Channel{
		api:   slackgo.New("xoxb-test", slackgo.OptionAPIURL(f.srv.URL+"/")),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}
}

func callText(c mdCall) string { return c.vals.Get("text") + c.vals.Get("blocks") }

// liveThenDelegate streams a partial line that a live edit posts, the rest
// of it, then team_message and its result.
func liveThenDelegate(c *Channel) {
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: "Sudah saya tanya"})
	c.flushLiveMessage("slack-t1")
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: "kan ke Franky."})
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolUse, ToolName: "team_message", ToolInput: `{"to":"@franky","message":"cek"}`, ToolUseID: "u1"})
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolResult, ToolUseID: "u1", Text: `{"task_id":"t1","state":"working"}`})
}

// The live message showed the line cut mid-word: dropping the text
// deletes it rather than leaving it truncated.
func TestInterimDropDeletesLiveMessage(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	f := newLiveFake(t, false)
	c := f.channel()
	liveThenDelegate(c)
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.Done})
	calls := f.got()
	if len(calls) != 2 || calls[0].method != "post" || calls[1].method != "delete" {
		t.Fatalf("want post then delete, got %+v", calls)
	}
	if ts := calls[1].vals.Get("ts"); ts != "1700000000.000300" {
		t.Fatalf("deleted ts %q, want the live message", ts)
	}
	if c.turns["slack-t1"].liveTS != "" {
		t.Fatal("live ts kept after the delete")
	}
}

// chat.delete refused: the live message is completed instead.
func TestInterimDropDeleteFailsCompletesLiveMessage(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	f := newLiveFake(t, true)
	c := f.channel()
	liveThenDelegate(c)
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.Done})
	calls := f.got()
	if len(calls) != 3 || calls[1].method != "delete" || calls[2].method != "update" {
		t.Fatalf("want post, delete, update, got %+v", calls)
	}
	if got := callText(calls[2]); !strings.Contains(got, interimLine) {
		t.Fatalf("update carried %q, want the whole line", got)
	}
}

// Text after the last team_message result is the answer: it posts, into
// the live message that showed the line before the call.
func TestInterimAnswerAfterResultPosts(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	f := newLiveFake(t, false)
	c := f.channel()
	liveThenDelegate(c)
	answer := "Deploy terakhir jam 10, sudah sehat."
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: "\n\n" + answer})
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.Done})
	calls := f.got()
	last := calls[len(calls)-1]
	if last.method != "update" || callText(last) == "" || !strings.Contains(callText(last), answer) || strings.Contains(callText(last), "Franky") {
		t.Fatalf("want the live message updated to the answer alone, got %+v", calls)
	}
	for _, cl := range calls {
		if cl.method == "delete" {
			t.Fatalf("answer turn deleted its message: %+v", calls)
		}
	}
}

// A turn that fails while holding posts its text, as before the hold.
func TestInterimErrorPostsHeldText(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: interimLine})
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolUse, ToolName: "team_message", ToolInput: `{"to":"@franky"}`, ToolUseID: "u1"})
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolResult, ToolUseID: "u1", Text: `{"task_id":"t1","state":"working"}`})
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.Done, ErrorMsg: "boom"})
	if got := postedText(f); !strings.Contains(got, interimLine) || !strings.Contains(got, "boom") {
		t.Fatalf("posted %q", got)
	}
}

// A follow-up message that takes the turn over keeps its hold.
func TestCarryOverKeepsInterim(t *testing.T) {
	var old turn
	old.interim.Tool("team_message", `{"to":"@franky"}`, "u1", true)
	var nt turn
	nt.carryOver(&old)
	if !nt.interim.Holding() {
		t.Fatal("the carried-over turn lost the hold")
	}
}
