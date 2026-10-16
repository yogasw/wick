package agents

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// B6: the retry and reassign routes take the thread route's checks — a
// read-only viewer 403, a stranger, an unknown task or another chat's
// 404 — refuse a task in the wrong state (409), and reassign needs a
// teammate (400), another one than the task's (400), that the chat
// reaches (404).
func TestTeamTaskRetryReassignThroughTheRouter(t *testing.T) {
	withTeamWorld(t)
	owner, bob, eve := &entity.User{ID: "u1"}, &entity.User{ID: "bob"}, &entity.User{ID: "eve"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	ownChat := openChat(t, owner, p.ID, false)
	if code := shareWith(t, owner, p.ID, bob.ID); code != http.StatusOK {
		t.Fatalf("share: %d", code)
	}
	otherChat := openChat(t, owner, p.ID, true)

	dir := t.TempDir()
	now := time.Now()
	for id, task := range map[string][2]string{
		"t-failed": {ownChat, string(a2a.TaskStateFailed)}, "t-declined": {ownChat, string(a2a.TaskStateRejected)},
		"t-done": {ownChat, string(a2a.TaskStateCompleted)}, "t-other": {otherChat, string(a2a.TaskStateFailed)},
	} {
		b, _ := json.Marshal(map[string]any{
			"task_id": id, "context_id": "ctx-" + id, "agent_id": "a-anton", "to_handle": "anton", "to_name": "Anton",
			"caller_agent_id": p.ID, "caller_session": task[0], "title": "check logs", "brief": "check logs",
			"state": task[1], "finished": true, "reply": "no access", "started_at": now, "updated_at": now,
		})
		if err := os.WriteFile(filepath.Join(dir, id+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hub := teamlink.NewHub(teamDirectory{svc: globalTeam}, poolTurns{}, teamNotifier{})
	if err := hub.Persist(dir); err != nil {
		t.Fatal(err)
	}
	prev := globalTeamHub
	SetTeamHub(func() *teamlink.Hub { return hub })
	t.Cleanup(func() { globalTeamHub = prev })

	rr := &routeRecorder{routes: map[string]tool.HandlerFunc{}}
	Register(rr)
	call := func(route string, u *entity.User, task string, body any) (int, string) {
		w, c := teamReq(t, u, http.MethodPost, "/", body, map[string]string{"id": ownChat, "task": task})
		rr.routed(t, http.MethodPost, "/api/sessions/{id}/team-tasks/{task}/"+route)(c)
		return w.Code, w.Body.String()
	}
	to := map[string]any{"to": "vera"}
	for _, route := range []string{"retry", "reassign"} {
		if code, _ := call(route, bob, "t-failed", to); code != http.StatusForbidden {
			t.Fatalf("%s read-only viewer: %d, want 403", route, code)
		}
		if code, _ := call(route, eve, "t-failed", to); code != http.StatusNotFound {
			t.Fatalf("%s stranger: %d, want 404", route, code)
		}
		if code, _ := call(route, owner, "t-nope", to); code != http.StatusNotFound {
			t.Fatalf("%s unknown task: %d, want 404", route, code)
		}
		if code, _ := call(route, owner, "t-other", to); code != http.StatusNotFound {
			t.Fatalf("%s another chat's task: %d, want 404", route, code)
		}
		if code, _ := call(route, owner, "t-done", to); code != http.StatusConflict {
			t.Fatalf("%s completed task: %d, want 409", route, code)
		}
	}
	if code, _ := call("retry", owner, "t-declined", nil); code != http.StatusConflict {
		t.Fatalf("retry of a declined task: %d, want 409", code)
	}
	if code, _ := call("reassign", owner, "t-declined", map[string]any{"to": " "}); code != http.StatusBadRequest {
		t.Fatalf("reassign without to: %d, want 400", code)
	}
	if code, _ := call("reassign", owner, "t-declined", map[string]any{"to": strings.Repeat("x", 1<<20)}); code != http.StatusBadRequest {
		t.Fatalf("body over 1 MB: %d, want 400", code)
	}
	if code, _ := call("reassign", owner, "t-declined", map[string]any{"to": "@anton"}); code != http.StatusBadRequest {
		t.Fatalf("reassign to the same teammate: %d, want 400", code)
	}
	if code, body := call("reassign", owner, "t-declined", map[string]any{"to": "nobody"}); code != http.StatusNotFound || !strings.Contains(body, "no such teammate") {
		t.Fatalf("reassign to a stranger: %d %s, want 404", code, body)
	}
	// The menu's list takes the same checks, and never offers the task's own teammate.
	get := func(u *entity.User, task string) (int, string) {
		w, c := teamReq(t, u, http.MethodGet, "/", nil, map[string]string{"id": ownChat, "task": task})
		rr.routed(t, http.MethodGet, "/api/sessions/{id}/team-tasks/{task}/reassign-targets")(c)
		return w.Code, w.Body.String()
	}
	if code, _ := get(bob, "t-declined"); code != http.StatusForbidden {
		t.Fatalf("targets read-only viewer: %d, want 403", code)
	}
	for _, task := range []string{"t-nope", "t-other"} {
		if code, _ := get(owner, task); code != http.StatusNotFound {
			t.Fatalf("targets of %s: %d, want 404", task, code)
		}
	}
	if code, body := get(owner, "t-declined"); code != http.StatusOK || !strings.Contains(body, `"targets":[`) || strings.Contains(body, `"handle":"anton"`) {
		t.Fatalf("targets: %d %s", code, body)
	}
}
