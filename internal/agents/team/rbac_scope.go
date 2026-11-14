package team

import (
	"context"
	"fmt"
	"sync"

	"github.com/yogasw/wick/internal/connectors"
)

var _ connectors.RoleScope = (*Scope)(nil)

// SharedCheck answers whether agentID is shared with userID.
type SharedCheck func(ctx context.Context, agentID, userID string) bool

// roleScope is the "By role" half of a Scope: the document in force and
// how to tell whether the agent is shared with a caller.
type roleScope struct {
	doc    RBACDoc
	shared SharedCheck

	mu    sync.Mutex
	roles map[string]string // caller id -> role, for the scope's short life
}

// WithRoles sets the role document (and the share lookup a "shared" rule
// needs) on a By role scope; any other scope is returned unchanged.
func (s *Scope) WithRoles(doc RBACDoc, shared SharedCheck) *Scope {
	if s.denyAll || s.roles == nil {
		return s
	}
	s.roles = &roleScope{doc: doc, shared: shared}
	return s
}

// InRoles reports whether the scope is in By role access.
func (s *Scope) InRoles() bool { return s.roles != nil && !s.denyAll }

// RoleOf is callerID's role on this agent; owner for every caller when
// the agent is not in By role access.
func (s *Scope) RoleOf(ctx context.Context, callerID string) string {
	if s.roles == nil {
		return RoleOwner
	}
	r := s.roles
	r.mu.Lock()
	if role, ok := r.roles[callerID]; ok {
		r.mu.Unlock()
		return role
	}
	r.mu.Unlock()
	role := r.doc.RoleFor(s.ownerID, callerID, func() bool {
		return r.shared != nil && r.shared(ctx, s.agentID, callerID)
	})
	r.mu.Lock()
	if r.roles == nil {
		r.roles = map[string]string{}
	}
	r.roles[callerID] = role
	r.mu.Unlock()
	return role
}

// CallerLevel implements connectors.RoleScope. Outside By role access it
// permits everything (the checklist alone decides). In it: an owner (the
// real one or a co-owner) gets everything; a guest gets nothing of their
// own and only what the lend table lends to guest by name; anyone else
// keeps their own access; on a lend, the role's lend level applies, and a
// role whose writes need approval reads only until the approval flow
// exists.
func (s *Scope) CallerLevel(ctx context.Context, callerID, connectorID string, lent bool) (string, []string, string) {
	if s.roles == nil || s.denyAll {
		return LevelAll, nil, ""
	}
	role := s.RoleOf(ctx, callerID)
	switch {
	case role == RoleOwner:
		return LevelAll, nil, ""
	case role == RoleGuest && !lent:
		return LevelOff, nil, "this agent gives guests only the connectors its owner lends to guest; ask its owner for a role"
	case !lent:
		return LevelAll, nil, ""
	}
	lv := s.roles.doc.LendLevel(role, connectorID)
	if lv.Level == LevelOff {
		if role == RoleGuest {
			return LevelOff, nil, "this agent does not lend this connector to guests; ask its owner for a role"
		}
		return LevelOff, nil, fmt.Sprintf("the %s role does not borrow this connector from the agent's owner", role)
	}
	if s.roles.doc.Roles[role].WriteNeedsApproval && lv.Level == LevelAll {
		return LevelRead, nil, fmt.Sprintf("writes by the %s role on the owner's account need the owner's approval, which is not available yet", role)
	}
	if lv.Level == LevelPick {
		return LevelPick, lv.Ops, fmt.Sprintf("the %s role may only run %d chosen operations of this connector", role, len(lv.Ops))
	}
	if lv.Level == LevelRead {
		return LevelRead, nil, fmt.Sprintf("the %s role may only read this connector on the owner's account", role)
	}
	return lv.Level, nil, ""
}
