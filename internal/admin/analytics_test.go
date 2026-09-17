package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// The people table is sorted by "most recently here", and that key is built
// from two stamps that arrive as RFC3339 strings. RFC3339 sorts correctly as
// text ONLY while the offsets match, which is why everything is stamped UTC —
// this pins that decision.
func TestMaxStampPrefersTheLaterInstant(t *testing.T) {
	older := "2026-09-01T10:00:00Z"
	newer := "2026-09-16T09:00:00Z"
	if got := maxStamp(older, newer); got != newer {
		t.Fatalf("maxStamp(%q,%q) = %q", older, newer, got)
	}
	if got := maxStamp(newer, ""); got != newer {
		t.Fatalf("an empty stamp must never win: %q", got)
	}
	if got := maxStamp("", ""); got != "" {
		t.Fatalf("two unknowns stay unknown: %q", got)
	}
}

func TestLaterKeepsTheNewest(t *testing.T) {
	a := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	b := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	if !later(a, b).Equal(b) || !later(b, a).Equal(b) {
		t.Fatal("later() must be order-independent")
	}
	var zero time.Time
	if !later(zero, a).Equal(a) {
		t.Fatal("a real stamp must beat 'never'")
	}
}

func TestRfc3339TreatsZeroAsUnknown(t *testing.T) {
	if rfc3339(time.Time{}) != "" {
		t.Fatal("a zero time must render as unknown, not as year 1")
	}
	if rfc3339(time.Date(2026, 9, 16, 7, 0, 0, 0, time.UTC)) != "2026-09-16T07:00:00Z" {
		t.Fatal("stamps are UTC RFC3339 — the sort depends on it")
	}
}

func TestSortedKeysIsStable(t *testing.T) {
	got := sortedKeys(map[string]bool{"ui": true, "slack": true, "telegram": true})
	want := []string{"slack", "telegram", "ui"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sortedKeys = %v, want %v", got, want)
		}
	}
}

// ── the page's data, end to end ────────────────────────────────────────

func newAnalyticsRepo(t *testing.T) *repo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&entity.User{}, &entity.Session{}, &entity.PersonalAccessToken{}); err != nil {
		t.Fatal(err)
	}
	return newRepo(db)
}

