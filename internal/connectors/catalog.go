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
	// Op toggles and accounts for every row up front: one query each
	// instead of one (or three) per row, which on a remote database made
	// this listing — read by every Team roster load — take seconds.
	ids := make([]string, 0, len(rows))
	var oauthIDs []string
	for _, row := range rows {
		ids = append(ids, row.ID)
		if mod, ok := s.Module(row.Key); ok && mod.OAuth != nil {
			oauthIDs = append(oauthIDs, row.ID)
		}
	}
	opRows, err := s.repo.ListOperationsFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	accRows, aerr := s.repo.ListAccountsFor(ctx, oauthIDs)
	if aerr != nil {
		log.Ctx(ctx).Warn().Err(aerr).Msg("catalog: list accounts")
	}
	// Type switches once too: TypeEnabled is a query per call.
	disabled := s.DisabledTypeKeys()
	out := make([]CatalogEntry, 0, len(rows))
	// needTags are the entries whose accounts still hang on their filter
	// tags; those are resolved in one query after the loop.
	var needTags []int
	var callers []AccountAccess
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
		if disabled[row.Key] {
			continue
		}
		ops := enabledOps(mod, s.liveOpStates(ctx, row.ID, row.Key, foldOpStates(mod, opRows[row.ID])))
		// Live-catalog modules (custom MCP) at zero ops may simply not
		// have synced yet — run the lazy refresh (throttled) and
		// recount before deciding to hide the connector. Without this,
		// an unsynced connector would never surface: invisible here
		// means no wick_get, and no wick_get means no refresh.
		if len(ops) == 0 && mod.Meta.LiveCatalog {
			s.CatalogRefresh(ctx, row.Key, row.ID)
			if fresh, ok2 := s.Module(row.Key); ok2 {
				mod = fresh
				states, err := s.OperationStates(ctx, row.ID, row.Key)
				if err != nil {
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
		if mod.OAuth != nil && aerr == nil {
			caller := s.AccountAccessFor(row, userID, isAdmin, tagIDs)
			e.Accounts = filterAccountsByScope(ctx, row.ID, accRows[row.ID])
			// Skip the tag lookup when the answer cannot depend on it.
			if !caller.Privileged && !row.AllowOthersSeeAccounts && len(e.Accounts) > 0 {
				needTags = append(needTags, len(out))
				callers = append(callers, caller)
			}
		}
		out = append(out, e)
	}
	if len(needTags) > 0 {
		var accs []entity.ConnectorAccount
		for _, i := range needTags {
			accs = append(accs, out[i].Accounts...)
		}
		tags, err := s.AccountTagIDs(ctx, accs)
		for n, i := range needTags {
			if err != nil {
				log.Ctx(ctx).Warn().Err(err).Str("connector", out[i].Row.ID).Msg("catalog: list accounts")
				out[i].Accounts = nil
				continue
			}
			out[i].Accounts = AccountsVisibleTo(out[i].Row, out[i].Accounts, tags, callers[n])
		}
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
