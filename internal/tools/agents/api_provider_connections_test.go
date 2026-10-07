package agents

import (
	"context"
	"encoding/json"
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
	// identityFor maps a config dir onto an account key, standing in
	// for logintty.UsageIdentity reading the credential files. Absent
	// entries fall back to the dir itself.
	identityFor map[string]string
	// unsupported marks provider types with no usage API.
	unsupported map[provider.Type]bool
	// credsAtFor maps a config dir onto the credential file's mtime,
	// standing in for logintty.CredentialsChangedAt. Absent entries
	// mean "unknown", which is what a dir with no credentials gives.
	credsAtFor map[string]time.Time
}

func (f *fakeProbe) credentialsChangedAt(_ provider.Type, env []string) time.Time {
	return f.credsAtFor[envVal(env, "DIR")]
}

func (f *fakeProbe) identity(_ provider.Type, env []string) string {
	dir := envVal(env, "DIR")
	if k, ok := f.identityFor[dir]; ok {
		return k
	}
	return dir
}

func (f *fakeProbe) usageSupported(t provider.Type) bool {
	return !f.unsupported[t]
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

	got := collectConnections(context.Background(), instances, f, testUsageCache(time.Minute))

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

func TestCollectConnectionsDedupesUsageByAccount(t *testing.T) {
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

	got := collectConnections(context.Background(), instances, f, testUsageCache(time.Minute))

	if len(got) != 4 {
		t.Fatalf("got %d connections, want 4", len(got))
	}
	if len(f.usageCalls) != 1 {
		t.Errorf("usage probed %d times (%v), want 1 per distinct account", len(f.usageCalls), f.usageCalls)
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
		unsupported: map[provider.Type]bool{provider.TypeCodex: true},
	}

	got := collectConnections(context.Background(), []provider.Instance{inst(provider.TypeCodex, "cx", "/c")}, f, testUsageCache(time.Minute))

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
	if len(f.usageCalls) != 0 {
		t.Errorf("usage probed %v, want no call at all for a type with no usage API", f.usageCalls)
	}
}

func TestCollectConnectionsUsageErrorSurfaces(t *testing.T) {
	f := &fakeProbe{
		accountFor: map[string]logintty.Account{
			"/d": {Connected: true, Email: "d@abc.com"},
		},
		usageErr: map[string]error{"/d": context.DeadlineExceeded},
	}

	got := collectConnections(context.Background(), []provider.Instance{inst(provider.TypeClaude, "dd", "/d")}, f, testUsageCache(time.Minute))

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

func TestCollectConnectionsSkipsTypesWithoutCredentials(t *testing.T) {
	f := &fakeProbe{accountFor: map[string]logintty.Account{}}
	// wick runs in-process and has no credential dir; the fake returns
	// "" for its config dir, which must not be probed for usage.
	instances := []provider.Instance{{Type: provider.TypeWick, Name: "builtin"}}

	got := collectConnections(context.Background(), instances, f, testUsageCache(time.Minute))

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

	got := collectConnections(context.Background(), instances, f, testUsageCache(time.Minute))

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

// Two instances in DIFFERENT credential folders that resolve to the same
// account must still cost one request — the endpoint rate-limits the
// login, not the folder. This is the case the old per-dir dedup missed.
func TestCollectConnectionsDedupesAcrossDirsOnSameAccount(t *testing.T) {
	f := &fakeProbe{
		accountFor: map[string]logintty.Account{
			"/dir-a": {Connected: true, Email: "dev@abc.com"},
			"/dir-b": {Connected: true, Email: "dev@abc.com"},
		},
		identityFor: map[string]string{
			"/dir-a": "claude:email:dev@abc.com",
			"/dir-b": "claude:email:dev@abc.com",
		},
		usageFor: map[string][]logintty.UsageWindow{
			"/dir-a": {{Key: "five_hour", Utilization: 33}},
			"/dir-b": {{Key: "five_hour", Utilization: 99}},
		},
	}
	instances := []provider.Instance{
		inst(provider.TypeClaude, "one", "/dir-a"),
		inst(provider.TypeClaude, "two", "/dir-b"),
	}

	got := collectConnections(context.Background(), instances, f, testUsageCache(time.Minute))

	if len(f.usageCalls) != 1 {
		t.Errorf("usage probed %v, want 1 — both instances are the same login", f.usageCalls)
	}
	for _, c := range got {
		if len(c.Windows) != 1 {
			t.Fatalf("%s has no windows: %+v", c.Name, c)
		}
		if c.Windows[0].Utilization != got[0].Windows[0].Utilization {
			t.Errorf("instances on one account show different numbers: %+v", got)
		}
	}
}

// Every row must say how old its reading is: the numbers come from a
// shared cache, so a card with no provenance implies a live fetch that
// never happened.
func TestCollectConnectionsStampsProvenance(t *testing.T) {
	f := &fakeProbe{
		accountFor: map[string]logintty.Account{"/a": {Connected: true, Email: "a@abc.com"}},
		usageFor:   map[string][]logintty.UsageWindow{"/a": {{Key: "five_hour", Utilization: 5}}},
	}

	got := collectConnections(context.Background(), []provider.Instance{inst(provider.TypeClaude, "one", "/a")}, f, testUsageCache(time.Minute))

	if len(got) != 1 {
		t.Fatalf("got %d connections", len(got))
	}
	c := got[0]
	if c.UsageFetchedAt == "" {
		t.Error("UsageFetchedAt empty — the row cannot say when it was read")
	}
	if _, err := time.Parse(time.RFC3339, c.UsageFetchedAt); err != nil {
		t.Errorf("UsageFetchedAt = %q, not RFC3339: %v", c.UsageFetchedAt, err)
	}
	// Just fetched, so the countdown to the next probe is the full TTL
	// (rounded), and the age is still zero.
	if c.UsageNextS <= 0 || c.UsageNextS > 60 {
		t.Errorf("UsageNextS = %d, want the remaining TTL", c.UsageNextS)
	}
	if c.UsagePending {
		t.Error("UsagePending = true for a row that has its reading")
	}
}

// A reading that has not arrived yet is pending, NOT an error: the probe
// is paced deliberately, and a card must not accuse the endpoint of
// failing while it waits its turn.
func TestCollectConnectionsReportsPendingWhileProbeQueued(t *testing.T) {
	f := &fakeProbe{accountFor: map[string]logintty.Account{"/a": {Connected: true, Email: "a@abc.com"}}}
	cache := testUsageCache(time.Minute)
	// Never runs the refresh — stands in for a probe still waiting
	// behind the pacing gate when the page is rendered.
	cache.run = func(_ func()) {}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the page is out of budget immediately
	got := collectConnections(ctx, []provider.Instance{inst(provider.TypeClaude, "one", "/a")}, f, cache)

	if len(got) != 1 {
		t.Fatalf("got %d connections", len(got))
	}
	c := got[0]
	if !c.UsagePending {
		t.Errorf("UsagePending = false, want true: %+v", c)
	}
	if c.UsageErr != "" {
		t.Errorf("UsageErr = %q, want empty — waiting is not failing", c.UsageErr)
	}
	if !c.Connected || c.Email != "a@abc.com" {
		t.Errorf("account dropped while usage was pending: %+v", c)
	}
}
