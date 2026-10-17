package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
)

// teamFoldersWire is the GET/PUT answer as the sidebar reads it.
type teamFoldersWire struct {
	Folders   []team.Folder     `json:"folders"`
	Place     map[string]string `json:"place"`
	Inherited map[string]string `json:"inherited"`
}

func decodeTeamFolders(t *testing.T, body string) teamFoldersWire {
	t.Helper()
	var d teamFoldersWire
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return d
}

// A shared agent arrives in a folder named like its owner's, until the
// recipient files it themselves; PUT keeps only agents the caller sees.
func TestTeamFoldersAPI(t *testing.T) {
	w0 := withCaptainWorld(t)
	ctx := context.Background()
	u1, u2 := &entity.User{ID: "u1"}, &entity.User{ID: "u2"}
	if err := globalTeam.AddShare(ctx, w0.stranger.ID, "u1", "u2"); err != nil {
		t.Fatal(err)
	}

	w, c := teamReq(t, u1, http.MethodGet, "/api/team/folders", nil, nil)
	apiTeamFoldersGet(c)
	if d := decodeTeamFolders(t, w.Body.String()); w.Code != http.StatusOK || d.Folders == nil || d.Place == nil || len(d.Inherited) != 0 {
		t.Fatalf("empty: %d %s", w.Code, w.Body)
	}

	// The owner files the agent; u1 inherits the folder name.
	w, c = teamReq(t, u2, http.MethodPut, "/api/team/folders", map[string]any{
		"folders": []team.Folder{{ID: "f-p", Name: "Partners"}},
		"place":   map[string]string{w0.stranger.ID: "f-p"},
	}, nil)
	apiTeamFoldersSave(c)
	if d := decodeTeamFolders(t, w.Body.String()); w.Code != http.StatusOK || d.Place[w0.stranger.ID] != "f-p" {
		t.Fatalf("owner save: %d %s", w.Code, w.Body)
	}
	w, c = teamReq(t, u1, http.MethodGet, "/api/team/folders", nil, nil)
	apiTeamFoldersGet(c)
	if d := decodeTeamFolders(t, w.Body.String()); d.Inherited[w0.stranger.ID] != "Partners" {
		t.Fatalf("inherited missing: %s", w.Body)
	}

	// u1 files their own agent and the shared one; a stranger's id and an
	// unknown id are dropped, and the shared one no longer inherits.
	w, c = teamReq(t, u1, http.MethodPut, "/api/team/folders", map[string]any{
		"folders": []team.Folder{{ID: "f-w", Name: "Work"}},
		"place": map[string]string{
			w0.worker.ID: "f-w", w0.stranger.ID: "f-w", "ghost": "f-w",
		},
	}, nil)
	apiTeamFoldersSave(c)
	d := decodeTeamFolders(t, w.Body.String())
	if w.Code != http.StatusOK || len(d.Place) != 2 || d.Place[w0.worker.ID] != "f-w" || d.Place[w0.stranger.ID] != "f-w" {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	if len(d.Inherited) != 0 {
		t.Fatalf("a filed shared agent must not inherit: %s", w.Body)
	}

	// u2 cannot file u1's worker: it is neither theirs nor shared.
	w, c = teamReq(t, u2, http.MethodPut, "/api/team/folders", map[string]any{
		"folders": []team.Folder{{ID: "f-p", Name: "Partners"}},
		"place":   map[string]string{w0.worker.ID: "f-p"},
	}, nil)
	apiTeamFoldersSave(c)
	if d := decodeTeamFolders(t, w.Body.String()); len(d.Place) != 0 {
		t.Fatalf("stranger placement kept: %s", w.Body)
	}
	// u2's save did not touch u1's row.
	if f, _ := globalTeam.TeamFolders(ctx, "u1"); f.NameOf(w0.worker.ID) != "Work" {
		t.Fatalf("u1 row changed: %+v", f)
	}
}

func TestTeamFoldersAPIRejectsInvalid(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	w, c := teamReq(t, u, http.MethodPut, "/api/team/folders", map[string]any{
		"folders": []team.Folder{{ID: "a", Name: "Ops"}, {ID: "b", Name: "OPS"}},
	}, nil)
	apiTeamFoldersSave(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("dup names: %d %s", w.Code, w.Body)
	}
	if f, _ := globalTeam.TeamFolders(context.Background(), "u1"); len(f.Folders) != 0 {
		t.Fatalf("an invalid PUT saved: %+v", f)
	}
}
