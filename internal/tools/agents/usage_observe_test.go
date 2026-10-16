package agents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/logintty"
	"github.com/yogasw/wick/internal/agents/provider/savedresets"
)

// observedCache is a clocked cache whose usage file is a counter.
func observedCache() (*usageCache, *fixedClock, *[]provider.UsageDisk) {
	c, clk := clockedCache(time.Minute)
	var saved []provider.UsageDisk
	c.load = func(string) (provider.UsageDisk, bool) { return provider.UsageDisk{}, false }
	c.save = func(d provider.UsageDisk) { saved = append(saved, d) }
	return c, clk, &saved
}

func TestUsageObserveReplacesReadingKeepsProbeState(t *testing.T) {
	c, clk, saved := observedCache()
	resetAt := clk.now().Add(3 * time.Hour).Truncate(time.Second)
	resets := &savedresets.SavedResets{}
	c.store("k", []logintty.UsageWindow{
		{Key: "five_hour", Utilization: 10},
		{Key: "seven_day", Utilization: 20},
		{Key: "seven_day_opus", Utilization: 5},
	}, nil, resets)
	probedAt := clk.now()
	// A later probe was refused with a server cooldown.
	clk.advance(time.Minute)
	c.store("k", nil, &logintty.RateLimitedError{Status: "429", RetryAfter: time.Hour})
	until := c.entries["k"].serverUntil
	attempted := c.entries["k"].attemptedAt
	*saved = nil

	clk.advance(time.Minute)
	if !c.observe("k", []logintty.UsageWindow{
		{Key: "five_hour", Utilization: 39, ResetsAt: resetAt},
		{Key: "seven_day", Utilization: 66},
	}) {
		t.Fatal("first observation was not persisted")
	}
	e := c.entries["k"]
	if e.err != nil || e.fails != 0 {
		t.Fatalf("err/fails = %v/%d, want cleared", e.err, e.fails)
	}
	if !e.goodAt.Equal(clk.now()) {
		t.Fatalf("goodAt = %v, want now", e.goodAt)
	}
	if !e.serverUntil.Equal(until) || !e.attemptedAt.Equal(attempted) {
		t.Fatal("observe touched the probe bookkeeping")
	}
	if e.resets != resets {
		t.Fatal("saved resets dropped")
	}
	got := map[string]logintty.UsageWindow{}
	for _, w := range e.windows {
		got[w.Key] = w
	}
	if got["five_hour"].Utilization != 39 || !got["five_hour"].ResetsAt.Equal(resetAt) || got["seven_day"].Utilization != 66 {
		t.Fatalf("windows = %+v", e.windows)
	}
	opus, ok := got["seven_day_opus"]
	if !ok || opus.Utilization != 5 || !opus.ObservedAt.Equal(probedAt) {
		t.Fatalf("unreported window = %+v, want kept and stamped with its own reading time", opus)
	}
	if len(*saved) != 1 || (*saved)[0].ServerUntil != until || (*saved)[0].SavedResets != resets {
		t.Fatalf("persisted = %+v", *saved)
	}

	// The server's cooldown still blocks Re-check.
	if ok, wait := c.forceRefresh("k", func() ([]logintty.UsageWindow, error) { return nil, nil }); ok || wait <= 0 {
		t.Fatalf("forceRefresh = %v/%v, want refused by the server cooldown", ok, wait)
	}
}

func TestUsageObserveThrottlesUnchangedWrites(t *testing.T) {
	c, clk, saved := observedCache()
	reset := clk.now().Add(time.Hour)
	obs := func(u float64, r time.Time) bool {
		return c.observe("k", []logintty.UsageWindow{{Key: "five_hour", Utilization: u, ResetsAt: r}})
	}
	if !obs(39, reset) {
		t.Fatal("first observation not written")
	}
	clk.advance(10 * time.Second)
	if obs(39, reset) || obs(39.3, reset) {
		t.Fatal("same whole percent and reset rewrote the file")
	}
	if got := c.entries["k"].windows[0].Utilization; got != 39.3 {
		t.Fatalf("memory = %v, want the latest value even when not written", got)
	}
	if !c.entries["k"].goodAt.Equal(clk.now()) {
		t.Fatal("goodAt not refreshed on an unwritten observation")
	}
	if !obs(40, reset) {
		t.Fatal("a new whole percent was not written")
	}
	if !obs(40, reset.Add(5*time.Hour)) {
		t.Fatal("a new reset was not written")
	}
	clk.advance(usageObservePersistEvery)
	if !obs(40, reset.Add(5*time.Hour)) {
		t.Fatal("unchanged reading never rewritten after the persist interval")
	}
	if len(*saved) != 4 {
		t.Fatalf("writes = %d, want 4", len(*saved))
	}
}

func TestUsageObserveRecoversFailedAccount(t *testing.T) {
	c, clk, saved := observedCache()
	c.store("k", nil, errors.New("boom"))
	c.store("k", nil, errors.New("boom"))
	*saved = nil
	clk.advance(time.Second)
	if !c.observe("k", windows(12)) {
		t.Fatal("observation after failures not written")
	}
	v := c.serve(c.entries["k"])
	if !v.Known || v.Err != nil || len(v.Windows) != 1 || v.Windows[0].Utilization != 12 {
		t.Fatalf("served = %+v", v)
	}
	if (*saved)[0].Fails != 0 || (*saved)[0].LastError != "" {
		t.Fatalf("persisted = %+v", (*saved)[0])
	}
}

// A rate_limit_event read off a claude stream lands under the same key
// the providers page reads the instance's usage with.
func TestObserveRateLimitFeedsInstanceUsage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"oauthAccount":{"emailAddress":"observe@example.test"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ins := provider.Instance{Type: provider.TypeClaude, Name: "observe", Env: []string{"CLAUDE_CONFIG_DIR=" + dir}}

	c, _, saved := observedCache()
	prev := usageProbes
	usageProbes = c
	t.Cleanup(func() { usageProbes = prev })

	ev, err := event.NewClaudeParser().Parse(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"five_hour":{"utilization":0.39,"resetsAt":1792134600},"seven_day":{"utilization":0.66,"resetsAt":1792270800}}}}`)
	if err != nil || ev.RateLimit == nil {
		t.Fatalf("parse: %v %+v", err, ev)
	}
	ObserveRateLimit(ins, ev.RateLimit)

	key := logintty.UsageIdentity(ins.Type, provider.AccountEnv(ins))
	if !strings.HasPrefix(key, "claude:email:") {
		t.Fatalf("key = %q", key)
	}
	if len(*saved) != 1 || (*saved)[0].Key != key {
		t.Fatalf("persisted = %+v, want one write under %q", *saved, key)
	}
	v := peekInstanceUsage(ins)
	if !v.Known || len(v.Windows) != 2 || v.Windows[0].Key != "five_hour" || v.Windows[0].Utilization != 39 ||
		v.Windows[1].Key != "seven_day" || v.Windows[1].Utilization != 66 {
		t.Fatalf("instance usage = %+v", v)
	}
	if !v.Windows[1].ResetsAt.Equal(time.Unix(1792270800, 0)) {
		t.Fatalf("seven_day reset = %v", v.Windows[1].ResetsAt)
	}

	// A report with no windows leaves the reading alone.
	ObserveRateLimit(ins, &event.RateLimitInfo{Status: "allowed"})
	if len(*saved) != 1 {
		t.Fatal("empty report was written")
	}
}
