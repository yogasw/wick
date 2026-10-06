package slack

import (
	"context"
	"testing"
	"time"

	"github.com/slack-go/slack/slackevents"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

func TestDefaultConfigIgnoresBots(t *testing.T) {
	cfg := agentconfig.DefaultSlackChannelConfig()
	if cfg.BotsMode != "none" {
		t.Fatalf("default BotsMode = %q, want none", cfg.BotsMode)
	}
	s := &Channel{}
	ev := &slackevents.MessageEvent{User: "UOTHER", BotID: "BOTHER"}
	if ok, reason := s.allowedBotCfg(cfg, ev, "C1"); ok || reason != "bots" {
		t.Errorf("default config let a bot in: ok=%v reason=%q", ok, reason)
	}
}

func TestAllowedBotCfg(t *testing.T) {
	s := &Channel{botUserID: "USELF", botUserName: "captain"}
	cfg := agentconfig.DefaultSlackChannelConfig()
	cfg.BotsMode = "whitelist"
	cfg.AllowedBots = `[{"id":"UFRIEND","name":"Friend"},{"id":"BLEGACY","name":"Legacy"}]`

	cases := []struct {
		name   string
		ev     slackevents.MessageEvent
		ok     bool
		reason string
	}{
		{"whitelisted by user id", slackevents.MessageEvent{User: "UFRIEND", BotID: "B1"}, true, ""},
		{"whitelisted by bot id", slackevents.MessageEvent{BotID: "BLEGACY", SubType: "bot_message"}, true, ""},
		{"not whitelisted", slackevents.MessageEvent{User: "USTRANGER", BotID: "B2"}, false, "bots"},
		{"own bot by user id", slackevents.MessageEvent{User: "USELF", BotID: "B3"}, false, "self"},
		{"own bot by handle", slackevents.MessageEvent{BotID: "B3", Username: "Captain"}, false, "self"},
	}
	for _, c := range cases {
		ev := c.ev
		ok, reason := s.allowedBotCfg(cfg, &ev, "C1")
		if ok != c.ok || reason != c.reason {
			t.Errorf("%s: got ok=%v reason=%q, want %v/%q", c.name, ok, reason, c.ok, c.reason)
		}
	}

	// all lets any other bot in, but never the agent's own.
	cfg.BotsMode = "all"
	if ok, _ := s.allowedBotCfg(cfg, &slackevents.MessageEvent{User: "USTRANGER", BotID: "B2"}, "C1"); !ok {
		t.Error("bots_mode=all should let another bot in")
	}
	if ok, reason := s.allowedBotCfg(cfg, &slackevents.MessageEvent{User: "USELF", BotID: "B3"}, "C1"); ok || reason != "self" {
		t.Errorf("bots_mode=all let the agent's own bot in: ok=%v reason=%q", ok, reason)
	}

	// Channels stay AND'd on top of the bot check.
	cfg.ChannelsMode = "whitelist"
	cfg.AllowedChannels = `[{"id":"COK","name":"#ok"}]`
	if ok, reason := s.allowedBotCfg(cfg, &slackevents.MessageEvent{User: "UFRIEND", BotID: "B1"}, "CNO"); ok || reason != "channels" {
		t.Errorf("channel gate: got ok=%v reason=%q, want false/channels", ok, reason)
	}
}

// TestBotTurnBudget is the ping-pong guard: two agents that whitelist each
// other stop after botTurnLimit turns per thread inside botTurnWindow.
func TestBotTurnBudget(t *testing.T) {
	s := &Channel{}
	now := time.Now()
	for i := 0; i < botTurnLimit; i++ {
		if !s.allowBotTurn("T1", now.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("turn %d refused inside the budget", i+1)
		}
	}
	if s.allowBotTurn("T1", now.Add(10*time.Second)) {
		t.Fatal("turn past the budget was allowed: bot ping-pong is unbounded")
	}
	if !s.allowBotTurn("T2", now.Add(10*time.Second)) {
		t.Error("budget leaked across threads")
	}
	if !s.allowBotTurn("T1", now.Add(botTurnWindow+time.Second)) {
		t.Error("budget did not recover once the window slid")
	}
}

func TestApprovalGateRefusesBots(t *testing.T) {
	s := &Channel{botUserID: "USELF"}
	cfg := agentconfig.DefaultSlackChannelConfig()
	cfg.BotsMode = "whitelist"
	cfg.AllowedBots = `[{"id":"UFRIEND","name":"Friend"}]`
	cfg.GateApprovers = "custom"
	cfg.GateApproverUsers = `[{"id":"UFRIEND","name":"Friend"},{"id":"USELF","name":"Self"},{"id":"UHUMAN","name":"Human"}]`
	if s.approverAllowed(cfg, "UFRIEND") {
		t.Error("a whitelisted bot resolved an approval gate")
	}
	if s.approverAllowed(cfg, "USELF") {
		t.Error("the agent's own bot resolved an approval gate")
	}
	if !s.approverAllowed(cfg, "UHUMAN") {
		t.Error("a listed human approver was refused")
	}
}

// TestIgnoredBotNeverDispatches drives handleMessage: with the default
// BotsMode a bot message returns before any Slack call or pool send.
func TestIgnoredBotNeverDispatches(t *testing.T) {
	s := &Channel{cfg: agentconfig.DefaultSlackChannelConfig()}
	s.sendFn = func(context.Context, string, string, string, string, string) error {
		t.Fatal("bot message reached the pool with bots_mode=none")
		return nil
	}
	s.handleMessage(context.Background(), &slackevents.MessageEvent{
		User: "UOTHER", BotID: "BOTHER", Text: "hi", Channel: "D1", ChannelType: "im", TimeStamp: "1.0",
	}, nil)
}
