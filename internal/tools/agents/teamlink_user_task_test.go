package agents

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// seedTask is one settled task on disk: the chat that sent it and its state.
type seedTask struct {
	session string
	state   a2a.TaskState
}

// seedUserTasks wires a Hub whose disk holds the given tasks (by id), all
// sent by agentID, and hands it to the handlers.
func seedUserTasks(t *testing.T, agentID string, tasks map[string]seedTask) *teamlink.Hub {
	t.Helper()
	dir := t.TempDir()
	now := time.Now()
	for id, st := range tasks {
		rec := map[string]any{
			"task_id": id, "context_id": "ctx-" + id, "agent_id": "a-anton", "to_handle": "anton", "to_name": "Anton",
			"caller_agent_id": agentID, "caller_session": st.session, "title": "deploy it",
			"state": string(st.state), "finished": true, "reply": "which environment?",
			"started_at": now, "updated_at": now,
		}
		b, _ := json.Marshal(rec)
		if err := os.WriteFile(filepath.Join(dir, id+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hub := teamlink.NewHub(teamDirectory{svc: globalTeam}, poolTurns{}, teamNotifier{})
	if err := hub.Persist(dir); err != nil {
		t.Fatal(err)
	}
	hub.CallerBusy = func(string) bool { return false }
	prev := globalTeamHub
	SetTeamHub(func() *teamlink.Hub { return hub })
	t.Cleanup(func() { globalTeamHub = prev })
	return hub
}

// The chat's owner lists, cancels and answers the tasks it sent; anyone
// else, and any other chat's task, gets 404; a settled task 409.
func TestSessionTeamTaskActions(t *testing.T) {
	withTeamWorld(t)
	owner, eve := &entity.User{ID: "u1"}, &entity.User{ID: "eve"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	w, c := teamReq(t, owner, http.MethodPost, "/", map[string]any{}, map[string]string{"id": p.ID})
	apiTeamAgentChat(c)
	var chat struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &chat)
	if w.Code != http.StatusOK || chat.SessionID == "" {
		t.Fatalf("chat: %d %s", w.Code, w.Body)
	}
	seedUserTasks(t, p.ID, map[string]seedTask{
		"t-ask":  {chat.SessionID, a2a.TaskStateInputRequired},
		"t-done": {chat.SessionID, a2a.TaskStateCompleted},
		// Another chat's task: never reachable from this one.
		"t-foreign": {"sess-other", a2a.TaskStateInputRequired},
	})

	call := func(u *entity.User, h func(c *tool.Ctx), task string, body any) (int, string) {
		w, c := teamReq(t, u, http.MethodPost, "/", body, map[string]string{"id": chat.SessionID, "task": task})
		h(c)
		return w.Code, w.Body.String()
	}
	answer := map[string]string{"text": "prod"}
	if code, body := call(eve, sessionTeamTaskAnswer, "t-ask", answer); code != http.StatusNotFound {
		t.Fatalf("answer as another user: %d %s", code, body)
	}
	if code, body := call(eve, sessionTeamTaskCancel, "t-ask", nil); code != http.StatusNotFound {
		t.Fatalf("cancel as another user: %d %s", code, body)
	}
	if code, body := call(owner, sessionTeamTaskCancel, "t-foreign", nil); code != http.StatusNotFound {
		t.Fatalf("cancel another chat's task: %d %s", code, body)
	}
	if code, body := call(owner, sessionTeamTaskAnswer, "t-done", answer); code != http.StatusConflict {
		t.Fatalf("answer a settled task: %d %s", code, body)
	}
	if code, body := call(owner, sessionTeamTaskAnswer, "t-ask", map[string]string{"text": " "}); code != http.StatusBadRequest {
		t.Fatalf("empty answer: %d %s", code, body)
	}
	if code, body := call(owner, sessionTeamTaskCancel, "t-done", nil); code != http.StatusConflict {
		t.Fatalf("cancel a settled task: %d %s", code, body)
	}
	if code, body := call(owner, sessionTeamTaskCancel, "t-ask", nil); code != http.StatusOK {
		t.Fatalf("cancel: %d %s", code, body)
	}
	if code, body := call(owner, sessionTeamTaskAnswer, "t-ask", answer); code != http.StatusConflict {
		t.Fatalf("answer after cancel: %d %s", code, body)
	}
}

// The list says which question waits for the user.
func TestSessionTeamTasksNeedsYou(t *testing.T) {
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	w, c := teamReq(t, owner, http.MethodPost, "/", map[string]any{}, map[string]string{"id": p.ID})
	apiTeamAgentChat(c)
	var chat struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &chat)
	hub := seedUserTasks(t, p.ID, map[string]seedTask{"t-ask": {chat.SessionID, a2a.TaskStateInputRequired}})
	list := func() []teamlink.TaskView {
		w, c := teamReq(t, owner, http.MethodGet, "/", nil, map[string]string{"id": chat.SessionID})
		sessionTeamTasks(c)
		var out struct {
			Tasks []teamlink.TaskView `json:"tasks"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != http.StatusOK || len(out.Tasks) != 1 {
			t.Fatalf("list: %d %s", w.Code, w.Body)
		}
		return out.Tasks
	}
	if v := list()[0]; !v.NeedsYou || v.Reply != "which environment?" {
		t.Fatalf("idle chat: %+v", v)
	}
	hub.CallerBusy = func(string) bool { return true }
	if v := list()[0]; v.NeedsYou {
		t.Fatalf("busy chat: %+v", v)
	}
}
