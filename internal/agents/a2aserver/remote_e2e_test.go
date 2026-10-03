package a2aserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yogasw/wick/internal/agents/a2aremote"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/remote"
	"github.com/yogasw/wick/internal/agents/remote/slackremote"
)

// wirePool stands in for the wick pool: every user turn spawns the
// session's spawner (a remote adapter) and feeds its stdout, parsed the
// way a claude spawn is, back into the server. It returns the dispatch
// count.
func wirePool(t *testing.T, f *fixture, spawnerFor func(sid string) provider.Spawner) *atomic.Int32 {
	t.Helper()
	base := t.TempDir()
	var wg sync.WaitGroup
	// Registered after TempDir, so it runs first: every turn has written
	// its state before the dir goes.
	t.Cleanup(wg.Wait)
	var n atomic.Int32
	var mu sync.Mutex
	dirs := map[string]string{}
	f.srv.SetSendFunc(func(_ context.Context, sid, _, _, role, text string) error {
		if role != "user" {
			return nil
		}
		n.Add(1)
		mu.Lock()
		dir, ok := dirs[sid]
		if !ok {
			var err error
			if dir, err = os.MkdirTemp(base, "s"); err != nil {
				mu.Unlock()
				return err
			}
			dirs[sid] = dir
		}
		mu.Unlock()
		p, err := spawnerFor(sid).Spawn(context.Background(), provider.SpawnOptions{InitialMessage: text, SessionDir: dir, SessionID: sid})
		if err != nil {
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = p.Kill(); _ = p.Wait() }()
			parser := event.NewClaudeParser()
			sc := bufio.NewScanner(p.Stdout())
			sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
			for sc.Scan() {
				evs, _ := event.ParseLine(parser, sc.Text())
				for _, ev := range evs {
					f.srv.OnAgentEvent(sid, ev)
					if ev.Type == event.Done || ev.Type == event.Error {
						return
					}
				}
			}
		}()
		return nil
	})
	return &n
}

// stream sends text over a streaming call and returns the streamed
// artifact text and the final task state with its message.
func stream(t *testing.T, f *fixture, text string) (string, a2a.TaskState, string) {
	t.Helper()
	c := f.client(t, f.key)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var sb strings.Builder
	var state a2a.TaskState
	var msg string
	for ev, err := range c.SendStreamingMessage(ctx, &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))}) {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		switch e := ev.(type) {
		case *a2a.TaskArtifactUpdateEvent:
			for _, p := range e.Artifact.Parts {
				sb.WriteString(p.Text())
			}
		case *a2a.TaskStatusUpdateEvent:
			state = e.Status.State
			msg = messageText(e.Status.Message)
		}
	}
	return sb.String(), state, msg
}

// slackDouble is a Slack Web API double whose remote agent answers every
// post with reply and the post's end marker.
type slackDouble struct {
	mu      sync.Mutex
	n       int
	posts   []string
	replies []map[string]any
	reply   string
}

var markerRE = regexp.MustCompile(`END RESPONSE ([0-9a-f]+)$`)

func (d *slackDouble) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	d.mu.Lock()
	defer d.mu.Unlock()
	next := func() string { d.n++; return fmt.Sprintf("1700000000.%06d", d.n) }
	out := map[string]any{"ok": true}
	switch strings.TrimPrefix(r.URL.Path, "/") {
	case "auth.test":
		out["user_id"], out["bot_id"] = "UWICK", "BWICK"
	case "conversations.open":
		out["channel"] = map[string]any{"id": "D1"}
	case "chat.postMessage":
		ts := next()
		text := r.Form.Get("text")
		d.posts = append(d.posts, text)
		out["ts"] = ts
		thread := r.Form.Get("thread_ts")
		if thread == "" {
			thread = ts
		}
		if m := markerRE.FindStringSubmatch(text); m != nil {
			d.replies = append(d.replies, map[string]any{"ts": next(), "thread_ts": thread, "user": "UBOT", "text": d.reply + "\nEND RESPONSE " + m[1]})
		}
	case "conversations.replies", "conversations.history":
		out["messages"] = d.replies
	default:
		out = map[string]any{"ok": false, "error": "unknown_method"}
	}
	_ = json.NewEncoder(w).Encode(out)
}

// A2A caller → wick A2A server → pool → Slack remote adapter → Slack and
// back: the remote's reply streams out as the task's artifact.
func TestRemoteSlackAgentOverA2A(t *testing.T) {
	d := &slackDouble{reply: "Pong from Slack."}
	api := httptest.NewServer(d)
	t.Cleanup(api.Close)
	oldBase, oldSteps := slackremote.BaseURL, remote.PullSteps
	slackremote.BaseURL = api.URL
	remote.PullSteps = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	t.Cleanup(func() { slackremote.BaseURL, remote.PullSteps = oldBase, oldSteps })

	f := newFixture(t)
	rt := slackremote.NewRouter()
	wirePool(t, f, func(string) provider.Spawner {
		return remote.Spawner{Source: slackremote.NewSource(
			slackremote.Config{ConnectorID: "c", Target: slackremote.TargetDM, User: "UBOT"},
			slackremote.Deps{API: slackremote.HTTPAPI{Token: "xoxb-test"}, Router: rt, WickIDs: func() []string { return nil }},
		)}
	})

	text, state, msg := stream(t, f, "ping")
	if text != "Pong from Slack." || state != a2a.TaskStateCompleted || msg != "Pong from Slack." {
		t.Fatalf("text=%q state=%v msg=%q", text, state, msg)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.posts) != 1 || !strings.HasPrefix(d.posts[0], "ping") {
		t.Fatalf("slack posts = %q", d.posts)
	}
}

// An A2A remote agent pointing at its own wick endpoint calls itself; the
// hop count stops it after remote.MaxHops and the failure comes back up.
func TestRemoteLoopIsCut(t *testing.T) {
	f := newFixture(t)
	guard := a2aremote.Guard{Allowed: []string{"127.0.0.1"}}
	auth := a2aremote.PlainAuth{Type: a2aremote.AuthBearer, Secret: f.key}
	res, err := a2aremote.Resolve(context.Background(), guard, f.http.URL+CardPath(agentID), auth)
	if err != nil {
		t.Fatalf("resolve own card: %v", err)
	}
	cfg := a2aremote.Config{CardURL: res.CardURL, CardJSON: res.JSON, TimeoutSec: 10}
	n := wirePool(t, f, func(string) provider.Spawner {
		return a2aremote.Spawner{Runtime: a2aremote.Runtime{Config: cfg, Auth: auth, Guard: guard}}
	})

	_, state, msg := stream(t, f, "hi")
	if state != a2a.TaskStateFailed || !strings.Contains(msg, "wick A2A hops") {
		t.Fatalf("state=%v msg=%q", state, msg)
	}
	// The caller's turn (0 hops) plus one per hop allowed; the next is refused.
	if got := n.Load(); got != remote.MaxHops+1 {
		t.Fatalf("dispatched %d turns, want %d", got, remote.MaxHops+1)
	}
}

func TestInboundHops(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want int
	}{{float64(2), 2}, {3, 3}, {"4", 4}, {nil, 0}, {"x", 0}} {
		if got := remote.HopsOf(tc.v); got != tc.want {
			t.Errorf("HopsOf(%v) = %d, want %d", tc.v, got, tc.want)
		}
	}
	remote.SetHops("s-1", 2)
	if remote.Hops("s-1") != 2 {
		t.Fatal("Hops lost the count")
	}
	remote.SetHops("s-1", 0)
	if remote.Hops("s-1") != 0 {
		t.Fatal("SetHops 0 kept the count")
	}
}
