package systemprompt

import (
	"strings"
	"testing"
)

// An ordinary main spawn keeps every gated section and none of the marker
// lines: gates exist only for Team agents.
func TestImmutableFor_KeepsGatedSectionsWithoutMarkers(t *testing.T) {
	got := ImmutableFor("claude", false)
	for _, h := range []string{"## Session title", "## Scheduling yourself", "## Delegating work"} {
		if !strings.Contains(got, h) {
			t.Errorf("main overlay lost %q", h)
		}
	}
	if strings.Contains(got, "gate:") {
		t.Error("gate marker leaked into the prompt")
	}
}

func TestImmutableForTeam_Gates(t *testing.T) {
	for _, tc := range []struct {
		g                  TeamGates
		delegate, schedule bool
	}{
		{TeamGates{}, false, false},
		{TeamGates{Subagents: true}, true, false},
		{TeamGates{Schedule: true}, false, true},
		{TeamGates{Subagents: true, Schedule: true}, true, true},
	} {
		got := ImmutableForTeam("claude", tc.g)
		if strings.Contains(got, "## Session title") {
			t.Errorf("%+v: Team prompt keeps the session-title section", tc.g)
		}
		if strings.Contains(got, "## Delegating work") != tc.delegate {
			t.Errorf("%+v: delegating present = %v, want %v", tc.g, !tc.delegate, tc.delegate)
		}
		if strings.Contains(got, "## Scheduling yourself") != tc.schedule {
			t.Errorf("%+v: scheduling present = %v, want %v", tc.g, !tc.schedule, tc.schedule)
		}
		// Ungated neighbours survive whatever is cut.
		for _, h := range []string{"## Long work reports back", "## Knowing your own context", "## Silent replies"} {
			if !strings.Contains(got, h) {
				t.Errorf("%+v: lost ungated %q", tc.g, h)
			}
		}
		if strings.Contains(got, "gate:") {
			t.Errorf("%+v: gate marker leaked", tc.g)
		}
	}
	all := ImmutableForTeam("claude", TeamGates{Subagents: true, Schedule: true})
	t.Logf("immutable chars: main=%d team(all on)=%d team(all off)=%d",
		len(ImmutableFor("claude", false)), len(all), len(ImmutableForTeam("claude", TeamGates{})))
}

func TestApplyGates(t *testing.T) {
	in := "a\n<!-- gate:x -->\nX\n<!-- /gate:x -->\nb\n<!-- gate:y -->\nY\n<!-- /gate:y -->\n"
	if got := applyGates(in, nil); got != "a\nX\nb\nY\n" {
		t.Errorf("no skip: %q", got)
	}
	if got := applyGates(in, map[string]bool{"x": true}); got != "a\nb\nY\n" {
		t.Errorf("skip x: %q", got)
	}
	// An unclosed gate is left alone rather than eating the rest.
	if got := applyGates("a\n<!-- gate:z -->\nZ\n", map[string]bool{"z": true}); got != "a\n<!-- gate:z -->\nZ\n" {
		t.Errorf("unclosed: %q", got)
	}
}

func TestImmutableForTeamGatesHTMLOnFiles(t *testing.T) {
	on := ImmutableForTeam("claude", TeamGates{Files: true})
	off := ImmutableForTeam("claude", TeamGates{Files: false})
	for _, want := range []string{"HTML preview (inline)", "### HTML artifacts", "htmlfile"} {
		if !strings.Contains(on, want) {
			t.Errorf("Files on lost %q", want)
		}
	}
	if strings.Contains(off, "HTML preview") || strings.Contains(off, "### HTML artifacts") || strings.Contains(off, "```htmlfile") {
		t.Error("Files off still describes the HTML formats")
	}
	if !strings.Contains(off, "Mermaid diagrams") || strings.Contains(on+off, "<!-- gate:") {
		t.Error("table broken or gate markers leaked")
	}
	if strings.Contains(ImmutableFor("claude", false), "<!-- gate:") || !strings.Contains(ImmutableFor("claude", false), "### HTML artifacts") {
		t.Error("non-Team prompt changed")
	}
}
