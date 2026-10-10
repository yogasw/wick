package slack

import (
	"strings"
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// The reply posted at Done names its sources in plain text; the raw tag
// is web-only and must never reach the thread.
func TestFinalReplyRewritesSourceTags(t *testing.T) {
	prev := agentchannels.SourceNames
	t.Cleanup(func() { agentchannels.SourceNames = prev })
	agentchannels.SourceNames = func(string) map[string]string {
		return map[string]string{"alpha": "Alpha", "beta": "Beta"}
	}
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}

	c.NotifyState("slack-t1", "done", "- Logs clean [from:@alpha]\n- Both agree [from:@alpha,@beta]\n- My own point")

	posts := f.posts()
	if len(posts) == 0 {
		t.Fatal("nothing posted")
	}
	var all strings.Builder
	for _, p := range posts {
		all.WriteString(p.Get("text"))
		all.WriteString(p.Get("blocks"))
	}
	got := all.String()
	if strings.Contains(got, "[from:") {
		t.Fatalf("raw source tag leaked: %s", got)
	}
	for _, want := range []string{"Logs clean (from Alpha)", "Both agree (from Alpha, Beta)", "My own point"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

func postedText(f *fakeSlack) string {
	var all strings.Builder
	for _, p := range f.posts() {
		all.WriteString(p.Get("text"))
		all.WriteString(p.Get("blocks"))
	}
	return all.String()
}

// A turn that ended with blocked commands posts the agent's text too; it
// must not carry the raw tag either.
func TestBlockedReplyRewritesSourceTags(t *testing.T) {
	prev := agentchannels.SourceNames
	t.Cleanup(func() { agentchannels.SourceNames = prev })
	agentchannels.SourceNames = func(string) map[string]string { return map[string]string{"alpha": "Alpha"} }
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}

	c.NotifyState("slack-t1", "blocked", "- x [from:@alpha]\n- y [from:@ghost]")

	got := postedText(f)
	if strings.Contains(got, "[from:") || strings.Contains(got, "ghost") {
		t.Fatalf("raw tag or unknown handle leaked: %s", got)
	}
	if !strings.Contains(got, "x (from Alpha)") {
		t.Fatalf("source not named: %s", got)
	}
}

// A name comes from the roster (another owner's shared agent included),
// so it is escaped: it must not become a <!channel> ping or a link.
func TestReplySourceNameCannotPing(t *testing.T) {
	prev := agentchannels.SourceNames
	t.Cleanup(func() { agentchannels.SourceNames = prev })
	agentchannels.SourceNames = func(string) map[string]string { return map[string]string{"evil": "<!channel>"} }
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}

	c.NotifyState("slack-t1", "done", "- x [from:@evil]")

	got := postedText(f)
	if strings.Contains(got, "<!channel>") {
		t.Fatalf("unescaped name posted: %s", got)
	}
	if !strings.Contains(got, "&lt;!channel&gt;") {
		t.Fatalf("escaped name missing: %s", got)
	}
}

// The roster is read once per turn, not on every live edit, and read
// again for the next turn.
func TestSourceNamesResolvedOncePerTurn(t *testing.T) {
	prev := agentchannels.SourceNames
	t.Cleanup(func() { agentchannels.SourceNames = prev })
	calls := 0
	agentchannels.SourceNames = func(string) map[string]string {
		calls++
		return map[string]string{"alpha": "Alpha"}
	}
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}
	for i := 0; i < 3; i++ {
		if got := c.rewriteSources("slack-t1", "- x [from:@alpha]"); got != "- x (from Alpha)" {
			t.Fatalf("got %q", got)
		}
	}
	if calls != 1 {
		t.Fatalf("roster read %d times during one turn, want 1", calls)
	}
	c.NotifyState("slack-t1", "done", "- x [from:@alpha]")
	if calls != 1 {
		t.Fatalf("Done re-read the roster: %d", calls)
	}
	c.rewriteSources("slack-t1", "- y [from:@alpha]")
	if calls != 2 {
		t.Fatalf("next turn must read the roster again, got %d", calls)
	}
}
