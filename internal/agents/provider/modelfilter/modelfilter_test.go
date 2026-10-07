package modelfilter

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		hay, q string
		want   bool
	}{
		{"openai/gpt-5.5", "", true},
		{"openai/gpt-5.5", "   ", true},
		{"openai/gpt-5.5", "GPT", true},
		{"openai/gpt-5.5", "gpt openai", true},
		{"openai/gpt-5.5", "gpt claude", false},
		{"openai/gpt-5.5-mini", "-mini", false},
		{"openai/gpt-5.5-mini", "!mini", false},
		{"openai/gpt-5.5", "!mini", true},
		{"openai/gpt-5.5", "- !", true},
		// OR inside one term
		{"anthropic/claude-sonnet", "claude|gpt", true},
		{"openai/gpt-5.5", "claude|gpt", true},
		{"google/gemini-3", "claude|gpt", false},
		{"openai/gpt-5.5-mini", "claude|gpt !mini", false},
		{"openai/gpt-5.5", "claude|gpt !mini", true},
		// excluded OR: neither
		{"openai/gpt-5.5", "!claude|gpt", false},
		{"anthropic/claude", "!claude|gpt", false},
		{"google/gemini-3", "!claude|gpt", true},
		// empty alternatives are ignored
		{"google/gemini-3", "gemini||", true},
		{"google/gemini-3", "|", true},
		{"google/gemini-3", "!|", true},
	}
	for _, c := range cases {
		if got := Match(c.hay, c.q); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.hay, c.q, got, c.want)
		}
	}
}
