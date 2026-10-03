package team

import (
	"strings"

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
	// offKeys and offTools are what the agent's switched-off features
	// take away (see featureGates).
	offKeys     map[string]bool
	offTools    map[string]bool
	offPrefixes []string
}

var (
	_ connectors.AgentScope        = (*Scope)(nil)
	_ connectors.AgentFeatureScope = (*Scope)(nil)
)

// featureGate is what one feature switches off on the server: connector
// types (every instance, session instances included) and wick MCP tools,
// by exact name or by prefix. Keys mirror the connector packages'
// Key constants, which this package does not import.
type featureGate struct {
	keys     []string
	tools    []string
	prefixes []string
}

// featureGates maps the features with a server-side surface. Files,
// Process, Todos and Workspace are panels only: hiding the tab is all
// switching them off does.
func featureGates(f Features) []featureGate {
	var out []featureGate
	if !f.Schedule {
		out = append(out, featureGate{tools: []string{"wick_schedule_message"}})
	}
	if !f.Notes {
		out = append(out, featureGate{keys: []string{"notes"}})
	}
	if !f.Tickets {
		out = append(out, featureGate{keys: []string{"tickets"}})
	}
	if !f.Subagents {
		out = append(out, featureGate{keys: []string{"sub-agents"}, prefixes: []string{"wick_agent_"}})
	}
	if !f.Browser {
		out = append(out, featureGate{keys: []string{"playwright_browser"}})
	}
	if !f.Source {
		out = append(out, featureGate{keys: []string{"source"}})
	}
	return out
}

// WithFeatures switches off what f's disabled features cover and returns
// s for chaining. A deny-all scope is left as is.
func (s *Scope) WithFeatures(f Features) *Scope {
	if s.denyAll {
		return s
	}
	s.offKeys, s.offTools, s.offPrefixes = map[string]bool{}, map[string]bool{}, nil
	for _, g := range featureGates(f) {
		for _, k := range g.keys {
			s.offKeys[k] = true
		}
		for _, t := range g.tools {
			s.offTools[t] = true
		}
		s.offPrefixes = append(s.offPrefixes, g.prefixes...)
	}
	return s
}

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
	return NewScope(DecodeGrants(p.AllowedConnectors), p.IncludeNewConnectors, reach).
		WithFeatures(DecodeFeatures(p.Features))
}

// DenyAll returns a scope that permits nothing.
func DenyAll() *Scope { return &Scope{denyAll: true} }

// includes reports whether includeNew lets an unlisted connector in.
func (s *Scope) includes(connectorID string) bool {
	return s.includeNew && s.reach[connectorID]
}

// AllowKey implements connectors.AgentFeatureScope.
func (s *Scope) AllowKey(connectorKey string) bool {
	return !s.denyAll && !s.offKeys[connectorKey]
}

// AllowTool implements connectors.AgentFeatureScope. A deny-all scope
// leaves wick's own tools alone: it already reaches no connector.
func (s *Scope) AllowTool(name string) bool {
	if s.offTools[name] {
		return false
	}
	for _, p := range s.offPrefixes {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
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
