package channels

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitOptions(t *testing.T) {
	long := strings.Repeat("x", 50)
	cases := []struct {
		name, in, q string
		opts        []string
	}{
		{"parsed and stripped", "Diff both, or only the 20:55 push?\n[options: Both deploys | Only 20:55 | Only v2.14.0]",
			"Diff both, or only the 20:55 push?", []string{"Both deploys", "Only 20:55", "Only v2.14.0"}},
		{"trailing blank lines", "Which env?\n[options: prod | staging]\n\n", "Which env?", []string{"prod", "staging"}},
		{"CRLF and case", "Which env?\r\n[Options:prod|staging]\r\n", "Which env?", []string{"prod", "staging"}},
		{"empty choices dropped", "Pick\n[options: a || b | ]", "Pick", []string{"a", "b"}},
		{"capped at 4", "Pick\n[options: a | b | c | d | e]", "Pick", []string{"a", "b", "c", "d"}},
		{"long choice dropped", "Pick\n[options: " + long + " | b]", "Pick", []string{"b"}},
		{"choice at the cap kept", "Pick\n[options: " + strings.Repeat("y", MaxOptionLen) + "]", "Pick", []string{strings.Repeat("y", MaxOptionLen)}},
		{"no choices still stripped", "Pick\n[options: ]", "Pick", nil},
		{"only the line", "[options: a | b]", "", []string{"a", "b"}},
		{"no line", "Which env?", "Which env?", nil},
		{"not the last line", "[options: a | b]\nWhich one?", "[options: a | b]\nWhich one?", nil},
		{"malformed: no colon", "Pick\n[options a | b]", "Pick\n[options a | b]", nil},
		{"malformed: unclosed", "Pick\n[options: a | b", "Pick\n[options: a | b", nil},
		{"malformed: text after", "Pick\n[options: a | b] thanks", "Pick\n[options: a | b] thanks", nil},
	}
	for _, c := range cases {
		q, opts := SplitOptions(c.in)
		if q != c.q || !reflect.DeepEqual(opts, c.opts) {
			t.Errorf("%s: got %q %q, want %q %q", c.name, q, opts, c.q, c.opts)
		}
	}
}

func TestRewriteOptionsLine(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"choices", "Which env?\n[options: prod | staging]", "Which env?\n\nOptions: prod / staging"},
		{"no choices", "Which env?\n[options:]", "Which env?"},
		{"only the line", "[options: a | b]", "Options: a / b"},
		{"untouched", "Use [options: a] mid-text\nthen more", "Use [options: a] mid-text\nthen more"},
	}
	for _, c := range cases {
		if got := RewriteOptionsLine(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTrimOpenOptionsLine(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"open choices", "Which env?\n[options: prod | sta", "Which env?"},
		{"prefix only", "Which env?\n[opt", "Which env?"},
		{"bracket only", "Which env?\n[", "Which env?"},
		{"case-insensitive", "Which env?\n[OPTIONS: a", "Which env?"},
		{"closed line untouched", "Which env?\n[options: a | b]", "Which env?\n[options: a | b]"},
		{"mid-line bracket untouched", "see [opt", "see [opt"},
		{"other bracket line untouched", "Which env?\n[note", "Which env?\n[note"},
		{"plain text", "Which env?", "Which env?"},
	}
	for _, c := range cases {
		if got := TrimOpenOptionsLine(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
