package team

import (
	"context"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/config"
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
	agentID := AgentOfSession(s.layout, sessionID)
	if agentID == "" {
		return nil
	}
	now := s.now()
	s.mu.Lock()
	if e, ok := s.cache[agentID]; ok && now.Before(e.expiry) {
		s.mu.Unlock()
		return e.scope
	}
	s.mu.Unlock()

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
		scope = ScopeOf(p)
	}
	s.mu.Lock()
	s.cache[agentID] = scopeEntry{scope: scope, expiry: now.Add(scopeCacheTTL)}
	s.mu.Unlock()
	return scope
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
