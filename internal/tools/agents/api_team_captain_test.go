package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// withTeamWorld wires the package globals the /api/team handlers read —
// a temp agents layout, its project manager and pool, and a team service
// over an in-memory DB — and restores them after the test.
func withTeamWorld(t *testing.T) {
	t.Helper()
	layout := agentconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&entity.AgentPersona{}, &entity.TeamSettings{}, &entity.AgentAccessHistory{}, &entity.AgentShare{}); err != nil {
		t.Fatal(err)
	}
	reg := registry.New(layout)
	if err := reg.Reload(); err != nil {
		t.Fatal(err)
	}
	prevMgr, prevLayout, prevPool, prevTeam := globalMgr, globalLayout, globalPool, globalTeam
	globalMgr, globalLayout = registry.NewManager(reg), layout
	globalPool = pool.New(pool.PoolConfig{Layout: layout})
	globalTeam = team.NewService(db, layout)
	t.Cleanup(func() { globalMgr, globalLayout, globalPool, globalTeam = prevMgr, prevLayout, prevPool, prevTeam })
}

// seedTeamProject creates a project owned by owner.
func seedTeamProject(t *testing.T, id, owner string) {
	t.Helper()
	if _, err := globalMgr.CreateProject(context.Background(), project.CreateOptions{ID: id, Name: id, OwnerUserID: owner}); err != nil {
		t.Fatal(err)
	}
}

// teamReq builds a request as user u with an optional JSON body.
func teamReq(t *testing.T, u *entity.User, method, target string, body any, pathVals map[string]string) (*httptest.ResponseRecorder, *tool.Ctx) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, target, &buf)
	r = r.WithContext(login.WithUser(r.Context(), u, nil))
	for k, v := range pathVals {
		r.SetPathValue(k, v)
	}
	w := httptest.NewRecorder()
	return w, tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)
}

// Listing never makes a Captain any more, with or without ?ensure=0: an
// empty roster stays empty for the Team app's empty state.
func TestTeamAgentListNeverCreatesCaptain(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	for _, target := range []string{"/api/team/agents?ensure=0", "/api/team/agents"} {
		w, c := teamReq(t, u, http.MethodGet, target, nil, nil)
		apiTeamAgentList(c)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", target, w.Code, w.Body)
		}
		var body struct {
			Agents    []TeamAgentItem `json:"agents"`
			CaptainID string          `json:"captain_id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Agents) != 0 || body.CaptainID != "" {
			t.Fatalf("%s: got %+v", target, body)
		}
	}
	if rows, _ := globalTeam.List(context.Background(), u.ID); len(rows) != 0 {
		t.Fatalf("list created %d agents", len(rows))
	}
	if n := len(globalMgr.Registry().Projects()); n != 0 {
		t.Fatalf("list created %d projects", n)
	}
}

// createAgent posts a Wick agent on its own project and returns its row.
func createAgent(t *testing.T, u *entity.User, handle string) entity.AgentPersona {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPost, "/api/team/agents", map[string]any{"handle": handle, "name": handle}, nil)
	apiTeamAgentCreate(c)
	if w.Code != http.StatusOK {
		t.Fatalf("create %s: status %d: %s", handle, w.Code, w.Body)
	}
	row, err := globalTeam.GetByHandle(context.Background(), u.ID, handle)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// The first agent becomes the Captain whatever its name; the next one
// does not.
func TestTeamFirstAgentBecomesCaptain(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	first := createAgent(t, u, "ops-lead")
	if !first.IsCaptain {
		t.Fatal("first agent must be the Captain")
	}
	if second := createAgent(t, u, "reviewer"); second.IsCaptain {
		t.Fatal("second agent must not be the Captain")
	}
	// Each owner has their own Captain.
	if other := createAgent(t, &entity.User{ID: "u2"}, "helper"); !other.IsCaptain {
		t.Fatal("another owner's first agent must be their Captain")
	}
}

// A remote agent is never the Captain, so the owner's first Wick agent
// takes the role even when a remote one came first.
func TestTeamRemoteFirstLeavesCaptainToWickAgent(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	remote := &entity.AgentPersona{OwnerUserID: u.ID, Handle: "remote-bot", Kind: "a2a-remote", AllowedConnectors: "[]"}
	if err := globalTeam.Create(context.Background(), remote); err != nil {
		t.Fatal(err)
	}
	if remote.IsCaptain {
		t.Fatal("remote agent must not be the Captain")
	}
	if wick := createAgent(t, u, "ops-lead"); !wick.IsCaptain {
		t.Fatal("first Wick agent must be the Captain")
	}
}

// makeCaptain posts Make Captain for id.
func makeCaptain(t *testing.T, u *entity.User, id string) *httptest.ResponseRecorder {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPost, "/api/team/agents/"+id+"/captain", nil, map[string]string{"id": id})
	apiTeamAgentMakeCaptain(c)
	return w
}

// Make Captain moves the role in one go: the old Captain is cleared, the
// new one set, and "Manage other agents" moves with the role.
func TestTeamMakeCaptainTransfers(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	old := createAgent(t, u, "ops-lead")
	next := createAgent(t, u, "reviewer")
	off := false
	old.ManageAgents = &off
	if err := globalTeam.Update(context.Background(), &old); err != nil {
		t.Fatal(err)
	}

	w := makeCaptain(t, u, next.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("make captain: status %d: %s", w.Code, w.Body)
	}
	var body struct {
		Agent      TeamAgentItem `json:"agent"`
		PreviousID string        `json:"previous_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Agent.IsCaptain || body.PreviousID != old.ID {
		t.Fatalf("response %+v", body)
	}
	gotOld, _ := globalTeam.Get(context.Background(), old.ID)
	gotNext, _ := globalTeam.Get(context.Background(), next.ID)
	if gotOld.IsCaptain || !gotNext.IsCaptain {
		t.Fatalf("captain not moved: old %v next %v", gotOld.IsCaptain, gotNext.IsCaptain)
	}
	if gotOld.ManageAgents != nil || gotNext.ManageAgents == nil || *gotNext.ManageAgents {
		t.Fatalf("manage_agents did not move: old %v next %v", gotOld.ManageAgents, gotNext.ManageAgents)
	}
	if team.ManagesAgents(gotOld) {
		t.Fatal("old Captain must lose Manage other agents")
	}
	// Name, handle and project stay.
	if gotNext.Handle != "reviewer" || gotNext.ProjectID != next.ProjectID {
		t.Fatalf("identity changed: %+v", gotNext)
	}
	rows, _ := globalTeam.List(context.Background(), u.ID)
	n := 0
	for _, r := range rows {
		if r.IsCaptain {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d captains after transfer", n)
	}
	// Again on the Captain is a no-op.
	if w := makeCaptain(t, u, next.ID); w.Code != http.StatusOK {
		t.Fatalf("repeat: status %d", w.Code)
	}
}

// Remote and shared agents are refused and the Captain stays put; someone
// else's agent is not found.
func TestTeamMakeCaptainRefusals(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	captain := createAgent(t, u, "ops-lead")
	remote := &entity.AgentPersona{OwnerUserID: u.ID, Handle: "remote-bot", Kind: "a2a-remote", AllowedConnectors: "[]"}
	if err := globalTeam.Create(context.Background(), remote); err != nil {
		t.Fatal(err)
	}
	shared := createAgent(t, u, "shared-one")
	if err := globalTeam.AddShare(context.Background(), shared.ID, "u2", u.ID); err != nil {
		t.Fatal(err)
	}
	if w := makeCaptain(t, u, remote.ID); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "remote agent") {
		t.Fatalf("remote: status %d: %s", w.Code, w.Body)
	}
	if w := makeCaptain(t, u, shared.ID); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "stop sharing") {
		t.Fatalf("shared: status %d: %s", w.Code, w.Body)
	}
	// The PATCH path runs the same checks.
	w, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+shared.ID, map[string]any{"is_captain": true}, map[string]string{"id": shared.ID})
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusConflict {
		t.Fatalf("patch shared: status %d: %s", w.Code, w.Body)
	}
	if w := makeCaptain(t, &entity.User{ID: "u2"}, captain.ID); w.Code != http.StatusNotFound {
		t.Fatalf("other owner: status %d", w.Code)
	}
	if got, _ := globalTeam.Get(context.Background(), captain.ID); !got.IsCaptain {
		t.Fatal("a refused transfer must leave the Captain in place")
	}
}

