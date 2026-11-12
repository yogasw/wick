package slack

import (
	"strings"
	"testing"
	"time"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/event"
)

// silentRun is what one turn played through the registry gave: the
// thread's posts and the reply held, if any.
type silentRun struct {
	posted, held string
}

// playOrigin plays parts through a registry holding the channel, for a
// turn started as tr says (human, quiet, untyped, …). "" is a tool call;
// "!failover" a provider failover. It returns what the thread got.
func playOrigin(t *testing.T, tr turn, parts ...string) silentRun {
	t.Helper()
	f := newFakeSlack(t)
	tr.channelID, tr.threadTS, tr.running = "C1", "1700000000.000100", true
	c := &Channel{api: f.client(), turns: map[string]*turn{"slack-t1": &tr}}
	heldCh := make(chan string, 1)
	c.SetReplyHeldFn(func(_, text string) { heldCh <- text })
	reg := agentchannels.NewRegistry()
	reg.Add(c, nil)
	for i, p := range parts {
		switch p {
		case "":
			id := string(rune('a' + i))
			reg.DispatchAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolUse, ToolName: "Bash", ToolInput: `{"command":"ls"}`, ToolUseID: id})
			reg.DispatchAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolResult, ToolUseID: id, Text: "ok"})
		case "!failover":
			reg.DispatchAgentEvent("slack-t1", event.AgentEvent{Type: event.Warning, ErrorMsg: "a stopped", FailedOver: true})
		default:
			reg.DispatchAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: p})
		}
	}
	reg.DispatchAgentEvent("slack-t1", event.AgentEvent{Type: event.Done})
	run := silentRun{posted: postedText(f)}
	select {
	case run.held = <-heldCh:
	case <-time.After(200 * time.Millisecond):
	}
	return run
}

const report = "Test 1 passed: room 1001 waits for an agent.\n• step one\n• step two"

var (
	humanTurn   = turn{human: true}
	quietTurn   = turn{human: true, quiet: true}
	untypedRun  = turn{untyped: true}
	listenReply = turn{} // a listen reply: typed, not a person's mention
)

// A person's turn always posts, the marker gone, wherever it sits.
func TestSilentHumanTurnAlwaysPosts(t *testing.T) {
	for name, parts := range map[string][]string{
		"no marker":       {report},
		"leading":         {"[silent] " + report},
		"trailing":        {report + "\n\n[silent]"},
		"trailing tools":  {report, "", "\n\n[silent]"},
		"bare after tool": {report, "", "[silent]"},
		"middle":          {"first part\n[silent]\nsecond part"},
		"bold":            {"**[silent]** " + report},
		"bold trailing":   {report + "\n**[silent]**"},
	} {
		got := playOrigin(t, humanTurn, parts...)
		if got.held != "" || strings.Contains(strings.ToLower(got.posted), "[silent]") {
			t.Errorf("%s: posted %q held %q", name, got.posted, got.held)
		}
		want := "step two"
		if name == "middle" {
			want = "second part"
		}
		if !strings.Contains(got.posted, want) {
			t.Errorf("%s: posted %q, want it to hold %q", name, got.posted, want)
		}
	}
}

// A person who asked for no reply gets none when the agent used the
// marker, at the start or at the end; the web UI still has the reply
// (the registry never gates it, see TestSilentReplySuppressedFromChannel).
func TestSilentHumanAskedQuiet(t *testing.T) {
	for name, parts := range map[string][]string{
		"leading":  {"[silent] noted"},
		"trailing": {report + "\n[silent]"},
		"tools":    {report, "", "[silent]"},
	} {
		if got := playOrigin(t, quietTurn, parts...); got.posted != "" || got.held != "" {
			t.Errorf("%s: posted %q held %q", name, got.posted, got.held)
		}
	}
	// Asked for quiet, but the agent answered without the marker: posted.
	if got := playOrigin(t, quietTurn, "Done, nothing to add."); !strings.Contains(got.posted, "Done, nothing") {
		t.Errorf("unmarked: posted %q", got.posted)
	}
}

