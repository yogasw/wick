package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// stubForget records the cwds purgeProject asks to forget instead of
// touching the provider config dirs of the machine running the test.
func stubForget(t *testing.T) *[]string {
	t.Helper()
	var got []string
	prev := forgetWorkspaceFn
	forgetWorkspaceFn = func(_ context.Context, cwd string) { got = append(got, cwd) }
	t.Cleanup(func() { forgetWorkspaceFn = prev })
	return &got
}

func mkProjectSession(t *testing.T, id, projectID, parent string) {
	t.Helper()
	if _, err := globalMgr.CreateSession(context.Background(), session.CreateOptions{
		ID: id, ProjectID: projectID, ParentSessionID: parent, Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeProjectManagedForgetsWorkspace(t *testing.T) {
	withTeamWorld(t)
	forgot := stubForget(t)
	seedTeamProject(t, "p1", "u1")
	mkProjectSession(t, "s1", "p1", "")
	mkProjectSession(t, "s1-sub", "", "s1")

	p, _ := globalMgr.Registry().Project("p1")
	if got := previewProjectDelete(p); got.Chats != 2 || got.Protected {
		t.Fatalf("preview = %+v, want 2 chats, not protected", got)
	}
	if err := purgeProject(context.Background(), p, "user"); err != nil {
		t.Fatal(err)
	}
	if n := len(globalMgr.Registry().SessionIDs()); n != 0 {
		t.Fatalf("%d sessions survived", n)
	}
	want := globalLayout.ProjectManagedPath("p1")
	if !slices.Equal(*forgot, []string{want}) {
		t.Fatalf("forgot %v, want [%s]", *forgot, want)
	}
}

func TestPurgeProjectCustomPathKeepsFolderAndMemory(t *testing.T) {
	withTeamWorld(t)
	forgot := stubForget(t)
	custom := t.TempDir()
	keep := filepath.Join(custom, "notes.md")
	_ = os.WriteFile(keep, []byte("x"), 0o600)
	if _, err := globalMgr.CreateProject(context.Background(), project.CreateOptions{ID: "pc", Name: "c", CustomPath: custom}); err != nil {
		t.Fatal(err)
	}
	mkProjectSession(t, "sc", "pc", "")
	p, _ := globalMgr.Registry().Project("pc")
	if err := purgeProject(context.Background(), p, "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("custom folder touched: %v", err)
	}
	if len(*forgot) != 0 {
		t.Fatalf("custom path memory forgotten: %v", *forgot)
	}
	if _, ok := globalMgr.Registry().Session("sc"); ok {
		t.Fatal("session survived")
	}
}

func TestDeleteProjectProtectedRefused(t *testing.T) {
	withTeamWorld(t)
	stubForget(t)
	if _, err := globalMgr.CreateProject(context.Background(), project.CreateOptions{ID: "pp", Name: "me", Tags: []string{project.PersonalTag}}); err != nil {
		t.Fatal(err)
	}
	p, _ := globalMgr.Registry().Project("pp")
	if err := purgeProject(context.Background(), p, "user"); err == nil {
		t.Fatal("protected project purged")
	}
	w, c := teamReq(t, &entity.User{ID: "u1"}, http.MethodDelete, "/projects/pp", nil, map[string]string{"id": "pp"})
	deleteProject(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("DELETE status %d, want 403", w.Code)
	}
	if _, ok := globalMgr.Registry().Project("pp"); !ok {
		t.Fatal("protected project gone")
	}
}

func seedAgentProject(t *testing.T, id string) {
	t.Helper()
	if _, err := globalMgr.CreateProject(context.Background(), project.CreateOptions{
		ID: id, Name: id, OwnerUserID: "u1", Tags: []string{project.AgentTag},
	}); err != nil {
		t.Fatal(err)
	}
	mkProjectSession(t, id+"-chat", id, "")
}

func TestTeamAgentDeleteChatsDeletePurgesProject(t *testing.T) {
	withTeamWorld(t)
	stubForget(t)
	seedAgentProject(t, "pa")
	a := seedTeamAgent(t, "u1", "worker", "pa")
	w, c := teamReq(t, &entity.User{ID: "u1"}, http.MethodDelete, "/api/team/agents/"+a.ID+"?chats=delete", nil, map[string]string{"id": a.ID})
	apiTeamAgentDelete(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if _, ok := globalMgr.Registry().Project("pa"); ok {
		t.Fatal("agent project survived chats=delete")
	}
	if _, ok := globalMgr.Registry().Session("pa-chat"); ok {
		t.Fatal("agent chat survived chats=delete")
	}
}

func TestTeamAgentDeleteChatsKeepUntagsProject(t *testing.T) {
	withTeamWorld(t)
	stubForget(t)
	seedAgentProject(t, "pk")
	a := seedTeamAgent(t, "u1", "worker", "pk")
	w, c := teamReq(t, &entity.User{ID: "u1"}, http.MethodDelete, "/api/team/agents/"+a.ID+"?chats=keep", nil, map[string]string{"id": a.ID})
	apiTeamAgentDelete(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	p, ok := globalMgr.Registry().Project("pk")
	if !ok || slices.Contains(p.Meta.Tags, project.AgentTag) {
		t.Fatalf("kept project missing or still tagged: ok=%v tags=%v", ok, p.Meta.Tags)
	}
	if _, ok := globalMgr.Registry().Session("pk-chat"); !ok {
		t.Fatal("kept chat was deleted")
	}
}

// An agent pointed at the user's own (untagged) project never takes that
// project with it.
func TestTeamAgentDeleteNeverPurgesUntaggedProject(t *testing.T) {
	withTeamWorld(t)
	stubForget(t)
	seedTeamProject(t, "mine", "u1")
	a := seedTeamAgent(t, "u1", "worker", "mine")
	w, c := teamReq(t, &entity.User{ID: "u1"}, http.MethodDelete, "/api/team/agents/"+a.ID+"?chats=delete", nil, map[string]string{"id": a.ID})
	apiTeamAgentDelete(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if _, ok := globalMgr.Registry().Project("mine"); !ok {
		t.Fatal("untagged project deleted with the agent")
	}
}

func TestProjectDeletePreviewJSON(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "pv", "u1")
	mkProjectSession(t, "pv-1", "pv", "")
	w, c := teamReq(t, &entity.User{ID: "u1"}, http.MethodGet, "/projects/pv/delete-preview", nil, map[string]string{"id": "pv"})
	projectDeletePreviewJSON(c)
	var got projectDeletePreview
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Chats != 1 || got.Name != "pv" {
		t.Fatalf("preview %d %s (%v)", w.Code, w.Body, err)
	}
}
