package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func buildFixture(t *testing.T, h *Handler) analyticsResponse {
	t.Helper()
	out, err := h.buildAnalytics(httptest.NewRequest(http.MethodGet, "/admin/analytics/users.json", nil), defaultWindowDays, nil)
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
		if got := windowDays(tc.in); got != tc.want {
			t.Errorf("windowDays(%q) = %d, want %d", tc.in, got, tc.want)
		}
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
	out2, err := h2.buildAnalytics(httptest.NewRequest(http.MethodGet, "/", nil), defaultWindowDays, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out2.LoginsRecordedSince != "" {
		t.Errorf("no sign-ins recorded, but the page claims history since %q", out2.LoginsRecordedSince)
	}
}
