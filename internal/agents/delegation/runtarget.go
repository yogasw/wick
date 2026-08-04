package delegation

import (
	"strings"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
)

// SessionRunTarget reports which provider instance and model a session is
// actually running on, read from its active agent entry. ok=false when the
// session or its agent entry cannot be read — distinct from "runs on
// nothing", so a lookup failure never silently reads as "inherit nothing".
type SessionRunTarget func(sessionID string) (provider.RunTarget, bool)

// ProjectRunTarget reports a project's default provider+model. ok=false
// when the project cannot be read.
type ProjectRunTarget func(projectID string) (provider.RunTarget, bool)

// resolveChildTarget decides which provider instance and model a
// sub-agent spawns on.
//
// Precedence, most specific first:
//
//  1. the role's own provider+model — a role may pin its own runtime all
//     the way down to a live-set leaf, and when it does, that is the
//     whole answer
//  2. the parent conversation's active provider+model — the default
//     expectation: work delegated from a conversation runs where that
//     conversation runs
//  3. the project's defaults
//
// The chain deliberately STOPS at the project. The global DefaultProvider
// is a new-session default (channels, API, quick-create); letting a
// sub-agent fall through to it is how delegated work ends up on whichever
// instance an operator happened to set globally, rather than on the
// instance its own conversation is using. When nothing here answers, the
// caller leaves the target empty and the spawn path applies its own
// per-type default — a visible, documented fallback rather than a
// surprising jump.
//
// A model never crosses an instance boundary: a pin belongs to the model
// registry of the instance it was chosen on, so a role pointing at a
// DIFFERENT instance than its parent gets no model at all and lets that
// provider resolve its own default.
//
// It does, however, cross when the instance is the SAME. A role that names
// only a provider ("codex") has expressed an opinion about the runtime and
// none about the model, so when the parent conversation runs on that very
// instance its model is the best available answer — and it is what the user
// picked in the composer. Withholding it made a role with a bare provider
// silently override the session's model, which reads as the switch being
// ignored.
func resolveChildTarget(
	profile *entity.AgentProfile,
	parentSessionID, projectID string,
	sessionTarget SessionRunTarget,
	projectTarget ProjectRunTarget,
) provider.RunTarget {
	candidates := make([]provider.RunTarget, 0, 3)

	if profile != nil {
		candidates = append(candidates, provider.RunTarget{
			Provider: profile.Provider,
			ModelID:  profile.Model,
		})
	}
	var parent provider.RunTarget
	haveParent := false
	if sessionTarget != nil && strings.TrimSpace(parentSessionID) != "" {
		if t, ok := sessionTarget(parentSessionID); ok {
			candidates = append(candidates, t)
			parent, haveParent = t, true
		}
	}
	if projectTarget != nil && strings.TrimSpace(projectID) != "" {
		if t, ok := projectTarget(projectID); ok {
			candidates = append(candidates, t)
		}
	}
	got := provider.ResolveRunTarget(candidates...)

	// Fill a missing model from the parent when both run on the same
	// instance. Guarded by SameInstance so this can never move a pin across
	// registries — the property the whole type exists to protect.
	if got.ModelID == "" && haveParent && parent.ModelID != "" &&
		provider.SameInstance(got.Provider, parent.Provider) {
		got.ModelID = parent.ModelID
	}
	return got
}
