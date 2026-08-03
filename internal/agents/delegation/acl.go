package delegation

import (
	"context"

	"github.com/yogasw/wick/internal/entity"
)

// TagResolver reports which filter tags a user holds. login.Service
// satisfies it; kept as an interface so this package does not depend on
// the login package and tests can stub freely.
type TagResolver interface {
	GetUserFilterTagIDs(ctx context.Context, userID string) []string
}

// EffectiveTags computes the tag set a sub-agent runs with.
//
// The rule, stated once so every call site can rely on it:
//
//	effective = user_tags ∩ (profile.allowed_tag_ids ?: user_tags)
//
// Two consequences that are the entire security model of this feature:
//
//  1. A profile can only ever NARROW access. Listing a tag the
//     triggering user does not hold grants nothing — the intersection
//     drops it. So an admin cannot accidentally build a profile that
//     escalates whoever calls it.
//  2. An empty allowed list means "inherit the user's tags in full",
//     not "no tags" and not "all tags". Empty is the common case for a
//     general-purpose role.
func EffectiveTags(userTags []string, profile *entity.AgentProfile) []string {
	if len(userTags) == 0 {
		return nil
	}
	if profile == nil {
		return dedupe(userTags)
	}
	allowed := decodeStringSlice(profile.AllowedTagIDs)
	if len(allowed) == 0 {
		return dedupe(userTags)
	}
	have := make(map[string]bool, len(userTags))
	for _, t := range userTags {
		have[t] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, t := range allowed {
		if have[t] && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Ancestors decodes the root→parent profile-key chain recorded on a
// delegation. A child extends this list with its own profile key, and
// the cycle guard rejects any key already present.
func Ancestors(d *entity.AgentDelegation) []string {
	if d == nil {
		return nil
	}
	return decodeStringSlice(d.AncestorKeys)
}

// CanInterrupt reports whether actor may stop this delegation.
//
// Only the human who triggered the tree, or an administrator. A leader
// stopping its OWN child goes through a separate path (it knows the
// delegation it created); it must not be able to reach a sibling, its
// parent, or another user's tree.
func CanInterrupt(d *entity.AgentDelegation, actorID string, isAdmin bool) bool {
	if d == nil {
		return false
	}
	if isAdmin {
		return true
	}
	return actorID != "" && d.TriggeredBy == actorID
}

// VisibleProfiles filters a roster down to what this user may delegate
// to, using the same "profile narrows, never widens" rule as
// EffectiveTags: a profile whose narrowing list excludes every tag the
// user holds would run with no access at all, so it is not offered.
//
// Profiles with no narrowing list are visible to everyone — they simply
// inherit the caller's own permissions.
func VisibleProfiles(profiles []entity.AgentProfile, userTags []string, isAdmin bool) []entity.AgentProfile {
	out := make([]entity.AgentProfile, 0, len(profiles))
	for _, p := range profiles {
		if p.Disabled {
			continue
		}
		if isAdmin {
			out = append(out, p)
			continue
		}
		allowed := decodeStringSlice(p.AllowedTagIDs)
		if len(allowed) == 0 {
			out = append(out, p)
			continue
		}
		if len(EffectiveTags(userTags, &p)) > 0 {
			out = append(out, p)
		}
	}
	return out
}

// CanAgentStop reports whether an agent may stop another agent.
//
// Same tree only. This is the one authority an agent gains over a peer,
// and it is deliberately narrow: takeover.go draws the line at STEERING
// ("letting one agent inject turns into another would blur the delegation
// boundary"), while stopping stays allowed. A leader that can spawn work
// it cannot stop is the worse failure — it has to wait out a runaway it
// can see going wrong.
//
// The human's own authorization still applies on top: the caller passes
// its user id to Interrupt, so an agent can never stop something its
// operator could not.
func CanAgentStop(d *entity.AgentDelegation, callerRootID string) bool {
	if d == nil || callerRootID == "" {
		return false
	}
	return d.RootID == callerRootID
}

// TagNamer resolves tag ids to human names.
//
// Separate from TagResolver because it is only needed for DISCOVERY: an
// agent choosing which tools a role should carry has to see what the ids
// mean. Enforcement never needs a name, so the security path does not
// depend on this being wired.
type TagNamer interface {
	TagsByIDs(ctx context.Context, ids []string) ([]*entity.Tag, error)
}

// NarrowTags intersects a requested tag set with what the caller holds.
//
// The rule that makes it safe to let an AGENT choose a role's tools: a
// request for a tag the caller does not have is dropped, not honoured, so
// a role can only ever be narrower than the human who triggered it.
// Requesting nothing means "inherit everything the caller has".
func NarrowTags(requested, callerTags []string) []string {
	if len(requested) == 0 {
		return nil
	}
	have := make(map[string]bool, len(callerTags))
	for _, t := range callerTags {
		have[t] = true
	}
	out := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, t := range requested {
		if have[t] && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}
