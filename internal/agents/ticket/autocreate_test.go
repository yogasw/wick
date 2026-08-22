package ticket

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/project"
)

func rule(origin, kind, match string) project.AutoCreateRule {
	return project.AutoCreateRule{Origin: origin, ChannelKind: kind, Match: match, Enabled: true}
}

func TestValidateAutoCreate(t *testing.T) {
	cases := []struct {
		name    string
		rules   []project.AutoCreateRule
		wantErr string
	}{
		{"empty is fine", nil, ""},
		{"origin required", []project.AutoCreateRule{{Enabled: true}}, "origin"},
		{"wildcard origin ok", []project.AutoCreateRule{rule("*", "", "")}, ""},
		{"contains ok", []project.AutoCreateRule{rule("slack", "", "contains:bug")}, ""},
		{"regex ok", []project.AutoCreateRule{rule("*", "", `regex:^BUG-\d+`)}, ""},
		// A regex that cannot compile would otherwise be a rule that
		// silently never fires — refuse it at the door instead.
		{"broken regex refused", []project.AutoCreateRule{rule("*", "", "regex:[unclosed")}, "regex"},
		{"unknown match prefix refused", []project.AutoCreateRule{rule("*", "", "startswith:foo")}, "match"},
		{"empty contains refused", []project.AutoCreateRule{rule("*", "", "contains:")}, "match"},
		{"unknown channel kind refused", []project.AutoCreateRule{rule("slack", "group", "")}, "channel_kind"},
	}
	for _, c := range cases {
		err := ValidateAutoCreate(c.rules)
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: error = %v, want it to mention %q", c.name, err, c.wantErr)
		}
	}
}

