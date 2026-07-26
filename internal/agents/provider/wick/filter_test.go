package wick

import "testing"

func TestMatchFilter(t *testing.T) {
	m := DiscoveredModel{ID: "cc/claude-opus-4-6", Label: "Claude Opus"}
	cases := []struct {
		query string
		want  bool
	}{
		{"", true},                    // blank → everything
		{"claude", true},              // contains (label)
		{"cc/", true},                 // contains (id)
		{"gpt", false},                // missing include term
		{"cc/ -opus", false},          // excluded by -opus
		{"cc/ !opus", false},          // excluded by !opus
		{"cc/ -mini", true},           // exclude term absent → still matches
		{"claude opus", true},         // multiple include terms, all present
		{"claude sonnet", false},      // one include term missing
		{"-", true},                   // lone dash is ignored
	}
	for _, c := range cases {
		if got := MatchFilter(m, c.query); got != c.want {
			t.Errorf("MatchFilter(%q) = %v, want %v", c.query, got, c.want)
		}
	}
}

func TestFilterModels(t *testing.T) {
	models := []DiscoveredModel{
		{ID: "gpt-5.2", Label: "GPT 5.2"},
		{ID: "gpt-5.2-mini", Label: "GPT 5.2 mini"},
		{ID: "claude-x", Label: "Claude X"},
	}
	got := FilterModels(models, "gpt -mini")
	if len(got) != 1 || got[0].ID != "gpt-5.2" {
		t.Fatalf("FilterModels(gpt -mini) = %+v, want [gpt-5.2]", got)
	}
	// Blank query returns the input untouched.
	if len(FilterModels(models, "  ")) != len(models) {
		t.Errorf("blank query should return all %d models", len(models))
	}
}
