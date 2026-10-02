package persona

import (
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// Scope is an agent's checklist in the shape connectors.Service checks.
// It only ever narrows: the service consults it after the owner's own
// visibility rules, so nothing here can grant what the owner lacks.
type Scope struct {
	grants map[string]ConnectorGrant
	// includeNew lets connectors absent from the checklist through with
	// read-only ops. The checklist UI lists every connector the owner
	// sees, so "absent" means either gained after the last save or left
	// unticked; both are treated alike, which is why the toggle reads
	// "include new (read-only)" rather than promising more.
	includeNew bool
	// denyAll is the disabled-agent scope: it answers no to everything.
	denyAll bool
}

var _ connectors.AgentScope = (*Scope)(nil)

// NewScope builds the scope for grants.
func NewScope(grants []ConnectorGrant, includeNew bool) *Scope {
	m := make(map[string]ConnectorGrant, len(grants))
	for _, g := range grants {
		if g.ConnectorID == "" {
			continue
		}
		m[g.ConnectorID] = g
	}
	return &Scope{grants: m, includeNew: includeNew}
}

// ScopeOf builds the scope an agent row describes. A disabled agent gets
// a deny-all scope rather than nil: nil would mean "not an agent" and
// hand its sessions the owner's full reach.
func ScopeOf(p entity.AgentPersona) *Scope {
	if p.Disabled {
		return DenyAll()
	}
	return NewScope(DecodeGrants(p.AllowedConnectors), p.IncludeNewConnectors)
}

// DenyAll returns a scope that permits nothing.
func DenyAll() *Scope { return &Scope{denyAll: true} }

// AllowConnector implements connectors.AgentScope.
func (s *Scope) AllowConnector(connectorID string) bool {
	if s.denyAll {
		return false
	}
	if _, ok := s.grants[connectorID]; ok {
		return true
	}
	return s.includeNew
}

// AllowAccount implements connectors.AgentScope.
func (s *Scope) AllowAccount(connectorID, accountID string) bool {
	if s.denyAll {
		return false
	}
	g, ok := s.grants[connectorID]
	if !ok {
		// An included-new connector carries no account list: the owner's
		// own account visibility is the only limit.
		return s.includeNew
	}
	if len(g.Accounts) == 0 {
		return true
	}
	for _, a := range g.Accounts {
		if a == accountID {
			return true
		}
	}
	return false
}

// AllowOp implements connectors.AgentScope.
func (s *Scope) AllowOp(connectorID, opKey string, destructive bool) bool {
	if s.denyAll {
		return false
	}
	g, ok := s.grants[connectorID]
	if !ok {
		return s.includeNew && !destructive
	}
	switch g.Level {
	case LevelAll:
		return true
	case LevelPick:
		for _, k := range g.Ops {
			if k == opKey {
				return true
			}
		}
		return false
	default:
		// LevelRead, and any value this build does not know.
		return !destructive
	}
}
