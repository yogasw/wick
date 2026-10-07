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
	"time"

	slackgo "github.com/slack-go/slack"
	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
)

// bannerSlack records chat.postMessage and assistant.threads.setStatus, so a
// test can read both the thread and its banner.
type bannerSlack struct {
	srv    *httptest.Server
	mu     sync.Mutex
	posts  []url.Values
	status []string
}

func newBannerSlack(t *testing.T) *bannerSlack {
	t.Helper()
	f := &bannerSlack{}
	mux := http.NewServeMux()
	record := func(dst *[]url.Values) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			vals, _ := url.ParseQuery(string(body))
			f.mu.Lock()
			*dst = append(*dst, vals)
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"channel":"C1","ts":"1700000000.000200"}`))
		}
	}
	mux.HandleFunc("/chat.postMessage", record(&f.posts))
	mux.HandleFunc("/assistant.threads.setStatus", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		st := ""
		// The client sends JSON; pull the status field out without caring
		// about the rest of the shape.
		if i := strings.Index(string(body), `"status":"`); i >= 0 {
			rest := string(body)[i+len(`"status":"`):]
			st = rest[:strings.Index(rest, `"`)]
		} else if vals, err := url.ParseQuery(string(body)); err == nil {
			st = vals.Get("status")
		}
		f.mu.Lock()
		f.status = append(f.status, st)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *bannerSlack) client() *slackgo.Client {
	return slackgo.New("xoxb-test", slackgo.OptionAPIURL(f.srv.URL+"/"))
}

func (f *bannerSlack) snapshot() ([]url.Values, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.posts...), append([]string(nil), f.status...)
}

func (f *bannerSlack) lastStatus() string {
	_, st := f.snapshot()
	if len(st) == 0 {
		return "<none>"
	}
	return st[len(st)-1]
}

const bgSID = "slack-bg1"

func bgChannel(f *bannerSlack, running bool) *Channel {
	return &Channel{
		api:   f.client(),
		turns: map[string]*turn{bgSID: {channelID: "C1", threadTS: "1700000000.000100", running: running}},
	}
}

func stopBanner(c *Channel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.turns {
		t.stopBackgroundBanner()
		c.stopStatusAnimation(t)
	}
}

var fixer = agentchannels.DetachedSurvivor{Handle: "wick-fixer", ProfileKey: "wick-feature-implementer"}

// The leader fired a sub-agent and its turn ended: the banner must come back
// naming the sub-agent, stay up while it runs, and clear once it is done.
func TestBackgroundBannerKeptWhileChildRunsThenCleared(t *testing.T) {
	f := newBannerSlack(t)
	c := bgChannel(f, true)
	t.Cleanup(func() { stopBanner(c) })

	// Reported during the leader's turn: that turn owns the banner.
	c.OnBackgroundAgents(bgSID, []agentchannels.DetachedSurvivor{fixer})
	c.mu.Lock()
	up := c.turns[bgSID].bgTicker != nil
	c.mu.Unlock()
	if up {
		t.Fatal("background banner started while the leader's own turn was running")
	}

	c.OnAgentEvent(bgSID, event.AgentEvent{Type: event.Done})
	if got := f.lastStatus(); !strings.HasPrefix(got, "wick-fixer is working") {
		t.Fatalf("banner after the leader's turn = %q, want it to name the running sub-agent", got)
	}
	c.mu.Lock()
	up = c.turns[bgSID].bgTicker != nil
	c.mu.Unlock()
	if !up {
		t.Fatal("no keep-alive for the background banner; Slack would drop it after ~2 min")
	}

	c.OnBackgroundAgents(bgSID, nil)
	if got := f.lastStatus(); got != "" {
		t.Fatalf("banner after the last sub-agent ended = %q, want it cleared", got)
	}
	c.mu.Lock()
	up = c.turns[bgSID].bgTicker != nil
	c.mu.Unlock()
	if up {
		t.Fatal("keep-alive still running after the set emptied")
	}
}

func TestBackgroundBannerNamesSeveral(t *testing.T) {
	got := backgroundBannerText([]agentchannels.DetachedSurvivor{fixer, {Handle: "wick-deployer"}})
	if got != "2 sub-agents working: wick-fixer, wick-deployer" {
		t.Fatalf("banner = %q", got)
	}
	if got := backgroundBannerText([]agentchannels.DetachedSurvivor{{ProfileKey: "researcher"}}); got != "researcher is working" {
		t.Fatalf("handle-less banner = %q, want the role", got)
	}
}

