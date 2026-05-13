package config

import _ "embed"

//go:embed system_prompt_default.md
var defaultSystemPrompt string

// DefaultSystemPrompt is the baseline interaction policy embedded at
// build time. Seeded into the `system_prompt` config row on fresh
// installs and surfaced as the target of the Reset button on the
// Agents settings page so operators can restore it after edits.
func DefaultSystemPrompt() string { return defaultSystemPrompt }
