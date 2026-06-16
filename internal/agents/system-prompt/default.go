package systemprompt

import (
	_ "embed"
	"strings"

	"github.com/yogasw/wick/internal/appname"
)

//go:embed default.md
var defaultSystemPromptTemplate string

//go:embed immutable.md
var immutableSystemPromptTemplate string

// Split-out sections of the shared immutable prompt. Each lives as its
// own .md file in this package so a topic is easy to find and extend in
// isolation; the base immutable file controls WHERE each lands via a
// {{PLACEHOLDER}} token (order matters), and baseImmutable() splices
// them in. render_formats.md is the place to add a newly supported chat
// render type (and how to use it); asking_user.md holds the interactive
// -prompt contract.
//
//go:embed asking_user.md
var immutableAskUserTemplate string

//go:embed render_formats.md
var immutableRenderFormatsTemplate string

// Per-provider immutable overrides. Currently empty — the shared rules
// (base + ask-user + render-formats) cover both providers. Kept as dedicated
// files + append points so a future provider-specific rule has an
// obvious home without re-plumbing the loader.
//
//go:embed immutable_claude.md
var immutableSystemPromptClaudeTemplate string

//go:embed immutable_codex.md
var immutableSystemPromptCodexTemplate string

// DefaultSystemPrompt is the baseline interaction policy embedded at
// build time. Seeded into the `system_prompt` config row on fresh
// installs and surfaced as the target of the Reset button on the
// Agents settings page so operators can restore it after edits.
//
// The embedded markdown uses `{{app}}` wherever the resolved binary
// name should appear (paths like `~/.<app>/sessions/**` change per
// install — `wick init <name>` produces a custom-branded binary, and
// every reference to `~/.wick/` would otherwise be wrong). Resolved
// once at call time via appname.Resolve.
func DefaultSystemPrompt() string {
	return resolve(defaultSystemPromptTemplate)
}

// ImmutableSystemPrompt returns the global rules combined with
// claude-specific rules. Passed via --append-system-prompt on every
// claude spawn; operator-uneditable, always wins on conflict.
//
// Connector catalog is NOT appended here — the catalog needs the live
// connectors service to filter for ready instances, which only the
// factory can wire. See ClaudeFactory.ConnectorCatalogLoader.
func ImmutableSystemPrompt() string {
	return resolve(joinImmutable(baseImmutable(), immutableSystemPromptClaudeTemplate))
}

// baseImmutable assembles the shared (provider-agnostic) immutable
// prompt by splicing each split-out section file into its placeholder
// in the base file. The base file owns ordering — move a {{TOKEN}} to
// move the section. A new shared section = new .md under system-prompt/,
// embed it, add a {{TOKEN}} where it should land, and a line here.
//
// Replacing rather than appending keeps each section in its intended
// neighbourhood (render formats next to "Sending links", asking-user
// before the connector rules) instead of all piled at the end.
func baseImmutable() string {
	r := strings.NewReplacer(
		"{{ASKING_USER}}", strings.TrimSpace(immutableAskUserTemplate),
		"{{RENDER_FORMATS}}", strings.TrimSpace(immutableRenderFormatsTemplate),
	)
	return r.Replace(immutableSystemPromptTemplate)
}

// ImmutableSystemPromptCodex returns the global rules combined with
// codex-specific rules. Written as AGENTS.md into the workspace so
// codex picks it up automatically on every spawn.
func ImmutableSystemPromptCodex() string {
	return resolve(joinImmutable(baseImmutable(), immutableSystemPromptCodexTemplate))
}

// joinImmutable appends the per-provider override only when it has
// content, so an empty override file adds no trailing whitespace to
// the prompt the agent actually receives.
func joinImmutable(base, extra string) string {
	if strings.TrimSpace(extra) == "" {
		return base
	}
	return base + "\n\n" + strings.TrimSpace(extra)
}

func resolve(s string) string {
	return strings.ReplaceAll(s, "{{app}}", appname.Resolve())
}
