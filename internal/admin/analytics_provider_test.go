package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// seedForProviders builds two people and seven conversations: one per
// provider, one that switched provider mid-life, one with no provider
// recorded, one that ran two models on one account, one nobody is
// attributed to, and one outside the week.
func seedForProviders(t *testing.T) *Handler {
	t.Helper()
	r := newAnalyticsRepo(t)
	ctx := context.Background()
	for _, u := range []entity.User{
		{ID: "user-a", Name: "User A", Email: "a@example.com", Approved: true},
		{ID: "user-b", Name: "User B", Email: "b@example.com", Approved: true},
	} {
		if err := r.db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	layout := agentconfig.NewLayout(t.TempDir())
	agentsLayout = layout
	sessionCache = &factsCache{m: map[string]sessionFacts{}}
	now := time.Now().UTC()

	mk := func(id, origin, owner string, at time.Time, providers ...string) {
		t.Helper()
		if _, err := session.Create(ctx, layout, session.CreateOptions{ID: id, Origin: session.Origin(origin), UserID: owner}); err != nil {
			t.Fatal(err)
		}
		s, err := session.Load(layout, id)
		if err != nil {
			t.Fatal(err)
		}
		s.Meta.CreatedAt, s.Meta.LastActive = at, at
		if err := session.SaveMeta(layout, id, s.Meta); err != nil {
			t.Fatal(err)
		}
		// "provider:model", so one session can run several of either.
		var agents []session.AgentEntry
		for i, p := range providers {
			key, model, _ := strings.Cut(p, ":")
			agents = append(agents, session.AgentEntry{Name: "agent-" + string(rune('a'+i)), Provider: key, ModelID: model, CreatedAt: at})
		}
		if len(agents) > 0 {
			if err := session.SaveAgents(layout, id, agents); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("s-a-ui", "ui", "user-a", now.AddDate(0, 0, -1), "claude/acct-a:sonnet")
	mk("s-b-slack", "slack", "user-b", now.AddDate(0, 0, -2), "codex/acct-b:gpt")
	mk("s-switch", "ui", "user-a", now.AddDate(0, 0, -3), "claude/acct-a:opus", "codex/acct-b:gpt")
	mk("s-none", "rest", "user-b", now.AddDate(0, 0, -4))
	mk("s-two-models", "ui", "user-b", now.AddDate(0, 0, -5), "claude/acct-c:opus", "claude/acct-c:sonnet")
	mk("s-orphan", "rest", "", now.AddDate(0, 0, -6), "claude/acct-c:opus")
	mk("s-old", "ui", "user-a", now.AddDate(0, 0, -60), "claude/acct-a:opus")
	return &Handler{repo: r}
}

func providerFilter(keys ...string) analyticsFilter {
	f := testFilter(7, false)
	f.Prov = map[string]bool{}
	for _, k := range keys {
		f.Prov[k] = true
	}
	return f
}

// recentIDs is every conversation the people drill-downs list.
func recentIDs(out analyticsResponse) map[string]bool {
	ids := map[string]bool{}
	for _, u := range out.Users {
		for _, r := range u.Recent {
			ids[r.ID] = true
		}
	}
	return ids
}

func seriesTotal(out analyticsResponse) int {
	n := 0
	for _, p := range out.Series.Points {
		n += p.Sessions
	}
	return n
}

func TestProviderFilterNarrowsEverything(t *testing.T) {
	h := seedForProviders(t)
	out := build(t, h, providerFilter("claude/acct-a"))

	if out.Sessions != 2 {
		t.Errorf("claude/acct-a = %d conversations, want 2 (s-a-ui and s-switch)", out.Sessions)
	}
	if out.UsersInWindow != 1 {
		t.Errorf("people in range = %d, want only user-a", out.UsersInWindow)
	}
	if len(out.Channels) != 1 || out.Channels[0].Channel != "ui" {
		t.Errorf("channels = %+v, want only ui", out.Channels)
	}
	if got := seriesTotal(out); got != 2 {
		t.Errorf("chart adds up to %d, want the filter's 2", got)
	}
	for _, u := range out.Users {
		if u.ID == "user-b" && u.Sessions != 0 {
			t.Errorf("user-b has %d conversations under a provider they never used", u.Sessions)
		}
	}
	if ids := recentIDs(out); !ids["s-a-ui"] || !ids["s-switch"] || ids["s-b-slack"] || ids["s-none"] {
		t.Errorf("drill-downs list %v, want s-a-ui and s-switch only", ids)
	}
	if got := out.Window.Providers; len(got) != 1 || got[0] != "claude/acct-a" {
		t.Errorf("window echoes providers %v, want the selection", got)
	}
}

func TestProviderFilterWithTwoProviders(t *testing.T) {
	h := seedForProviders(t)
	out := build(t, h, providerFilter("claude/acct-a", "codex/acct-b"))
	if out.Sessions != 3 {
		t.Errorf("two providers = %d conversations, want 3 — the switched one counted once", out.Sessions)
	}
	if out.UsersInWindow != 2 {
		t.Errorf("people in range = %d, want both", out.UsersInWindow)
	}
}

// A conversation that switched provider ran on both accounts, so it
// belongs to either selection.
func TestSwitchedConversationMatchesEitherProvider(t *testing.T) {
	h := seedForProviders(t)
	for _, key := range []string{"claude/acct-a", "codex/acct-b"} {
		if ids := recentIDs(build(t, h, providerFilter(key))); !ids["s-switch"] {
			t.Errorf("%s: switched conversation missing from %v", key, ids)
		}
	}
	out := build(t, h, providerFilter("codex/acct-b"))
	if out.Sessions != 2 {
		t.Errorf("codex/acct-b = %d conversations, want 2 (s-b-slack and s-switch)", out.Sessions)
	}
}

// No provider recorded is a chip of its own, present only when the range
// holds such a conversation.
func TestNoProviderBucket(t *testing.T) {
	h := seedForProviders(t)
	all := build(t, h, providerFilter())
	want := []string{noProvider, "claude/acct-a", "claude/acct-c", "codex/acct-b"}
	if len(all.KnownProviders) != len(want) {
		t.Fatalf("known providers = %v, want %v", all.KnownProviders, want)
	}
	for i, k := range want {
		if all.KnownProviders[i] != k {
			t.Errorf("known providers = %v, want %v", all.KnownProviders, want)
			break
		}
	}

	none := build(t, h, providerFilter(noProvider))
	if none.Sessions != 1 {
		t.Errorf("(none) = %d conversations, want the one with nothing recorded", none.Sessions)
	}
	if ids := recentIDs(none); !ids["s-none"] {
		t.Errorf("(none) lists %v, want s-none", ids)
	}
	// Picking one provider must not take the other chips away.
	if got := build(t, h, providerFilter("codex/acct-b")).KnownProviders; len(got) != 4 {
		t.Errorf("known providers under a filter = %v, want every one in range", got)
	}

	// Every conversation here has a provider: no empty chip.
	for _, k := range build(t, seedForFilter(t), testFilter(defaultWindowDays, false)).KnownProviders {
		if k == noProvider {
			t.Error("(none) offered with no such conversation in range")
		}
	}
}

func TestProviderFilterCombinesWithChannel(t *testing.T) {
	h := seedForProviders(t)
	f := providerFilter("codex/acct-b")
	f.Chan = map[string]bool{"ui": true}
	out := build(t, h, f)
	if out.Sessions != 1 {
		t.Errorf("ui AND codex/acct-b = %d conversations, want only s-switch", out.Sessions)
	}
	if ids := recentIDs(out); !ids["s-switch"] || len(ids) != 1 {
		t.Errorf("drill-downs list %v, want s-switch only", ids)
	}
}

func TestParseFilterReadsProviders(t *testing.T) {
	q := url.Values{}
	q.Set("providers", "claude/acct-a, (none) ,all,")
	f := parseFilter(q)
	if len(f.Prov) != 2 || !f.Prov["claude/acct-a"] || !f.Prov[noProvider] {
		t.Errorf("providers = %v, want claude/acct-a and (none)", f.Prov)
	}
}

// The ledger reads the same fact cache, so its provider filter sees the
// same keys the counts do.
func TestLedgerTagsCarryProviders(t *testing.T) {
	seedForProviders(t)
	if got := ledgerTags("s-switch").Providers; len(got) != 2 {
		t.Errorf("ledger tags providers = %v, want both", got)
	}
	if got := ledgerTags("s-none").Providers; len(got) != 0 {
		t.Errorf("ledger tags providers = %v, want none", got)
	}
}

func peopleFilter(ids ...string) analyticsFilter {
	f := providerFilter()
	f.Who = map[string]bool{}
	for _, id := range ids {
		f.Who[id] = true
	}
	return f
}

func TestPeopleFilter(t *testing.T) {
	h := seedForProviders(t)
	out := build(t, h, peopleFilter("user-b"))
	if out.Sessions != 3 {
		t.Errorf("user-b = %d conversations, want 3 (s-b-slack, s-none, s-two-models)", out.Sessions)
	}
	if ids := recentIDs(out); ids["s-a-ui"] || ids["s-orphan"] {
		t.Errorf("drill-downs list %v, want only user-b's", ids)
	}
	orphans := build(t, h, peopleFilter(noPerson))
	if orphans.Sessions != 1 || orphans.Unattributed != 1 {
		t.Errorf("(unattributed) = %d conversations, want the ownerless one", orphans.Sessions)
	}
	known := map[string]bool{}
	for _, k := range build(t, h, peopleFilter("user-a")).KnownPeople {
		known[k] = true
	}
	if !known["user-a"] || !known["user-b"] || !known[noPerson] {
		t.Errorf("known people = %v, want both people and the unattributed bucket", known)
	}
}

func TestProviderAndPeopleCombine(t *testing.T) {
	h := seedForProviders(t)
	f := peopleFilter("user-a")
	f.Prov = map[string]bool{"codex/acct-b": true}
	out := build(t, h, f)
	if out.Sessions != 1 {
		t.Errorf("codex/acct-b AND user-a = %d conversations, want only s-switch", out.Sessions)
	}
	if len(out.ProviderUsage) != 1 || out.ProviderUsage[0].Sessions != 1 || out.ProviderUsage[0].TopUserID != "user-a" {
		t.Errorf("usage = %+v, want one conversation, all user-a's", out.ProviderUsage)
	}
}

// "Who used it last, and who used it most" for each picked provider.
func TestProviderUsageLastAndTopUser(t *testing.T) {
	h := seedForProviders(t)
	out := build(t, h, providerFilter("claude/acct-a", "codex/acct-b", noProvider))
	if len(out.ProviderUsage) != 2 {
		t.Fatalf("usage rows = %+v, want one per picked provider, (none) excluded", out.ProviderUsage)
	}
	a, b := out.ProviderUsage[0], out.ProviderUsage[1]
	if a.Key != "claude/acct-a" || a.Sessions != 2 || a.People != 1 {
		t.Errorf("claude/acct-a = %+v, want 2 conversations by 1 person", a)
	}
	if a.LastUsedBy != "User A" || a.TopUser != "User A" || a.TopUserSessions != 2 {
		t.Errorf("claude/acct-a last/top = %q/%q×%d, want User A both, 2", a.LastUsedBy, a.TopUser, a.TopUserSessions)
	}
	// One conversation each: the tie goes to whoever used it more recently.
	if b.Key != "codex/acct-b" || b.TopUser != "User B" || b.TopUserSessions != 1 || b.LastUsedBy != "User B" {
		t.Errorf("codex/acct-b = %+v, want User B last and top on the tie", b)
	}
	if len(b.Models) != 1 || b.Models[0].Key != "gpt" || b.Models[0].Sessions != 2 {
		t.Errorf("codex/acct-b models = %+v, want gpt×2", b.Models)
	}
	if build(t, h, providerFilter()).ProviderUsage != nil {
		t.Error("no provider picked, but a usage table was sent")
	}
}

func modelFilter(models ...string) analyticsFilter {
	f := providerFilter()
	f.Model = map[string]bool{}
	for _, m := range models {
		f.Model[m] = true
	}
	return f
}

func TestModelFilter(t *testing.T) {
	h := seedForProviders(t)
	if got := build(t, h, modelFilter("opus")).Sessions; got != 3 {
		t.Errorf("opus = %d conversations, want 3 (s-switch, s-two-models, s-orphan)", got)
	}
	// A conversation that ran two models matches either.
	sonnet := build(t, h, modelFilter("sonnet"))
	if ids := recentIDs(sonnet); sonnet.Sessions != 2 || !ids["s-two-models"] {
		t.Errorf("sonnet = %d conversations %v, want s-a-ui and s-two-models", sonnet.Sessions, ids)
	}
	if got := build(t, h, modelFilter(noProvider)).Sessions; got != 1 {
		t.Errorf("(none) model = %d conversations, want the one with no agent recorded", got)
	}
	want := []string{noProvider, "gpt", "opus", "sonnet"}
	if got := build(t, h, modelFilter("opus")).KnownModels; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("known models = %v, want %v", got, want)
	}
}

func TestModelAndProviderCombine(t *testing.T) {
	h := seedForProviders(t)
	f := modelFilter("opus")
	f.Prov = map[string]bool{"claude/acct-a": true}
	if got := build(t, h, f).Sessions; got != 1 {
		t.Errorf("opus AND claude/acct-a = %d conversations, want only s-switch", got)
	}
	// Both models on one account show in that account's breakdown.
	f = providerFilter("claude/acct-c")
	usage := build(t, h, f).ProviderUsage
	models := map[string]int{}
	for _, m := range usage[0].Models {
		models[m.Key] = m.Sessions
	}
	if models["opus"] != 2 || models["sonnet"] != 1 {
		t.Errorf("claude/acct-c models = %v, want opus×2 and sonnet×1", models)
	}
	if usage[0].People != 1 {
		t.Errorf("claude/acct-c people = %d, want user-b only — the orphan has nobody", usage[0].People)
	}
}

func TestParseFilterReadsPeopleAndModels(t *testing.T) {
	q := url.Values{}
	q.Set("people", "user-a,(unattributed)")
	q.Set("models", "opus")
	f := parseFilter(q)
	if !f.Who["user-a"] || !f.Who[noPerson] || !f.Model["opus"] {
		t.Errorf("filter = %+v / %+v, want people and models read", f.Who, f.Model)
	}
}
