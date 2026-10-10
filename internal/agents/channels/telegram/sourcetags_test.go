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

// The reply sent at Done names its sources in plain text; the raw tag
// is web-only and must never reach the chat, and an unknown handle is
// dropped.
func TestDoneRewritesSourceTags(t *testing.T) {
	prev := agentchannels.SourceNames
	t.Cleanup(func() { agentchannels.SourceNames = prev })
	agentchannels.SourceNames = func(string) map[string]string {
		return map[string]string{"alpha": "Alpha", "beta": "Beta"}
	}
	got := doneText(t, "- Logs clean [from:@alpha]\n- Both agree [from:@alpha,@beta]\n- Spoof [from:@ghost]")
	if got != "- Logs clean (from Alpha)\n- Both agree (from Alpha, Beta)\n- Spoof" {
		t.Fatalf("sent %q", got)
	}
}

// A question's "[options: …]" line reaches the chat as plain choices.
func TestDoneRewritesOptionsLine(t *testing.T) {
	got := doneText(t, "Which env?\n[options: prod | staging]")
	if got != "Which env?\n\nOptions: prod / staging" {
		t.Fatalf("sent %q", got)
	}
}

// doneText streams delta as one turn and returns what it sent at Done.
func doneText(t *testing.T, delta string) string {
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

	c.OnAgentEvent("tg-5", event.AgentEvent{Type: event.TextDelta, Text: delta})
	c.OnAgentEvent("tg-5", event.AgentEvent{Type: event.Done})

	mu.Lock()
	defer mu.Unlock()
	return strings.Join(sent, "\n")
}
