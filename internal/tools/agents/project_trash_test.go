package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/trash"
	"github.com/yogasw/wick/internal/entity"
)

// stubHistory makes purgeProject find one fake provider history folder
// for the managed cwd and returns its path.
func stubHistory(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cfg", "projects", "-acme-files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "chat.jsonl"), []byte("{}"), 0o600)
	prev := workspaceHistoryFn
	workspaceHistoryFn = func(context.Context, string) map[string][]string {
		return map[string][]string{"claude": {dir}}
	}
	t.Cleanup(func() { workspaceHistoryFn = prev })
	return dir
}

func onlyTrashEntry(t *testing.T) trash.Manifest {
	t.Helper()
	all, err := trash.List(globalLayout.BaseDir)
	if err != nil || len(all) != 1 {
		t.Fatalf("trash = %v (%v), want one entry", all, err)
	}
	return all[0]
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestDeleteProjectMovesEverythingToTrash(t *testing.T) {
	withTeamWorld(t)
	hist := stubHistory(t)
	seedTeamProject(t, "acme", "user-a")
	mkProjectSession(t, "s1", "acme", "")
	mkProjectSession(t, "s1"+"--sub-x", "", "s1")
	mkProjectSession(t, "s2", "acme", "")
	p, _ := globalMgr.Registry().Project("acme")
	if err := purgeProject(context.Background(), p, "user"); err != nil {
		t.Fatal(err)
	}
	if _, ok := globalMgr.Registry().Project("acme"); ok {
		t.Fatal("registry still lists the project")
	}
	if n := len(globalMgr.Registry().SessionIDs()); n != 0 {
		t.Fatalf("registry still lists %d sessions", n)
	}
	for _, gone := range []string{globalLayout.ProjectDir("acme"), globalLayout.SessionDir("s1"), globalLayout.SessionDir("s2"), hist} {
		if exists(gone) {
			t.Fatalf("%s still in place", gone)
		}
	}
	m := onlyTrashEntry(t)
	dir := filepath.Join(trash.Root(globalLayout.BaseDir), m.Key)
	for _, kept := range []string{"project/meta.json", "sessions/s1/subagents/x", "sessions/s2", m.Providers[0].Stored + "/chat.jsonl"} {
		if !exists(filepath.Join(dir, kept)) {
			t.Fatalf("trash lacks %s", kept)
		}
	}
	if m.ID != "acme" || m.OwnerUserID != "user-a" || len(m.SessionIDs) != 3 || m.Providers[0].Original != hist {
		t.Fatalf("manifest %+v", m)
	}
}

func TestRestoreProjectBringsItBack(t *testing.T) {
	withTeamWorld(t)
	hist := stubHistory(t)
	seedTeamProject(t, "acme", "user-a")
	mkProjectSession(t, "s1", "acme", "")
	p, _ := globalMgr.Registry().Project("acme")
	if err := purgeProject(context.Background(), p, "user"); err != nil {
		t.Fatal(err)
	}
	m := onlyTrashEntry(t)

	// Another user may neither see nor restore it.
	w, c := teamReq(t, &entity.User{ID: "user-b"}, http.MethodPost, "/projects-trash/"+m.Key+"/restore", nil, map[string]string{"key": m.Key})
	projectTrashRestore(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("stranger restore status %d", w.Code)
	}

	w, c = teamReq(t, &entity.User{ID: "user-a"}, http.MethodPost, "/projects-trash/"+m.Key+"/restore", nil, map[string]string{"key": m.Key})
	projectTrashRestore(c)
	if w.Code != http.StatusOK {
		t.Fatalf("restore status %d: %s", w.Code, w.Body)
	}
	var res trashRestoreResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Sessions != 1 {
		t.Fatalf("result %s (%v)", w.Body, err)
	}
	if _, ok := globalMgr.Registry().Project("acme"); !ok {
		t.Fatal("registry does not see the restored project")
	}
	if s, ok := globalMgr.Registry().Session("s1"); !ok || s.Meta.ProjectID != "acme" {
		t.Fatal("registry does not see the restored session")
	}
	if !exists(filepath.Join(hist, "chat.jsonl")) {
		t.Fatal("provider history not moved back")
	}
	if all, _ := trash.List(globalLayout.BaseDir); len(all) != 0 {
		t.Fatalf("trash entry left behind: %v", all)
	}
}

func TestRestoreProjectRefusesIDCollision(t *testing.T) {
	withTeamWorld(t)
	stubForget(t)
	seedTeamProject(t, "acme", "user-a")
	p, _ := globalMgr.Registry().Project("acme")
	if err := purgeProject(context.Background(), p, "user"); err != nil {
		t.Fatal(err)
	}
	m := onlyTrashEntry(t)
	seedTeamProject(t, "acme", "user-a")
	w, c := teamReq(t, &entity.User{ID: "user-a"}, http.MethodPost, "/projects-trash/"+m.Key+"/restore", nil, map[string]string{"key": m.Key})
	projectTrashRestore(c)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", w.Code, w.Body)
	}
	if _, err := trash.Load(globalLayout.BaseDir, m.Key); err != nil {
		t.Fatalf("refused restore touched the entry: %v", err)
	}
}

