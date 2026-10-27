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

// The ledger filters by the same rule, reading the id the roll-up stamps.
func TestScopeKeepsByText(t *testing.T) {
	tags := store.SessionTags{ID: "sess-a", Label: "nightly sync", Channel: "ui"}
	if !(Scope{Text: ParseTextMatch("nightly", "title", "contains")}).Keep(tags) {
		t.Error("title match dropped")
	}
	if (Scope{Text: ParseTextMatch("sess-b", "session", "exact")}).Keep(tags) {
		t.Error("other session id kept")
	}
	if (Scope{Text: ParseTextMatch("x", "", "")}).Empty() {
		t.Error("text scope reports empty")
	}
	a := Scope{Text: ParseTextMatch("nightly", "title", "contains")}.Key()
	b := Scope{Text: ParseTextMatch("nightly", "title", "regex")}.Key()
	if a == b || a == "" {
		t.Errorf("text scopes share cache key %q", a)
	}
}
