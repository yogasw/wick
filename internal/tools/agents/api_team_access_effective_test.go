package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
)

func getEffective(t *testing.T, u *entity.User, agentID, query string) (int, []team.EffectiveRow) {
	t.Helper()
	w, c := teamReq(t, u, http.MethodGet, "/api/team/agents/"+agentID+"/access/effective"+query, nil, map[string]string{"id": agentID})
	apiTeamAgentAccessEffective(c)
	var out struct {
		Items []team.EffectiveRow `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out.Items
}

// The owner previews what a caller gets, from the stored grants; opening
// it changes nothing, and nobody else may read it.
func TestAccessEffectiveEndpoint(t *testing.T) {
	w := withCaptainWorld(t)
	ctx := context.Background()
	p, _ := globalTeam.Get(ctx, w.worker.ID)
	p.AllowedConnectors = team.EncodeGrants([]team.ConnectorGrant{{ConnectorID: "n1", Level: team.LevelRead, Lend: true}})
	if err := globalTeam.Update(ctx, &p); err != nil {
		t.Fatal(err)
	}
	before := p.AllowedConnectors
	owner := &entity.User{ID: "u1", Role: entity.RoleUser}

	code, rows := getEffective(t, owner, p.ID, "?as=owner")
	if code != http.StatusOK || len(rows) != 2 {
		t.Fatalf("owner: %d %+v", code, rows)
	}
	for _, r := range rows {
		switch r.ConnectorID {
		case "n1":
			if r.RunsAs != team.RunsAsSelf || r.Level != team.LevelRead {
				t.Errorf("owner n1 = %+v", r)
			}
		case "s1":
			if r.Level != team.LevelOff || r.RunsAs != team.RunsAsNone {
				t.Errorf("owner s1 = %+v", r)
			}
		}
	}
	_, rows = getEffective(t, owner, p.ID, "?as=guest")
	for _, r := range rows {
		if r.ConnectorID == "n1" && r.RunsAs != team.RunsAsOwner {
			t.Errorf("guest n1 = %+v, want lent as owner", r)
		}
	}
	if code, _ := getEffective(t, owner, p.ID, "?as=bogus"); code != http.StatusBadRequest {
		t.Errorf("bad as: %d", code)
	}
	if code, _ := getEffective(t, owner, p.ID, "?as=user"); code != http.StatusBadRequest {
		t.Errorf("as=user without user_id: %d", code)
	}
	if code, _ := getEffective(t, &entity.User{ID: "u2", Role: entity.RoleUser}, p.ID, "?as=owner"); code == http.StatusOK {
		t.Error("someone other than the owner read the preview")
	}
	if got, _ := globalTeam.Get(ctx, p.ID); got.AllowedConnectors != before {
		t.Error("opening the preview changed the grants")
	}
}

// The trail answers an empty list when there is nothing to show.
func TestAccessTrailEndpointEmpty(t *testing.T) {
	w := withCaptainWorld(t)
	rw, c := teamReq(t, &entity.User{ID: "u1", Role: entity.RoleUser}, http.MethodGet, "/api/team/agents/"+w.worker.ID+"/access/trail", nil, map[string]string{"id": w.worker.ID})
	apiTeamAgentAccessTrail(c)
	if rw.Code != http.StatusOK || rw.Body.String() == "" {
		t.Fatalf("trail: %d %s", rw.Code, rw.Body.String())
	}
}
