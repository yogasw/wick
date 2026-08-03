package delegation

import (
	"context"

	"github.com/yogasw/wick/internal/entity"
)

// Resolving an @token.
//
// A mention can mean two different things and the difference matters:
// "@reviewer, what did you find?" addressed to an agent already working
// is a message, while the same token addressed to a role nobody has
// spawned is a request to start one.
//
// Precedence is not negotiable: a LIVE agent always wins. The opposite
// order would answer "@code-investigator follow up on that" by starting a
// fresh agent with no memory of the thing it is meant to follow up on,
// and the leader would never know the difference.

// TargetKind is what an @token resolved to.
type TargetKind int

const (
	// TargetUnknown: not a handle, not a role. The token stays plain text.
	TargetUnknown TargetKind = iota
	// TargetAgent: a live instance in this tree. Message it.
	TargetAgent
	// TargetRole: a spawnable profile key. Delegate to it.
	TargetRole
)

// Target is one resolved mention.
type Target struct {
	Kind   TargetKind
	Handle string // set when Kind == TargetAgent
	Role   string // set when Kind == TargetRole
}

// Resolver is a snapshot of what @tokens can mean for one tree in one
// project. Built per routed turn: the roster changes as instances appear
// and finish, so a resolver held across turns would name agents that no
// longer exist.
type Resolver struct {
	handles map[string]bool
	roles   map[string]bool
	// order preserves a stable roster listing for ParseMentions and for
	// the spawn-time roster block.
	handleOrder []string
	roleOrder   []string
}

// NewResolver builds the snapshot for a tree.
//
// rootID may be empty — a conversation that has never delegated has no
// tree yet, and every mention in it can only be a role.
func (s *Service) NewResolver(ctx context.Context, rootID, projectID string) (*Resolver, error) {
	r := &Resolver{handles: map[string]bool{}, roles: map[string]bool{}}

	if rootID != "" {
		rows, err := s.Repo.ListByRoot(ctx, rootID)
		if err != nil {
			return nil, err
		}
		for _, d := range rows {
			// Terminal instances are deliberately excluded. Their process
			// is gone, so a message to one queues for a reader that will
			// never come — indistinguishable, from the sender's side, from
			// a question being considered.
			if d.Handle == "" || entity.IsTerminalDelegationStatus(d.Status) {
				continue
			}
			if !r.handles[d.Handle] {
				r.handleOrder = append(r.handleOrder, d.Handle)
			}
			r.handles[d.Handle] = true
		}
		// The conversation owner is always addressable, and always by the
		// same name, so a sub-agent can report back without discovering an
		// address first.
		if !r.handles[entity.LeaderHandle] {
			r.handles[entity.LeaderHandle] = true
			r.handleOrder = append(r.handleOrder, entity.LeaderHandle)
		}
	}

	rows, err := s.Repo.ListProfilesInScopes(ctx, projectID, false)
	if err != nil {
		return nil, err
	}
	for _, p := range ResolveScoped(rows, projectID) {
		if p.Disabled {
			continue
		}
		if !r.roles[p.Key] {
			r.roleOrder = append(r.roleOrder, p.Key)
		}
		r.roles[p.Key] = true
	}
	return r, nil
}

// Resolve maps one @token to what it addresses.
func (r *Resolver) Resolve(token string) Target {
	if r == nil {
		return Target{}
	}
	if r.handles[token] {
		return Target{Kind: TargetAgent, Handle: token}
	}
	if r.roles[token] {
		return Target{Kind: TargetRole, Role: token}
	}
	return Target{}
}

// AllNames is the roster ParseMentions is given: every token that could
// legitimately be a mention here. Anything outside it stays plain text,
// which is what keeps @media and email addresses from spawning agents.
func (r *Resolver) AllNames() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.handleOrder)+len(r.roleOrder))
	out = append(out, r.handleOrder...)
	for _, k := range r.roleOrder {
		if !r.handles[k] {
			out = append(out, k)
		}
	}
	return out
}

// SpawnableRoles lists role keys that have no live instance yet — what a
// sub-agent can start rather than talk to.
func (r *Resolver) SpawnableRoles() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.roleOrder))
	for _, k := range r.roleOrder {
		if !r.handles[k] {
			out = append(out, k)
		}
	}
	return out
}
