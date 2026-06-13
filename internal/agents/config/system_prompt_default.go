package config

import (
	_ "embed"
	"strings"

	"github.com/yogasw/wick/internal/appname"
)

//go:embed system_prompt_default.md
var defaultSystemPromptTemplate string

//go:embed system_prompt_immutable.md
var immutableSystemPromptTemplate string

// Per-provider immutable overrides. Currently empty — the shared rules
// (incl. "Asking the user") cover both providers. Kept as dedicated
// files + append points so a future provider-specific rule has an
// obvious home without re-plumbing the loader.
//
//go:embed system_prompt_immutable_claude.md
var immutableSystemPromptClaudeTemplate string

//go:embed system_prompt_immutable_codex.md
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
	return resolve(joinImmutable(immutableSystemPromptTemplate, immutableSystemPromptClaudeTemplate))
}

// ImmutableSystemPromptCodex returns the global rules combined with
// codex-specific rules. Written as AGENTS.md into the workspace so
// codex picks it up automatically on every spawn.
func ImmutableSystemPromptCodex() string {
	return resolve(joinImmutable(immutableSystemPromptTemplate, immutableSystemPromptCodexTemplate))
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
