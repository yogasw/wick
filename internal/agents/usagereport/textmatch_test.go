package usagereport

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/store"
)

// One use case is followed by the shape of its title or its id: each mode
// and each field has to read the pattern the way the page labels it.
func TestTextMatchKeep(t *testing.T) {
	const id = "0f3c2a10-aaaa-bbbb-cccc-123456789abc"
	cases := []struct {
		name, pattern, in, mode, title string
		want                           bool
	}{
		{"empty keeps all", "", "", "", "anything", true},
		{"contains, any case", "report", "title", "contains", "Daily REPORT 2026-01-02", true},
		{"contains misses", "report", "title", "contains", "weekly digest", false},
		{"exact needs the whole title", "daily report", "title", "exact", "daily report 2", false},
		{"exact ignores case and padding", "daily report", "title", "exact", " Daily Report ", true},
		{"regex", `^daily-\d{4}-\d{2}`, "title", "regex", "daily-2026-01", true},
		{"regex misses", `^daily-\d{4}$`, "title", "regex", "daily-2026-01", false},
		{"session id contains", "0f3c2a10", "session", "contains", "unrelated", true},
		{"session field ignores title", "report", "session", "contains", "report", false},
		{"any reads the title", "report", "any", "contains", "report", true},
		{"any reads the id", "123456789abc", "any", "contains", "x", true},
		{"unknown field reads the title", "report", "bogus", "contains", "report", true},
		{"unknown mode is contains", "port", "title", "bogus", "report", true},
		{"not contains", "test", "title", "not_contains", "daily report", true},
		{"not contains drops a hit", "test", "title", "not_contains", "daily test", false},
		{"not exact", "daily", "title", "not_exact", "daily", false},
		{"not regex", `^daily`, "title", "not_regex", "weekly", true},
		{"not over either field", "0f3c", "any", "not_contains", "x", false},
	}
	for _, c := range cases {
		m := ParseTextMatch(c.pattern, c.in, c.mode)
		if got := m.Keep(id, c.title); got != c.want {
			t.Errorf("%s: Keep = %v, want %v", c.name, got, c.want)
		}
	}
}

// A regex that does not compile must keep nothing and say why: numbers
// that look filtered while showing everything would be a silent lie.
func TestTextMatchBadRegexKeepsNothing(t *testing.T) {
	m := ParseTextMatch("daily-(", "title", "regex")
	if m.Err == "" {
		t.Fatal("bad regex: no error reported")
	}
	if m.Keep("id", "daily-(") {
		t.Error("bad regex kept a session")
	}
}

// Several plain rules stand in for a regex: AND needs every one, OR any.
func TestTextMatchesJoin(t *testing.T) {
	vals := []string{"title:contains:daily-2026-10", "title:contains:shift-1"}
	and := ParseTextMatches(vals, "", "", "and")
	or := ParseTextMatches(vals, "", "", "or")
	cases := []struct {
		title       string
		wantAnd, or bool
	}{
		{"daily-2026-10-27 shift-1", true, true},
		{"daily-2026-10-27 shift-2", false, true},
		{"daily shift-1", false, true},
		{"unrelated", false, false},
	}
	for _, c := range cases {
		if got := and.Keep("id", c.title); got != c.wantAnd {
			t.Errorf("AND %q = %v, want %v", c.title, got, c.wantAnd)
		}
		if got := or.Keep("id", c.title); got != c.or {
			t.Errorf("OR %q = %v, want %v", c.title, got, c.or)
		}
	}
	not := ParseTextMatches(append(vals, "title:not_contains:test"), "", "", "and")
	if not.Keep("id", "daily-2026-10-27 shift-1 test") || !not.Keep("id", "daily-2026-10-27 shift-1") {
		t.Error("AND NOT did not drop the excluded title")
	}
	if got := not.Values()[2]; got != "title:not_contains:test" {
		t.Errorf("echo of a NOT rule = %q", got)
	}
	if and.Op() != "and" || or.Op() != "or" {
		t.Errorf("ops = %q / %q", and.Op(), or.Op())
	}
}

