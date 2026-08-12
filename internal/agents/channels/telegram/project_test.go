package telegram

import (
	"context"
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// TestSendCtxCarriesInstanceProject pins the multi-bot invariant: every
// Telegram instance shares one SendFunc closure, so each must stamp its
// own configured project on the dispatch ctx. Without it, two bots with
// different projects both resolve to whichever agent_channels row the
// dispatcher happened to read first.
func TestSendCtxCarriesInstanceProject(t *testing.T) {
	a := New(agentconfig.TelegramChannelConfig{ProjectID: "proj-alpha"})
	b := New(agentconfig.TelegramChannelConfig{ProjectID: "proj-beta"})

	if got := agentchannels.ChannelProject(a.sendCtx(context.Background())); got != "proj-alpha" {
		t.Errorf("instance A project = %q, want proj-alpha", got)
	}
	if got := agentchannels.ChannelProject(b.sendCtx(context.Background())); got != "proj-beta" {
		t.Fatalf("instance B project = %q, want proj-beta — instances share a project (the bug)", got)
	}

	none := New(agentconfig.TelegramChannelConfig{})
	if got := agentchannels.ChannelProject(none.sendCtx(context.Background())); got != "" {
		t.Errorf("unconfigured instance project = %q, want empty", got)
	}
}

// TestSendCtxFollowsReloadedProject proves changing the project in the UI
// takes effect on the next message without restarting the process.
func TestSendCtxFollowsReloadedProject(t *testing.T) {
	ch := New(agentconfig.TelegramChannelConfig{ProjectID: "proj-old"})

	// No bot token, so Reload leaves the channel dormant and performs no
	// network calls — it still swaps the live config, which is the part
	// under test.
	ch.Reload(context.Background(), agentconfig.TelegramChannelConfig{ProjectID: "proj-new"})

	if got := agentchannels.ChannelProject(ch.sendCtx(context.Background())); got != "proj-new" {
		t.Fatalf("project after reload = %q, want proj-new — a restart would be needed", got)
	}
}
