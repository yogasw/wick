package slack

import (
	"context"
	"slices"
	"testing"

	"github.com/slack-go/slack/slackevents"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// A Stop click must survive slackevents.ParseEvent: socket mode drops any
// payload whose inner type is not in the mapping.
func TestAgentSessionStoppedParses(t *testing.T) {
	raw := `{"type":"event_callback","team_id":"T01TEST","event":{"type":"agent_session_stopped","channel":"C01TESTCHAN","thread_ts":"1700000000.000100","user":"U01TESTUSER","event_ts":"1700000001.000200","streaming_message_ts":["1700000000.000300"]}}`
	ev, err := slackevents.ParseEvent([]byte(raw), slackevents.OptionNoVerifyToken())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, ok := ev.InnerEvent.Data.(*AgentSessionStoppedEvent)
	if !ok {
		t.Fatalf("inner data = %T, want *AgentSessionStoppedEvent", ev.InnerEvent.Data)
	}
	if got.Channel != "C01TESTCHAN" || got.ThreadTS != "1700000000.000100" || got.User != "U01TESTUSER" {
		t.Errorf("event = %+v", got)
	}
	if len(got.StreamingMessageTS) != 1 {
		t.Errorf("streaming_message_ts = %v", got.StreamingMessageTS)
	}
}

func TestGenerateManifestHasStopEvent(t *testing.T) {
	ev := GenerateManifest(ManifestInput{Name: "acme"}).Settings.EventSubscriptions.BotEvents
	if !slices.Contains(ev, agentSessionStoppedType) {
		t.Errorf("bot events %v lack %s", ev, agentSessionStoppedType)
	}
}

func stopTestChannel(cfg agentconfig.SlackChannelConfig) *Channel {
	s := New(cfg)
	s.SetSessionPrefix("slack:test:")
	return s
}

func TestStopButtonStopsRunningTurn(t *testing.T) {
	s := stopTestChannel(agentconfig.SlackChannelConfig{})
	// A DM main chat: the session is not sessionKey(thread), the live turn
	// says which one runs there.
	s.mu.Lock()
	s.turns["main-chat"] = &turn{channelID: "D01TEST", threadTS: "1700000000.000100", running: true}
	s.turns["other"] = &turn{channelID: "D01TEST", threadTS: "1700000000.999999", running: true}
	s.mu.Unlock()
	var stopped []string
	var by string
	s.SetStopFn(func(sid, who string) error {
		stopped = append(stopped, sid)
		by = who
		return nil
	})
	s.handleAgentSessionStopped(context.Background(), &AgentSessionStoppedEvent{
		Channel: "D01TEST", ThreadTS: "1700000000.000100", User: "U01TESTUSER",
	})
	if !slices.Equal(stopped, []string{"main-chat"}) {
		t.Fatalf("stopped = %v, want [main-chat]", stopped)
	}
	if by == "" {
		t.Error("stop author empty")
	}
}

func TestStopButtonIdleThreadStopsNothing(t *testing.T) {
	s := stopTestChannel(agentconfig.SlackChannelConfig{})
	s.mu.Lock()
	s.turns["done"] = &turn{channelID: "C01TESTCHAN", threadTS: "1700000000.000100", running: false}
	s.mu.Unlock()
	called := false
	s.SetStopFn(func(string, string) error { called = true; return nil })
	s.handleAgentSessionStopped(context.Background(), &AgentSessionStoppedEvent{
		Channel: "C01TESTCHAN", ThreadTS: "1700000000.000100", User: "U01TESTUSER",
	})
	if called {
		t.Error("stop called for a finished turn")
	}
	if sid, running := s.stopSessionFor("C01TESTCHAN", "1700000000.000555"); running || sid != "slack:test:1700000000.000555" {
		t.Errorf("fallback = %q running=%v", sid, running)
	}
}

func TestStopButtonSenderOutsideAllowlist(t *testing.T) {
	s := stopTestChannel(agentconfig.SlackChannelConfig{
		UsersMode:    "whitelist",
		AllowedUsers: `[{"id":"U01ALLOWED"}]`,
	})
	s.mu.Lock()
	s.turns["sess"] = &turn{channelID: "C01TESTCHAN", threadTS: "1700000000.000100", running: true}
	s.mu.Unlock()
	called := false
	s.SetStopFn(func(string, string) error { called = true; return nil })
	s.handleAgentSessionStopped(context.Background(), &AgentSessionStoppedEvent{
		Channel: "C01TESTCHAN", ThreadTS: "1700000000.000100", User: "U01STRANGER",
	})
	if called {
		t.Error("a sender outside the allowlist stopped the turn")
	}
}
