package agents

import (
	"context"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// seedTeamAgent stores an agent of owner linked to projectID.
func seedTeamAgent(t *testing.T, owner, handle, projectID string) entity.AgentPersona {
	t.Helper()
	p := &entity.AgentPersona{OwnerUserID: owner, Handle: handle, ProjectID: projectID, AllowedConnectors: "[]"}
	if err := globalTeam.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return *p
}

// Someone else's agent answers 404 on every {id} route, as if it did not
// exist, rather than 403 that would confirm it does.
func TestTeamAgentOfAnotherOwnerIs404(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p2", "u2")
	other := seedTeamAgent(t, "u2", "theirs", "p2")
	u := &entity.User{ID: "u1"}
	id := map[string]string{"id": other.ID}

	for name, h := range map[string]func(*tool.Ctx){
		"update": apiTeamAgentUpdate,
		"delete": apiTeamAgentDelete,
		"chat":   apiTeamAgentChat,
	} {
		w, c := teamReq(t, u, http.MethodPost, "/api/team/agents/"+other.ID, map[string]any{}, id)
		h(c)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", name, w.Code)
		}
	}
	if _, err := globalTeam.Get(context.Background(), other.ID); err != nil {
		t.Fatal("the other owner's agent must survive")
	}
}

// A grant outside the caller's catalog and an unknown run_as are 400s,
// refused before any project is made for the new agent.
func TestTeamAgentCreateRejectsBadInput(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	cases := map[string]map[string]any{
		"grant outside catalog": {
			"handle":             "worker",
			"allowed_connectors": []team.ConnectorGrant{{ConnectorID: "not-mine", Level: team.LevelAll}},
		},
		"invalid run_as":      {"handle": "worker", "run_as": "root"},
		"invalid access_mode": {"handle": "worker", "access_mode": "everything"},
		"tagline too long":    {"handle": "worker", "tagline": "a tagline far longer than fifty characters, which is the cap"},
	}
	for name, body := range cases {
		w, c := teamReq(t, u, http.MethodPost, "/api/team/agents", body, nil)
		apiTeamAgentCreate(c)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, w.Code, w.Body)
		}
	}
	if n := len(globalMgr.Registry().Projects()); n != 0 {
		t.Fatalf("refused creates left %d projects behind", n)
	}
	if rows, _ := globalTeam.List(context.Background(), u.ID); len(rows) != 0 {
		t.Fatalf("refused creates stored %d agents", len(rows))
	}

	// run_as on PATCH is validated the same way.
	seedTeamProject(t, "p1", u.ID)
	mine := seedTeamAgent(t, u.ID, "mine", "p1")
	w, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+mine.ID, map[string]any{"run_as": "root"}, map[string]string{"id": mine.ID})
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("patch run_as: status %d, want 400", w.Code)
	}
}

// access_mode defaults to choose, is stored on PATCH, and a PATCH that
// does not name it keeps it.
func TestTeamAgentAccessMode(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", u.ID)
	w, c := teamReq(t, u, http.MethodPost, "/api/team/agents", map[string]any{"handle": "anton", "project_id": "p1"}, nil)
	apiTeamAgentCreate(c)
	if w.Code != http.StatusOK {
		t.Fatalf("create: status %d: %s", w.Code, w.Body)
	}
	row, err := globalTeam.GetByHandle(context.Background(), u.ID, "anton")
	if err != nil || team.NormalizeAccessMode(row.AccessMode) != team.AccessChoose {
		t.Fatalf("new agent access mode %q, err %v", row.AccessMode, err)
	}
	id := map[string]string{"id": row.ID}
	patch := func(body map[string]any) string {
		t.Helper()
		w, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+row.ID, body, id)
		apiTeamAgentUpdate(c)
		if w.Code != http.StatusOK {
			t.Fatalf("patch %v: status %d: %s", body, w.Code, w.Body)
		}
		got, _ := globalTeam.Get(context.Background(), row.ID)
		return got.AccessMode
	}
	if got := patch(map[string]any{"access_mode": "owner"}); got != team.AccessOwner {
		t.Fatalf("patched access mode %q", got)
	}
	if got := patch(map[string]any{"run_as": "owner"}); got != team.AccessOwner {
		t.Fatalf("unrelated patch changed access mode to %q", got)
	}
	w, c = teamReq(t, u, http.MethodPatch, "/api/team/agents/"+row.ID, map[string]any{"access_mode": "root"}, id)
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("patch bad access_mode: status %d, want 400", w.Code)
	}
}

