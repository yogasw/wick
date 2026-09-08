package agents

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/logintty"
)

// fakeProbe records which config dirs were asked for so the tests can
// assert the per-account dedup, and returns canned account/usage data.
type fakeProbe struct {
	mu         sync.Mutex
	accountFor map[string]logintty.Account
	usageFor   map[string][]logintty.UsageWindow
	usageErr   map[string]error
	usageCalls []string
}

func (f *fakeProbe) configDir(_ provider.Type, env []string) string {
	return envVal(env, "DIR")
}

func (f *fakeProbe) account(_ provider.Type, env []string) logintty.Account {
	return f.accountFor[envVal(env, "DIR")]
}

func (f *fakeProbe) usage(_ context.Context, _ provider.Type, env []string) ([]logintty.UsageWindow, error) {
	dir := envVal(env, "DIR")
	f.mu.Lock()
	f.usageCalls = append(f.usageCalls, dir)
	f.mu.Unlock()
	if err := f.usageErr[dir]; err != nil {
		return nil, err
	}
	return f.usageFor[dir], nil
}

func envVal(env []string, key string) string {
	for _, kv := range env {
		if len(kv) > len(key)+1 && kv[:len(key)+1] == key+"=" {
			return kv[len(key)+1:]
		}
	}
	return ""
}

func inst(t provider.Type, name, dir string) provider.Instance {
	return provider.Instance{Type: t, Name: name, Env: []string{"DIR=" + dir}}
}

func TestCollectConnectionsOnePerInstance(t *testing.T) {
	reset := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	f := &fakeProbe{
		accountFor: map[string]logintty.Account{
			"/a": {Connected: true, Email: "a@abc.com", Plan: "max", AuthMethod: "Claude AI"},
			"/b": {Connected: true, Email: "b@abc.com"},
		},
		usageFor: map[string][]logintty.UsageWindow{
			"/a": {{Key: "five_hour", Utilization: 42, ResetsAt: reset}},
		},
	}
	instances := []provider.Instance{
		inst(provider.TypeClaude, "enginer", "/a"),
		inst(provider.TypeClaude, "waba", "/b"),
	}

	got := collectConnections(context.Background(), instances, f, newUsageCache(time.Minute))

	if len(got) != 2 {
		t.Fatalf("got %d connections, want one per instance", len(got))
	}
	byName := map[string]providerConnectionDTO{}
	for _, c := range got {
		byName[c.Name] = c
	}
	a := byName["enginer"]
	if a.Type != string(provider.TypeClaude) {
		t.Errorf("enginer type = %q", a.Type)
	}
	if !a.Connected || a.Email != "a@abc.com" {
		t.Errorf("enginer account = %+v, want connected a@abc.com", a)
	}
	if a.Plan != "max" || a.AuthMethod != "Claude AI" {
		t.Errorf("enginer plan/auth = %q/%q", a.Plan, a.AuthMethod)
	}
	if len(a.Windows) != 1 || a.Windows[0].Key != "five_hour" || a.Windows[0].Utilization != 42 {
		t.Errorf("enginer windows = %+v, want the five_hour window", a.Windows)
	}
	if b := byName["waba"]; !b.Connected || b.Email != "b@abc.com" {
		t.Errorf("waba account = %+v", b)
	}
}

func TestCollectConnectionsDedupesUsageByConfigDir(t *testing.T) {
	f := &fakeProbe{
		accountFor: map[string]logintty.Account{
			"/shared": {Connected: true, Email: "shared@abc.com"},
		},
		usageFor: map[string][]logintty.UsageWindow{
			"/shared": {{Key: "five_hour", Utilization: 10}},
		},
	}
	// Four instances, one credential dir: the usage endpoint is remote,
	// so it must be probed once and fanned out to every instance.
	instances := []provider.Instance{
		inst(provider.TypeClaude, "one", "/shared"),
		inst(provider.TypeClaude, "two", "/shared"),
		inst(provider.TypeClaude, "three", "/shared"),
		inst(provider.TypeClaude, "four", "/shared"),
	}

	got := collectConnections(context.Background(), instances, f, newUsageCache(time.Minute))

	if len(got) != 4 {
		t.Fatalf("got %d connections, want 4", len(got))
	}
	if len(f.usageCalls) != 1 {
		t.Errorf("usage probed %d times (%v), want 1 per distinct config dir", len(f.usageCalls), f.usageCalls)
	}
	for _, c := range got {
		if len(c.Windows) != 1 || c.Windows[0].Utilization != 10 {
			t.Errorf("%s got windows %+v, want the shared probe's result", c.Name, c.Windows)
		}
	}
}