// PATCH is_captain:true moves the role through the same transfer.
func TestTeamPatchIsCaptainTransfers(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	old := createAgent(t, u, "ops-lead")
	next := createAgent(t, u, "reviewer")
	w, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+next.ID, map[string]any{"is_captain": true}, map[string]string{"id": next.ID})
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusOK {
		t.Fatalf("patch: status %d: %s", w.Code, w.Body)
	}
	gotOld, _ := globalTeam.Get(context.Background(), old.ID)
	gotNext, _ := globalTeam.Get(context.Background(), next.ID)
	if gotOld.IsCaptain || !gotNext.IsCaptain {
		t.Fatalf("captain not moved: old %v next %v", gotOld.IsCaptain, gotNext.IsCaptain)
	}
}

// A Captain-race leftover: a project made for an agent row that was then
// not saved is removed instead of lingering as an orphan.
func TestEnsureCaptainLostRaceLeavesNoProject(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	seedTeamProject(t, "p-winner", u.ID)
	// Replays the race by hand: this request makes its project, another
	// agent lands first on the handle, this insert clashes, and the losing
	// project is discarded.
	_, c := teamReq(t, u, http.MethodGet, "/api/team/agents", nil, nil)
	pid, err := createTeamAgentProject(c, "Captain", "", "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := globalTeam.Create(context.Background(), &entity.AgentPersona{OwnerUserID: u.ID, Handle: "captain", ProjectID: "p-winner", IsCaptain: true}); err != nil {
		t.Fatal(err)
	}
	if err := globalTeam.Create(context.Background(), &entity.AgentPersona{OwnerUserID: u.ID, Handle: "captain", ProjectID: pid}); err == nil {
		t.Fatal("second captain must clash on the handle")
	}
	discardTeamAgentProject(c, pid)
	if _, ok := globalMgr.Registry().Project(pid); ok {
		t.Fatal("losing project must be removed")
	}
	if _, ok := globalMgr.Registry().Project("p-winner"); !ok {
		t.Fatal("winner's project must stay")
	}
}
