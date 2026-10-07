package telegram

import (
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

func TestChatIDOf(t *testing.T) {
	cases := []struct {
		prefix, sid string
		want        int64
		ok          bool
	}{
		{"", "tg-42", 42, true},
		{"", "tg--100123", -100123, true},
		{"tgagent-a1-", "tgagent-a1-tg-7", 7, true},
		{"tgagent-a1-", "tgagent-b2-tg-7", 0, false},
		{"", "tgagent-a1-tg-7", 0, false},
		{"", "tg-abc", 0, false},
		{"", "main-session", 0, false},
	}
	for _, c := range cases {
		got, ok := ChatIDOf(c.prefix, c.sid)
		if got != c.want || ok != c.ok {
			t.Errorf("ChatIDOf(%q, %q) = %d, %v; want %d, %v", c.prefix, c.sid, got, ok, c.want, c.ok)
		}
	}
}

// A schedule firing into a chat nobody has typed in since boot still
// finds where to post the reply.
func TestTurnForUntypedChat(t *testing.T) {
	ch := New(agentconfig.TelegramChannelConfig{})
	ch.SetSessionPrefix("tgagent-a1-")
	ch.mu.Lock()
	tn := ch.turnLocked("tgagent-a1-tg-55")
	other := ch.turnLocked("someone-else")
	ch.mu.Unlock()
	if tn == nil || tn.chatID != 55 {
		t.Fatalf("turn = %+v, want chat 55", tn)
	}
	if other != nil {
		t.Fatalf("foreign session got a turn: %+v", other)
	}
}
