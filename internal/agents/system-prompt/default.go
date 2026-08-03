package systemprompt

import (
	_ "embed"
	"strings"

	"github.com/yogasw/wick/internal/appname"
)

//go:embed default.md
var defaultSystemPromptTemplate string

// The immutable prompt is split by AUDIENCE, because a sub-agent's spawn
// used to swallow the whole thing — 260 lines of chat render formats, the
// session-title ritual, self-scheduling, ask_user — none of which a child
// whose only reader is its caller can use. Three files:
//
//   - immutable.md          GLOBAL: every agent. Links, connector routing,
//     session_id discipline, and the agent-to-agent rules (mentions,
//     message/reply, never fabricating another agent's output) — those are
//     global because a sub-agent can mention and message peers too.
//   - immutable_main.md     MAIN agent only: the one a human talks to.
//     Render formats, session title, scheduling, [silent], ask_user, and
//     the delegation ops (delegate/collect/create_agent).
//   - immutable_subagent.md SUB-agent only: the delegated-child contract.
//     No human in the loop (so no ask_user), message the caller instead,
//     report_result, lean output.
//
// Which one a spawn gets is decided by pool/factory.go from the session's
// ParentSessionID — not by role, provider, or anything a prompt could get
// wrong.
//
//go:embed immutable.md
var immutableSystemPromptTemplate string

//go:embed immutable_main.md
var immutableMainTemplate string

//go:embed immutable_subagent.md
var immutableSubagentTemplate string

// Split-out sections spliced into the MAIN overlay. Each lives as its
// own .md file in this package so a topic is easy to find and extend in
// isolation; immutable_main.md controls WHERE each lands via a
// {{PLACEHOLDER}} token (order matters), and mainImmutable() splices
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

//go:embed immutable_wick.md
var immutableSystemPromptWickTemplate string

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

// ImmutableFor assembles the immutable prompt for one spawn:
// global core, then the audience overlay (main or sub-agent), then the
// provider overlay. providerType is the raw provider.Type string
// ("claude" / "codex" / "wick"); anything unrecognised gets the claude
// overlay, matching the factory's historical default branch.
//
// Connector catalog is NOT appended here — the catalog needs the live
// connectors service to filter for ready instances, which only the
// factory can wire. See ClaudeFactory.ConnectorCatalogLoader.
func ImmutableFor(providerType string, subAgent bool) string {
	audience := mainImmutable()
	if subAgent {
		audience = strings.TrimSpace(immutableSubagentTemplate)
	}
	base := strings.TrimSpace(immutableSystemPromptTemplate) + "\n\n" + audience

	overlay := immutableSystemPromptClaudeTemplate
	switch providerType {
	case "codex":
		overlay = immutableSystemPromptCodexTemplate
	case "wick":
		overlay = immutableSystemPromptWickTemplate
	}
	return resolve(joinImmutable(base, overlay))
}

// mainImmutable assembles the main-agent overlay by splicing each
// split-out section file into its placeholder in immutable_main.md. The
// overlay file owns ordering — move a {{TOKEN}} to move the section. A
// new main-only section = new .md under system-prompt/, embed it, add a
// {{TOKEN}} where it should land, and a line here.
func mainImmutable() string {
	r := strings.NewReplacer(
		"{{ASKING_USER}}", strings.TrimSpace(immutableAskUserTemplate),
		"{{RENDER_FORMATS}}", strings.TrimSpace(immutableRenderFormatsTemplate),
	)
	return strings.TrimSpace(r.Replace(immutableMainTemplate))
}

// ImmutableSystemPrompt returns the main-agent rules with the
// claude-specific overlay. Passed via --append-system-prompt on every
// claude spawn; operator-uneditable, always wins on conflict.
func ImmutableSystemPrompt() string {
	return ImmutableFor("claude", false)
}

// ImmutableSystemPromptCodex returns the main-agent rules with the
// codex-specific overlay. Reaches codex via model_instructions_file on
// every spawn.
func ImmutableSystemPromptCodex() string {
	return ImmutableFor("codex", false)
}

// ImmutableSystemPromptWick returns the main-agent rules with the
// wick-specific overlay. Passed as the engine's sysPrompt on every wick
// spawn (see internal/agents/provider/wick/spawn.go).
func ImmutableSystemPromptWick() string {
	return ImmutableFor("wick", false)
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
