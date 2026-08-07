package slack

import (
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/event"
)

// A finished turn keeps its entry (threadTS/msgTS stay needed), so presence in
// the map must NOT read as "still working" — otherwise a sub-agent's progress
// gets relayed onto a thread whose conversation ended.
func TestHasLiveTurnFalseAfterDone(t *testing.T) {
	c := &Channel{turns: map[string]*turn{"slack-123": {running: true}}}
	if !c.HasLiveTurn("slack-123") {
		t.Fatal("in-flight turn should be live")
	}

	c.OnAgentEvent("slack-123", event.AgentEvent{Type: event.Done})

	if c.HasLiveTurn("slack-123") {
		t.Fatal("turn must not report live after Done")
	}
	c.mu.Lock()
	_, stillThere := c.turns["slack-123"]
	c.mu.Unlock()
	if !stillThere {
		t.Fatal("entry must survive Done — threadTS/msgTS are still needed")
	}
}

func TestHasLiveTurnFalseAfterError(t *testing.T) {
	c := &Channel{turns: map[string]*turn{"slack-123": {running: true}}}
	c.OnAgentEvent("slack-123", event.AgentEvent{Type: event.Error, ErrorMsg: "boom"})
	if c.HasLiveTurn("slack-123") {
		t.Fatal("turn must not report live after Error")
	}
}

// ── [silent] marker stripping ────────────────────────────────────────────
// The marker is plumbing and must never be shown. The web UI already strips it
// for the conversation view; Slack has to match rather than invent its own rule.

func TestStripSilentMarker(t *testing.T) {
	cases := []struct{ in, want string }{
		{"[silent] run 3/5 ok", "run 3/5 ok"},
		{"[SILENT] upper", "upper"},
		{"  [silent]   padded", "padded"},
		{"\n[silent] leading newline", "leading newline"},
		{"no marker here", "no marker here"},
		// Only a LEADING marker is plumbing. One mid-text is the agent talking
		// about the marker, and rewriting that would corrupt the message.
		{"see the [silent] marker docs", "see the [silent] marker docs"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := stripSilentMarker(tc.in); got != tc.want {
			t.Errorf("stripSilentMarker(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ── detached survivor notice ─────────────────────────────────────────────

func TestDetachedNoticeBodyNamesEachSurvivor(t *testing.T) {
	got := detachedNoticeBody([]agentchannels.DetachedSurvivor{
		{Handle: "researcher-1", ProfileKey: "researcher"},
	})
	want := "Agent stopped, but 1 background sub-agent is still running: researcher-1 (researcher).\n" +
		"Progress continues below; results arrive when each finishes."
	if got != want {
		t.Fatalf("body =\n%q\nwant\n%q", got, want)
	}
}

func TestDetachedNoticeBodyPluralises(t *testing.T) {
	got := detachedNoticeBody([]agentchannels.DetachedSurvivor{
		{Handle: "researcher-1", ProfileKey: "researcher"},
		{Handle: "coder-1", ProfileKey: "coder"},
	})
	if want := "Agent stopped, but 2 background sub-agents are still running: " +
		"researcher-1 (researcher), coder-1 (coder).\n" +
		"Progress continues below; results arrive when each finishes."; got != want {
		t.Fatalf("body =\n%q\nwant\n%q", got, want)
	}
}

// An empty list rewrites the notice instead of leaving it claiming work is
// still in flight.
func TestDetachedNoticeBodyEmptyMeansFinished(t *testing.T) {
	got := detachedNoticeBody(nil)
	if want := "Agent stopped. Its background sub-agents have finished."; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestDetachedNoticeBodyFallsBackWhenUnnamed(t *testing.T) {
	got := detachedNoticeBody([]agentchannels.DetachedSurvivor{{}})
	if want := "Agent stopped, but 1 background sub-agent is still running: unnamed.\n" +
		"Progress continues below; results arrive when each finishes."; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

// No thread on this instance → nothing to post. Must not panic or invent one.
func TestOnDetachedSurvivorsWithNoTurnIsNoop(t *testing.T) {
	c := &Channel{turns: map[string]*turn{}}
	c.OnDetachedSurvivors("slack-123", []agentchannels.DetachedSurvivor{
		{Handle: "researcher-1", ProfileKey: "researcher"},
	})
	if len(c.turns) != 0 {
		t.Fatalf("must not create a turn, got %d", len(c.turns))
	}
}

// The notice is posted once and then edited, so an unchanged survivor list must
// cost no API call. api is nil here: reaching the send path would panic, which
// is exactly the regression this guards.
func TestOnDetachedSurvivorsSkipsUnchangedBody(t *testing.T) {
	body := detachedNoticeBody([]agentchannels.DetachedSurvivor{
		{Handle: "researcher-1", ProfileKey: "researcher"},
	})
	c := &Channel{turns: map[string]*turn{
		"slack-123": {
			channelID:          "C1",
			threadTS:           "111.222",
			detachedNoticeTS:   "333.444",
			detachedNoticeText: body,
		},
	}}
	c.OnDetachedSurvivors("slack-123", []agentchannels.DetachedSurvivor{
		{Handle: "researcher-1", ProfileKey: "researcher"},
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	if ts := c.turns["slack-123"].detachedNoticeTS; ts != "333.444" {
		t.Fatalf("notice ts changed to %q", ts)
	}
}
