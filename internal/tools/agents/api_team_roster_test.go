package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// stubAgentCatalog serves cat for every catalog read and returns how many
// reads there were.
func stubAgentCatalog(t *testing.T, cat []connectors.CatalogEntry) *int {
	t.Helper()
	n := 0
	prev := readAgentCatalog
	readAgentCatalog = func(context.Context, string, []string, bool) ([]connectors.CatalogEntry, error) {
		n++
		return cat, nil
	}
	t.Cleanup(func() { readAgentCatalog = prev })
	return &n
}

// seedOldSwitchAgent stores an agent of u1 with Notes switched off the old
// way: a feature flag, no grant.
func seedOldSwitchAgent(t *testing.T) entity.AgentPersona {
	t.Helper()
	seedTeamProject(t, "p1", "u1")
	p := &entity.AgentPersona{OwnerUserID: "u1", Handle: "worker", ProjectID: "p1",
		Features: `{"notes":false,"tickets":true,"source":true,"subagents":true,"schedule":true}`, AllowedConnectors: "[]"}
	if err := globalTeam.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return *p
}

var notesCatalog = []connectors.CatalogEntry{
	{Row: entity.Connector{ID: "n1", Key: "notes", Label: "Notes"}, Ops: []connectors.CatalogOp{{Key: "list"}}},
}

// The roster never reads the connector catalog and never writes: the
// old switches stay as stored until Settings opens or a save.
func TestTeamAgentListSkipsCatalog(t *testing.T) {
	withTeamWorld(t)
	reads := stubAgentCatalog(t, notesCatalog)
	p := seedOldSwitchAgent(t)

	w, c := teamReq(t, &entity.User{ID: "u1"}, http.MethodGet, "/api/team/agents?ensure=0", nil, nil)
	apiTeamAgentList(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if *reads != 0 {
		t.Fatalf("roster read the catalog %d times, want 0", *reads)
	}
	var out struct {
		Agents []TeamAgentItem `json:"agents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Agents) != 1 {
		t.Fatalf("agents %+v err %v", out.Agents, err)
	}
	if out.Agents[0].Features.Notes {
		t.Fatal("roster must show the stored Notes switch (off)")
	}
	got, _ := globalTeam.Get(context.Background(), p.ID)
	if got.Features != p.Features || got.AllowedConnectors != p.AllowedConnectors {
		t.Fatalf("roster GET rewrote the agent: features %s grants %s", got.Features, got.AllowedConnectors)
	}
}

// GET /api/team/agents/{id} resolves Features against the catalog and
// saves the migrated access; another owner's agent is a 404.
func TestTeamAgentGetMigratesAccess(t *testing.T) {
	withTeamWorld(t)
	reads := stubAgentCatalog(t, notesCatalog)
	p := seedOldSwitchAgent(t)
	u := &entity.User{ID: "u1"}

	w, c := teamReq(t, u, http.MethodGet, "/api/team/agents/"+p.ID, nil, map[string]string{"id": p.ID})
	apiTeamAgentGet(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if *reads != 1 {
		t.Fatalf("catalog reads %d, want 1", *reads)
	}
	var it TeamAgentItem
	if err := json.Unmarshal(w.Body.Bytes(), &it); err != nil {
		t.Fatal(err)
	}
	if it.Features.Notes {
		t.Fatal("Notes must stay off: its connector is now an off grant")
	}
	if len(it.AllowedConnectors) != 1 || it.AllowedConnectors[0].ConnectorID != "n1" || it.AllowedConnectors[0].Level != team.LevelOff {
		t.Fatalf("grants %+v, want n1 off", it.AllowedConnectors)
	}
	got, _ := globalTeam.Get(context.Background(), p.ID)
	if !team.DecodeFeatures(got.Features).Notes || len(team.DecodeGrants(got.AllowedConnectors)) != 1 {
		t.Fatalf("migration not saved: features %s grants %s", got.Features, got.AllowedConnectors)
	}

	w, c = teamReq(t, &entity.User{ID: "u2"}, http.MethodGet, "/api/team/agents/"+p.ID, nil, map[string]string{"id": p.ID})
	apiTeamAgentGet(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("another owner: status %d, want 404", w.Code)
	}
}