// A turn nobody typed: a leading marker stays silent, a report with the
// marker after it is held (never posted, never lost), a narration that
// closes on the marker posts nothing (6a14faef).
func TestSilentInternalTurn(t *testing.T) {
	cases := []struct {
		name  string
		parts []string
		held  bool
	}{
		{"marker only", []string{"[silent]"}, false},
		{"marker with trace", []string{"[silent] run 3/5 ok", "", "still nothing"}, false},
		{"report then marker", []string{report + "\n\n[silent]"}, true},
		{"report, tools, marker", []string{report, "", "", "\n\n[silent]"}, true},
		{"report, tools, bare marker", []string{report, "", "[silent]"}, true},
		{"narration then marker", []string{"Now the docs.", "", "\n\n[silent]"}, false},
		{"narration then bare marker", []string{"Now the docs.", "", "[silent]"}, false},
	}
	for _, tc := range cases {
		got := playOrigin(t, untypedRun, tc.parts...)
		if got.posted != "" {
			t.Errorf("%s: posted %q", tc.name, got.posted)
		}
		if tc.held != (got.held != "") {
			t.Errorf("%s: held %q, want held=%v", tc.name, got.held, tc.held)
		}
		if tc.held && (!strings.Contains(got.held, "step two") || strings.Contains(got.held, "[silent]")) {
			t.Errorf("%s: held %q", tc.name, got.held)
		}
	}
	// No marker at all: posted as before.
	if got := playOrigin(t, untypedRun, report); !strings.Contains(got.posted, "step two") || got.held != "" {
		t.Errorf("unmarked: %+v", got)
	}
}

// A listen reply (no mention) that opens with the marker stays silent.
func TestSilentListenTurn(t *testing.T) {
	if got := playOrigin(t, listenReply, "[silent]"); got.posted != "" || got.held != "" {
		t.Fatalf("got %+v", got)
	}
}

// After a provider failover the same rules hold: the failed provider's
// text is gone and the new one's reply is decided afresh.
func TestSilentRulesAfterFailover(t *testing.T) {
	got := playOrigin(t, humanTurn, "You've hit your limit\n", "!failover", "[silent] "+report)
	if !strings.Contains(got.posted, "step two") || strings.Contains(got.posted, "[silent]") || strings.Contains(got.posted, "hit your limit") {
		t.Errorf("human: %+v", got)
	}
	got = playOrigin(t, untypedRun, "partial\n", "!failover", report, "", "[silent]")
	if got.posted != "" || !strings.Contains(got.held, "step two") {
		t.Errorf("internal held: %+v", got)
	}
	got = playOrigin(t, untypedRun, "partial\n", "!failover", "[silent] ok")
	if got.posted != "" || got.held != "" {
		t.Errorf("internal leading: %+v", got)
	}
}

// What a person wrote decides whether they asked for no reply.
func TestAsksQuiet(t *testing.T) {
	for in, want := range map[string]bool{
		"@bot just [silent] please":  true,
		"stay SILENT on this":        true,
		"diam aja ya":                true,
		"jangan dibalas":             true,
		"ga usah dibalas":            true,
		"don't reply here":           true,
		"can you check room 1001?":   false,
		"why did the bot not reply?": false,
	} {
		if got := asksQuiet(in); got != want {
			t.Errorf("asksQuiet(%q) = %v, want %v", in, got, want)
		}
	}
}

// An emphasized marker is stripped anywhere; a plain one mid-sentence is
// still the agent talking about it.
func TestStripEmphasizedSilentMarker(t *testing.T) {
	for in, want := range map[string]string{
		"**[silent]** done":        "done",
		"done\n**[silent]**":       "done\n",
		"done **[silent]**":        "done",
		"see the [silent] marker":  "see the [silent] marker",
		"_[SILENT]_ quiet run ok.": "quiet run ok.",
	} {
		if got := stripSilentMarker(in); got != want {
			t.Errorf("stripSilentMarker(%q) = %q, want %q", in, got, want)
		}
	}
}