// seedAnalytics builds a small but realistic install: two people, a
// project with a name, and four conversations — one of them started by a
// script holding a token, one of them ownerless the way every REST
// session used to be.
func seedAnalytics(t *testing.T) (*Handler, string) {
	t.Helper()
	r := newAnalyticsRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()

	users := []entity.User{
		{ID: "u-yoga", Name: "Yoga", Email: "yoga@example.com", Approved: true},
		{ID: "u-rina", Name: "Rina", Email: "rina@example.com", Approved: true},
	}
	for i := range users {
		if err := r.db.WithContext(ctx).Create(&users[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Two sign-ins for Yoga, on two different days inside the window.
	for _, at := range []time.Time{now.Add(-24 * time.Hour), now.Add(-2 * time.Hour)} {
		if err := r.db.WithContext(ctx).Create(&entity.Session{
			Token: "s" + at.Format("150405"), UserID: "u-yoga",
			CreatedAt: at, ExpiresAt: now.Add(24 * time.Hour),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	used := now.Add(-time.Hour)
	if err := r.db.WithContext(ctx).Create(&entity.PersonalAccessToken{
		ID: "tok-ci", UserID: "u-yoga", Name: "ci-runner",
		TokenHash: "hash-1", Last4: "beef", CreatedAt: now.Add(-72 * time.Hour), LastUsedAt: &used,
	}).Error; err != nil {
		t.Fatal(err)
	}

	layout := agentconfig.NewLayout(t.TempDir())
	agentsLayout = layout
	proj, err := project.Create(layout, project.CreateOptions{ID: "proj-support", Name: "Support Tools"})
	if err != nil {
		t.Fatal(err)
	}
	pid := proj.ID()

	mk := func(id, owner, origin, tokenID, tokenName, label string) {
		t.Helper()
		if _, err := session.Create(ctx, layout, session.CreateOptions{
			ID: id, ProjectID: pid, Origin: session.Origin(origin),
			UserID: owner, TokenID: tokenID, TokenName: tokenName,
		}); err != nil {
			t.Fatal(err)
		}
		s, err := session.Load(layout, id)
		if err != nil {
			t.Fatal(err)
		}
		s.Meta.Label = label
		if err := session.SaveMeta(layout, id, s.Meta); err != nil {
			t.Fatal(err)
		}
	}
	mk("sess-ui", "u-yoga", "ui", "", "", "dashboard chat")
	mk("sess-slack", "u-rina", "slack", "", "", "slack thread")
	mk("sess-rest", "u-yoga", "rest", "tok-ci", "ci-runner", "nightly run")
	mk("sess-orphan", "", "rest", "", "", "before ownership was recorded")

	return &Handler{repo: r}, pid
}

// testFilter is what parseFilter would produce for a plain ?days= request.
func testFilter(days int, all bool) analyticsFilter {
	now := time.Now().UTC()
	f := analyticsFilter{Days: days, All: all, To: endOfDay(now), Chan: map[string]bool{}}
	f.From = now.AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)
	if all {
		f.From = time.Time{}
	}
	return f
}

func buildFixture(t *testing.T, h *Handler) analyticsResponse {
	t.Helper()
	out, err := h.buildAnalytics(httptest.NewRequest(http.MethodGet, "/admin/analytics/users.json", nil), testFilter(defaultWindowDays, false), nil)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A column of UUIDs is a column nobody can read, so a project must arrive
// with the name it was created under — and keep its id for the link.
func TestProjectsCarryTheirName(t *testing.T) {
	h, pid := seedAnalytics(t)
	out := buildFixture(t, h)

	var found *analyticsProject
	for i := range out.Projects {
		if out.Projects[i].ID == pid {
			found = &out.Projects[i]
		}
	}
	if found == nil {
		t.Fatalf("the seeded project is missing from %+v", out.Projects)
	}
	if found.Name != "Support Tools" {
		t.Errorf("project name = %q, want the name a human would recognise", found.Name)
	}
	if found.Sessions != 4 || found.Users != 2 {
		t.Errorf("project = %d sessions / %d people, want 4/2", found.Sessions, found.Users)
	}
	if found.Unattributed != 1 {
		t.Errorf("unattributed = %d, want the one session nobody owns", found.Unattributed)
	}
	// The drill-down is the point: the cell shows a name, this holds the list.
	if len(found.Recent) != 4 || len(found.Members) != 2 {
		t.Errorf("detail = %d conversations / %d members, want 4/2", len(found.Recent), len(found.Members))
	}
	for _, s := range found.Recent {
		if s.ID == "sess-rest" && s.Token != "ci-runner" {
			t.Errorf("a machine-made conversation must name its token, got %q", s.Token)
		}
		if s.ID == "sess-ui" && s.User != "Yoga" {
			t.Errorf("session user = %q, want the person's name rather than their uuid", s.User)
		}
	}
}

// "Authenticated as Yoga" does not identify a machine caller: one account
// can hold several tokens, handed to a CI job, a script, a laptop. The
// page has to say which one has been calling.
func TestTokensAreAttributedToTheirOwner(t *testing.T) {
	h, _ := seedAnalytics(t)
	out := buildFixture(t, h)

	var yoga *analyticsUser
	for i := range out.Users {
		if out.Users[i].ID == "u-yoga" {
			yoga = &out.Users[i]
		}
	}
	if yoga == nil {
		t.Fatal("the token's owner is missing from the people list")
	}
	if len(yoga.Tokens) != 1 {
		t.Fatalf("tokens = %+v, want the one that exists", yoga.Tokens)
	}
	tok := yoga.Tokens[0]
	if tok.Name != "ci-runner" || tok.Sessions != 1 {
		t.Errorf("token = %+v, want ci-runner with the conversation it made", tok)
	}
	if tok.Masked != "wick_pat_****beef" {
		t.Errorf("masked = %q — the page shows the record, never the token", tok.Masked)
	}
	if strings.Contains(tok.Masked, "hash-1") {
		t.Error("the stored hash must never reach the page")
	}
}

// A session nobody owns is counted, not hidden: a table whose numbers do
// not add up to the total invites the wrong conclusion.
func TestUnattributedSessionsAreCounted(t *testing.T) {
	h, _ := seedAnalytics(t)
	out := buildFixture(t, h)

	if out.Sessions != 4 || out.Unattributed != 1 {
		t.Errorf("%d sessions / %d unattributed, want 4/1", out.Sessions, out.Unattributed)
	}
	for _, c := range out.Channels {
		if c.Channel == "rest" && c.Unattributed != 1 {
			t.Errorf("rest unattributed = %d, want 1", c.Unattributed)
		}
		if c.Channel == "rest" && c.Users != 1 {
			t.Errorf("rest users = %d — the token's owner must count as a person", c.Users)
		}
	}
}

// The chart's x axis comes from the window, not from the data, so a quiet
// day is a zero rather than a gap. A gap reads as "no data", which is a
// different claim from "nobody came".
func TestSeriesCoversEveryDayInTheWindow(t *testing.T) {
	h, _ := seedAnalytics(t)
	out := buildFixture(t, h)

	if len(out.Series.Points) != defaultWindowDays {
		t.Fatalf("%d points for a %d-day window", len(out.Series.Points), defaultWindowDays)
	}
	if out.Series.Points[0].Date >= out.Series.Points[len(out.Series.Points)-1].Date {
		t.Error("points must run oldest → newest")
	}
	today := time.Now().UTC().Format("2006-01-02")
	var todays analyticsPoint
	for _, p := range out.Series.Points {
		if p.Date == today {
			todays = p
		}
	}
	if todays.Sessions != 4 {
		t.Errorf("today = %d conversations, want the 4 just created", todays.Sessions)
	}
	if todays.People != 2 {
		t.Errorf("today = %d people, want 2 distinct", todays.People)
	}
	// Per-channel curves are what make "the rise came from Slack" readable.
	if got := out.Series.ByChannel["rest"]; len(got) != defaultWindowDays {
		t.Errorf("rest curve has %d points, want the same axis as the total", len(got))
	}
}

// Sign-ins have their own history in the auth sessions table — same rows
// LoginStats already counts, grouped by day instead of totalled.
func TestLoginHistoryFeedsTheCurve(t *testing.T) {
	h, _ := seedAnalytics(t)
	out := buildFixture(t, h)

	total := 0
	for _, p := range out.Series.Points {
		total += p.Logins
	}
	if total != 2 {
		t.Errorf("%d sign-ins across the window, want the 2 seeded", total)
	}
	for _, u := range out.Users {
		if u.ID != "u-yoga" {
			continue
		}
		own := 0
		for _, p := range u.Daily {
			own += p.Logins
		}
		if own != 2 {
			t.Errorf("per-person curve has %d sign-ins, want 2", own)
		}
	}
}

// Out-of-range windows are clamped rather than refused: a chart is not
// worth a 400, and a caller who asks for a year gets the longest honest
// answer instead of an error.
func TestWindowDaysClamps(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"", defaultWindowDays},
		{"nonsense", defaultWindowDays},
		{"0", defaultWindowDays},
		{"1", minWindowDays},
		{"-5", minWindowDays},
		{"3650", maxWindowDays},
		{"45", 45},
	} {
		got, all := windowDays(tc.in)
		if got != tc.want || all {
			t.Errorf("windowDays(%q) = %d,%v want %d,false", tc.in, got, all, tc.want)
		}
	}
	if _, all := windowDays("all"); !all {
		t.Error("\"all\" must select the everything window")
	}
	if _, all := windowDays("ALL"); !all {
		t.Error("the everything window should not hinge on case")
	}
}

// "All" cannot be a fixed number of days: it runs back to the oldest
// conversation, which is only known once the sessions have been read.
func TestAllWindowSpansBackToTheOldestConversation(t *testing.T) {
	h, _ := seedAnalytics(t)
	layout := agentsLayout
	// One conversation from well before the default window.
	oldID := "sess-ancient"
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID: oldID, Origin: session.OriginUI, UserID: "u-yoga",
	}); err != nil {
		t.Fatal(err)
	}
	s, err := session.Load(layout, oldID)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().AddDate(0, 0, -120)
	s.Meta.CreatedAt = old
	s.Meta.LastActive = old
	if err := session.SaveMeta(layout, oldID, s.Meta); err != nil {
		t.Fatal(err)
	}

	windowed, err := h.buildAnalytics(httptest.NewRequest(http.MethodGet, "/", nil), testFilter(defaultWindowDays, false), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(windowed.Series.Points) != defaultWindowDays {
		t.Fatalf("a fixed window must stay fixed: %d points", len(windowed.Series.Points))
	}

	everything, err := h.buildAnalytics(httptest.NewRequest(http.MethodGet, "/", nil), testFilter(defaultWindowDays, true), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(everything.Series.Points) < 120 {
		t.Errorf("all-window has %d points, want at least the 120 days back to the oldest conversation", len(everything.Series.Points))
	}
	if everything.Series.Points[0].Date != old.Format("2006-01-02") {
		t.Errorf("axis starts %q, want the oldest conversation's day %q", everything.Series.Points[0].Date, old.Format("2006-01-02"))
	}
	if everything.Series.Points[0].Sessions != 1 {
		t.Errorf("the oldest day should carry its one conversation, got %d", everything.Series.Points[0].Sessions)
	}
}

// An install with nothing in it must not draw an axis starting in year one.
func TestAllWindowFallsBackWhenThereIsNothing(t *testing.T) {
	agentsLayout = agentconfig.NewLayout(t.TempDir())
	h := &Handler{repo: newAnalyticsRepo(t)}
	out, err := h.buildAnalytics(httptest.NewRequest(http.MethodGet, "/", nil), testFilter(defaultWindowDays, true), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Series.Points) != defaultWindowDays {
		t.Errorf("%d points for an empty install, want the default window", len(out.Series.Points))
	}
}

// The streaming path exists so a slow load is legibly slow. It must end
// with the same payload the plain endpoint returns, and say how far along
// it was on the way.
func TestStreamReportsProgressThenTheData(t *testing.T) {
	h, _ := seedAnalytics(t)
	w := httptest.NewRecorder()
	h.analyticsUsersJSON(w, httptest.NewRequest(http.MethodGet, "/admin/analytics/users.json?stream=1", nil))

	if ct := w.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("content type = %q, want ndjson", ct)
	}
	var kinds []string
	var result *analyticsResponse
	for _, line := range strings.Split(strings.TrimSpace(w.Body.String()), "\n") {
		var msg struct {
			Type string             `json:"type"`
			Done int                `json:"done"`
			Data *analyticsResponse `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("line %q is not JSON: %v", line, err)
		}
		kinds = append(kinds, msg.Type)
		if msg.Type == "result" {
			result = msg.Data
		}
	}
	if len(kinds) < 2 || kinds[0] != "progress" {
		t.Errorf("stream = %v, want progress before the data", kinds)
	}
	if kinds[len(kinds)-1] != "result" {
		t.Errorf("stream ends with %q, want the result last", kinds[len(kinds)-1])
	}
	if result == nil || result.Sessions != 4 {
		t.Errorf("streamed payload = %+v, want the same numbers as the plain call", result)
	}
}

// The page must not confuse "we have no record" with "nobody ever signed
// in" — the second is a much stronger claim, and it was the wrong one for
// every account until sign-ins started being recorded.
func TestLoginsRecordedSinceSaysWhetherThereIsAnyHistory(t *testing.T) {
	h, _ := seedAnalytics(t)
	out := buildFixture(t, h)
	if out.LoginsRecordedSince == "" {
		t.Fatal("with sign-ins seeded, the page must say since when they exist")
	}

	// A fresh install with no sign-in yet: the field is empty, and the page
	// uses that to say "no record" instead of "never".
	empty := newAnalyticsRepo(t)
	h2 := &Handler{repo: empty}
	out2, err := h2.buildAnalytics(httptest.NewRequest(http.MethodGet, "/", nil), testFilter(defaultWindowDays, false), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out2.LoginsRecordedSince != "" {
		t.Errorf("no sign-ins recorded, but the page claims history since %q", out2.LoginsRecordedSince)
	}
}

// ── the filter binds every number, not just the chart ─────────────────

// seedForFilter builds sessions across two channels, two providers and two
// months, so a window or a channel filter has something to cut.
func seedForFilter(t *testing.T) *Handler {
	t.Helper()
	r := newAnalyticsRepo(t)
	ctx := context.Background()
	if err := r.db.Create(&entity.User{ID: "u-yoga", Name: "Yoga", Email: "y@example.com", Approved: true}).Error; err != nil {
		t.Fatal(err)
	}
	layout := agentconfig.NewLayout(t.TempDir())
	agentsLayout = layout
	now := time.Now().UTC()

	mk := func(id, origin string, at time.Time, provider, model string) {
		t.Helper()
		if _, err := session.Create(ctx, layout, session.CreateOptions{ID: id, Origin: session.Origin(origin), UserID: "u-yoga"}); err != nil {
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
		if provider != "" {
			if err := session.SaveAgents(layout, id, []session.AgentEntry{
				{Name: "main", Provider: provider, ModelID: model, CreatedAt: at},
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("recent-slack", "slack", now.AddDate(0, 0, -2), "claude/work", "opus")
	mk("recent-ui", "ui", now.AddDate(0, 0, -3), "claude/work", "sonnet")
	mk("recent-ui-2", "ui", now.AddDate(0, 0, -4), "codex/personal", "")
	mk("old-slack", "slack", now.AddDate(0, 0, -60), "claude/work", "opus")
	return &Handler{repo: r}
}

func build(t *testing.T, h *Handler, f analyticsFilter) analyticsResponse {
	t.Helper()
	out, err := h.buildAnalytics(httptest.NewRequest(http.MethodGet, "/", nil), f, nil)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The complaint that prompted this: picking a range moved the chart while
// the totals stayed put, so the page reported two different slices side by
// side. Every number now describes the window.
func TestWindowBindsEveryNumber(t *testing.T) {
	h := seedForFilter(t)

	week := build(t, h, testFilter(7, false))
	if week.Sessions != 3 {
		t.Errorf("7-day window = %d conversations, want the 3 recent ones", week.Sessions)
	}
	if week.SessionsAllTime != 4 {
		t.Errorf("all-time = %d, want 4 — the page must still say what it is a slice of", week.SessionsAllTime)
	}
	for _, c := range week.Channels {
		if c.Channel == "slack" && c.Sessions != 1 {
			t.Errorf("slack in window = %d, want 1 (the 60-day-old one is outside)", c.Sessions)
		}
	}
	total := 0
	for _, p := range week.Projects {
		total += p.Sessions
	}
	if total != 3 {
		t.Errorf("projects add up to %d, want the window's 3", total)
	}

	everything := build(t, h, testFilter(30, true))
	if everything.Sessions != 4 {
		t.Errorf("all-window = %d conversations, want every one", everything.Sessions)
	}
}

// A custom range is the more specific ask, so it wins over ?days=.
func TestCustomRangeSelectsItsOwnDays(t *testing.T) {
	now := time.Now().UTC()
	q := url.Values{}
	q.Set("days", "7")
	q.Set("from", now.AddDate(0, 0, -10).Format("2006-01-02"))
	q.Set("to", now.AddDate(0, 0, -5).Format("2006-01-02"))
	f := parseFilter(q)
	if f.Days != 6 {
		t.Errorf("days = %d, want the 6 days the range spans", f.Days)
	}
	if f.All {
		t.Error("a custom range is not the all-window")
	}

	// Backwards is a slip, not a request for nothing.
	q.Set("from", now.Format("2006-01-02"))
	q.Set("to", now.AddDate(0, 0, -3).Format("2006-01-02"))
	if got := parseFilter(q); got.From.After(got.To) {
		t.Errorf("a backwards range must be swapped, got %v → %v", got.From, got.To)
	}
}

// Filtering to a channel filters the whole page — and must not remove the
// means of filtering back out.
func TestChannelFilterAppliesEverywhere(t *testing.T) {
	h := seedForFilter(t)
	f := testFilter(defaultWindowDays, false)
	f.Chan = map[string]bool{"slack": true}
	out := build(t, h, f)

	if out.Sessions != 1 {
		t.Errorf("slack-only = %d conversations, want 1", out.Sessions)
	}
	if len(out.Channels) != 1 || out.Channels[0].Channel != "slack" {
		t.Errorf("channels = %+v, want only slack", out.Channels)
	}
	for _, p := range out.Providers {
		if p.Key != "claude/work" {
			t.Errorf("provider %q survived a slack-only filter", p.Key)
		}
	}
	// Without this the UI has no way back: the filter chip for "ui" would
	// disappear along with its rows.
	if len(out.KnownChannels) < 2 {
		t.Errorf("known channels = %v, want every channel ever seen", out.KnownChannels)
	}
}

// "Which account do we lean on, and with which model" — read from each
// session's agents.json, not from the spawn log, which keeps only 50 files.
func TestProvidersAndModelsAreCounted(t *testing.T) {
	h := seedForFilter(t)
	out := build(t, h, testFilter(defaultWindowDays, false))

	byKey := map[string]analyticsProvider{}
	for _, p := range out.Providers {
		byKey[p.Key] = p
	}
	claude, ok := byKey["claude/work"]
	if !ok {
		t.Fatalf("providers = %+v, want claude/work", out.Providers)
	}
	if claude.Type != "claude" || claude.Instance != "work" {
		t.Errorf("provider = %+v, want type/instance split out", claude)
	}
	if claude.Sessions != 2 {
		t.Errorf("claude/work = %d conversations in window, want 2", claude.Sessions)
	}
	models := map[string]int{}
	for _, m := range claude.Models {
		models[m.Key] = m.Sessions
	}
	if models["opus"] != 1 || models["sonnet"] != 1 {
		t.Errorf("models = %v, want one opus and one sonnet", models)
	}
	codex, ok := byKey["codex/personal"]
	if !ok {
		t.Fatal("a second provider must appear on its own")
	}
	// No pin is a real answer — the provider chose — not a missing value.
	if len(codex.Models) != 1 || codex.Models[0].Key != "(default)" {
		t.Errorf("codex models = %+v, want the default marked as such", codex.Models)
	}
	if len(out.Series.ByProvider["claude/work"]) != defaultWindowDays {
		t.Errorf("provider curve has %d points, want the page's axis", len(out.Series.ByProvider["claude/work"]))
	}
}

// "Which provider does this person actually use" needs the tally per
// person, not only per provider — otherwise the answer is only available
// by opening every session they touched.
func TestPerUserProviderAndModelBreakdown(t *testing.T) {
	h := seedForFilter(t)
	out := build(t, h, testFilter(defaultWindowDays, false))

	var yoga *analyticsUser
	for i := range out.Users {
		if out.Users[i].ID == "u-yoga" {
			yoga = &out.Users[i]
		}
	}
	if yoga == nil {
		t.Fatal("the seeded person is missing")
	}
	if len(yoga.Providers) == 0 {
		t.Fatalf("no per-person providers: %+v", yoga)
	}
	// Busiest first, so the head is "the one they mostly use".
	if yoga.Providers[0].Key != "claude/work" || yoga.Providers[0].Sessions != 2 {
		t.Errorf("top provider = %+v, want claude/work with 2", yoga.Providers[0])
	}
	if len(yoga.Providers) != 2 {
		t.Errorf("providers = %+v, want both accounts they used", yoga.Providers)
	}
	models := map[string]int{}
	for _, m := range yoga.Models {
		models[m.Key] = m.Sessions
	}
	if models["opus"] != 1 || models["sonnet"] != 1 || models["(default)"] != 1 {
		t.Errorf("models = %v, want opus, sonnet and the unpinned one", models)
	}

	// And it moves with the window like every other number.
	narrow := testFilter(7, false)
	narrow.Chan = map[string]bool{"slack": true}
	only := build(t, h, narrow)
	for _, u := range only.Users {
		if u.ID != "u-yoga" {
			continue
		}
		if len(u.Providers) != 1 || u.Providers[0].Sessions != 1 {
			t.Errorf("slack-only providers = %+v, want just the one slack conversation", u.Providers)
		}
	}
}

// ── which bot, and whose ──────────────────────────────────────────────

// "slack: 372 conversations" does not say which bot is busy when several
// share the workspace, and it does not say whose connection it is. The
// instance is derivable without recording anything new: channel sessions
// are named after the bot that owns them.
func TestChannelInstanceComesFromTheBinding(t *testing.T) {
	owner := "ec0c0b8b-e73c-4561-9d19-bfa9c481a816"
	for _, tc := range []struct {
		name    string
		id      string
		channel string
		meta    session.Meta
		wantKey string
		wantOwn string
	}{
		{
			name:    "thread binding wins",
			id:      "anything",
			channel: "slack",
			meta:    session.Meta{ChannelRef: &session.ChannelRef{Instance: "slack-" + owner + "-"}},
			wantKey: "slack:" + owner,
			wantOwn: owner,
		},
		{
			name:    "falls back to the session id",
			id:      "slack-" + owner + "-1789006128.650979",
			channel: "slack",
			wantKey: "slack:" + owner,
			wantOwn: owner,
		},
		{
			// A dashboard session has no bot behind it, and "default" is
			// the honest answer rather than a guessed owner.
			name:    "a ui session has no instance",
			id:      "c045cbe1-934c-4b55-bbfc-6d0b34950612",
			channel: "ui",
			wantKey: "ui:default",
		},
		{
			name:    "a prefix that is not a uuid is not an owner",
			id:      "slack-not-a-uuid-at-all-1789006128.650979",
			channel: "slack",
			wantKey: "slack:default",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, own := channelInstanceOf(tc.id, tc.channel, tc.meta)
			if key != tc.wantKey || own != tc.wantOwn {
				t.Errorf("= %q,%q want %q,%q", key, own, tc.wantKey, tc.wantOwn)
			}
		})
	}
}

// The instance breakdown has to name a person, not a uuid, and has to be
// filterable — that is the whole point of splitting it out.
func TestChannelsSplitByInstanceWithOwner(t *testing.T) {
	r := newAnalyticsRepo(t)
	ctx := context.Background()
	if err := r.db.Create(&entity.User{ID: "u-yoga", Name: "Yoga", Email: "y@example.com", Approved: true}).Error; err != nil {
		t.Fatal(err)
	}
	// An owner uuid the id parser will accept.
	const botA = "ec0c0b8b-e73c-4561-9d19-bfa9c481a816"
	const botB = "aa11bb22-cc33-dd44-ee55-ff6677889900"
	if err := r.db.Create(&entity.User{ID: botA, Name: "Ygsw Bot Owner", Email: "bot@example.com", Approved: true}).Error; err != nil {
		t.Fatal(err)
	}

	layout := agentconfig.NewLayout(t.TempDir())
	agentsLayout = layout
	now := time.Now().UTC()
	mk := func(id string) {
		t.Helper()
		if _, err := session.Create(ctx, layout, session.CreateOptions{ID: id, Origin: session.OriginSlack, UserID: "u-yoga"}); err != nil {
			t.Fatal(err)
		}
		s, err := session.Load(layout, id)
		if err != nil {
			t.Fatal(err)
		}
		s.Meta.CreatedAt, s.Meta.LastActive = now, now
		if err := session.SaveMeta(layout, id, s.Meta); err != nil {
			t.Fatal(err)
		}
	}
	mk("slack-" + botA + "-1789006128.650979")
	mk("slack-" + botA + "-1789006128.650980")
	mk("slack-" + botB + "-1789006128.650981")

	h := &Handler{repo: r}
	out := build(t, h, testFilter(defaultWindowDays, false))

	var slack *analyticsChannel
	for i := range out.Channels {
		if out.Channels[i].Channel == "slack" {
			slack = &out.Channels[i]
		}
	}
	if slack == nil || len(slack.Instances) != 2 {
		t.Fatalf("slack instances = %+v, want two bots", slack)
	}
	// Busiest first, so the head answers "which bot is carrying this".
	top := slack.Instances[0]
	if top.Key != "slack:"+botA || top.Sessions != 2 {
		t.Errorf("top instance = %+v, want botA with 2", top)
	}
	if top.OwnerName != "Ygsw Bot Owner" {
		t.Errorf("owner = %q — a uuid is not an answer to \"whose bot is this\"", top.OwnerName)
	}
	// An instance whose owner is not an account still appears; it just has
	// no name. Dropping it would hide real traffic.
	if slack.Instances[1].OwnerName != "" || slack.Instances[1].Sessions != 1 {
		t.Errorf("second instance = %+v, want the unnamed one kept", slack.Instances[1])
	}

	// And it filters the whole page, exactly like a channel does.
	f := testFilter(defaultWindowDays, false)
	f.Inst = map[string]bool{"slack:" + botA: true}
	only := build(t, h, f)
	if only.Sessions != 2 {
		t.Errorf("filtered to one bot = %d conversations, want 2", only.Sessions)
	}
	if got := only.Window.Instances; len(got) != 1 || got[0] != "slack:"+botA {
		t.Errorf("window echoes %v, want the instance it filtered on", got)
	}
}

// ── the cache ─────────────────────────────────────────────────────────

// The page's cost is I/O, not arithmetic: every render reads a file per
// session. The second render must not.
func TestSessionFactsAreCachedUntilTheFileChanges(t *testing.T) {
	layout := agentconfig.NewLayout(t.TempDir())
	agentsLayout = layout
	sessionCache = &factsCache{m: map[string]sessionFacts{}}
	ctx := context.Background()

	if _, err := session.Create(ctx, layout, session.CreateOptions{ID: "s1", Origin: session.OriginUI, UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	first, ok := sessionCache.get(layout, "s1")
	if !ok {
		t.Fatal("a session that exists must be readable")
	}
	if sessionCache.len() != 1 {
		t.Fatalf("cache holds %d, want the one session", sessionCache.len())
	}

	// Unchanged file: same facts, and no re-read (proved by the label,
	// which only a re-read could pick up — see below).
	again, _ := sessionCache.get(layout, "s1")
	if again.LastActive != first.LastActive {
		t.Error("an unchanged session must return the cached facts")
	}

	// Changed file: the cache must notice. A TTL would not — this is why
	// validation is by mtime+size rather than by a timer.
	s, err := session.Load(layout, "s1")
	if err != nil {
		t.Fatal(err)
	}
	s.Meta.Label = "renamed"
	s.Meta.LastActive = time.Now().UTC().Add(time.Hour)
	if err := session.SaveMeta(layout, "s1", s.Meta); err != nil {
		t.Fatal(err)
	}
	fresh, _ := sessionCache.get(layout, "s1")
	if fresh.Label != "renamed" {
		t.Errorf("label = %q, want the edit — a stale cache would serve numbers that are quietly wrong", fresh.Label)
	}
}

// A deleted session must stop being remembered, or the cache grows with
// everything the process has ever seen.
func TestCacheForgetsDeletedSessions(t *testing.T) {
	layout := agentconfig.NewLayout(t.TempDir())
	agentsLayout = layout
	sessionCache = &factsCache{m: map[string]sessionFacts{}}
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if _, err := session.Create(ctx, layout, session.CreateOptions{ID: id, Origin: session.OriginUI}); err != nil {
			t.Fatal(err)
		}
		if _, ok := sessionCache.get(layout, id); !ok {
			t.Fatal(id)
		}
	}
	if sessionCache.len() != 2 {
		t.Fatalf("cache holds %d, want 2", sessionCache.len())
	}
	sessionCache.keep(map[string]bool{"a": true})
	if sessionCache.len() != 1 {
		t.Errorf("cache holds %d after a delete, want 1", sessionCache.len())
	}
}

// A file that cannot be stat'd (mid-write, or gone between the listing and
// the read) is skipped, not cached as an error — otherwise that session
// stays missing from the page until the process restarts.
func TestUnreadableSessionIsNotCached(t *testing.T) {
	layout := agentconfig.NewLayout(t.TempDir())
	sessionCache = &factsCache{m: map[string]sessionFacts{}}
	if _, ok := sessionCache.get(layout, "does-not-exist"); ok {
		t.Error("a missing session must not report facts")
	}
	if sessionCache.len() != 0 {
		t.Error("a failure must not be remembered")
	}
}

// A person's panel used to end at "providers & models", which says what
// they ran on but not what they were running. The drill-down answers the
// question the numbers raise: which conversations, on which provider, in
// which project, and how long ago.
func TestUsersCarryTheirRecentConversations(t *testing.T) {
	h := seedForFilter(t)
	out := build(t, h, testFilter(7, false))

	var yoga *analyticsUser
	for i := range out.Users {
		if out.Users[i].ID == "u-yoga" {
			yoga = &out.Users[i]
		}
	}
	if yoga == nil {
		t.Fatalf("the seeded person is missing from %+v", out.Users)
	}
	if len(yoga.Recent) != 3 {
		t.Fatalf("recent = %d conversations, want the window's 3", len(yoga.Recent))
	}
	// Newest first: "what were they just working on" is the question.
	if yoga.Recent[0].ID != "recent-slack" || yoga.Recent[2].ID != "recent-ui-2" {
		t.Errorf("order = %s…%s, want newest first", yoga.Recent[0].ID, yoga.Recent[2].ID)
	}
	for _, s := range yoga.Recent {
		if len(s.Providers) == 0 {
			t.Errorf("%s names no provider, but the page shows which account ran it", s.ID)
		}
		if s.LastActiveAt == "" {
			t.Errorf("%s has no last activity, so the row cannot say when it last moved", s.ID)
		}
		if s.Project == "" {
			t.Errorf("%s names no project, and a person's list is the one place that matters", s.ID)
		}
	}
	if got := yoga.Recent[0].Providers[0]; got != "claude/work" {
		t.Errorf("provider = %q, want the account the session actually ran on", got)
	}
	// The project list and the person's list are built from the same rows.
	for _, p := range out.Projects {
		if len(p.Recent) == 0 {
			t.Errorf("project %s lost its own list", p.ID)
		}
	}
}

// Both drill-downs trim through one helper, so they cannot disagree about
// which conversations are the newest — or leave a uuid where a name goes.
func TestNewestFirstTrimsAndNamesTheCreator(t *testing.T) {
	list := []analyticsSessionRef{
		{ID: "c", User: "u-yoga", LastActiveAt: "2026-09-10T00:00:00Z"},
		{ID: "a", User: "u-gone", LastActiveAt: "2026-09-12T00:00:00Z"},
		{ID: "b", User: "", LastActiveAt: "2026-09-11T00:00:00Z"},
	}
	got := newestFirst(list, 2, map[string]string{"u-yoga": "Yoga"})
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("trimmed = %+v, want the newest 2 in order", got)
	}
	if got[0].User != "u-gone" {
		t.Errorf("an unknown account = %q, want the id kept rather than blanked", got[0].User)
	}
	named := newestFirst(list, 3, map[string]string{"u-yoga": "Yoga"})
	if named[2].User != "Yoga" {
		t.Errorf("creator = %q, want the name a human recognises", named[2].User)
	}
}
