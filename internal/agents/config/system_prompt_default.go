package config

import (
	_ "embed"
	"strings"

	"github.com/yogasw/wick/internal/appname"
)

//go:embed system_prompt_default.md
var defaultSystemPromptTemplate string

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
	return strings.ReplaceAll(defaultSystemPromptTemplate, "{{app}}", appname.Resolve())
}
