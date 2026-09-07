package logintty

import "testing"

func linksOf(evs []Event) []string {
	var out []string
	for _, e := range evs {
		if e.Type == EventLink {
			out = append(out, e.Value)
		}
	}
	return out
}

func TestLinkParserFindsURLInPlainOutput(t *testing.T) {
	p := &LinkParser{}
	evs := p.Feed([]byte("Open this link:\r\nhttps://claude.ai/oauth/authorize?code=abc&state=xyz\r\n"))
	links := linksOf(evs)
	if len(links) != 1 || links[0] != "https://claude.ai/oauth/authorize?code=abc&state=xyz" {
		t.Fatalf("links = %v", links)
	}
}

func TestLinkParserStripsANSIEscapes(t *testing.T) {
	p := &LinkParser{}
	raw := "\x1b[1m\x1b[38;5;153mhttps://auth.openai.com/oauth/authorize?x=1\x1b[0m\r\n"
	links := linksOf(p.Feed([]byte(raw)))
	if len(links) != 1 || links[0] != "https://auth.openai.com/oauth/authorize?x=1" {
		t.Fatalf("links = %v", links)
	}
}

func TestLinkParserJoinsURLSplitAcrossChunks(t *testing.T) {
	p := &LinkParser{}
	var links []string
	links = append(links, linksOf(p.Feed([]byte("go to https://accounts.google.com/o/oauth2/v2/auth?client")))...)
	links = append(links, linksOf(p.Feed([]byte("_id=123&scope=email\r\n")))...)
	if len(links) != 1 || links[0] != "https://accounts.google.com/o/oauth2/v2/auth?client_id=123&scope=email" {
		t.Fatalf("links = %v", links)
	}
}

func TestLinkParserReportsEachURLOnce(t *testing.T) {
	p := &LinkParser{}
	url := "https://claude.ai/oauth/authorize?code=abc\r\n"
	first := linksOf(p.Feed([]byte(url)))
	second := linksOf(p.Feed([]byte(url)))
	if len(first) != 1 {
		t.Fatalf("first = %v", first)
	}
	if len(second) != 0 {
		t.Fatalf("second = %v, want none (dedupe)", second)
	}
}

func TestLinkParserTrimsTrailingPunctuation(t *testing.T) {
	p := &LinkParser{}
	links := linksOf(p.Feed([]byte("visit https://claude.ai/oauth/authorize?s=1).\r\n")))
	if len(links) != 1 || links[0] != "https://claude.ai/oauth/authorize?s=1" {
		t.Fatalf("links = %v", links)
	}
}

func TestLinkParserJoinsConPTYWrappedURL(t *testing.T) {
	// ConPTY re-renders the screen and injects literal \r\n at the
	// terminal width. A URL longer than the width arrives as
	// exact-width lines that must be re-joined before matching.
	p := &LinkParser{Cols: 40}
	long := "https://claude.ai/oauth/authorize?code_challenge=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&state=bbbbbbbb"
	wrapped := long[:40] + "\r\n" + long[40:80] + "\r\n" + long[80:] + "\r\n"
	links := linksOf(p.Feed([]byte(wrapped)))
	if len(links) != 1 || links[0] != long {
		t.Fatalf("links = %v, want joined %q", links, long)
	}
}

func TestLinkParserDoesNotJoinShortLines(t *testing.T) {
	p := &LinkParser{Cols: 40}
	links := linksOf(p.Feed([]byte("https://claude.ai/oauth?s=1\r\nSuccess text\r\n")))
	if len(links) != 1 || links[0] != "https://claude.ai/oauth?s=1" {
		t.Fatalf("links = %v (short URL line must not absorb next line)", links)
	}
}

func TestLinkParserJoinsWrappedURLWithoutKnownWidth(t *testing.T) {
	// Ink-style TUIs redraw lines at arbitrary widths, so wrap joining
	// can't rely on exact-cols matching: a URL cut at any line end must
	// be joined with the next line when it continues without a space.
	p := &LinkParser{Cols: 120} // deliberately wrong width
	raw := "https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a-e61b-44d9\r\n" +
		"&response_type=code&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth\r\n" +
		"&code_challenge=EP9ZFyZt7vmXiM0yWH1VGzUKD3ON7I2JrQF2rRwjtNo&state=gtDWEWJy\r\n" +
		"\r\nPaste code here if prompted >\r\n"
	links := linksOf(p.Feed([]byte(raw)))
	want := "https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a-e61b-44d9" +
		"&response_type=code&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth" +
		"&code_challenge=EP9ZFyZt7vmXiM0yWH1VGzUKD3ON7I2JrQF2rRwjtNo&state=gtDWEWJy"
	if len(links) != 1 || links[0] != want {
		t.Fatalf("links = %v\nwant %q", links, want)
	}
}

func TestLinkParserCursorMovesKeepWordGaps(t *testing.T) {
	// Ink positions text with cursor moves instead of literal spaces:
	// ESC[nC (cursor forward) is a horizontal gap and ESC[B / ESC[H
	// (vertical/absolute moves) are line boundaries. Stripping them to
	// nothing glued "Paste code here if prompted" into one word, which
	// then looked like a URL wrap continuation and got absorbed into
	// the login link.
	p := &LinkParser{}
	raw := "https://claude.com/oauth/authorize?state=mdjxp31Xije\r\n" +
		"\x1b[1B\x1b[2CPaste\x1b[1Ccode\x1b[1Chere\x1b[1Cif\x1b[1Cprompted\x1b[1C>\r\n"
	links := linksOf(p.Feed([]byte(raw)))
	if len(links) != 1 || links[0] != "https://claude.com/oauth/authorize?state=mdjxp31Xije" {
		t.Fatalf("links = %v, prompt text must not be absorbed", links)
	}
}

func TestLinkParserBlankLineEndsWrappedURL(t *testing.T) {
	// The line after a URL block is blank in the CLI's output — text
	// after the blank line must never be absorbed into the URL.
	p := &LinkParser{}
	links := linksOf(p.Feed([]byte("https://claude.ai/oauth?s=1\r\n\r\nSuccess text\r\n")))
	if len(links) != 1 || links[0] != "https://claude.ai/oauth?s=1" {
		t.Fatalf("links = %v", links)
	}
}

func TestLinkParserDetectsLoginFailure(t *testing.T) {
	p := &LinkParser{}
	evs := p.Feed([]byte("\x1b[31mOAuth error: invalid authorization code\x1b[0m\r\n"))
	var got string
	for _, e := range evs {
		if e.Type == EventFailure {
			got = e.Value
		}
	}
	if got == "" {
		t.Fatalf("no failure event in %v", evs)
	}
}

func TestLinkParserFailureReportedOnce(t *testing.T) {
	p := &LinkParser{}
	line := []byte("Login failed. Try again.\r\n")
	first := p.Feed(line)
	second := p.Feed(line)
	count := 0
	for _, e := range append(first, second...) {
		if e.Type == EventFailure {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("failure events = %d, want 1", count)
	}
}

func TestLinkParserDetectsLoginSuccess(t *testing.T) {
	p := &LinkParser{}
	evs := p.Feed([]byte("\x1b[32mLogged in as dev@abc.com\x1b[0m\r\n"))
	var got string
	for _, e := range evs {
		if e.Type == EventSuccess {
			got = e.Value
		}
	}
	if got == "" {
		t.Fatalf("no success event in %v", evs)
	}
}