// Each rule carries its own field and mode; a value with no prefix is
// the older single-rule link and reads match_in / match_mode.
func TestParseTextMatchesForms(t *testing.T) {
	m := ParseTextMatches([]string{"session:exact:sess-a", "any:regex:^a:b$", ""}, "", "", "")
	if len(m.Rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(m.Rules))
	}
	if r := m.Rules[1]; r.In != "any" || r.Mode != "regex" || r.Pattern != "^a:b$" {
		t.Errorf("colon in pattern split wrong: %+v", r)
	}
	legacy := ParseTextMatches([]string{"report"}, "session", "exact", "")
	if r := legacy.Rules[0]; r.In != "session" || r.Mode != "exact" || r.Pattern != "report" {
		t.Errorf("legacy rule = %+v", r)
	}
	bad := ParseTextMatches([]string{"title:contains:a", "title:regex:("}, "", "", "or")
	if bad.Err() == "" || bad.Keep("id", "a") {
		t.Error("a broken rule must keep nothing, even under OR")
	}
}

// The ledger filters by the same rule, reading the id the roll-up stamps.
func TestScopeKeepsByText(t *testing.T) {
	tags := store.SessionTags{ID: "sess-a", Label: "nightly sync", Channel: "ui"}
	text := func(v ...string) TextMatches { return ParseTextMatches(v, "", "", "") }
	if !(Scope{Text: text("title:contains:nightly")}).Keep(tags) {
		t.Error("title match dropped")
	}
	if (Scope{Text: text("session:exact:sess-b")}).Keep(tags) {
		t.Error("other session id kept")
	}
	if (Scope{Text: text("x")}).Empty() {
		t.Error("text scope reports empty")
	}
	a := Scope{Text: text("title:contains:nightly")}.Key()
	b := Scope{Text: text("title:regex:nightly")}.Key()
	c := Scope{Text: ParseTextMatches([]string{"title:contains:nightly"}, "", "", "or")}.Key()
	if a == b || a == "" || a == c {
		t.Errorf("text scopes share cache key %q", a)
	}
}

// Conditions reach past the title: "title contains X AND model is not Y"
// is the reason the builder exists. List fields hit on any value, an
// empty list reads as the chips' none bucket, and ids answer to names.
func TestTextMatchesFields(t *testing.T) {
	s := Subject{
		ID: "sess-a", Title: "daily-2026-10-27 shift-1", Channel: "slack", Project: "proj-1",
		Agent: "main", Models: []string{"opus", "haiku"}, Providers: []string{"claude/acct-a"},
		People: []string{"user-a"},
	}
	alias := func(field, key string) []string {
		if field == MatchInPerson && key == "user-a" {
			return []string{"User A"}
		}
		if field == MatchInProject && key == "proj-1" {
			return []string{"Acme Support"}
		}
		return nil
	}
	cases := []struct {
		name string
		vals []string
		want bool
	}{
		{"title and model", []string{"title:contains:shift-1", "model:exact:haiku"}, true},
		{"model is not, any value hits", []string{"model:not_exact:haiku"}, false},
		{"channel", []string{"channel:exact:slack"}, true},
		{"channel is not", []string{"channel:not_exact:ui"}, true},
		{"provider", []string{"provider:contains:acct-a"}, true},
		{"person by name", []string{"person:contains:user a"}, true},
		{"person by id", []string{"person:exact:user-a"}, true},
		{"project by name", []string{"project:contains:acme"}, true},
		{"agent", []string{"agent:exact:main"}, true},
		{"empty model list is (none)", []string{"model:exact:(none)"}, false},
	}
	for _, c := range cases {
		m := ParseTextMatches(c.vals, "", "", "and")
		m.Alias = alias
		if got := m.KeepSubject(s); got != c.want {
			t.Errorf("%s: KeepSubject = %v, want %v", c.name, got, c.want)
		}
	}
	none := ParseTextMatches([]string{"model:exact:(none)"}, "", "", "")
	if !none.KeepSubject(Subject{ID: "b"}) {
		t.Error("a conversation with no model did not match (none)")
	}
}
