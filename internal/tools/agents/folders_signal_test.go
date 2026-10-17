package agents

import (
	"context"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
	"github.com/yogasw/wick/internal/entity"
)

// foldersSignalled is who, among uids, receives a folders agent_changed
// in evs — read the way /stream/sessions reads it.
func foldersSignalled(t *testing.T, evs []Event, uids ...string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, ev := range evs {
		if ev.Type != evAgentChanged {
			continue
		}
		for _, uid := range uids {
			if data, ok := projectAgentSignal(ev, uid); ok {
				if data != `{"folders":true}` {
					t.Fatalf("folders signal = %s", data)
				}
				out[uid] = true
			}
		}
	}
	return out
}

// Saving folders, from the sidebar's PUT or the Captain's agents.folders,
// tells only the owner's open rosters; a read tells nobody.
func TestSavingFoldersSignalsTheOwner(t *testing.T) {
	withCaptainWorld(t)
	prev := globalBcast
	globalBcast = NewBroadcaster()
	ch, unsub := globalBcast.Subscribe("")
	t.Cleanup(func() { unsub(); globalBcast = prev })
	ctx := context.Background()

	w, c := teamReq(t, &entity.User{ID: "u1"}, http.MethodPut, "/api/team/folders", map[string]any{
		"folders": []team.Folder{{ID: "f-w", Name: "Work"}},
		"place":   map[string]string{},
	}, nil)
	apiTeamFoldersSave(c)
	if w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	wantSignalled(t, "PUT", foldersSignalled(t, drain(ch), "u1", "u2"), "u1")

	w, c = teamReq(t, &entity.User{ID: "u1"}, http.MethodPut, "/api/team/folders", map[string]any{"folders": []team.Folder{{ID: "", Name: ""}}}, nil)
	apiTeamFoldersSave(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid save: %d %s", w.Code, w.Body)
	}
	wantSignalled(t, "rejected PUT", foldersSignalled(t, drain(ch), "u1", "u2"))

	if _, err := (CaptainOps{}).Folders(ctx, "s-captain", teamagents.FoldersInput{Action: "list"}); err != nil {
		t.Fatal(err)
	}
	wantSignalled(t, "list", foldersSignalled(t, drain(ch), "u1", "u2"))

	if _, err := (CaptainOps{}).Folders(ctx, "s-captain", teamagents.FoldersInput{Action: "move", Agent: "@worker", Folder: "Ops"}); err != nil {
		t.Fatal(err)
	}
	wantSignalled(t, "move", foldersSignalled(t, drain(ch), "u1", "u2"), "u1")
}
