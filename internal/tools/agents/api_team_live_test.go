package agents

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// The live read follows the pool: a working turn with a tool in flight
// names it, one without reads running with no action, and an agent that
// is not the caller's (or unknown) reads idle.
func TestTeamLiveRows(t *testing.T) {
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", owner.ID)
	a := seedTeamAgent(t, owner.ID, "worker", "p1")
	b := seedTeamAgent(t, owner.ID, "thinker", "p1")
	sa := openChat(t, owner, a.ID, false)
	sb := openChat(t, owner, b.ID, false)

	live := teamLive{
		actions:    map[string]string{sa: "Bash", sb: ""},
		lifecycles: map[string]string{sa: "working", sb: "working"},
		approvals:  map[string]string{},
	}
	rows := teamLiveRows(owner.ID, []string{a.ID, b.ID, "nope"}, live)
	if len(rows) != 3 {
		t.Fatalf("rows %+v", rows)
	}
	if rows[0].Status != "running" || rows[0].CurrentAction != "Bash" {
		t.Fatalf("tool row %+v", rows[0])
	}
	if rows[1].Status != "running" || rows[1].CurrentAction != "" {
		t.Fatalf("thinking row %+v", rows[1])
	}
	if rows[2].Status != "idle" || rows[2].CurrentAction != "" {
		t.Fatalf("unknown row %+v", rows[2])
	}
	// A failed tool shows on its working row only.
	live.failed = map[string]bool{sa: true, sb: true}
	live.lifecycles[sb] = "idle"
	if r := teamLiveRows(owner.ID, []string{a.ID, b.ID}, live); !r[0].ToolError || r[1].ToolError {
		t.Fatalf("tool error rows %+v", r)
	}
	live.failed, live.lifecycles[sb] = nil, "working"
	// Another user asking about the same agents learns nothing.
	for _, r := range teamLiveRows("bob", []string{a.ID}, live) {
		if r.Status != "idle" || r.CurrentAction != "" {
			t.Fatalf("stranger row %+v", r)
		}
	}
	// A finished turn drops its tool even if the pool still lists one.
	live.lifecycles[sa] = "idle"
	if r := teamLiveRows(owner.ID, []string{a.ID}, live)[0]; r.Status != "idle" || r.CurrentAction != "" {
		t.Fatalf("idle row %+v", r)
	}
}

// GET /api/team/agents/live parses ?ids=, drops blanks and repeats.
func TestTeamAgentLiveHandler(t *testing.T) {
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", owner.ID)
	a := seedTeamAgent(t, owner.ID, "worker", "p1")
	w, c := teamReq(t, owner, http.MethodGet, "/api/team/agents/live?ids="+a.ID+",,"+a.ID+",x", nil, nil)
	apiTeamAgentLive(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var out struct {
		Agents []TeamAgentLive `json:"agents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Agents) != 2 {
		t.Fatalf("agents %+v err %v", out.Agents, err)
	}
	if out.Agents[0].ID != a.ID || out.Agents[0].Status != "idle" || out.Agents[1].ID != "x" {
		t.Fatalf("agents %+v", out.Agents)
	}
}
