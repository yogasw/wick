package team

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
	// read-only ops — but only those in reach, the connector ids of the
	// owner's own catalog. The toggle reads "all my connectors, including
	// new ones (read only)": the owner's, not whoever triggers the turn,
	// and never one the catalog skips on purpose (wickmanager).
	includeNew bool
	// reach is the owner's VisibleCatalog as a set of connector ids. nil
	// (unknown, or the lookup failed) lets nothing in through includeNew.
	reach map[string]bool
	// denyAll is the disabled-agent scope: it answers no to everything.
	denyAll bool
}

var _ connectors.AgentScope = (*Scope)(nil)

// NewScope builds the scope for grants. reach is the owner's catalog (see
// Scope.reach); it only matters when includeNew is on.
func NewScope(grants []ConnectorGrant, includeNew bool, reach map[string]bool) *Scope {
	m := make(map[string]ConnectorGrant, len(grants))
	for _, g := range grants {
		if g.ConnectorID == "" {
			continue
		}
		m[g.ConnectorID] = g
	}
	return &Scope{grants: m, includeNew: includeNew, reach: reach}
}

// ScopeOf builds the scope an agent row describes. A disabled agent gets
// a deny-all scope rather than nil: nil would mean "not an agent" and
// hand its sessions the owner's full reach. reach is the owner's catalog,
// see Scope.reach.
func ScopeOf(p entity.AgentPersona, reach map[string]bool) *Scope {
	if p.Disabled {
		return DenyAll()
	}
	return NewScope(DecodeGrants(p.AllowedConnectors), p.IncludeNewConnectors, reach)
}

// DenyAll returns a scope that permits nothing.
func DenyAll() *Scope { return &Scope{denyAll: true} }

// includes reports whether includeNew lets an unlisted connector in.
func (s *Scope) includes(connectorID string) bool {
	return s.includeNew && s.reach[connectorID]
}

// AllowConnector implements connectors.AgentScope.
func (s *Scope) AllowConnector(connectorID string) bool {
	if s.denyAll {
		return false
	}
	if _, ok := s.grants[connectorID]; ok {
		return true
	}
	return s.includes(connectorID)
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
		return s.includes(connectorID)
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
		return s.includes(connectorID) && !destructive
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
