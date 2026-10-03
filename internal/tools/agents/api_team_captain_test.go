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
	if err := db.AutoMigrate(&entity.AgentPersona{}); err != nil {
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

// The Overview card reads the roster with ?ensure=0 and must not create a
// Captain (or its project); the Team app's plain GET still does.
func TestTeamAgentListEnsureOffIsReadOnly(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}

	w, c := teamReq(t, u, http.MethodGet, "/api/team/agents?ensure=0", nil, nil)
	apiTeamAgentList(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if rows, _ := globalTeam.List(context.Background(), u.ID); len(rows) != 0 {
		t.Fatalf("ensure=0 created %d agents", len(rows))
	}
	if n := len(globalMgr.Registry().Projects()); n != 0 {
		t.Fatalf("ensure=0 created %d projects", n)
	}

	w, c = teamReq(t, u, http.MethodGet, "/api/team/agents", nil, nil)
	apiTeamAgentList(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	rows, _ := globalTeam.List(context.Background(), u.ID)
	if len(rows) != 1 || !rows[0].IsCaptain {
		t.Fatalf("plain GET must create the Captain, got %+v", rows)
	}
}

// A first load that loses the Captain race removes the project it made
// instead of leaving it orphaned.
func TestEnsureCaptainLostRaceLeavesNoProject(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	seedTeamProject(t, "p-winner", u.ID)
	// Replays the race by hand: this load makes its project, the other
	// load's Captain lands first, this insert clashes, and the losing
	// project is discarded the way ensureCaptain does.
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
