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

func TestStructToConfigsInternalFlag(t *testing.T) {
	type cfg struct {
		Token   string `wick:"secret;key=token"`
		Expires string `wick:"hidden;internal;key=expires"`
	}
	rows := StructToConfigs(cfg{Expires: "2026-10-15T00:00:00Z"})
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (internal rows are still seeded)", len(rows))
	}
	if rows[0].Internal {
		t.Error("token marked internal")
	}
	if !rows[1].Internal || !rows[1].Hidden || rows[1].Value != "2026-10-15T00:00:00Z" {
		t.Errorf("expires row = %+v, want internal + hidden with its value", rows[1])
	}
}
