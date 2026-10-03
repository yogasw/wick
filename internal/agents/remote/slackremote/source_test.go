package slackremote

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/remote"
)

// fakeSlack is a Slack Web API double: it records posts and serves the
// replies the test scripts.
type fakeSlack struct {
	mu      sync.Mutex
	n       int
	posts   []map[string]string
	replies []map[string]any // returned by conversations.replies
	fetches int
	token   string
}

func (f *fakeSlack) nextTS() string {
	f.n++
	return fmt.Sprintf("1700000000.%06d", f.n)
}

func (f *fakeSlack) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
		return
	}
	out := map[string]any{"ok": true}
	switch strings.TrimPrefix(r.URL.Path, "/") {
	case "auth.test":
		out["user_id"], out["bot_id"] = "UWICK", "BWICK"
	case "conversations.open":
		out["channel"] = map[string]any{"id": "D1"}
	case "chat.postMessage":
		ts := f.nextTS()
		f.posts = append(f.posts, map[string]string{"channel": r.Form.Get("channel"), "text": r.Form.Get("text"), "thread_ts": r.Form.Get("thread_ts"), "ts": ts})
		out["ts"] = ts
	case "conversations.replies", "conversations.history":
		f.fetches++
		out["messages"] = f.replies
	default:
		out = map[string]any{"ok": false, "error": "unknown_method"}
	}
	_ = json.NewEncoder(w).Encode(out)
}

// reply scripts a message from user, ts after every post so far.
func (f *fakeSlack) reply(user, text string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ts := f.nextTS()
	thread := ""
	if len(f.posts) > 0 {
		thread = f.posts[0]["ts"]
	}
	f.replies = append(f.replies, map[string]any{"ts": ts, "thread_ts": thread, "user": user, "text": text})
	return ts
}

func (f *fakeSlack) lastToken(t *testing.T) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := regexp.MustCompile(`END RESPONSE ([0-9a-f]+)$`).FindStringSubmatch(f.posts[len(f.posts)-1]["text"])
	if m == nil {
		t.Fatalf("no marker in %q", f.posts[len(f.posts)-1]["text"])
	}
	return m[1]
}

func setup(t *testing.T, cfg Config) (*fakeSlack, *Source, *Router) {
	t.Helper()
	f := &fakeSlack{token: "xoxb-test"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	old := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = old })
	oldSteps := remote.PullSteps
	remote.PullSteps = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}
	t.Cleanup(func() { remote.PullSteps = oldSteps })
	rt := NewRouter()
	src := NewSource(cfg, Deps{API: HTTPAPI{Token: f.token}, Router: rt, WickIDs: func() []string { return []string{"UOTHERWICK"} }})
	return f, src, rt
}

type result struct {
	text string
	line map[string]any
}

// start spawns src with one message; the returned func waits for the
// turn's text and closing line.
func start(t *testing.T, src *Source, msg string) func() result {
	t.Helper()
	p, err := remote.Spawner{Source: src}.Spawn(context.Background(), provider.SpawnOptions{InitialMessage: msg, SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// Wait lets the turn's End finish writing state before TempDir goes.
	t.Cleanup(func() { _ = p.Kill(); _ = p.Wait() })
	ch := make(chan result, 1)
	go func() {
		sc := bufio.NewScanner(p.Stdout())
		var sb strings.Builder
		for sc.Scan() {
			var l map[string]any
			_ = json.Unmarshal(sc.Bytes(), &l)
			switch l["type"] {
			case "assistant":
				for _, c := range l["message"].(map[string]any)["content"].([]any) {
					sb.WriteString(c.(map[string]any)["text"].(string))
				}
			case "result":
				ch <- result{sb.String(), l}
				return
			}
		}
	}()
	return func() result {
		select {
		case r := <-ch:
			return r
		case <-time.After(5 * time.Second):
			t.Fatal("turn did not end")
			return result{}
		}
	}
}

func waitPost(t *testing.T, f *fakeSlack) {
	t.Helper()
	for i := 0; i < 200; i++ {
		f.mu.Lock()
		n := len(f.posts)
		f.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("nothing posted")
}

func TestMarkerEndsTurnAndIsStripped(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetDM, User: "UBOT"})
	wait := start(t, src, "hello")
	waitPost(t, f)
	f.mu.Lock()
	post := f.posts[0]
	f.mu.Unlock()
	if post["channel"] != "D1" || !strings.HasPrefix(post["text"], "hello\n\nWhen your reply is complete") {
		t.Fatalf("post = %+v", post)
	}
	tok := f.lastToken(t)
	f.reply("UBOT", "Hi there.\nEND RESPONSE "+tok)
	r := wait()
	if r.text != "Hi there." || r.line["result"] != "Hi there." || r.line["is_error"] != false {
		t.Fatalf("result = %+v", r)
	}
	if strings.Contains(r.text, "END RESPONSE") {
		t.Fatal("marker leaked")
	}
	// Polling stops once the turn is done.
	f.mu.Lock()
	n := f.fetches
	f.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fetches != n {
		t.Fatalf("polled after done: %d → %d", n, f.fetches)
	}
}

func TestIdleEndsWithoutMarker(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetDM, User: "UBOT"})
	src.cfg.IdleSec = 1
	wait := start(t, src, "hello")
	waitPost(t, f)
	f.reply("UBOT", "I forgot the marker")
	r := wait()
	if r.text != "I forgot the marker" || r.line["remote_note"] != remote.NoteNoMarker {
		t.Fatalf("result = %+v", r)
	}
}