// A new message takes the banner over; when that turn ends with the child
// still running, the background banner is back.
func TestBackgroundBannerHandsOverToNewTurn(t *testing.T) {
	f := newBannerSlack(t)
	c := bgChannel(f, false)
	t.Cleanup(func() { stopBanner(c) })
	c.OnBackgroundAgents(bgSID, []agentchannels.DetachedSurvivor{fixer})

	c.mu.Lock()
	old := c.turns[bgSID]
	nt := &turn{channelID: old.channelID, threadTS: old.threadTS, running: true}
	nt.carryOver(old)
	c.turns[bgSID] = nt
	oldStopped := old.bgTicker == nil
	c.mu.Unlock()
	if !oldStopped {
		t.Fatal("old turn's background keep-alive survived the new turn")
	}

	c.OnAgentEvent(bgSID, event.AgentEvent{Type: event.Done})
	if got := f.lastStatus(); !strings.HasPrefix(got, "wick-fixer is working") {
		t.Fatalf("banner after the new turn ended = %q, want the sub-agent banner back", got)
	}
}

// A set nothing has confirmed for staleActivityAfter is asked for again, so a
// child that died without closing its row does not hold the banner.
func TestBackgroundBannerStaleRecheckClears(t *testing.T) {
	f := newBannerSlack(t)
	c := bgChannel(f, false)
	t.Cleanup(func() { stopBanner(c) })
	asked := make(chan struct{}, 1)
	c.SetBackgroundRecheck(func(ctx context.Context, sid string) ([]agentchannels.DetachedSurvivor, bool) {
		select {
		case asked <- struct{}{}:
		default:
		}
		return nil, true
	})
	c.OnBackgroundAgents(bgSID, []agentchannels.DetachedSurvivor{fixer})
	c.mu.Lock()
	c.turns[bgSID].bgCheckedAt = time.Now().Add(-2 * staleActivityAfter)
	c.mu.Unlock()

	select {
	case <-asked:
	case <-time.After(3 * statusAnimInterval):
		t.Fatal("stale background set was never re-checked")
	}
	deadline := time.Now().Add(2 * time.Second)
	for f.lastStatus() != "" {
		if time.Now().After(deadline) {
			t.Fatalf("banner = %q after the re-check found nothing, want cleared", f.lastStatus())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// One ping per background start, carrying who and what.
func TestBackgroundStartPostsOnce(t *testing.T) {
	f := newBannerSlack(t)
	c := bgChannel(f, true)

	c.OnBackgroundStart(bgSID, fixer, "fix the <@U123> banner", false)

	posts, _ := f.snapshot()
	if len(posts) != 1 {
		t.Fatalf("posted %d messages, want exactly 1", len(posts))
	}
	if got := posts[0].Get("thread_ts"); got != "1700000000.000100" {
		t.Errorf("ping posted to thread_ts %q, want the session's thread", got)
	}
	text := posts[0].Get("text")
	if !strings.Contains(text, "🔧 wick-fixer started: fix the") {
		t.Errorf("ping = %q, want the start line", text)
	}
	if strings.Contains(text, "<@U123>") {
		t.Errorf("ping = %q still carries a live mention", text)
	}
	if got := backgroundStartText(fixer, "", true); got != "⏳ wick-fixer queued" {
		t.Errorf("queued ping = %q", got)
	}
}

// Foreground delegations never reach the channel hooks (the delegation layer
// only announces async runs); the opt-out silences both behaviours.
func TestBackgroundStatusOptOut(t *testing.T) {
	f := newBannerSlack(t)
	c := bgChannel(f, false)
	c.cfg = agentconfig.SlackChannelConfig{HideSubAgentStatus: true}
	t.Cleanup(func() { stopBanner(c) })

	c.OnBackgroundStart(bgSID, fixer, "task", false)
	c.OnBackgroundAgents(bgSID, []agentchannels.DetachedSurvivor{fixer})

	posts, st := f.snapshot()
	if len(posts) != 0 || len(st) != 0 {
		t.Fatalf("opted-out channel posted %d messages and %d banners, want none", len(posts), len(st))
	}
}