func TestShouldAutoCreate(t *testing.T) {
	cases := []struct {
		name  string
		rules []project.AutoCreateRule
		in    AutoCreateInput
		want  bool
	}{
		{
			"off by default",
			nil,
			AutoCreateInput{Origin: "ui", FirstMessage: "anything"},
			false,
		},
		{
			"origin match",
			[]project.AutoCreateRule{rule("ui", "", "")},
			AutoCreateInput{Origin: "ui", FirstMessage: "hello"},
			true,
		},
		{
			"origin mismatch",
			[]project.AutoCreateRule{rule("ui", "", "")},
			AutoCreateInput{Origin: "slack", FirstMessage: "hello"},
			false,
		},
		{
			"wildcard origin matches anything",
			[]project.AutoCreateRule{rule("*", "", "")},
			AutoCreateInput{Origin: "telegram", FirstMessage: "hi"},
			true,
		},
		{
			"disabled rule is skipped",
			[]project.AutoCreateRule{{Origin: "*", Enabled: false}},
			AutoCreateInput{Origin: "ui", FirstMessage: "hi"},
			false,
		},
		// The case the user named: track Slack channels, leave DMs alone.
		{
			"slack channel yes",
			[]project.AutoCreateRule{rule("slack", "channel", "")},
			AutoCreateInput{Origin: "slack", ChannelKind: "channel", FirstMessage: "deploy broke"},
			true,
		},
		{
			"slack dm no",
			[]project.AutoCreateRule{rule("slack", "channel", "")},
			AutoCreateInput{Origin: "slack", ChannelKind: "dm", FirstMessage: "deploy broke"},
			false,
		},
		{
			"contains matches case-insensitively",
			[]project.AutoCreateRule{rule("*", "", "contains:BUG")},
			AutoCreateInput{Origin: "ui", FirstMessage: "there is a bug in checkout"},
			true,
		},
		{
			"contains not present",
			[]project.AutoCreateRule{rule("*", "", "contains:bug")},
			AutoCreateInput{Origin: "ui", FirstMessage: "just a question"},
			false,
		},
		{
			"regex matches",
			[]project.AutoCreateRule{rule("*", "", `regex:^BUG-\d+`)},
			AutoCreateInput{Origin: "ui", FirstMessage: "BUG-1234 payments down"},
			true,
		},
		{
			"regex does not match",
			[]project.AutoCreateRule{rule("*", "", `regex:^BUG-\d+`)},
			AutoCreateInput{Origin: "ui", FirstMessage: "payments down"},
			false,
		},
		// First match wins, so a narrow exception placed first can carve a
		// hole in a broad rule below it.
		{
			"first match wins: exception before the broad rule",
			[]project.AutoCreateRule{
				{Origin: "slack", ChannelKind: "dm", Enabled: false},
				rule("slack", "", ""),
			},
			AutoCreateInput{Origin: "slack", ChannelKind: "dm", FirstMessage: "hi"},
			false,
		},
		{
			"broad rule still applies to other kinds",
			[]project.AutoCreateRule{
				{Origin: "slack", ChannelKind: "dm", Enabled: false},
				rule("slack", "", ""),
			},
			AutoCreateInput{Origin: "slack", ChannelKind: "channel", FirstMessage: "hi"},
			true,
		},
		// A sub-agent's session is a working context, not somebody's piece
		// of work, so it never gets a ticket of its own.
		{
			"sub-agent session never auto-created",
			[]project.AutoCreateRule{rule("*", "", "")},
			AutoCreateInput{Origin: "ui", FirstMessage: "do the thing", IsSubAgent: true},
			false,
		},
		{
			"a session already on a ticket is left alone",
			[]project.AutoCreateRule{rule("*", "", "")},
			AutoCreateInput{Origin: "ui", FirstMessage: "hi", AlreadyTicketed: true},
			false,
		},
		// An empty first message means the text condition cannot be judged
		// yet; a rule with no text condition still fires.
		{
			"no message, text rule waits",
			[]project.AutoCreateRule{rule("*", "", "contains:bug")},
			AutoCreateInput{Origin: "ui", FirstMessage: ""},
			false,
		},
		{
			"no message, origin-only rule fires",
			[]project.AutoCreateRule{rule("*", "", "")},
			AutoCreateInput{Origin: "ui", FirstMessage: ""},
			true,
		},
	}
	for _, c := range cases {
		got, _ := ShouldAutoCreate(c.rules, c.in)
		if got != c.want {
			t.Errorf("%s: ShouldAutoCreate = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestShouldAutoCreateReturnsTheMatchedRule(t *testing.T) {
	rules := []project.AutoCreateRule{
		rule("slack", "channel", ""),
		{Origin: "ui", Enabled: true, Title: "From the dashboard: {message}"},
	}
	ok, matched := ShouldAutoCreate(rules, AutoCreateInput{Origin: "ui", FirstMessage: "check the queue"})
	if !ok {
		t.Fatal("expected a match")
	}
	if matched.Title != "From the dashboard: {message}" {
		t.Fatalf("matched the wrong rule: %+v", matched)
	}
}

func TestAutoCreateTitle(t *testing.T) {
	long := strings.Repeat("a very long first message ", 20)
	cases := []struct {
		name string
		rule project.AutoCreateRule
		in   AutoCreateInput
		want string
	}{
		{
			"defaults to the message",
			rule("*", "", ""),
			AutoCreateInput{Origin: "ui", FirstMessage: "Payments are failing"},
			"Payments are failing",
		},
		{
			"template substitutes message and origin",
			project.AutoCreateRule{Origin: "*", Enabled: true, Title: "[{origin}] {message}"},
			AutoCreateInput{Origin: "slack", FirstMessage: "queue stuck"},
			"[slack] queue stuck",
		},
		{
			"a message-less session still gets a usable title",
			rule("*", "", ""),
			AutoCreateInput{Origin: "slack", FirstMessage: ""},
			"New slack session",
		},
	}
	for _, c := range cases {
		if got := AutoCreateTitle(c.rule, c.in); got != c.want {
			t.Errorf("%s: title = %q, want %q", c.name, got, c.want)
		}
	}

	// Titles are read on a board card, so they are capped rather than
	// letting one message stretch the column.
	got := AutoCreateTitle(rule("*", "", ""), AutoCreateInput{Origin: "ui", FirstMessage: long})
	if len([]rune(got)) > titleMaxRunes {
		t.Fatalf("title not truncated: %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated title should be marked: %q", got)
	}
}