func TestMultiMessageAndLoopGuard(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1", MentionID: "UBOT"})
	wait := start(t, src, "q")
	waitPost(t, f)
	f.mu.Lock()
	post := f.posts[0]
	f.mu.Unlock()
	if !strings.HasPrefix(post["text"], "<@UBOT> q") {
		t.Fatalf("mention missing: %q", post["text"])
	}
	tok := f.lastToken(t)
	f.reply("UWICK", "wick's own echo")
	f.reply("UOTHERWICK", "another wick agent")
	f.reply("USTRANGER", "not the target")
	f.reply("UBOT", "part one")
	f.reply("UBOT", "part two\nEND RESPONSE "+tok)
	r := wait()
	if r.text != "part one\n\npart two" {
		t.Fatalf("text = %q", r.text)
	}
}

func TestPushEditsStream(t *testing.T) {
	f, src, rt := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1"})
	remote.PullSteps = []time.Duration{time.Hour} // events only
	wait := start(t, src, "q")
	waitPost(t, f)
	for rt.Waiting() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	tok := f.lastToken(t)
	f.mu.Lock()
	thread := f.posts[0]["ts"]
	f.mu.Unlock()
	rt.Dispatch(Message{Channel: "C1", TS: "1700000001.000001", ThreadTS: thread, User: "UBOT", Text: "Thinking…"})
	rt.Dispatch(Message{Channel: "C1", TS: "1700000001.000001", ThreadTS: thread, User: "UBOT", Text: "Hello", Edited: true})
	rt.Dispatch(Message{Channel: "C1", TS: "1700000001.000001", ThreadTS: thread, User: "UBOT", Text: "Hello world", Edited: true})
	rt.Dispatch(Message{Channel: "C1", TS: "1700000001.000001", ThreadTS: thread, User: "UBOT", Text: "Hello world\nEND RESPONSE " + tok, Edited: true})
	r := wait()
	if r.text != "Hello world" || r.line["result"] != "Hello world" {
		t.Fatalf("result = %+v", r)
	}
	for i := 0; rt.Waiting() != 0; i++ {
		if i > 200 {
			t.Fatal("turn still routed after it ended")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNextTurnRepliesInThread(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1", Marker: new(bool)})
	dir := t.TempDir()
	if _, err := src.Send(context.Background(), remote.Turn{Text: "one", SessionDir: dir}); err != nil {
		t.Fatal(err)
	}
	src.End(remote.Handle{})
	if _, err := src.Send(context.Background(), remote.Turn{Text: "two", SessionDir: dir}); err != nil {
		t.Fatal(err)
	}
	src.End(remote.Handle{})
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.posts[0]["thread_ts"] != "" || f.posts[1]["thread_ts"] != f.posts[0]["ts"] {
		t.Fatalf("posts = %+v", f.posts)
	}
	if strings.Contains(f.posts[1]["text"], "END RESPONSE") {
		t.Fatal("marker off but instruction sent")
	}
}

func TestAccountOwner(t *testing.T) {
	if err := CheckAccountOwner("u1", "u1"); err != nil {
		t.Fatal(err)
	}
	if err := CheckAccountOwner("u2", "u1"); err != ErrNotOwnAccount {
		t.Fatalf("someone else's account allowed: %v", err)
	}
	c := Config{ConnectorID: "c", Identity: IdentityUser, Target: TargetDM, User: "U"}
	if err := c.Normalize(); err == nil {
		t.Fatal("identity user without account accepted")
	}
}

func TestProgressLine(t *testing.T) {
	for in, want := range map[string]bool{"Thinking…": true, "_Bash: ls -la_": true, ":hourglass: Working": true, "Bash is a shell": false, "Hello": false} {
		if _, ok := progressLine(in); ok != want {
			t.Errorf("progressLine(%q) = %v", in, ok)
		}
	}
}

func TestFromEventAndRouterKeys(t *testing.T) {
	if _, ok := FromEvent("C1", "channel_join", "1.1", "", "U", "", "joined", nil); ok {
		t.Fatal("channel_join routed")
	}
	m, ok := FromEvent("C1", "message_changed", "9.9", "", "", "", "", &Message{TS: "1.2", ThreadTS: "1.0", User: "UBOT", Text: "edited"})
	if !ok || m.Channel != "C1" || m.TS != "1.2" || !m.Edited || m.Text != "edited" {
		t.Fatalf("message_changed = %+v %v", m, ok)
	}
	rt := NewRouter()
	tr := newTracker("C1", "1.0", "1.0", "", false, map[string]bool{}, func(Message) bool { return true })
	rt.add(tr)
	rt.Dispatch(Message{Channel: "C2", TS: "1.5", ThreadTS: "1.0", User: "UBOT", Text: "other channel"})
	rt.Dispatch(Message{Channel: "C1", TS: "1.6", ThreadTS: "1.3", User: "UBOT", Text: "other thread"})
	if len(tr.events) != 0 {
		t.Fatalf("foreign messages routed: %d", len(tr.events))
	}
	rt.Dispatch(m)
	if ev := <-tr.events; ev.Kind != remote.EventText || ev.Text != "edited" {
		t.Fatalf("event = %+v", ev)
	}
	rt.remove(tr)
	if rt.Waiting() != 0 {
		t.Fatal("still waiting")
	}
}
