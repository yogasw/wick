package systemprompt

import (
	"regexp"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/appname"
)

// tokenRe matches any leftover {{TEMPLATE}} placeholder. The assembler
// must resolve every token ({{app}} + each {{SECTION}} splice point);
// a survivor means a placeholder was added to a .md file without a
// matching replacement in the loader.
var tokenRe = regexp.MustCompile(`\{\{[A-Za-z0-9_]+\}\}`)

// splices is the set of split-out section files spliced into the base
// immutable prompt. Auto-derived assertions iterate this so adding a
// new section = embed it + add one line here (or nothing, if a future
// loader walks the splice table itself). Keep in sync with the
// strings.NewReplacer call in baseImmutable.
var splices = []struct {
	name    string
	content string
}{
	{"asking_user", immutableAskUserTemplate},
	{"render_formats", immutableRenderFormatsTemplate},
}

// TestDefaultSystemPromptResolvesApp checks the default prompt is
// non-empty and that the {{app}} template token is resolved to the
// running binary name (never leaked raw into the spawned prompt).
func TestDefaultSystemPromptResolvesApp(t *testing.T) {
	p := DefaultSystemPrompt()
	if strings.TrimSpace(p) == "" {
		t.Fatal("DefaultSystemPrompt is empty")
	}
	if tok := tokenRe.FindString(p); tok != "" {
		t.Errorf("DefaultSystemPrompt leaked unresolved token %q", tok)
	}
	if app := appname.Resolve(); !strings.Contains(p, app) {
		t.Errorf("DefaultSystemPrompt missing resolved app name %q", app)
	}
}

// TestImmutableHasNoLeftoverTokens is the core regression guard: after
// assembly, no {{...}} placeholder may survive in either provider
// variant. This catches a new {{SECTION}} added to a .md file without a
// replacement wired into baseImmutable — without needing a hand-kept
// list of which tokens exist.
func TestImmutableHasNoLeftoverTokens(t *testing.T) {
	for _, c := range providerVariants() {
		t.Run(c.name, func(t *testing.T) {
			if tok := tokenRe.FindString(c.fn()); tok != "" {
				t.Errorf("leftover unresolved token %q", tok)
			}
		})
	}
}

// TestImmutableSplicesEverySection auto-checks that every spliced
// section file's body actually lands in the assembled prompt — derived
// from the `splices` table, so a newly added section is covered the
// moment it's listed there (one line) rather than via scattered
// hardcoded markers. Asserts on a distinctive interior line of each
// file so a stray empty embed or a missing replacement is caught.
func TestImmutableSplicesEverySection(t *testing.T) {
	for _, c := range providerVariants() {
		p := c.fn()
		for _, s := range splices {
			body := strings.TrimSpace(s.content)
			if body == "" {
				t.Errorf("%s: split section %q embedded empty", c.name, s.name)
				continue
			}
			// First non-blank line is the section heading; assert it
			// plus the last non-blank line so both ends made it in.
			lines := nonBlankLines(body)
			for _, anchor := range []string{lines[0], lines[len(lines)-1]} {
				if !strings.Contains(p, anchor) {
					t.Errorf("%s: section %q missing line %q", c.name, s.name, anchor)
				}
			}
		}
	}
}

// TestImmutableSectionOrder pins the placeholder splice order relative
// to the surrounding base headings — the reason placeholders replaced a
// plain append: render formats sits with the link guidance up top,
// asking-user lands just before the connector rules. Anchors are
// derived from the splice files themselves (their heading line), not
// re-typed, so a renamed heading can't silently drift out of the check.
func TestImmutableSectionOrder(t *testing.T) {
	askUserHead := nonBlankLines(strings.TrimSpace(immutableAskUserTemplate))[0]
	renderHead := nonBlankLines(strings.TrimSpace(immutableRenderFormatsTemplate))[0]
	order := []string{
		"## Sending links",
		renderHead,
		"## Session title",
		askUserHead,
		"## Wick connectors",
	}
	p := ImmutableSystemPrompt()
	prev := -1
	for _, s := range order {
		idx := strings.Index(p, s)
		if idx < 0 {
			t.Fatalf("section %q not found", s)
		}
		if idx <= prev {
			t.Errorf("section %q out of order (idx %d <= prev %d)", s, idx, prev)
		}
		prev = idx
	}
}

func providerVariants() []struct {
	name string
	fn   func() string
} {
	return []struct {
		name string
		fn   func() string
	}{
		{"claude", ImmutableSystemPrompt},
		{"codex", ImmutableSystemPromptCodex},
	}
}

func nonBlankLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}
