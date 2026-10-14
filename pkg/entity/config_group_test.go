package entity

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEffectiveGroup(t *testing.T) {
	t.Cleanup(func() { DefaultGroupsCollapsed = nil })
	cases := map[string]string{
		"":                      "",
		"Safety":                "Safety||collapsed",
		"Safety|Look first":     "Safety|Look first|collapsed",
		"Safety|Look|collapsed": "Safety|Look|collapsed",
		"Connection|Creds|open": "Connection|Creds|open",
	}
	DefaultGroupsCollapsed = func() bool { return true }
	for in, want := range cases {
		if got := (Config{Group: in}).EffectiveGroup(); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
	DefaultGroupsCollapsed = func() bool { return false }
	if got := (Config{Group: "Safety"}).EffectiveGroup(); got != "Safety" {
		t.Errorf("setting off: %q", got)
	}
	DefaultGroupsCollapsed = func() bool { return true }
	b, _ := json.Marshal(Config{Key: "k", Group: "Safety"})
	if !strings.Contains(string(b), `"group":"Safety||collapsed"`) {
		t.Errorf("json: %s", b)
	}
}
