package slack

import (
	"strings"
	"testing"
	"time"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
)

func mate(handle string, hidden bool, replied, of int) agentchannels.DetachedSurvivor {
	return agentchannels.DetachedSurvivor{Handle: handle, Teammate: true, Hidden: hidden, Replied: replied, Of: of}
}

func TestBackgroundBannerTextTeammates(t *testing.T) {
	cases := []struct {
		name   string
		agents []agentchannels.DetachedSurvivor
		want   string
	}{
		{"named", []agentchannels.DetachedSurvivor{mate("halodev", false, 1, 3), mate("qa", false, 1, 3)}, "is waiting on @halodev, @qa (1/3 replied)"},
		{"no count", []agentchannels.DetachedSurvivor{mate("halodev", false, 0, 0)}, "is waiting on @halodev"},
		{"hidden = a normal turn", []agentchannels.DetachedSurvivor{mate("halodev", true, 1, 3), mate("qa", true, 1, 3)}, footerWorking},
		{"mixed", []agentchannels.DetachedSurvivor{fixer, mate("qa", false, 0, 1)}, "is waiting on @qa (0/1 replied) · wick-fixer is working"},
		{"hidden + sub-agent", []agentchannels.DetachedSurvivor{fixer, mate("qa", true, 0, 1)}, "wick-fixer is working"},
	}
	for _, tc := range cases {
		if got := backgroundBannerText(tc.agents); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	// The dots animate after the names, never after the count.
	if got := backgroundFooter([]agentchannels.DetachedSurvivor{mate("qa", false, 0, 2)}, 2); got != "is waiting on @qa.. (0/2 replied)" {
		t.Errorf("footer with dots = %q", got)
	}
}

func TestBackgroundStartTextTeammate(t *testing.T) {
	if got := backgroundStartText(mate("halodev", false, 0, 1), "check the logs", false); got != "🤝 @halodev started: check the logs" {
		t.Fatalf("start = %q", got)
	}
}

// With "Show teammates in channels" off, the thread reads like any turn:
// the generic working state, no names, no start ping.
func TestHiddenTeammatesShowGenericStatus(t *testing.T) {
	f := newBannerSlack(t)
	c := bgChannel(f, false)
	t.Cleanup(func() { stopBanner(c) })
	c.OnBackgroundStart(bgSID, mate("halodev", true, 0, 2), "secret brief", false)
	c.OnBackgroundAgents(bgSID, []agentchannels.DetachedSurvivor{mate("halodev", true, 0, 2)})
	posts, st := f.snapshot()
	if len(posts) != 0 {
		t.Fatalf("start ping posted for a hidden teammate: %v", posts)
	}
	got := f.lastStatus()
	if !strings.HasPrefix(got, footerWorking) || strings.Contains(got, "halodev") {
		t.Fatalf("status = %q (all %q), want the generic working state", got, st)
	}
	c.OnBackgroundAgents(bgSID, nil)
	if got := f.lastStatus(); got != "" {
		t.Fatalf("status after the teammate settled = %q, want cleared", got)
	}
}

// Teammates are not sub-agents: hiding sub-agent status leaves them shown.
func TestTeammatesShownWithSubAgentStatusHidden(t *testing.T) {
	f := newBannerSlack(t)
	c := bgChannel(f, false)
	c.cfg = agentconfig.SlackChannelConfig{HideSubAgentStatus: true}
	t.Cleanup(func() { stopBanner(c) })
	c.OnBackgroundAgents(bgSID, []agentchannels.DetachedSurvivor{fixer, mate("qa", false, 0, 1)})
	if got := f.lastStatus(); got != "is waiting on @qa (0/1 replied)" {
		t.Fatalf("status = %q", got)
	}
	// Only the hidden sub-agent left: nothing to show, banner cleared.
	c.OnBackgroundAgents(bgSID, []agentchannels.DetachedSurvivor{fixer})
	if got := f.lastStatus(); got != "" {
		t.Fatalf("status = %q, want cleared", got)
	}
	c.mu.Lock()
	up := c.turns[bgSID].bgTicker != nil
	c.mu.Unlock()
	if up {
		t.Fatal("keep-alive left running with nothing shown")
	}
}

// After a restart there is no turn: the set rebinds the stored thread and
// the banner shows (and keeps itself alive) without a reply in flight.
func TestBackgroundSetRebindsThreadAfterRestart(t *testing.T) {
	f := newBannerSlack(t)
	sess := &sessionsWithBinding{
		binding: agentchannels.ThreadBinding{Channel: "slack", ChatID: "C1", ThreadID: "1700000000.000100"},
		has:     true,
	}
	key := "slack-owner-1700000000.000100"
	c := &Channel{api: f.client(), turns: map[string]*turn{}, sessions: sess, sessionPrefix: "slack-owner-"}
	t.Cleanup(func() { stopBanner(c) })
	c.OnBackgroundAgents(key, []agentchannels.DetachedSurvivor{mate("halodev", false, 0, 1)})
	if got := f.lastStatus(); got != "is waiting on @halodev (0/1 replied)" {
		t.Fatalf("status after rebind = %q", got)
	}
	c.mu.Lock()
	tr := c.turns[key]
	up := tr != nil && tr.bgTicker != nil && !tr.running
	c.mu.Unlock()
	if !up {
		t.Fatal("rebound turn has no keep-alive, or reads as running")
	}
	// A DM-style turn end keeps the set: Done hands the banner back.
	c.OnAgentEvent(key, event.AgentEvent{Type: event.Done})
	if !c.hasBackgroundAgents(key) {
		t.Fatal("teammate dropped from the set on turn end")
	}
}

// Unbound session: an empty probe, no turn is made.
func TestBackgroundSetUnboundNoop(t *testing.T) {
	f := newBannerSlack(t)
	c := &Channel{api: f.client(), turns: map[string]*turn{}, sessions: &sessionsWithBinding{}, sessionPrefix: "slack-owner-"}
	c.OnBackgroundAgents("slack-owner-1", []agentchannels.DetachedSurvivor{mate("halodev", false, 0, 1)})
	if len(c.turns) != 0 || f.lastStatus() != "<none>" {
		t.Fatalf("unbound session got a turn/status: %v %q", c.turns, f.lastStatus())
	}
}

// R2: with only teammates working the banner is not rewritten every tick;
// it is kept alive on the keep-alive, and a set change repaints at once.
// Tick and keep-alive are the channel's own (set under its lock), so the
// banner goroutine reads them race-free.
func TestTeammateOnlyBannerThrottled(t *testing.T) {
	f := newBannerSlack(t)
	c := bgChannel(f, false)
	c.bgTick, c.bgKeepAlive = 20*time.Millisecond, time.Hour
	t.Cleanup(func() { stopBanner(c) })
	c.OnBackgroundAgents(bgSID, []agentchannels.DetachedSurvivor{mate("halodev", false, 0, 1)})
	time.Sleep(150 * time.Millisecond) // several ticks
	if _, st := f.snapshot(); len(st) != 1 {
		t.Fatalf("teammate-only banner written %d times, want 1: %q", len(st), st)
	}
	// Keep-alive due: the next tick writes it again.
	c.mu.Lock()
	c.bgKeepAlive = time.Millisecond
	c.mu.Unlock()
	waitStatuses(t, f, 2)
	c.mu.Lock()
	c.bgKeepAlive = time.Hour
	c.mu.Unlock()
	time.Sleep(60 * time.Millisecond) // let an in-flight tick land
	// A set change repaints at once, whatever the keep-alive.
	_, before := f.snapshot()
	c.OnBackgroundAgents(bgSID, []agentchannels.DetachedSurvivor{mate("halodev", false, 1, 2), mate("qa", false, 1, 2)})
	if _, st := f.snapshot(); len(st) != len(before)+1 || st[len(st)-1] != "is waiting on @halodev, @qa (1/2 replied)" {
		t.Fatalf("set change not painted at once: %q", st)
	}
	// A sub-agent brings the per-tick animation back.
	c.OnBackgroundAgents(bgSID, []agentchannels.DetachedSurvivor{fixer, mate("qa", false, 1, 2)})
	_, mid := f.snapshot()
	waitStatuses(t, f, len(mid)+2)
}

// waitStatuses waits until at least n statuses were written.
func waitStatuses(t *testing.T, f *bannerSlack, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, st := f.snapshot(); len(st) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, st := f.snapshot()
	t.Fatalf("got %d statuses, want ≥ %d: %q", len(st), n, st)
}
