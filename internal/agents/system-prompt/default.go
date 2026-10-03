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

// immutable_team.md is a fourth, narrower overlay: only the spawn of a
// Team agent's own session gets it, right after the main overlay and
// before the agent's persona, followed by a "Who you are" block that
// team.WhoYouAre generates at spawn from the agent's row. A sub-agent
// delegated from such a session gets neither — just one line saying
// whom it works for — and an ordinary session gets no Team text at all.
// The assembly lives in team.Service.PromptFor; pool/factory.go splices
// it in through Factory.TeamPromptLoader.
//
//go:embed immutable_team.md
var immutableTeamTemplate string

// ImmutableTeam is the static Team-agent overlay (immutable_team.md).
func ImmutableTeam() string { return resolve(strings.TrimSpace(immutableTeamTemplate)) }

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
	audience := mainImmutable(nil)
	if subAgent {
		audience = strings.TrimSpace(immutableSubagentTemplate)
	}
	return immutableWith(providerType, audience)
}

// TeamGates is what a Team agent's own access switches on in the main
// overlay. Each false drops the matching gated section of
// immutable_main.md: an agent that cannot delegate or schedule has no use
// for the rules on how to, and they cost every turn.
type TeamGates struct {
	// Subagents keeps "Delegating work" (the Sub-agents access is not Off).
	Subagents bool
	// Schedule keeps "Scheduling yourself" (the Schedule access is not Off).
	Schedule bool
}

// ImmutableForTeam is ImmutableFor for a Team agent's own session (never
// a sub-agent's: that one keeps the delegated-child overlay). The
// "Session title" section always goes — a Team agent's chat is titled by
// the agent's name — and the delegation and scheduling sections stay
// only when g says the agent has that access.
func ImmutableForTeam(providerType string, g TeamGates) string {
	skip := map[string]bool{
		gateSessionTitle: true,
		gateDelegating:   !g.Subagents,
		gateScheduling:   !g.Schedule,
	}
	return immutableWith(providerType, mainImmutable(skip))
}

// immutableWith joins the global core, one audience overlay and the
// provider overlay.
func immutableWith(providerType, audience string) string {
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

// Gated sections of immutable_main.md. Each is fenced by a pair of
// marker lines, "<!-- gate:NAME -->" before its heading and
// "<!-- /gate:NAME -->" after its last line.
const (
	gateSessionTitle = "session_title"
	gateDelegating   = "delegating"
	gateScheduling   = "scheduling"
)

// mainImmutable assembles the main-agent overlay by splicing each
// split-out section file into its placeholder in immutable_main.md. The
// overlay file owns ordering — move a {{TOKEN}} to move the section. A
// new main-only section = new .md under system-prompt/, embed it, add a
// {{TOKEN}} where it should land, and a line here.
//
// skip names the gated sections to drop; every other gate only loses its
// marker lines, so a nil skip yields the overlay as it read before gates
// existed.
func mainImmutable(skip map[string]bool) string {
	r := strings.NewReplacer(
		"{{ASKING_USER}}", strings.TrimSpace(immutableAskUserTemplate),
		"{{RENDER_FORMATS}}", strings.TrimSpace(immutableRenderFormatsTemplate),
	)
	return strings.TrimSpace(r.Replace(applyGates(immutableMainTemplate, skip)))
}

// applyGates drops each gated section named in skip, marker lines
// included, and strips the marker lines of the rest. An unclosed gate is
// left as is rather than eating the rest of the file.
func applyGates(s string, skip map[string]bool) string {
	for {
		i := strings.Index(s, "<!-- gate:")
		if i < 0 {
			return s
		}
		end := strings.Index(s[i:], " -->\n")
		if end < 0 {
			return s
		}
		name := s[i+len("<!-- gate:") : i+end]
		open := s[i : i+end+len(" -->\n")]
		closer := "<!-- /gate:" + name + " -->\n"
		j := strings.Index(s, closer)
		if j < i {
			return s
		}
		if skip[name] {
			s = s[:i] + s[j+len(closer):]
			continue
		}
		s = s[:i] + s[i+len(open):j] + s[j+len(closer):]
	}
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
