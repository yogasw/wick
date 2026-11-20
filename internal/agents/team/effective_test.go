package team

import (
	"testing"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

func effCatalog() []connectors.CatalogEntry {
	return []connectors.CatalogEntry{
		{Row: entity.Connector{ID: "docs", Label: "Docs", Key: "docs"}, Ops: []connectors.CatalogOp{{Key: "read"}, {Key: "delete", Destructive: true}}},
		{Row: entity.Connector{ID: "chat", Label: "Chat", Key: "chat"}, Ops: []connectors.CatalogOp{{Key: "post", Destructive: true}}},
		{Row: entity.Connector{ID: "music", Label: "Music", Key: "music"}, Ops: []connectors.CatalogOp{{Key: "play"}}},
	}
}

func effScope(grants []ConnectorGrant) *Scope {
	reach := Reach{"docs": {Tier: TierConnector}, "chat": {Tier: TierConnector}, "music": {Tier: TierConnector}}
	s := NewScope(grants, false, false, reach)
	s.agentID, s.ownerID = "agent-1", "user-owner"
	return s
}

func rowOf(rows []EffectiveRow, id string) EffectiveRow {
	for _, r := range rows {
		if r.ConnectorID == id {
			return r
		}
	}
	return EffectiveRow{}
}

// The preview follows the run-time order: level, own reach, then lend.
func TestEffectiveFollowsScopeAndLend(t *testing.T) {
	scope := effScope([]ConnectorGrant{
		{ConnectorID: "docs", Level: LevelRead},
		{ConnectorID: "music", Level: LevelRead, Lend: true, Accounts: []string{"acc-1"}},
	})
	accounts := func(id string) []entity.ConnectorAccount {
		if id == "music" {
			return []entity.ConnectorAccount{{ID: "acc-1", WickUserID: "user-owner"}}
		}
		return nil
	}

	owner := Effective(scope, effCatalog(), EffectiveCaller{UserID: "user-owner", Owner: true}, accounts)
	if r := rowOf(owner, "docs"); r.RunsAs != RunsAsSelf || len(r.Ops) != 1 || r.Ops[0] != "read" {
		t.Errorf("owner docs = %+v, want self with read only", r)
	}
	if r := rowOf(owner, "chat"); r.Level != LevelOff || r.RunsAs != RunsAsNone || len(r.Ops) != 0 {
		t.Errorf("ungranted chat = %+v, want off", r)
	}

	guest := Effective(scope, effCatalog(), EffectiveCaller{}, accounts)
	if r := rowOf(guest, "docs"); r.RunsAs != RunsAsNone {
		t.Errorf("guest docs = %+v, want none (not lent)", r)
	}
	r := rowOf(guest, "music")
	if r.RunsAs != RunsAsOwner || !r.Lent || r.Account != "acc-1" || r.AccountStatus != LentAccountConnected {
		t.Errorf("guest music = %+v, want lent as owner on a connected account", r)
	}

	mate := Effective(scope, effCatalog(), EffectiveCaller{UserID: "user-b", Reaches: func(id string) bool { return id == "music" }}, accounts)
	if r := rowOf(mate, "music"); r.RunsAs != RunsAsSelf {
		t.Errorf("a teammate who reaches music = %+v, want their own access", r)
	}
}

// The lent account's status says whether the lend will work.
func TestEffectiveLentAccountStatus(t *testing.T) {
	scope := effScope([]ConnectorGrant{{ConnectorID: "music", Level: LevelRead, Lend: true, Accounts: []string{"acc-1"}}})
	cases := map[string][]entity.ConnectorAccount{
		LentAccountOther:   {{ID: "acc-1", WickUserID: "user-b"}},
		LentAccountMissing: nil,
	}
	for want, accs := range cases {
		rows := Effective(scope, effCatalog(), EffectiveCaller{}, func(string) []entity.ConnectorAccount { return accs })
		if r := rowOf(rows, "music"); r.AccountStatus != want {
			t.Errorf("status = %q, want %q", r.AccountStatus, want)
		}
	}
	// A lend without a named account has no account status.
	scope = effScope([]ConnectorGrant{{ConnectorID: "music", Level: LevelRead, Lend: true}})
	if r := rowOf(Effective(scope, effCatalog(), EffectiveCaller{}, nil), "music"); r.AccountStatus != LentAccountNone || r.RunsAs != RunsAsOwner {
		t.Errorf("unnamed lend = %+v", r)
	}
}
