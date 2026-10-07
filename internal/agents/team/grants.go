package team

import (
	"errors"
	"strings"

	"github.com/yogasw/wick/internal/connectors"
)

// CatalogItem is what one connector of the owner's catalog lets a grant
// name: its accounts ("" = the instance's own identity) and its live ops.
type CatalogItem struct {
	Accounts map[string]bool
	Ops      map[string]bool
}

// Catalog is the owner's reach keyed by connector id — the set a checklist
// may pick from.
type Catalog map[string]CatalogItem

// CatalogOf indexes a connectors.VisibleCatalog result.
//
// The instance identity ("") is pickable on every listed connector: wick_list
// shows the connector entry itself whenever it shows the row, and running
// that entry is running as the instance.
func CatalogOf(entries []connectors.CatalogEntry) Catalog {
	out := make(Catalog, len(entries))
	for _, e := range entries {
		it := CatalogItem{Accounts: map[string]bool{"": true}, Ops: map[string]bool{}}
		for _, a := range e.Accounts {
			it.Accounts[a.ID] = true
		}
		for _, op := range e.Ops {
			it.Ops[op.Key] = true
		}
		out[e.Row.ID] = it
	}
	return out
}

// ReachOf indexes a connectors.AgentCatalog result with each entry's tier.
func ReachOf(entries []connectors.CatalogEntry) Reach {
	out := make(Reach, len(entries))
	for _, e := range entries {
		it := ReachItem{Key: e.Row.Key, Tier: TierOf(e.Module.Meta.DefaultTags), Label: e.Row.Label}
		if len(e.Accounts) > 0 {
			it.Accounts = make(map[string]string, len(e.Accounts))
			for _, a := range e.Accounts {
				it.Accounts[a.ID] = a.DisplayName
			}
		}
		out[e.Row.ID] = it
	}
	return out
}

// CheckGrants rejects grants this build cannot interpret and grants that
// reach past the owner's catalog: a connector the owner does not see, an
// account they cannot run as, or (for LevelPick) an op that is not live.
// The error names every rejected item so the editor can point at them.
func CheckGrants(gs []ConnectorGrant, cat Catalog) error {
	var bad []string
	for _, g := range gs {
		if strings.TrimSpace(g.ConnectorID) == "" {
			return errors.New("allowed_connectors: connector_id is required")
		}
		switch g.Level {
		case LevelAll, LevelRead, LevelPick, LevelOff:
		default:
			return errors.New("allowed_connectors: level must be all, read, pick or off")
		}
		if strings.HasPrefix(g.ConnectorID, ToolPrefix) {
			// wick's own tools know only on and off.
			if !isToolGrant(g.ConnectorID) || (g.Level != LevelAll && g.Level != LevelOff) {
				bad = append(bad, "tool "+strings.TrimPrefix(g.ConnectorID, ToolPrefix))
			}
			continue
		}
		it, ok := cat[g.ConnectorID]
		if !ok {
			bad = append(bad, "connector "+g.ConnectorID)
			continue
		}
		for _, a := range g.Accounts {
			if !it.Accounts[a] {
				bad = append(bad, "account "+g.ConnectorID+"/"+a)
			}
		}
		if g.Level == LevelPick {
			for _, op := range g.Ops {
				if !it.Ops[op] {
					bad = append(bad, "op "+g.ConnectorID+"/"+op)
				}
			}
		}
	}
	if len(bad) > 0 {
		return errors.New("allowed_connectors: outside your connector access: " + strings.Join(bad, ", "))
	}
	return nil
}
