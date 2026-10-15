package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

// TestModuleVisibleWhen pins the engine gates: get_markdown only on Moli,
// pdf only on Chromium-based engines. Every engine named must be one the
// browser picker can store, or the op would be hidden everywhere.
func TestModuleVisibleWhen(t *testing.T) {
	mod := Module()
	if err := connector.ValidateModule(mod); err != nil {
		t.Fatalf("ValidateModule: %v", err)
	}
	want := map[string]string{
		"get_markdown": "browser:moli",
		"pdf":          "browser:chromium|moli",
	}
	got := map[string]string{}
	for _, op := range mod.AllOps() {
		if op.VisibleWhen != "" {
			got[op.Key] = op.VisibleWhen
		}
	}
	for key, pred := range want {
		if got[key] != pred {
			t.Errorf("%s VisibleWhen = %q, want %q", key, got[key], pred)
		}
		for _, v := range strings.Split(strings.TrimPrefix(pred, "browser:"), "|") {
			if !slices.Contains(browserEngines, v) {
				t.Errorf("%s names engine %q the picker never stores", key, v)
			}
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected VisibleWhen ops: %v", got)
	}
}
