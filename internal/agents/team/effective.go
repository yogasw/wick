package team

import (
	"context"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// Who an effective-access row runs as.
const (
	RunsAsSelf  = "self"  // the caller's own access and accounts
	RunsAsOwner = "owner" // lent: the agent owner's access
	RunsAsNone  = "none"  // the caller cannot use it
)

// Status of the account a lent row runs as.
const (
	LentAccountNone      = ""           // the lend names no account: the caller's pick or the instance credential
	LentAccountConnected = "connected"  // named, connected, the owner's
	LentAccountOther     = "other_user" // named, but connected by someone else
	LentAccountMissing   = "missing"    // named, but not connected (any more)
)

// EffectiveRow is what one caller gets on one connector through an
// agent: the same answer the agent scope and the lend give at run time.
type EffectiveRow struct {
	ConnectorID string `json:"connector_id"`
	Label       string `json:"label"`
	Key         string `json:"key"`
	// Level is the agent's level (LevelAll/Read/Pick/Off); Ops lists the
	// ops the caller may run under it.
	Level string   `json:"level"`
	Ops   []string `json:"ops"`
	// RunsAs is RunsAsSelf, RunsAsOwner or RunsAsNone.
	RunsAs string `json:"runs_as"`
	// Lent is true when the agent lends this connector at all (whether
	// or not this caller needs the lend).
	Lent bool `json:"lent"`
	// Account is the lent account id when the lend names one; its owner
	// and status say whether it will actually work.
	Account       string `json:"account,omitempty"`
	AccountOwner  string `json:"account_owner,omitempty"`
	AccountStatus string `json:"account_status,omitempty"`
}

// EffectiveCaller describes the person the preview is for. Reaches
// reports whether they can use a connector on their own; nil = nobody's
// reach (a guest). Owner marks the agent's owner.
type EffectiveCaller struct {
	UserID  string
	Owner   bool
	Reaches func(connectorID string) bool
	// Level is the caller's role limit (Scope.CallerLevel) on their own
	// access (lent false) or on a lend; nil means no role applies.
	Level func(connectorID string, lent bool) (string, []string)
}

// Effective lists, for every connector in the owner's catalog, what
// caller gets through scope. The order of checks is the run-time one:
// the agent's level first (Scope.Level / AllowOp), then the caller's own
// reach, then the lend (Scope.LentBy / LentAccount). accounts lists a
// connector's connected accounts, for the lent-account status; nil skips
// it.
func Effective(scope *Scope, catalog []connectors.CatalogEntry, caller EffectiveCaller, accounts func(connectorID string) []entity.ConnectorAccount) []EffectiveRow {
	out := make([]EffectiveRow, 0, len(catalog))
	for _, e := range catalog {
		id := e.Row.ID
		row := EffectiveRow{ConnectorID: id, Label: e.Row.Label, Key: e.Row.Key, Level: scope.Level(id), Ops: []string{}, RunsAs: RunsAsNone}
		lender, lent := scope.LentBy(id)
		row.Lent = lent
		if acc, ok := scope.LentAccount(id); ok {
			row.Account = acc
			row.AccountStatus = LentAccountMissing
			if accounts != nil {
				for _, a := range accounts(id) {
					if a.ID != acc {
						continue
					}
					row.AccountOwner = a.WickUserID
					if a.WickUserID == "" || a.WickUserID == lender {
						row.AccountStatus = LentAccountConnected
					} else {
						row.AccountStatus = LentAccountOther
					}
				}
			}
		}
		if row.Level != LevelOff {
			switch {
			case caller.Owner || (caller.Reaches != nil && caller.Reaches(id)):
				row.RunsAs = RunsAsSelf
			case lent:
				row.RunsAs = RunsAsOwner
			}
		}
		roleLv, roleOps := LevelAll, []string(nil)
		if caller.Level != nil && row.RunsAs != RunsAsNone {
			roleLv, roleOps = caller.Level(id, row.RunsAs == RunsAsOwner)
			if roleLv == LevelOff && row.RunsAs == RunsAsSelf && lent && !caller.Owner {
				// A role that switches the caller's own access off (a
				// guest) can still borrow the lend: at run time the own
				// connector is hidden and the lend applies instead.
				row.RunsAs = RunsAsOwner
				roleLv, roleOps = caller.Level(id, true)
			}
			if roleLv == LevelOff {
				row.RunsAs = RunsAsNone
			}
		}
		if row.RunsAs != RunsAsNone {
			for _, op := range e.Ops {
				if scope.AllowOp(id, op.Key, op.Destructive) && roleAllows(roleLv, roleOps, op.Key, op.Destructive) {
					row.Ops = append(row.Ops, op.Key)
				}
			}
		}
		out = append(out, row)
	}
	return out
}

// ScopeForAgent is the scope p runs under — the one ScopeForSession
// caches for p's sessions — built fresh, for previews. A role document
// that cannot be read gives deny-all, as at run time.
func (s *Service) ScopeForAgent(ctx context.Context, p entity.AgentPersona) *Scope {
	sc, err := s.scopeWithRoles(ctx, p, ScopeOf(p, s.reachOf(ctx, p)).WithTiers(s.tiersOf(ctx, p)))
	if err != nil {
		return DenyAll()
	}
	return sc
}

// roleAllows is a role level applied to one op, as connectors does at run
// time.
func roleAllows(level string, ops []string, opKey string, destructive bool) bool {
	switch level {
	case LevelAll:
		return true
	case LevelRead:
		return !destructive
	case LevelPick:
		for _, k := range ops {
			if k == opKey {
				return true
			}
		}
	}
	return false
}
