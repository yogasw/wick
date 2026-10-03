package team

import (
	"strings"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// Scope is an agent's checklist in the shape connectors.Service checks.
// It only ever narrows: the service consults it after the owner's own
// visibility rules, so nothing here can grant what the owner lacks.
//
// What an agent gets on one connector is decided by Level, in this order:
// an explicit grant (LevelOff included), else the connector's tier default
// (TierPlatform: write; TierSystem: write for the Captain only), else the
// include-new toggle (read only), else nothing. All of it stops at reach.
type Scope struct {
	grants map[string]ConnectorGrant
	// includeNew lets untiered connectors absent from the checklist
	// through with read-only ops — the owner's, not whoever triggers the
	// turn.
	includeNew bool
	// captain unlocks the TierSystem default and the owner-wide data
	// scope (see CheckSessionTarget).
	captain bool
	// agentID is the agent this scope was built for, ownerID its owner:
	// the data scope's hard edge, whoever the call's login identity is.
	agentID, ownerID string
	// reach is the owner's catalog (see Reach). nil (unknown, or the
	// lookup failed) leaves explicit grants working and every default off.
	reach Reach
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

// featureGates maps the features with a server-side surface that
// MigrateFeatures could not turn into off grants (the owner's catalog was
// unknown). Files, Process, Todos, Workspace and Browser are panels only:
// the browser is an ordinary connector reached through a grant.
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
// Scope.reach); captain unlocks the TierSystem default.
func NewScope(grants []ConnectorGrant, includeNew, captain bool, reach Reach) *Scope {
	m := make(map[string]ConnectorGrant, len(grants))
	for _, g := range grants {
		if g.ConnectorID == "" {
			continue
		}
		m[g.ConnectorID] = g
	}
	return &Scope{grants: m, includeNew: includeNew, captain: captain, reach: reach}
}

// ScopeOf builds the scope an agent row describes. A disabled agent gets
// a deny-all scope rather than nil: nil would mean "not an agent" and
// hand its sessions the owner's full reach. reach is the owner's catalog,
// see Scope.reach. Old feature switches are read through MigrateFeatures.
func ScopeOf(p entity.AgentPersona, reach Reach) *Scope {
	if p.Disabled {
		return DenyAll()
	}
	f, grants, _ := MigrateFeatures(DecodeFeatures(p.Features), DecodeGrants(p.AllowedConnectors), reach)
	s := NewScope(grants, p.IncludeNewConnectors, p.IsCaptain, reach).WithFeatures(f)
	s.agentID, s.ownerID = p.ID, p.OwnerUserID
	return s
}

// DenyAll returns a scope that permits nothing.
func DenyAll() *Scope { return &Scope{denyAll: true} }

// Level resolves what the agent may do on connectorID: LevelAll,
// LevelRead, LevelPick or LevelOff. It is the one place the order lives —
// explicit grant, then tier default, then include-new — and everything
// stops at reach once reach is known.
func (s *Scope) Level(connectorID string) string {
	lv, _ := s.resolve(connectorID)
	return lv
}

// resolve is Level plus the grant it came from, if any.
func (s *Scope) resolve(connectorID string) (string, *ConnectorGrant) {
	if s.denyAll {
		return LevelOff, nil
	}
	it, inReach := s.reach[connectorID]
	if s.reach != nil && !inReach && !isToolGrant(connectorID) {
		return LevelOff, nil
	}
	if g, ok := s.grants[connectorID]; ok {
		switch g.Level {
		case LevelOff:
			return LevelOff, nil
		case LevelAll, LevelPick:
			return g.Level, &g
		}
		return LevelRead, &g
	}
	if isToolGrant(connectorID) {
		// wick's own session tools: on unless switched off.
		return LevelAll, nil
	}
	if s.reach == nil {
		return LevelOff, nil
	}
	switch it.Tier {
	case TierPlatform:
		return LevelAll, nil
	case TierSystem:
		if s.captain {
			return LevelAll, nil
		}
		return LevelOff, nil
	}
	if s.includeNew {
		return LevelRead, nil
	}
	return LevelOff, nil
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
	if !s.denyAll && IsPlatformTool(name) && s.Level(toolGrantID(name)) == LevelOff {
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
	return s.Level(connectorID) != LevelOff
}

// AllowAccount implements connectors.AgentScope. A connector reached
// through a tier default or include-new carries no account list: the
// owner's own account visibility is the only limit. A grant's list is
// exact — "" (the bot) included only when listed, so a grant naming just
// a personal account refuses ops run as the bot.
func (s *Scope) AllowAccount(connectorID, accountID string) bool {
	lv, g := s.resolve(connectorID)
	if lv == LevelOff {
		return false
	}
	if g == nil || len(g.Accounts) == 0 {
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
	lv, g := s.resolve(connectorID)
	switch lv {
	case LevelAll:
		return true
	case LevelPick:
		for _, k := range g.Ops {
			if k == opKey {
				return true
			}
		}
		return false
	case LevelRead:
		return !destructive
	}
	return false
}
