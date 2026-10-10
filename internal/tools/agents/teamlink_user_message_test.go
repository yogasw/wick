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

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// B5: the message route (Reply in thread on a task that asks nothing)
// takes the thread route's checks — a read-only viewer 403, a stranger,
// an unknown task or another chat's 404 — refuses an empty or overlong
// text (400), and lets the chat's owner write on their ended task, which
// keeps its state and logs the message as theirs.
func TestTeamTaskMessageThroughTheRouter(t *testing.T) {
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
	for id, session := range map[string]string{"t-done": ownChat, "t-other": otherChat} {
		b, _ := json.Marshal(map[string]any{
			"task_id": id, "context_id": "ctx-" + id, "agent_id": "a-anton", "to_handle": "anton", "to_name": "Anton",
			"caller_agent_id": p.ID, "caller_session": session, "title": "check logs", "chat": "sess-anton",
			"state": string(a2a.TaskStateCompleted), "finished": true, "reply": "no 401s", "started_at": now, "updated_at": now,
		})
		if err := os.WriteFile(filepath.Join(dir, id+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var delivered []string
	hub := teamlink.NewHub(teamDirectory{svc: globalTeam}, poolTurns{}, teamNotifier{deliver: func(_ context.Context, s, text string) error {
		delivered = append(delivered, s+": "+text)
		return nil
	}})
	if err := hub.Persist(dir); err != nil {
		t.Fatal(err)
	}
	prev := globalTeamHub
	SetTeamHub(func() *teamlink.Hub { return hub })
	t.Cleanup(func() { globalTeamHub = prev })

	rr := &routeRecorder{routes: map[string]tool.HandlerFunc{}}
	Register(rr)
	call := func(u *entity.User, task string, body any) (int, string) {
		w, c := teamReq(t, u, http.MethodPost, "/", body, map[string]string{"id": ownChat, "task": task})
		rr.routed(t, http.MethodPost, "/api/sessions/{id}/team-tasks/{task}/message")(c)
		return w.Code, w.Body.String()
	}
	text := map[string]any{"text": "check 08:00 too"}
	if code, _ := call(bob, "t-done", text); code != http.StatusForbidden {
		t.Fatalf("read-only viewer: %d, want 403", code)
	}
	if code, _ := call(eve, "t-done", text); code != http.StatusNotFound {
		t.Fatalf("stranger: %d, want 404", code)
	}
	if code, _ := call(owner, "t-nope", text); code != http.StatusNotFound {
		t.Fatalf("unknown task: %d, want 404", code)
	}
	if code, _ := call(owner, "t-other", text); code != http.StatusNotFound {
		t.Fatalf("another chat's task: %d, want 404", code)
	}
	if code, _ := call(owner, "t-done", map[string]any{"text": "  "}); code != http.StatusBadRequest {
		t.Fatalf("empty: %d, want 400", code)
	}
	if code, _ := call(owner, "t-done", map[string]any{"text": strings.Repeat("x", teamlink.MaxUserMessage+1)}); code != http.StatusBadRequest {
		t.Fatalf("too long: %d, want 400", code)
	}
	// A body over 1 MB is cut by the reader and no longer parses.
	if code, _ := call(owner, "t-done", map[string]any{"text": strings.Repeat("x", 1<<20)}); code != http.StatusBadRequest {
		t.Fatalf("body over 1 MB: %d, want 400", code)
	}
	if len(delivered) != 0 {
		t.Fatalf("refused messages delivered: %v", delivered)
	}
	if code, body := call(owner, "t-done", text); code != http.StatusOK || !strings.Contains(body, `"state":"completed"`) {
		t.Fatalf("owner: %d %s", code, body)
	}
	if len(delivered) != 1 || !strings.HasPrefix(delivered[0], "sess-anton: ") || !strings.HasSuffix(delivered[0], "> check 08:00 too") {
		t.Fatalf("delivered = %v", delivered)
	}
	log, err := hub.TaskThread(ownChat, "t-done")
	if err != nil || len(log) == 0 || log[len(log)-1].Role != teamlink.ThreadUser || log[len(log)-1].Text != "check 08:00 too" {
		t.Fatalf("thread = %+v, %v", log, err)
	}
}
