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

// TestImmutableSectionOrder pins the assembly order: global core first
// (links, connectors, agent comms), then the main overlay (render
// formats, title, asking-user, delegation). Anchors for spliced sections
// are derived from the splice files themselves (their heading line), not
// re-typed, so a renamed heading can't silently drift out of the check.
func TestImmutableSectionOrder(t *testing.T) {
	askUserHead := nonBlankLines(strings.TrimSpace(immutableAskUserTemplate))[0]
	renderHead := nonBlankLines(strings.TrimSpace(immutableRenderFormatsTemplate))[0]
	order := []string{
		"## Sending links",
		"## Wick connectors",
		"## Working with other agents",
		renderHead,
		"## Session title",
		askUserHead,
		"## Delegating work",
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

// The audience split is the point: a sub-agent spawn must not carry the
// human-facing machinery, and a main spawn must not carry the
// delegated-child contract. Anchored on one distinctive line per side.
func TestSubAgentPromptCarriesOnlyItsAudience(t *testing.T) {
	for _, pt := range []string{"claude", "codex", "wick"} {
		main := ImmutableFor(pt, false)
		sub := ImmutableFor(pt, true)

		// Both audiences share the global core.
		for _, shared := range []string{
			"## Sending links",
			"## Wick connectors",
			"## Working with other agents",
		} {
			if !strings.Contains(main, shared) {
				t.Errorf("%s main: missing shared section %q", pt, shared)
			}
			if !strings.Contains(sub, shared) {
				t.Errorf("%s sub: missing shared section %q", pt, shared)
			}
		}

		// Main-only machinery must not reach a sub-agent.
		for _, mainOnly := range []string{
			"## Renderable formats in chat",
			"## Session title",
			"## Scheduling yourself",
			"## Asking the user",
			"## Delegating work",
		} {
			if !strings.Contains(main, mainOnly) {
				t.Errorf("%s main: missing section %q", pt, mainOnly)
			}
			if strings.Contains(sub, mainOnly) {
				t.Errorf("%s sub: leaked main-only section %q", pt, mainOnly)
			}
		}

		// The child contract must not reach a main agent.
		for _, subOnly := range []string{
			"## You are a sub-agent",
			"report_result",
		} {
			if !strings.Contains(sub, subOnly) {
				t.Errorf("%s sub: missing section %q", pt, subOnly)
			}
		}
		if strings.Contains(main, "## You are a sub-agent") {
			t.Errorf("%s main: leaked the sub-agent contract", pt)
		}

		// The split exists to shrink the child's prompt; if the sub
		// variant is not substantially smaller the wiring regressed.
		if len(sub) >= len(main) {
			t.Errorf("%s: sub prompt (%d bytes) not smaller than main (%d bytes)",
				pt, len(sub), len(main))
		}
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
		{"wick", ImmutableSystemPromptWick},
	}
}

// TestImmutableWickHasOwnMemorySection is a regression guard: wick used
// to have no dedicated immutable_wick.md at all — pool/factory.go's
// provider-type switch had only a codex branch, so every non-codex
// provider (including wick) fell into the claude branch and got
// ImmutableSystemPrompt() (claude's overlay). The "Persistent memory"
// section names wick's own write_file/edit_file tools specifically, so
// it must land only in wick's prompt, not claude's or codex's (they use
// their own harness file tools, not these).
func TestImmutableWickHasOwnMemorySection(t *testing.T) {
	wick := ImmutableSystemPromptWick()
	if !strings.Contains(wick, "## Persistent memory") {
		t.Error("wick's immutable prompt is missing the Persistent memory section")
	}
	if !strings.Contains(wick, "write_file") {
		t.Error("wick's memory section should reference its own write_file tool")
	}

	claude := ImmutableSystemPrompt()
	if strings.Contains(claude, "## Persistent memory") {
		t.Error("claude's immutable prompt should NOT carry wick's memory section")
	}
	codex := ImmutableSystemPromptCodex()
	if strings.Contains(codex, "## Persistent memory") {
		t.Error("codex's immutable prompt should NOT carry wick's memory section")
	}

	// The context-window / auto-compaction guidance is likewise wick-only:
	// it describes wick's in-process compaction + its shell/job tools, which
	// the CLI providers don't have.
	if !strings.Contains(wick, "## Context window and long tool output") {
		t.Error("wick's immutable prompt is missing the context-window section")
	}
	if strings.Contains(claude, "## Context window and long tool output") {
		t.Error("claude's immutable prompt should NOT carry wick's context-window section")
	}
	if strings.Contains(codex, "## Context window and long tool output") {
		t.Error("codex's immutable prompt should NOT carry wick's context-window section")
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