func TestCollectConnectionsUnsupportedUsageIsNotAnError(t *testing.T) {
	f := &fakeProbe{
		accountFor: map[string]logintty.Account{
			"/c": {Connected: true, Email: "c@abc.com"},
		},
		usageErr: map[string]error{"/c": logintty.ErrUsageUnsupported},
	}

	got := collectConnections(context.Background(), []provider.Instance{inst(provider.TypeCodex, "cx", "/c")}, f, newUsageCache(time.Minute))

	if len(got) != 1 {
		t.Fatalf("got %d connections, want 1", len(got))
	}
	c := got[0]
	if !c.Connected || c.Email != "c@abc.com" {
		t.Errorf("account dropped: %+v", c)
	}
	if c.UsageSupported {
		t.Error("UsageSupported = true, want false for a type with no usage API")
	}
	if c.UsageErr != "" {
		t.Errorf("UsageErr = %q, want empty — unsupported is not a failure", c.UsageErr)
	}
}

func TestCollectConnectionsUsageErrorSurfaces(t *testing.T) {
	f := &fakeProbe{
		accountFor: map[string]logintty.Account{
			"/d": {Connected: true, Email: "d@abc.com"},
		},
		usageErr: map[string]error{"/d": context.DeadlineExceeded},
	}

	got := collectConnections(context.Background(), []provider.Instance{inst(provider.TypeClaude, "dd", "/d")}, f, newUsageCache(time.Minute))

	if len(got) != 1 {
		t.Fatalf("got %d connections, want 1", len(got))
	}
	c := got[0]
	if !c.Connected {
		t.Error("account dropped when usage failed — the two probes are independent")
	}
	if !c.UsageSupported {
		t.Error("UsageSupported = false, want true — claude has a usage API, this call just failed")
	}
	if c.UsageErr == "" {
		t.Error("UsageErr empty, want the failure surfaced")
	}
}

func TestUsageCacheServesWithinTTL(t *testing.T) {
	calls := 0
	c := newUsageCache(time.Minute)
	now := time.Now()
	fetch := func() ([]logintty.UsageWindow, error) {
		calls++
		return []logintty.UsageWindow{{Key: "five_hour", Utilization: 7}}, nil
	}

	first, _ := c.get("/a", now, fetch)
	second, _ := c.get("/a", now.Add(30*time.Second), fetch)

	if calls != 1 {
		t.Errorf("fetched %d times, want 1 — the second read is inside the TTL", calls)
	}
	if len(first) != 1 || len(second) != 1 || second[0].Utilization != 7 {
		t.Errorf("cached windows not returned: first=%+v second=%+v", first, second)
	}
}

func TestUsageCacheRefetchesAfterTTL(t *testing.T) {
	calls := 0
	c := newUsageCache(time.Minute)
	now := time.Now()
	fetch := func() ([]logintty.UsageWindow, error) {
		calls++
		return []logintty.UsageWindow{{Key: "five_hour", Utilization: float64(calls)}}, nil
	}

	_, _ = c.get("/a", now, fetch)
	got, _ := c.get("/a", now.Add(2*time.Minute), fetch)

	if calls != 2 {
		t.Errorf("fetched %d times, want 2 — the TTL expired", calls)
	}
	if len(got) != 1 || got[0].Utilization != 2 {
		t.Errorf("windows = %+v, want the refetched value", got)
	}
}

func TestUsageCacheKeysPerConfigDir(t *testing.T) {
	var asked []string
	c := newUsageCache(time.Minute)
	now := time.Now()
	mk := func(dir string) func() ([]logintty.UsageWindow, error) {
		return func() ([]logintty.UsageWindow, error) {
			asked = append(asked, dir)
			return nil, nil
		}
	}

	_, _ = c.get("/a", now, mk("/a"))
	_, _ = c.get("/b", now, mk("/b"))

	if len(asked) != 2 {
		t.Errorf("fetched for %v, want both dirs probed — separate accounts", asked)
	}
}

func TestUsageCacheDoesNotCacheFailures(t *testing.T) {
	calls := 0
	c := newUsageCache(time.Minute)
	now := time.Now()
	fetch := func() ([]logintty.UsageWindow, error) {
		calls++
		return nil, context.DeadlineExceeded
	}

	_, err1 := c.get("/a", now, fetch)
	_, err2 := c.get("/a", now.Add(time.Second), fetch)

	if err1 == nil || err2 == nil {
		t.Fatalf("errors swallowed: %v / %v", err1, err2)
	}
	if calls != 2 {
		t.Errorf("fetched %d times, want 2 — a failure must not be cached", calls)
	}
}

func TestUsageCacheCachesUnsupportedVerdict(t *testing.T) {
	calls := 0
	c := newUsageCache(time.Minute)
	now := time.Now()
	fetch := func() ([]logintty.UsageWindow, error) {
		calls++
		return nil, logintty.ErrUsageUnsupported
	}

	_, err1 := c.get("/a", now, fetch)
	_, err2 := c.get("/a", now.Add(time.Second), fetch)

	// "This provider type has no usage API" is a property of the build,
	// not a transient failure — re-asking every page load is pointless.
	if calls != 1 {
		t.Errorf("fetched %d times, want 1 — unsupported is a stable verdict", calls)
	}
	if !errors.Is(err1, logintty.ErrUsageUnsupported) || !errors.Is(err2, logintty.ErrUsageUnsupported) {
		t.Errorf("verdict not preserved: %v / %v", err1, err2)
	}
}

