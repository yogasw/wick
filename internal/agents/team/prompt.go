package team

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	systemprompt "github.com/yogasw/wick/internal/agents/system-prompt"
	"github.com/yogasw/wick/internal/entity"
)

// PromptFor is the Team part of a spawn's system prompt. A Team agent's
// own session gets the Team overlay plus its "Who you are" block, built
// from the agent's row every spawn whatever the persona says; a
// sub-agent delegated under one gets a single line naming whom it works
// for; any other session gets "".
func (s *Service) PromptFor(ctx context.Context, sessionID string, subAgent bool) string {
	p := s.AgentFor(ctx, sessionID)
	if p == nil {
		return ""
	}
	if subAgent {
		return SubAgentOfTeam(p.Handle)
	}
	return s.teamPrompt(ctx, *p)
}

// SpawnPrompt is what a Team agent's own session needs at spawn beyond
// its persona: the Team overlay and "Who you are" block, the "Your
// access" block, and which gated immutable sections its access keeps.
type SpawnPrompt struct {
	Prompt string
	Access string
	// Subagents and Schedule are the agent's effective access (see
	// systemprompt.TeamGates): false when the feature is switched off or
	// every entry backing it resolves to off.
	Subagents bool
	Schedule  bool
}

// SpawnPromptFor returns the spawn prompt parts of the Team agent
// sessionID belongs to; false for any other session. A sub-agent
// delegated under a Team session is "any other" here too: it keeps the
// ordinary assembly (see PromptFor).
func (s *Service) SpawnPromptFor(ctx context.Context, sessionID string) (SpawnPrompt, bool) {
	// AgentFor follows the parent chain, so it also names the agent a
	// sub-agent works for; only the agent's own session qualifies.
	if sess, err := session.Load(s.layout, sessionID); err != nil || sess.Meta.ParentSessionID != "" {
		return SpawnPrompt{}, false
	}
	p := s.AgentFor(ctx, sessionID)
	if p == nil {
		return SpawnPrompt{}, false
	}
	reach := s.reachOf(ctx, *p)
	scope := ScopeOf(*p, reach)
	f := EffectiveFeatures(*p, reach)
	return SpawnPrompt{
		Prompt:    s.teamPrompt(ctx, *p),
		Access:    YourAccess(scope, reach),
		Subagents: f.Subagents && scope.AllowKey("sub-agents") && scope.AllowTool("wick_agent_delegate"),
		Schedule:  f.Schedule && scope.AllowTool("wick_schedule_message"),
	}, true
}

// teamPrompt is the Team overlay plus p's "Who you are" block.
func (s *Service) teamPrompt(ctx context.Context, p entity.AgentPersona) string {
	var others []Member
	if all, err := s.List(ctx, p.OwnerUserID); err == nil {
		for _, o := range all {
			// A disabled agent cannot be reached, so it is not offered.
			if o.ID != p.ID && !o.Disabled {
				others = append(others, s.memberOf(o))
			}
		}
	}
	return systemprompt.ImmutableTeam() + "\n\n" + WhoYouAre(s.memberOf(p), others)
}

// memberOf reads an agent's name and description off its project; a
// missing project leaves the handle as the name.
func (s *Service) memberOf(p entity.AgentPersona) Member {
	m := Member{Name: p.Handle, Handle: p.Handle, IsCaptain: p.IsCaptain}
	if p.ProjectID != "" {
		if proj, err := project.Load(s.layout, p.ProjectID); err == nil {
			if proj.Meta.Name != "" {
				m.Name = proj.Meta.Name
			}
			m.Description = proj.Meta.Description
		}
	}
	return m
}

// maxAccessListed caps the connector lines of "Your access" so an owner
// with a large catalog does not grow every turn of every agent.
const maxAccessListed = 30

// YourAccess is the "Your access" block: each connector the agent may
// use, with its level and the accounts it may run as. It only informs —
// the server enforces the same scope whatever the block says. A nil reach
// (the owner's catalog could not be read) lists nothing and says so.
func YourAccess(sc *Scope, reach Reach) string {
	var b strings.Builder
	b.WriteString("## Your access\n")
	type line struct{ label, text string }
	var lines []line
	if sc != nil && reach != nil {
		for id, it := range reach {
			if isToolGrant(id) || !sc.AllowKey(it.Key) {
				continue
			}
			lv, g := sc.resolve(id)
			level := ""
			switch lv {
			case LevelAll:
				level = "Write"
			case LevelRead:
				level = "Read"
			case LevelPick:
				level = fmt.Sprintf("Selected ops (%d)", len(g.Ops))
			default:
				continue
			}
			label := it.Label
			if label == "" {
				label = it.Key
			}
			text := fmt.Sprintf("- %s (%s): %s", label, it.Key, level)
			if g != nil && len(g.Accounts) > 0 {
				names := make([]string, 0, len(g.Accounts))
				for _, a := range g.Accounts {
					switch n := it.Accounts[a]; {
					case a == "":
						names = append(names, "the bot")
					case n != "":
						names = append(names, n)
					default:
						names = append(names, a)
					}
				}
				text += ", as " + strings.Join(names, ", ")
			}
			lines = append(lines, line{label: strings.ToLower(label), text: text})
		}
	}
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].label != lines[j].label {
			return lines[i].label < lines[j].label
		}
		return lines[i].text < lines[j].text
	})
	switch {
	case reach == nil:
		b.WriteString("Your connector list could not be read for this spawn; wick_list shows what you can use.\n")
	case len(lines) == 0:
		b.WriteString("You have no connectors.\n")
	default:
		b.WriteString("Connectors you may use (Write = every op, Read = read-only ops):\n")
		for i, l := range lines {
			if i == maxAccessListed {
				fmt.Fprintf(&b, "- +%d more; wick_list shows them all.\n", len(lines)-maxAccessListed)
				break
			}
			b.WriteString(l.text + "\n")
		}
	}
	b.WriteString("Anything not listed is not available; the owner can change it in Settings.")
	return b.String()
}