// The tagline is stored on the row: set on create, kept by a PATCH that
// does not name it, changed or cleared by one that does.
func TestTeamAgentTagline(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", u.ID)
	w, c := teamReq(t, u, http.MethodPost, "/api/team/agents", map[string]any{"handle": "anton", "project_id": "p1", "tagline": "  The   Critic "}, nil)
	apiTeamAgentCreate(c)
	if w.Code != http.StatusOK {
		t.Fatalf("create: status %d: %s", w.Code, w.Body)
	}
	row, err := globalTeam.GetByHandle(context.Background(), u.ID, "anton")
	if err != nil || row.Tagline != "The Critic" {
		t.Fatalf("stored tagline %q, err %v", row.Tagline, err)
	}
	id := map[string]string{"id": row.ID}
	patch := func(body map[string]any) string {
		t.Helper()
		w, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+row.ID, body, id)
		apiTeamAgentUpdate(c)
		if w.Code != http.StatusOK {
			t.Fatalf("patch %v: status %d: %s", body, w.Code, w.Body)
		}
		got, _ := globalTeam.Get(context.Background(), row.ID)
		return got.Tagline
	}
	if got := patch(map[string]any{"run_as": "owner"}); got != "The Critic" {
		t.Fatalf("unrelated patch changed tagline to %q", got)
	}
	if got := patch(map[string]any{"tagline": "Si Kritikus"}); got != "Si Kritikus" {
		t.Fatalf("patched tagline %q", got)
	}
	if got := patch(map[string]any{"tagline": ""}); got != "" {
		t.Fatalf("cleared tagline %q", got)
	}
}

// A project someone else owns reads as shared; the caller's own and an
// ownerless (protected) one do not.
func TestSharedProjectOwners(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "mine", "u1")
	seedTeamProject(t, "theirs", "u2")
	seedTeamProject(t, "nobody", "")
	_, c := teamReq(t, &entity.User{ID: "u1"}, http.MethodGet, "/projects/options", nil, nil)
	got := sharedProjectOwners(c, globalMgr.Registry().Projects(), []string{"mine", "theirs", "nobody"})
	if len(got) != 1 || got["theirs"] == "" {
		t.Fatalf("shared = %v, want only theirs", got)
	}
}

// "Make this an agent…": the owner's ordinary project becomes the new
// agent's own (Team tag added); a second convert, someone else's project
// and a protected one are refused.
func TestTeamAgentConvertProject(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", u.ID)
	create := func(handle, pid string) int {
		t.Helper()
		w, c := teamReq(t, u, http.MethodPost, "/api/team/agents", map[string]any{"handle": handle, "project_id": pid, "convert": true, "tagline": "Ops"}, nil)
		apiTeamAgentCreate(c)
		return w.Code
	}
	mkProjectSession(t, "old-chat", "p1", "")
	if code := create("conv", "p1"); code != http.StatusOK {
		t.Fatalf("convert: status %d", code)
	}
	agent, _ := globalTeam.GetByHandle(context.Background(), u.ID, "conv")
	if !agent.UseGlobalPrompt {
		t.Error("a converted agent must default to the global system prompt")
	}
	if s, _ := globalMgr.Registry().Session("old-chat"); s.Meta.AgentID != agent.ID || s.Meta.ProjectID != "p1" {
		t.Errorf("existing session not stamped: agent %q project %q", s.Meta.AgentID, s.Meta.ProjectID)
	}
	// A channel thread created after the convert is the agent's too.
	prev := session.ProjectAgent
	session.ProjectAgent = func(pid string) string { return globalTeam.AgentOfProject(context.Background(), pid) }
	t.Cleanup(func() { session.ProjectAgent = prev })
	if s, err := globalMgr.CreateSession(context.Background(), session.CreateOptions{ID: "slack-new", ProjectID: "p1", Origin: session.OriginSlack}); err != nil || s.Meta.AgentID != agent.ID {
		t.Errorf("new channel session: agent %q, err %v", s.Meta.AgentID, err)
	}
	if p, _ := globalMgr.Registry().Project("p1"); !project.IsAgentProject(p.Meta) {
		t.Fatalf("project not tagged: %v", p.Meta.Tags)
	}
	if code := create("conv2", "p1"); code != http.StatusConflict {
		t.Fatalf("second convert: status %d, want 409", code)
	}
	if code := create("conv3", ""); code != http.StatusBadRequest {
		t.Fatalf("convert without project: status %d, want 400", code)
	}
	if _, err := globalMgr.CreateProject(context.Background(), project.CreateOptions{ID: "home", Name: "home", OwnerUserID: u.ID, Tags: []string{project.PersonalTag}}); err != nil {
		t.Fatal(err)
	}
	if code := create("conv4", "home"); code != http.StatusBadRequest {
		t.Fatalf("protected: status %d, want 400", code)
	}
}

