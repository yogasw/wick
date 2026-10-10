package slack

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	slackgo "github.com/slack-go/slack"

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

// A question's "[options: …]" line reaches the thread as plain choices,
// never as the raw line.
func TestReplyRewritesOptionsLine(t *testing.T) {
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}

	c.NotifyState("slack-t1", "done", "Which env?\n[options: prod | staging]")

	got := postedText(f)
	if strings.Contains(got, "[options:") || !strings.Contains(got, "Options: prod / staging") {
		t.Fatalf("options line not rewritten: %s", got)
	}
}

// While a question streams in, its options line is held back until it
// closes, then edited in as plain choices: the raw line never shows.
func TestLiveEditHoldsOpenOptionsLine(t *testing.T) {
	f := newMDFakeSlack(t)
	tr := &turn{channelID: "C1", threadTS: "1700000000.000100", running: true}
	tr.buf.WriteString("Which env?\n[options: prod | sta")
	c := newMDChannel(f, tr)

	c.flushLiveMessage("slack-t1")
	tr.buf.WriteString("ging]")
	c.flushLiveMessage("slack-t1")

	calls := f.got()
	if len(calls) != 2 {
		t.Fatalf("want post then update, got %+v", calls)
	}
	first := calls[0].vals.Get("text") + calls[0].vals.Get("blocks")
	if strings.Contains(first, "[options") || strings.Contains(first, "prod") || !strings.Contains(first, "Which env?") {
		t.Fatalf("open options line leaked into the live post: %s", first)
	}
	last := calls[1].vals.Get("text") + calls[1].vals.Get("blocks")
	if strings.Contains(last, "[options") || !strings.Contains(last, "Options: prod / staging") {
		t.Fatalf("closed options line not rewritten: %s", last)
	}
}

// The background start ping quotes the leader's brief, which may carry
// source tags and an options line: neither reaches the thread raw, and an
// unknown handle is dropped.
func TestBackgroundStartRewritesSourceTags(t *testing.T) {
	prev := agentchannels.SourceNames
	t.Cleanup(func() { agentchannels.SourceNames = prev })
	agentchannels.SourceNames = func(string) map[string]string {
		return map[string]string{"alpha": "Alpha", "evil": "A&B <!channel>"}
	}
	f := newBannerSlack(t)
	c := bgChannel(f, true)

	c.OnBackgroundStart(bgSID, fixer, "check logs [from:@alpha] and retry [from:@ghost] then ask [from:@evil]\n[options: prod | staging]", false)

	posts, _ := f.snapshot()
	if len(posts) != 1 {
		t.Fatalf("posted %d messages, want exactly 1", len(posts))
	}
	got := posts[0].Get("text")
	if strings.Contains(got, "[from:") || strings.Contains(got, "[options:") || strings.Contains(got, "ghost") {
		t.Fatalf("raw tag, options line or unknown handle leaked: %s", got)
	}
	// The post escapes the body once; the name must not be escaped again.
	if strings.Contains(got, "<!channel>") || strings.Contains(got, "&amp;amp;") || strings.Contains(got, "&amp;lt;") {
		t.Fatalf("name posted raw or escaped twice: %s", got)
	}
	for _, want := range []string{"check logs (from Alpha) and retry", "then ask (from A&amp;B &lt;!channel&gt;)", "Options: prod / staging"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

// A system DM has no Team roster: a stray source tag is dropped whole
// and an options line is posted as plain text.
func TestSendDirectStripsSourceTags(t *testing.T) {
	var (
		mu     sync.Mutex
		posted url.Values
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/conversations.open", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":{"id":"D1"}}`))
	})
	mux.HandleFunc("/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		mu.Lock()
		posted = vals
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"D1","ts":"1700000000.000200"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := &Channel{api: slackgo.New("xoxb-test", slackgo.OptionAPIURL(srv.URL+"/"))}

	if err := c.SendDirect(context.Background(), "U1", "Approved [from:@alpha]\n[options: ok | later]"); err != nil {
		t.Fatalf("SendDirect: %v", err)
	}
	mu.Lock()
	got := posted.Get("text")
	mu.Unlock()
	if strings.Contains(got, "[from:") || strings.Contains(got, "alpha") || strings.Contains(got, "[options:") {
		t.Fatalf("raw tag or options line leaked: %s", got)
	}
	if !strings.Contains(got, "Approved") || !strings.Contains(got, "Options: ok / later") {
		t.Fatalf("notice text lost: %s", got)
	}
}
