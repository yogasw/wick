package delegation

import "sync"

// ChildGrants remembers the NARROWED identity each running sub-agent was
// started with, keyed by its own session id.
//
// # Why this exists
//
// A delegation computes EffectiveTags(userTags, profile) — the triggering
// human's tags intersected with the role's allowed list — and mints a
// scoped token from it. That token is handed to the runner in
// ChildSpec.MCPToken, and the pool runner never reads it: a child spawns
// through Pool.Send like any other session, and the pool's factory mints
// its OWN credential from the session owner's FULL tag set.
//
// So the narrowing was computed, stored, revoked — and never applied. A
// role with allowed_tags saw exactly what its triggering user saw. Measured
// on 2026-09-21: a sub-agent reported the same 34 connectors as its parent.
//
// Rather than thread a token through the pool's spawn path (which mints
// lazily, per spawn, and re-mints after an idle kill), the narrowing is
// published here for the LIFETIME of the run. The minter consults it first
// and mints for the child accordingly, so every re-spawn of that child gets
// the same narrowed identity.
//
// Entries are cleared when the run reaches a terminal state, next to the
// token revoke — a child that has finished must not leave a grant behind
// for a session id that could be reused.
type ChildGrants struct {
	mu sync.Mutex
	m  map[string]ChildGrant
}

// ChildGrant is what a sub-agent's MCP credential must be minted from.
type ChildGrant struct {
	// UserID is the human who triggered the delegation. Never the leader
	// agent, and never a synthetic principal — per-owner gates and the
	// audit trail both want a real person.
	UserID string
	// TagIDs is already the intersection of that human's tags with the
	// role's allowed list. It can only be narrower than the user's own.
	TagIDs []string
}

func NewChildGrants() *ChildGrants { return &ChildGrants{m: map[string]ChildGrant{}} }

// Set publishes the identity a child session must run as. Safe on a nil
// receiver so callers that never wired one (tests) need no branch.
func (g *ChildGrants) Set(sessionID, userID string, tagIDs []string) {
	if g == nil || sessionID == "" || userID == "" {
		return
	}
	cp := make([]string, len(tagIDs))
	copy(cp, tagIDs)
	g.mu.Lock()
	g.m[sessionID] = ChildGrant{UserID: userID, TagIDs: cp}
	g.mu.Unlock()
}

// Get reports the narrowed identity for a session, if it is a running
// sub-agent. ok=false for an ordinary session — the caller then mints the
// way it always did.
func (g *ChildGrants) Get(sessionID string) (ChildGrant, bool) {
	if g == nil || sessionID == "" {
		return ChildGrant{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	got, ok := g.m[sessionID]
	if !ok {
		return ChildGrant{}, false
	}
	cp := make([]string, len(got.TagIDs))
	copy(cp, got.TagIDs)
	return ChildGrant{UserID: got.UserID, TagIDs: cp}, true
}

// Clear drops a finished child's grant.
func (g *ChildGrants) Clear(sessionID string) {
	if g == nil || sessionID == "" {
		return
	}
	g.mu.Lock()
	delete(g.m, sessionID)
	g.mu.Unlock()
}
