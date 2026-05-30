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
	return resolve(immutableSystemPromptTemplate + "\n\n" + immutableSystemPromptClaudeTemplate)
}

// ImmutableSystemPromptCodex returns the global rules combined with
// codex-specific rules. Written as AGENTS.md into the workspace so
// codex picks it up automatically on every spawn.
func ImmutableSystemPromptCodex() string {
	return resolve(immutableSystemPromptTemplate + "\n\n" + immutableSystemPromptCodexTemplate)
}

func resolve(s string) string {
	return strings.ReplaceAll(s, "{{app}}", appname.Resolve())
}