func TestDeleteCustomPathProjectNeverMovesItsFolder(t *testing.T) {
	withTeamWorld(t)
	forgot := stubForget(t)
	custom := t.TempDir()
	keep := filepath.Join(custom, "notes.md")
	_ = os.WriteFile(keep, []byte("x"), 0o600)
	if _, err := globalMgr.CreateProject(context.Background(), project.CreateOptions{ID: "acme", Name: "acme", CustomPath: custom, OwnerUserID: "user-a"}); err != nil {
		t.Fatal(err)
	}
	p, _ := globalMgr.Registry().Project("acme")
	if err := purgeProject(context.Background(), p, "user"); err != nil {
		t.Fatal(err)
	}
	if !exists(keep) || len(*forgot) != 0 {
		t.Fatalf("custom folder moved (%v) or its history looked up (%v)", exists(keep), *forgot)
	}
	m := onlyTrashEntry(t)
	if m.CustomPath != custom || len(m.Providers) != 0 {
		t.Fatalf("manifest %+v", m)
	}
}

func TestProjectTrashListScoped(t *testing.T) {
	withTeamWorld(t)
	stubForget(t)
	seedTeamProject(t, "acme", "user-a")
	p, _ := globalMgr.Registry().Project("acme")
	if err := purgeProject(context.Background(), p, "user"); err != nil {
		t.Fatal(err)
	}
	list := func(u *entity.User) []trashItem {
		w, c := teamReq(t, u, http.MethodGet, "/projects-trash", nil, nil)
		projectTrashJSON(c)
		var out struct{ Items []trashItem }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out.Items
	}
	if got := list(&entity.User{ID: "user-a"}); len(got) != 1 || got[0].DaysLeft != 29 && got[0].DaysLeft != 30 {
		t.Fatalf("owner list %+v", got)
	}
	if got := list(&entity.User{ID: "user-b"}); len(got) != 0 {
		t.Fatalf("stranger sees %+v", got)
	}
}

func TestSweepProjectTrashPurgesOnlyPastRetention(t *testing.T) {
	withTeamWorld(t)
	stubForget(t)
	seedTeamProject(t, "acme", "user-a")
	p, _ := globalMgr.Registry().Project("acme")
	if err := purgeProject(context.Background(), p, "user"); err != nil {
		t.Fatal(err)
	}
	if gone := sweepProjectTrash(context.Background(), time.Now().Add(trash.Retention-time.Hour)); len(gone) != 0 {
		t.Fatalf("purged inside the window: %v", gone)
	}
	if gone := sweepProjectTrash(context.Background(), time.Now().Add(trash.Retention+time.Hour)); len(gone) != 1 {
		t.Fatalf("purged %v past the window, want 1", gone)
	}
	if all, _ := trash.List(globalLayout.BaseDir); len(all) != 0 {
		t.Fatalf("trash still holds %v", all)
	}
}

// The retention sweep is the only hard delete of a trash entry: no route
// or other caller may purge one. A new trash.Purge call outside the sweep
// fails here and needs the owner's say-so first.
func TestOnlyTheSweepPurgesTrash(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		if strings.Contains(path, filepath.Join("agents", "trash")+string(filepath.Separator)) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		if strings.Contains(src, "trash.Purge(") {
			t.Errorf("%s calls trash.Purge: only the retention sweep may purge", path)
		}
		if strings.Contains(src, "trash.PurgeOlderThan(") && filepath.Base(path) != "project_trash.go" {
			t.Errorf("%s calls trash.PurgeOlderThan outside the sweep", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
