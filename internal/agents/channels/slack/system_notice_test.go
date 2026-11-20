package slack

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	slackgo "github.com/slack-go/slack"
	"github.com/yogasw/wick/internal/agents/event"
)

// fakeSlack records every chat.postMessage body so a test can assert on what
// the thread would actually receive.
type fakeSlack struct {
	srv  *httptest.Server
	mu   sync.Mutex
	post []url.Values
}

func newFakeSlack(t *testing.T) *fakeSlack {
	t.Helper()
	f := &fakeSlack{}
	mux := http.NewServeMux()
	mux.HandleFunc("/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		f.mu.Lock()
		f.post = append(f.post, vals)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C1","ts":"1700000000.000200"}`))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSlack) client() *slackgo.Client {
	return slackgo.New("xoxb-test", slackgo.OptionAPIURL(f.srv.URL+"/"))
}

func (f *fakeSlack) posts() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.post...)
}

// A compaction boundary is context-window bookkeeping. It shows in the web
// UI only; posted into a thread it reads as a stray system line above the
// agent's answer, so even a thread the session is bound to gets nothing.
func TestCompactionPostsNothingIntoThread(t *testing.T) {
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}

	for _, trigger := range []string{"auto", "manual"} {
		c.OnAgentEvent("slack-t1", event.AgentEvent{
			Type: event.Compaction,
			Compaction: &event.CompactionInfo{
				Trigger: trigger, PreTokens: 342500, PostTokens: 12500,
			},
		})
	}

	if n := len(f.posts()); n != 0 {
		t.Fatalf("a compaction must not post into the thread, got %d message(s)", n)
	}
}

// wick's own notice still reaches the thread, as a muted context block with
// Slack control characters escaped.
func TestNoticePostsIntoThread(t *testing.T) {
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}

	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.Notice, Text: "teammate replied <!channel>"})

	posts := f.posts()
	if len(posts) != 1 {
		t.Fatalf("want exactly 1 message posted, got %d", len(posts))
	}
	if got := posts[0].Get("thread_ts"); got != "1700000000.000100" {
		t.Errorf("notice posted to thread_ts %q, want the session's thread", got)
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(posts[0].Get("blocks")), &blocks); err != nil {
		t.Fatalf("blocks not valid JSON: %v (%q)", err, posts[0].Get("blocks"))
	}
	if len(blocks) != 1 || blocks[0]["type"] != "context" {
		t.Fatalf("want a single context block, got %v", blocks)
	}
	body := posts[0].Get("blocks")
	if strings.Contains(body, "<!channel>") || !strings.Contains(body, "teammate replied") {
		t.Errorf("notice text not escaped or missing: %q", body)
	}
}

// A compaction in a session this instance never opened a thread for has
// nowhere to go — it belongs to another channel or to the web UI.
func TestCompactionWithoutThreadPostsNothing(t *testing.T) {
	f := newFakeSlack(t)
	c := &Channel{api: f.client(), turns: map[string]*turn{}}

	c.OnAgentEvent("slack-unknown", event.AgentEvent{
		Type:       event.Compaction,
		Compaction: &event.CompactionInfo{Trigger: "auto", PreTokens: 100, PostTokens: 10},
	})

	if n := len(f.posts()); n != 0 {
		t.Fatalf("want no message for an unbound session, got %d", n)
	}
}

// The reply itself must keep flowing through the normal buffer — a notice
// must not become a second way for turn text to reach the thread.
func TestToolActivityDoesNotPostNotice(t *testing.T) {
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}

	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolResult, Text: "done"})

	if n := len(f.posts()); n != 0 {
		t.Fatalf("tool activity must not post a message, got %d", n)
	}
}
