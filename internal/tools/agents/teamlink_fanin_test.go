package agents

import (
	"encoding/json"
	"net/http"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
)

// T16: "Summarize now" flushes the chat's open batches; only the chat's
// owner may.
func TestSessionTeamTasksSummarize(t *testing.T) {
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
	hub := seedUserTasks(t, p.ID, map[string]seedTask{})
	hub.BatchResults = func() bool { return true }
	w, c = teamReq(t, eve, http.MethodPost, "/", nil, map[string]string{"id": chat.SessionID})
	sessionTeamTasksSummarize(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("summarize as another user: %d %s", w.Code, w.Body)
	}
	w, c = teamReq(t, owner, http.MethodPost, "/", nil, map[string]string{"id": chat.SessionID})
	sessionTeamTasksSummarize(c)
	var out struct {
		Delivered int `json:"delivered"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != http.StatusOK || out.Delivered != 0 {
		t.Fatalf("summarize: %d %s", w.Code, w.Body)
	}
}

// A deleted session is not woken (the pool would create it again): the
// notifier reports it gone, and the Hub's delivery never reaches the pool.
func TestTeamNotifierSkipsDeletedSession(t *testing.T) {
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
	n := teamNotifier{}
	if !n.SessionGone("deleted-session") || n.SessionGone(chat.SessionID) {
		t.Fatalf("SessionGone: deleted=%v live=%v", n.SessionGone("deleted-session"), n.SessionGone(chat.SessionID))
	}
	var _ teamlink.SessionChecker = n
}

// The switch ships on.
func TestTeamBatchResultsDefaultOn(t *testing.T) {
	if !agentconfig.DefaultGeneralConfig().TeamBatchResults {
		t.Fatal("team_batch_results default is off")
	}
	prev := globalConfigs
	globalConfigs = nil
	defer func() { globalConfigs = prev }()
	if !TeamBatchResultsEnabled() || teamBatchSoftTimeout() != 0 {
		t.Fatal("absent config: want batching on, no soft timeout")
	}
}