func TestCollectConnectionsSkipsTypesWithoutCredentials(t *testing.T) {
	f := &fakeProbe{accountFor: map[string]logintty.Account{}}
	// wick runs in-process and has no credential dir; the fake returns
	// "" for its config dir, which must not be probed for usage.
	instances := []provider.Instance{{Type: provider.TypeWick, Name: "builtin"}}

	got := collectConnections(context.Background(), instances, f, newUsageCache(time.Minute))

	if len(got) != 0 {
		t.Errorf("got %d connections, want none for a credential-less type", len(got))
	}
	if len(f.usageCalls) != 0 {
		t.Errorf("usage probed %v, want no probe without a config dir", f.usageCalls)
	}
}

// The DTO is the wire contract the SPA normalizes; assert the JSON shape
// directly so a field rename can't silently drop a badge.
func TestProviderConnectionDTOWireShape(t *testing.T) {
	b, err := json.Marshal(providerConnectionDTO{
		Type:           "claude",
		Name:           "enginer",
		Connected:      true,
		Email:          "dev@abc.com",
		Plan:           "max",
		AuthMethod:     "Claude AI",
		UsageSupported: true,
		Windows: []usageWindowDTO{
			{Key: "five_hour", Utilization: 42, ResetsAt: "2030-01-01T00:00:00Z"},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"type", "name", "connected", "email", "plan", "auth_method", "usage_supported", "windows"} {
		if _, ok := got[k]; !ok {
			t.Errorf("wire payload missing %q: %s", k, b)
		}
	}
	w, ok := got["windows"].([]any)
	if !ok || len(w) != 1 {
		t.Fatalf("windows = %#v, want one entry", got["windows"])
	}
	win, _ := w[0].(map[string]any)
	for _, k := range []string{"key", "utilization", "resets_at"} {
		if _, ok := win[k]; !ok {
			t.Errorf("window missing %q: %s", k, b)
		}
	}
}

// An account with no usage data must still serialize its identity — the
// SPA shows the connected badge + email even when the rings are absent.
func TestProviderConnectionDTOOmitsEmptyUsage(t *testing.T) {
	b, _ := json.Marshal(providerConnectionDTO{Type: "codex", Name: "cx", Connected: true, Email: "a@abc.com"})
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["email"] != "a@abc.com" || got["connected"] != true {
		t.Errorf("identity lost: %s", b)
	}
	if _, ok := got["windows"]; ok {
		t.Errorf("empty windows should be omitted, got %s", b)
	}
	// usage_supported has no omitempty: false is meaningful (no usage API)
	// and must reach the client rather than being elided.
	if v, ok := got["usage_supported"]; !ok || v != false {
		t.Errorf("usage_supported = %v (present=%v), want an explicit false", v, ok)
	}
}

// Instances that point CLAUDE_CONFIG_DIR at different folders are
// different accounts and must NOT share a usage probe — the dedup keys
// on the resolved config dir, so a per-instance env override splits them.
// This is the shape that matters in practice: one claude binary on PATH,
// several instances, each with its own credential folder.
func TestCollectConnectionsSplitsPerEnvConfigDir(t *testing.T) {
	f := &fakeProbe{
		accountFor: map[string]logintty.Account{
			"/home/u/.claude-work":     {Connected: true, Email: "work@abc.com"},
			"/home/u/.claude-personal": {Connected: true, Email: "me@abc.com"},
		},
		usageFor: map[string][]logintty.UsageWindow{
			"/home/u/.claude-work":     {{Key: "five_hour", Utilization: 90}},
			"/home/u/.claude-personal": {{Key: "five_hour", Utilization: 10}},
		},
	}
	instances := []provider.Instance{
		inst(provider.TypeClaude, "work", "/home/u/.claude-work"),
		inst(provider.TypeClaude, "personal", "/home/u/.claude-personal"),
	}

	got := collectConnections(context.Background(), instances, f, newUsageCache(time.Minute))

	if len(f.usageCalls) != 2 {
		t.Errorf("usage probed %v, want one probe per distinct config dir", f.usageCalls)
	}
	byName := map[string]providerConnectionDTO{}
	for _, c := range got {
		byName[c.Name] = c
	}
	if e := byName["work"].Email; e != "work@abc.com" {
		t.Errorf("work email = %q, want its own account", e)
	}
	if e := byName["personal"].Email; e != "me@abc.com" {
		t.Errorf("personal email = %q, want its own account", e)
	}
	if u := byName["work"].Windows[0].Utilization; u != 90 {
		t.Errorf("work utilization = %v, want 90 — not the other account's", u)
	}
	if u := byName["personal"].Windows[0].Utilization; u != 10 {
		t.Errorf("personal utilization = %v, want 10 — not the other account's", u)
	}
}
