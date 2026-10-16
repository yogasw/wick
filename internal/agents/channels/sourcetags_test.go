package channels

import (
	"strings"
	"testing"
)

func TestRewriteSourceTags(t *testing.T) {
	names := map[string]string{"alpha": "Alpha", "beta": "Beta Team"}
	cases := []struct{ name, in, want string }{
		{"single", "- Logs are clean [from:@alpha]", "- Logs are clean (from Alpha)"},
		{"several", "- Both agree [from:@alpha,@beta]\n- Own point", "- Both agree (from Alpha, Beta Team)\n- Own point"},
		{"spaces in list", "Done.[from: @alpha , @beta ]", "Done. (from Alpha, Beta Team)"},
		{"case-insensitive handle", "x [from:@Alpha]", "x (from Alpha)"},
		{"duplicate handle once", "x [from:@alpha,@alpha]", "x (from Alpha)"},
		{"unknown dropped, tag removed whole", "x [from:@ghost]", "x"},
		{"mixed keeps the known", "x [from:@ghost,@alpha]", "x (from Alpha)"},
		{"CRLF line end", "- a [from:@alpha]\r\n- b [from:@beta]\r\n", "- a (from Alpha)\r\n- b (from Beta Team)\r\n"},
		{"inline code untouched", "use `[from:@alpha]` to tag [from:@beta]", "use `[from:@alpha]` to tag (from Beta Team)"},
		{"fence untouched", "```\n- a [from:@alpha]\n```\n- b [from:@alpha]", "```\n- a [from:@alpha]\n```\n- b (from Alpha)"},
		{"CRLF fence untouched", "```go\r\n[from:@alpha]\r\n```\r\n", "```go\r\n[from:@alpha]\r\n```\r\n"},
		// The web renderer does not read a four-backtick line as a fence:
		// the ``` after it opens the block, so the tag inside stays.
		{"four backticks are not a fence", "````\n```\n- x [from:@alpha]\n````", "````\n```\n- x [from:@alpha]\n````"},
		{"not a tag", "see [from] and [from:alpha]", "see [from] and [from:alpha]"},
		{"no tag", "plain text", "plain text"},
	}
	for _, tc := range cases {
		if got := RewriteSourceTagsWith(tc.in, names, nil); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRewriteSourceTagsEscapesNames(t *testing.T) {
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace
	got := RewriteSourceTagsWith("x [from:@evil]", map[string]string{"evil": "<!channel> & <@U1>"}, esc)
	if got != "x (from &lt;!channel&gt; &amp; &lt;@U1&gt;)" {
		t.Fatalf("got %q", got)
	}
}

func TestRewriteSourceTagsUsesResolver(t *testing.T) {
	prev := SourceNames
	t.Cleanup(func() { SourceNames = prev })
	var asked string
	SourceNames = func(sessionID string) map[string]string {
		asked = sessionID
		return map[string]string{"alpha": "Alpha"}
	}
	if got := RewriteSourceTags("s1", "a [from:@alpha]", nil); got != "a (from Alpha)" {
		t.Fatalf("got %q", got)
	}
	if asked != "s1" {
		t.Fatalf("resolver asked for %q", asked)
	}
	asked = ""
	if got := RewriteSourceTags("s1", "no tags", nil); got != "no tags" || asked != "" {
		t.Fatalf("text without tags must not hit the roster: %q asked=%q", got, asked)
	}
	SourceNames = nil
	if got := RewriteSourceTags("s1", "a [from:@alpha]", nil); got != "a" {
		t.Fatalf("nil resolver: every handle unknown, tag removed; got %q", got)
	}
}

func TestTrimOpenSourceTag(t *testing.T) {
	cases := []struct{ in, want string }{
		{"- point [from:@alp", "- point"},
		{"- point [fro", "- point"},
		{"- point [", "- point"},
		{"- point (from Alpha)", "- point (from Alpha)"},
		{"see [link](x)", "see [link](x)"},
		{"```\ncode [from:@a", "```\ncode [from:@a"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := TrimOpenSourceTag(tc.in); got != tc.want {
			t.Errorf("TrimOpenSourceTag(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
