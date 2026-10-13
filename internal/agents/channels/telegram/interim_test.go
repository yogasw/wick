package telegram

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
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

// turnSent plays evs as one turn and returns what the chat got.
func turnSent(t *testing.T, evs ...event.AgentEvent) []string {
	t.Helper()
	var mu sync.Mutex
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"bot","username":"bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			body, _ := io.ReadAll(r.Body)
			vals, _ := url.ParseQuery(string(body))
			mu.Lock()
			sent = append(sent, vals.Get("text"))
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":5,"type":"private"}}}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	t.Cleanup(srv.Close)
	bot, err := tgbotapi.NewBotAPIWithClient("x", srv.URL+"/bot%s/%s", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	c := &Channel{bot: bot, turns: map[string]*turn{"tg-5": {chatID: 5}}}
	for _, ev := range evs {
		c.OnAgentEvent("tg-5", ev)
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), sent...)
}

func delegatingTurn(text, tool string) []event.AgentEvent {
	return []event.AgentEvent{
		{Type: event.ToolUse, ToolName: tool, ToolInput: `{"to":"@franky","message":"cek"}`, ToolUseID: "u1"},
		{Type: event.ToolResult, ToolUseID: "u1", Text: `{"task_id":"t1","state":"working"}`},
		{Type: event.TextDelta, Text: text},
		{Type: event.Done},
	}
}

const interimLine = "Sudah saya tanyakan ke Franky."

func TestInterimHeldWhileTaskWorks(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	if sent := turnSent(t, delegatingTurn(interimLine, "mcp__wick__team_message")...); len(sent) != 0 {
		t.Fatalf("in-between line posted: %q", sent)
	}
}

func TestInterimPostsWhenTasksSettled(t *testing.T) {
	interimHooks(t, true, map[string]bool{})
	if sent := turnSent(t, delegatingTurn(interimLine, "team_message")...); len(sent) != 1 || sent[0] != interimLine {
		t.Fatalf("sent %q", sent)
	}
}

func TestInterimGuard1Posts(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	q := "Sudah saya tanyakan. Mau sekalian dicek prod?"
	if sent := turnSent(t, delegatingTurn(q, "team_message")...); len(sent) != 1 || sent[0] != q {
		t.Fatalf("sent %q", sent)
	}
}

func TestInterimSwitchOffPosts(t *testing.T) {
	interimHooks(t, false, map[string]bool{"t1": true})
	if sent := turnSent(t, delegatingTurn(interimLine, "team_message")...); len(sent) != 1 || sent[0] != interimLine {
		t.Fatalf("sent %q", sent)
	}
}

func TestInterimOtherToolPosts(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	evs := delegatingTurn(interimLine, "team_message")
	evs = append([]event.AgentEvent{{Type: event.ToolUse, ToolName: "Bash", ToolInput: `{}`, ToolUseID: "u0"}}, evs...)
	if sent := turnSent(t, evs...); len(sent) != 1 || sent[0] != interimLine {
		t.Fatalf("sent %q", sent)
	}
}

func TestInterimCodexToolHeld(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	if sent := turnSent(t, delegatingTurn(interimLine, "wick.team_message")...); len(sent) != 0 {
		t.Fatalf("in-between line posted: %q", sent)
	}
}

func TestInterimNeutralToolStillHeld(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	evs := delegatingTurn(interimLine, "team_message")
	evs = append([]event.AgentEvent{{Type: event.ToolUse, ToolName: "mcp__wick__todo", ToolInput: `{}`, ToolUseID: "u0"}}, evs...)
	if sent := turnSent(t, evs...); len(sent) != 0 {
		t.Fatalf("a todo call cancelled the hold: %q", sent)
	}
}
