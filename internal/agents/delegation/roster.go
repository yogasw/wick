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
	// finished maps a handle to its terminal status, for the instances
	// that have stopped. Addressable all the same — a message respawns
	// them in their own session — but a caller that cannot tell a working
	// agent from a stopped one will wait for a reply that needs a wake
	// first.
	finished map[string]string
	// order preserves a stable roster listing for ParseMentions and for
	// the spawn-time roster block.
	handleOrder []string
	roleOrder   []string
}

// Finished reports a handle's terminal status, and false when it is still
// working.
func (r *Resolver) Finished(handle string) (string, bool) {
	if r == nil {
		return "", false
	}
	st, ok := r.finished[handle]
	return st, ok
}

// NewResolver builds the snapshot for a tree.
//
// rootID may be empty — a conversation that has never delegated has no
// tree yet, and every mention in it can only be a role.
func (s *Service) NewResolver(ctx context.Context, rootID, projectID string) (*Resolver, error) {
	r := &Resolver{handles: map[string]bool{}, roles: map[string]bool{}, finished: map[string]string{}}

	if rootID != "" {
		rows, err := s.Repo.ListByRoot(ctx, rootID)
		if err != nil {
			return nil, err
		}
		for _, d := range rows {
			// Finished instances are KEPT. They used to be dropped, on the
			// grounds that their process was gone and a message would queue
			// for a reader that never came. The waker changed that: a
			// message respawns the child in its own session, transcript and
			// all, so the reader arrives.
			//
			// Keeping them is what makes "@developer keep going" mean the
			// agent that did the work. Dropping the handle sent that token
			// down the role branch instead, which answered a follow-up by
			// spawning a stranger with no memory of the thing it was meant
			// to follow up on — and nothing in the reply said so.
			if d.Handle == "" {
				continue
			}
			if !r.handles[d.Handle] {
				r.handleOrder = append(r.handleOrder, d.Handle)
			}
			r.handles[d.Handle] = true
			if entity.IsTerminalDelegationStatus(d.Status) {
				r.finished[d.Handle] = d.Status
			}
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

// SpawnableRoles lists role keys that have no LIVE instance yet — what a
// sub-agent can start rather than talk to.
//
// A role whose only instance has finished is spawnable again. Keeping
// finished handles addressable (see NewResolver) must not cost the
// ability to start fresh work of the same kind: a review that finished an
// hour ago should not make "review this too" unavailable for the rest of
// the conversation.
func (r *Resolver) SpawnableRoles() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.roleOrder))
	for _, k := range r.roleOrder {
		if _, done := r.finished[k]; r.handles[k] && !done {
			continue
		}
		out = append(out, k)
	}
	return out
}
