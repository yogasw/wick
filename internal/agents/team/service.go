package team

import (
	"context"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// scopeCacheTTL bounds how long a checklist edit takes to reach a running
// agent. Short on purpose: the resolver runs on every MCP request, and a
// tool burst would otherwise be one DB read per call, but an owner who
// just unticked a connector expects it gone on the agent's next step.
const scopeCacheTTL = 5 * time.Second

type scopeEntry struct {
	scope  connectors.AgentScope
	expiry time.Time
}

// Service is the store plus the session → scope resolver the MCP layer
// uses.
type Service struct {
	*Store
	layout config.Layout

	mu    sync.Mutex
	cache map[string]scopeEntry
	now   func() time.Time

	// ownerReach answers which connectors an agent's owner reaches —
	// their AgentCatalog — with each one's tier. nil (not wired) leaves
	// explicit grants working and every default off.
	ownerReach OwnerReachFunc
	// ownerCatalog is the owner's full catalog (accounts and ops), what a
	// Captain's access proposal is checked against.
	ownerCatalog OwnerCatalogFunc
}

// OwnerReachFunc returns userID's own catalog (see connectors
// AgentCatalog) with no agent scope applied.
type OwnerReachFunc func(ctx context.Context, userID string) (Reach, error)

// SetOwnerReach wires the owner-catalog lookup. Set at boot, once the
// connectors service exists.
func (s *Service) SetOwnerReach(f OwnerReachFunc) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.ownerReach = f
	s.mu.Unlock()
}

// reachOf is p's owner catalog. A failed lookup is nil: tier defaults and
// include-new then let nothing in, the ticked connectors still work.
func (s *Service) reachOf(ctx context.Context, p entity.AgentPersona) Reach {
	s.mu.Lock()
	f := s.ownerReach
	s.mu.Unlock()
	if f == nil {
		return nil
	}
	reach, err := f(ctx, p.OwnerUserID)
	if err != nil {
		return nil
	}
	return reach
}

// NewService builds the service over db and the agents layout (needed to
// read session meta).
func NewService(db *gorm.DB, layout config.Layout) *Service {
	return &Service{
		Store:  NewStore(db),
		layout: layout,
		cache:  map[string]scopeEntry{},
		now:    time.Now,
	}
}

// ScopeForSession returns the connector scope of the agent sessionID
// belongs to, or nil when it belongs to none.
//
// An agent id the session names but the table no longer has (the agent
// was deleted) returns deny-all, and so does a lookup error: either way
// failing open would hand the session everything its owner can reach,
// which no checklist ever granted.
func (s *Service) ScopeForSession(ctx context.Context, sessionID string) connectors.AgentScope {
	if s == nil || sessionID == "" {
		return nil
	}
	agentID, err := AgentOfSession(s.layout, sessionID)
	if err != nil {
		// The chain broke above this session: it may be an agent's
		// sub-agent, so it gets nothing rather than its owner's reach.
		return DenyAll()
	}
	if agentID == "" {
		return nil
	}
	now := s.now()
	s.mu.Lock()
	if e, ok := s.cache[agentID]; ok && now.Before(e.expiry) {
		s.mu.Unlock()
		_, direct := s.directAgent(sessionID)
		return scopeFor(e.scope, direct)
	}
	s.mu.Unlock()

	_, direct := s.directAgent(sessionID)
	var scope connectors.AgentScope
	p, err := s.Get(ctx, agentID)
	switch {
	case err == ErrNotFound:
		// The session was an agent's and the agent is gone. Falling back
		// to "no scope" would hand the session its owner's full reach, so
		// a deleted agent's sessions keep running with no connectors.
		scope = DenyAll()
	case err != nil:
		return DenyAll()
	default:
		scope = ScopeOf(p, s.reachOf(ctx, p))
	}
	s.mu.Lock()
	s.cache[agentID] = scopeEntry{scope: scope, expiry: now.Add(scopeCacheTTL)}
	s.mu.Unlock()
	return scopeFor(scope, direct)
}

// scopeFor is the cached agent scope as one session sees it: a sub-agent
// delegated under the agent never inherits "Manage other agents".
func scopeFor(scope connectors.AgentScope, direct bool) connectors.AgentScope {
	if sc, ok := scope.(*Scope); ok && !direct && sc.manageAgents {
		cp := *sc
		cp.manageAgents = false
		return &cp
	}
	return scope
}

// directAgent is the agent sessionID itself carries (meta.agent_id), and
// whether it carries one at all — false for a sub-agent, whose agent is
// only found up the parent chain.
func (s *Service) directAgent(sessionID string) (string, bool) {
	if s == nil || sessionID == "" {
		return "", false
	}
	sess, err := session.Load(s.layout, sessionID)
	if err != nil || sess.Meta.AgentID == "" || sess.Meta.ParentSessionID != "" {
		return "", false
	}
	return sess.Meta.AgentID, true
}

// Invalidate drops the cached scope of one agent so an edit applies on
// the agent's very next call instead of after the TTL.
func (s *Service) Invalidate(agentID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.cache, agentID)
	s.mu.Unlock()
}

// Update saves p and invalidates its cached scope.
func (s *Service) Update(ctx context.Context, p *entity.AgentPersona) error {
	err := s.Store.Update(ctx, p)
	s.Invalidate(p.ID)
	return err
}

// Delete removes the agent and invalidates its cached scope.
func (s *Service) Delete(ctx context.Context, id string) error {
	err := s.Store.Delete(ctx, id)
	s.Invalidate(id)
	return err
}
