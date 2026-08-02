package delegation

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/rs/zerolog/log"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// applyRolePrompt writes a profile's system prompt onto the child
// session so the spawn factory appends it after the preset.
//
// A role IS its prompt; a profile whose prompt never reaches the spawn
// degrades to a provider plus a turn budget while still carrying the
// role's name. An empty prompt is a no-op rather than a blanking write,
// because the session may carry an addon set elsewhere.
func applyRolePrompt(layout agentconfig.Layout, sessionID string, profile *entity.AgentProfile) error {
	if profile == nil {
		return nil
	}
	prompt := strings.TrimSpace(profile.SystemPrompt)
	if prompt == "" {
		return nil
	}
	return session.SetSystemAddon(layout, sessionID, prompt)
}

// PoolRunner adapts the agent pool to the Runner interface. It is the
// only place in this package that knows how a sub-agent is actually
// spawned; everything above it works against the interface.
type PoolRunner struct {
	Pool   *pool.Pool
	Layout agentconfig.Layout
}

// NewPoolRunner builds the live runner.
func NewPoolRunner(p *pool.Pool, layout agentconfig.Layout) *PoolRunner {
	return &PoolRunner{Pool: p, Layout: layout}
}

// EnsureChildSession creates the sub-agent's isolated session.
//
// The child is a REAL session — own transcript, own store, own workspace
// resolution — deliberately, so the panel can render it with the same
// components as any conversation and nothing about it is a second-class
// object. What makes it a sub-agent is only ParentSessionID, which hides
// it from the conversation list and files it under its parent's rail.
func (r *PoolRunner) EnsureChildSession(ctx context.Context, childSessionID, parentSessionID, projectID, userID string) error {
	if _, err := session.Load(r.Layout, childSessionID); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := session.Create(ctx, r.Layout, session.CreateOptions{
		ID:     childSessionID,
		Origin: session.OriginUI,
		// Same project as the parent, so the sub-agent works in the same
		// checkout. Workspace isolation is a later phase.
		ProjectID: projectID,
		// Owned by the human who triggered the tree, not by the leader
		// agent — ownership checks and tag inheritance both key on a real
		// person.
		UserID:          userID,
		ParentSessionID: parentSessionID,
	})
	return err
}

// StartAgent spawns the profile's provider into the child session and
// delivers the task as its first user message.
func (r *PoolRunner) StartAgent(ctx context.Context, spec ChildSpec) error {
	if spec.Profile == nil {
		return errors.New("delegation: nil profile")
	}
	// Register the agent entry so the pool resolves the right provider
	// for this child rather than the session default.
	if err := session.AddAgent(r.Layout, spec.SessionID, spec.AgentName, spec.Profile.Provider); err != nil {
		// Already present is fine — a retried delegation reuses the entry.
		log.Debug().Err(err).Str("session", spec.SessionID).Msg("delegation: add agent entry")
	}
	if err := applyRolePrompt(r.Layout, spec.SessionID, spec.Profile); err != nil {
		// A sub-agent without its role prompt answers as a generic
		// assistant while still being labelled with the role. Loud,
		// because the output would look plausible and be wrong.
		log.Error().Err(err).Str("session", spec.SessionID).Str("profile", spec.Profile.Key).
			Msg("delegation: could not apply the role's system prompt; the sub-agent will run without its persona")
	}
	if spec.Profile.Model != "" {
		if err := session.SetModelID(r.Layout, spec.SessionID, spec.AgentName, spec.Profile.Model); err != nil {
			log.Warn().Err(err).Msg("delegation: pin model failed")
		}
	}
	// Set the native turn cap too where the provider supports it. This is
	// an OPTIMISATION, not the enforcement: it lets the CLI stop cleanly
	// on its own before wick's own Done-counter has to force a kill.
	// Providers without the flag simply ignore this and are stopped by
	// the counter, which is why the cap behaves the same everywhere.
	if spec.MaxTurns > 0 {
		if err := r.Pool.SetMaxTurns(spec.SessionID, spec.AgentName, spec.MaxTurns); err != nil {
			log.Debug().Err(err).Msg("delegation: native max-turns unavailable; wick-side counter still applies")
		}
	}
	if err := session.SetActiveAgent(r.Layout, spec.SessionID, spec.AgentName); err != nil {
		log.Debug().Err(err).Msg("delegation: set active agent")
	}
	// Title the child from its task, and mark it custom.
	//
	// Not cosmetic. The spawn factory shows every agent its session's
	// title state, and the standing rule is "if title_custom is false,
	// derive a title and call wick_set_title". A sub-agent therefore
	// spends a tool call titling a session that is HIDDEN from the
	// conversation list — nobody will ever read it. Pre-setting the title
	// satisfies the same rule instead of contradicting it, and the rail
	// gets a better label than the model would have invented anyway.
	if err := titleChildFromTask(r.Layout, spec.SessionID, spec.Task); err != nil {
		log.Debug().Err(err).Str("session", spec.SessionID).
			Msg("delegation: pre-titling the child failed; it may spend a turn titling itself")
	}

	return r.Pool.Send(ctx, spec.SessionID, spec.AgentName, string(session.OriginUI), "user", spec.Task)
}

// KillAgent stops one sub-agent, leaving siblings alone.
func (r *PoolRunner) KillAgent(sessionID, agentName string) error {
	return r.Pool.KillAgent(sessionID, agentName)
}

// PartialText returns the child's in-flight, unflushed output.
func (r *PoolRunner) PartialText(sessionID, agentName string) string {
	return r.Pool.PartialText(sessionID, agentName)
}

// childTitleRunes caps a generated child title. Long enough to identify
// the task in the rail, short enough not to wrap.
const childTitleRunes = 60

// titleChildFromTask names a sub-agent's session after its task and marks
// the title as explicitly chosen, so the agent leaves it alone.
//
// Idempotent by check: a title already set by a human (or by an earlier
// delegation reusing the session) is never overwritten.
func titleChildFromTask(layout agentconfig.Layout, sessionID, task string) error {
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		return err
	}
	if sess.Meta.TitleCustom || sess.Meta.Label != "" {
		return nil
	}
	title := strings.TrimSpace(strings.SplitN(strings.TrimSpace(task), "\n", 2)[0])
	if title == "" {
		title = "Sub-agent task"
	}
	if r := []rune(title); len(r) > childTitleRunes {
		title = strings.TrimSpace(string(r[:childTitleRunes])) + "…"
	}
	sess.Meta.Label = title
	sess.Meta.TitleCustom = true
	return session.SaveMeta(layout, sessionID, sess.Meta)
}
