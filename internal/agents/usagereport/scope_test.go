package usagereport

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/store"
)

// A conversation that failed over or was switched by hand ran on both
// accounts, so it belongs to both selections — and one with nothing
// recorded is selectable on its own rather than silently dropped.
func TestScopeKeepsByProvider(t *testing.T) {
	switched := store.SessionTags{Channel: "ui", Providers: []string{"claude/acct-a", "codex/acct-b"}}
	single := store.SessionTags{Channel: "slack", Providers: []string{"claude/acct-a"}}
	none := store.SessionTags{Channel: "rest"}

	cases := []struct {
		name string
		sc   Scope
		tags store.SessionTags
		want bool
	}{
		{"empty scope keeps all", Scope{}, none, true},
		{"picked provider", Scope{Providers: []string{"claude/acct-a"}}, single, true},
		{"other provider", Scope{Providers: []string{"codex/acct-b"}}, single, false},
		{"switched matches the first", Scope{Providers: []string{"claude/acct-a"}}, switched, true},
		{"switched matches the second", Scope{Providers: []string{"codex/acct-b"}}, switched, true},
		{"none bucket", Scope{Providers: []string{NoProvider}}, none, true},
		{"none bucket excludes recorded", Scope{Providers: []string{NoProvider}}, single, false},
		{"recorded excludes none", Scope{Providers: []string{"claude/acct-a"}}, none, false},
		{"and with channel", Scope{Channels: []string{"ui"}, Providers: []string{"claude/acct-a"}}, single, false},
	}
	for _, c := range cases {
		if got := c.sc.Keep(c.tags); got != c.want {
			t.Errorf("%s: Keep = %v, want %v", c.name, got, c.want)
		}
	}
}

// The roll-up cache is keyed by scope: a provider selection served the
// answer cached for another would be quietly wrong.
func TestScopeKeyDiffersPerProviderSelection(t *testing.T) {
	keys := map[string]string{}
	for name, sc := range map[string]Scope{
		"none":     {},
		"a":        {Providers: []string{"claude/acct-a"}},
		"b":        {Providers: []string{"codex/acct-b"}},
		"a+b":      {Providers: []string{"claude/acct-a", "codex/acct-b"}},
		"no-prov":  {Providers: []string{NoProvider}},
		"a+ui":     {Channels: []string{"ui"}, Providers: []string{"claude/acct-a"}},
		"ui alone": {Channels: []string{"ui"}},
		"person":   {People: []string{"user-a"}},
		"model":    {Models: []string{"user-a"}},
	} {
		k := sc.Key()
		if other, dup := keys[k]; dup {
			t.Errorf("scopes %q and %q share cache key %q", name, other, k)
		}
		keys[k] = name
	}
	if (Scope{Providers: []string{"claude/acct-a"}}).Empty() {
		t.Error("a provider selection narrows the scope, so it is not empty")
	}
}

func TestScopeKeepsByPeopleAndModel(t *testing.T) {
	both := store.SessionTags{UserID: "user-a", People: []string{"user-a", "user-b"}, Models: []string{"opus", "sonnet"}}
	ownerOnly := store.SessionTags{UserID: "user-b"}
	nobody := store.SessionTags{}
	cases := []struct {
		name string
		sc   Scope
		tags store.SessionTags
		want bool
	}{
		{"joined person", Scope{People: []string{"user-b"}}, both, true},
		{"owner when people unset", Scope{People: []string{"user-b"}}, ownerOnly, true},
		{"other person", Scope{People: []string{"user-c"}}, both, false},
		{"unattributed", Scope{People: []string{NoPerson}}, nobody, true},
		{"second model", Scope{Models: []string{"sonnet"}}, both, true},
		{"no model recorded", Scope{Models: []string{NoProvider}}, nobody, true},
		{"model excludes none", Scope{Models: []string{"opus"}}, nobody, false},
	}
	for _, c := range cases {
		if got := c.sc.Keep(c.tags); got != c.want {
			t.Errorf("%s: Keep = %v, want %v", c.name, got, c.want)
		}
	}
}