// show_in_projects is stored as project.VisibleTag on the agent's own
// project: on create (new project and convert), and on PATCH. Without it a
// new agent's project stays hidden, as before.
func TestTeamAgentShowInProjects(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	create := func(body map[string]any) TeamAgentItem {
		t.Helper()
		w, c := teamReq(t, u, http.MethodPost, "/api/team/agents", body, nil)
		apiTeamAgentCreate(c)
		if w.Code != http.StatusOK {
			t.Fatalf("create %v: status %d: %s", body, w.Code, w.Body)
		}
		row, err := globalTeam.GetByHandle(context.Background(), u.ID, body["handle"].(string))
		if err != nil {
			t.Fatal(err)
		}
		return teamAgentToItem(row, teamProjectUsers{}, teamLive{}, nil)
	}
	shows := func(pid string) bool {
		t.Helper()
		p, _ := globalMgr.Registry().Project(pid)
		return project.ShowsInProjects(p.Meta)
	}

	shown := create(map[string]any{"handle": "shown", "show_in_projects": true})
	if !shown.OwnProject || !shown.ShowInProjects || !shows(shown.ProjectID) {
		t.Fatalf("new agent with show_in_projects: own %v show %v", shown.OwnProject, shown.ShowInProjects)
	}
	hidden := create(map[string]any{"handle": "hidden"})
	if !hidden.OwnProject || hidden.ShowInProjects || shows(hidden.ProjectID) {
		t.Fatalf("new agent without the flag must stay hidden")
	}

	seedTeamProject(t, "p1", u.ID)
	conv := create(map[string]any{"handle": "conv", "project_id": "p1", "convert": true, "show_in_projects": true})
	if p, _ := globalMgr.Registry().Project("p1"); !project.IsAgentProject(p.Meta) || !shows("p1") || !conv.ShowInProjects {
		t.Fatalf("converted project tags %v", p.Meta.Tags)
	}

	// A project the agent was merely pointed at always shows; the flag is
	// not stamped on it.
	seedTeamProject(t, "p2", u.ID)
	ptd := create(map[string]any{"handle": "pointed", "project_id": "p2", "show_in_projects": false})
	if p, _ := globalMgr.Registry().Project("p2"); ptd.OwnProject || !ptd.ShowInProjects || len(p.Meta.Tags) != 0 {
		t.Fatalf("pointed project: own %v show %v tags %v", ptd.OwnProject, ptd.ShowInProjects, p.Meta.Tags)
	}

	patch := func(id string, body map[string]any) TeamAgentItem {
		t.Helper()
		w, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+id, body, map[string]string{"id": id})
		apiTeamAgentUpdate(c)
		if w.Code != http.StatusOK {
			t.Fatalf("patch %v: status %d: %s", body, w.Code, w.Body)
		}
		return teamAgentToItem(mustAgent(t, id), teamProjectUsers{}, teamLive{}, nil)
	}
	if it := patch(hidden.ID, map[string]any{"show_in_projects": true}); !it.ShowInProjects || !shows(hidden.ProjectID) {
		t.Fatal("patch on: project still hidden")
	}
	if it := patch(hidden.ID, map[string]any{"tagline": "Ops"}); !it.ShowInProjects {
		t.Fatal("an unrelated patch dropped the flag")
	}
	if it := patch(hidden.ID, map[string]any{"show_in_projects": false}); it.ShowInProjects || shows(hidden.ProjectID) {
		t.Fatal("patch off: project still shown")
	}
}

func mustAgent(t *testing.T, id string) entity.AgentPersona {
	t.Helper()
	row, err := globalTeam.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return row
}
