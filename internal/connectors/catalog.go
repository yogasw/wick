package connectors

import (
	"context"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// wickManagerKey is the connector whose ops surface as top-level
// wick_manager_* MCP tools; it never appears in the catalog. Mirrors
// wickmanager.Key, which this package cannot import.
const wickManagerKey = "wickmanager"

// CatalogOp is one op of a catalog entry that is live for the caller.
type CatalogOp struct {
	Key         string
	Name        string
	Destructive bool
}

// CatalogEntry is one connector instance as wick_list shows it to a caller.
type CatalogEntry struct {
	Row    entity.Connector
	Module connector.Module
	Status string
	// Accounts are the connected accounts the caller may see and run as.
	// Always empty for a module without OAuth.
	Accounts []entity.ConnectorAccount
	// Ops are the enabled ops, in declaration order. Never empty: a row
	// with no enabled op is not in the catalog at all.
	Ops []CatalogOp
}

// VisibleCatalog is what a caller reaches through MCP: the connectors
// wick_list lists, each with its live ops and the accounts the caller may
// run it as.
//
// One function for every surface that answers "what can this person use"
// — wick_list itself and the agent checklist — so a checklist can never
// offer what wick_list hides, nor hide what it shows. An agent scope on
// ctx narrows the result as it narrows wick_list; pass a ctx without one
// to get the person's own reach.
func (s *Service) VisibleCatalog(ctx context.Context, userID string, tagIDs []string, isAdmin bool) ([]CatalogEntry, error) {
	return s.catalog(ctx, userID, tagIDs, isAdmin, false)
}

// AgentCatalog is VisibleCatalog plus the wickmanager row when the caller
// may see it (admins only, by its System tag). wick_list leaves it out
// because its ops surface as wick_manager_* tools instead; an agent's
// Access tab still needs it as a System-tier entry.
func (s *Service) AgentCatalog(ctx context.Context, userID string, tagIDs []string, isAdmin bool) ([]CatalogEntry, error) {
	return s.catalog(ctx, userID, tagIDs, isAdmin, true)
}

func (s *Service) catalog(ctx context.Context, userID string, tagIDs []string, isAdmin, withManager bool) ([]CatalogEntry, error) {
	rows, err := s.ListVisibleTo(ctx, userID, tagIDs, isAdmin)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogEntry, 0, len(rows))
	for _, row := range rows {
		if row.Key == wickManagerKey && !withManager {
			continue
		}
		mod, ok := s.Module(row.Key)
		if !ok {
			continue
		}
		// Type-level off-switch: a disabled connector type is hidden from the
		// LLM entirely (the manager UI still shows it with a Disabled badge).
		if !s.TypeEnabled(row.Key) {
			continue
		}
		states, err := s.OperationStates(ctx, row.ID, row.Key)
		if err != nil {
			continue
		}
		ops := enabledOps(mod, states)
		// Live-catalog modules (custom MCP) at zero ops may simply not
		// have synced yet — run the lazy refresh (throttled) and
		// recount before deciding to hide the connector. Without this,
		// an unsynced connector would never surface: invisible here
		// means no wick_get, and no wick_get means no refresh.
		if len(ops) == 0 && mod.Meta.LiveCatalog {
			s.CatalogRefresh(ctx, row.Key, row.ID)
			if fresh, ok2 := s.Module(row.Key); ok2 {
				mod = fresh
				if states, err = s.OperationStates(ctx, row.ID, row.Key); err != nil {
					continue
				}
				ops = enabledOps(mod, states)
			}
		}
		if len(ops) == 0 {
			continue
		}
		status := s.Status(row)
		if status == "needs_setup" {
			continue
		}
		e := CatalogEntry{Row: row, Module: mod, Status: status, Ops: ops}
		if mod.OAuth != nil {
			caller := s.AccountAccessFor(row, userID, isAdmin, tagIDs)
			accs, aerr := s.ListAccountsVisibleTo(ctx, row, caller)
			if aerr != nil {
				log.Ctx(ctx).Warn().Err(aerr).Str("connector", row.ID).Msg("catalog: list accounts")
			}
			e.Accounts = accs
		}
		out = append(out, e)
	}
	return out, nil
}

// enabledOps lists the ops states switches on. ConfigOnly ops are already
// false in states (see OperationStates).
func enabledOps(mod connector.Module, states map[string]bool) []CatalogOp {
	var ops []CatalogOp
	for _, op := range mod.AllOps() {
		if states[op.Key] {
			ops = append(ops, CatalogOp{Key: op.Key, Name: op.Name, Destructive: op.Destructive})
		}
	}
	return ops
}
