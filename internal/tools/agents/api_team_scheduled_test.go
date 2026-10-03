package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
)

// withScheduleStore gives the Team world a schedule store of its own.
func withScheduleStore(t *testing.T) {
	t.Helper()
	dsn := "file:sched_" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&entity.ScheduledMessage{}); err != nil {
		t.Fatal(err)
	}
	prev := globalSchedule
	globalSchedule = schedule.NewStore(db)
	t.Cleanup(func() { globalSchedule = prev })
}

// seedAgentSession makes a session of the agent (its main chat when main).
func seedAgentSession(t *testing.T, p entity.AgentPersona, id string, main bool) {
	t.Helper()
	if _, err := globalMgr.CreateSession(context.Background(), session.CreateOptions{
		ID: id, ProjectID: p.ProjectID, Origin: session.OriginUI, UserID: p.OwnerUserID,
		AgentID: p.ID, AgentMain: main,
	}); err != nil {
		t.Fatal(err)
	}
}

func seedSchedule(t *testing.T, sessionID string, recurring bool) *entity.ScheduledMessage {
	t.Helper()
	m := &entity.ScheduledMessage{SessionID: sessionID, OwnerUserID: "u1", Message: "Daily digest\nmore", RunAt: time.Now().Add(time.Hour)}
	if recurring {
		m.Kind, m.Status, m.IntervalMs = entity.ScheduledKindRecurring, entity.ScheduledStatusActive, time.Hour.Milliseconds()
	}
	out, err := globalSchedule.Create(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type scheduledListResp struct {
	Items []struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Destination string `json:"destination"`
		Paused      bool   `json:"paused"`
	} `json:"items"`
	FeatureOn bool   `json:"feature_on"`
	ServerTZ  string `json:"server_timezone"`
}

func listScheduled(t *testing.T, u *entity.User, agentID string) (int, scheduledListResp) {
	t.Helper()
	w, c := teamReq(t, u, http.MethodGet, "/api/team/agents/"+agentID+"/scheduled", nil, map[string]string{"id": agentID})
	apiTeamAgentScheduledList(c)
	var out scheduledListResp
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// The drawer lists only what fires into this agent, and someone else's
// agent is a 404 on every scheduled route.
func TestTeamScheduledScopeAndAccess(t *testing.T) {
	withTeamWorld(t)
	withScheduleStore(t)
	u := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", "u1")
	seedTeamProject(t, "p2", "u1")
	a := seedTeamAgent(t, "u1", "alpha", "p1")
	b := seedTeamAgent(t, "u1", "beta", "p2")
	seedAgentSession(t, a, "main-a", true)
	seedAgentSession(t, b, "main-b", true)
	mine := seedSchedule(t, "main-a", false)
	theirs := seedSchedule(t, "main-b", false)

	code, out := listScheduled(t, u, a.ID)
	if code != http.StatusOK || len(out.Items) != 1 || out.Items[0].ID != mine.ID {
		t.Fatalf("list = %d %+v", code, out)
	}
	if out.Items[0].Destination != "main" || out.Items[0].Title != "Daily digest" || !out.FeatureOn || out.ServerTZ == "" {
		t.Fatalf("row = %+v", out)
	}

	// beta's schedule through alpha's route is not alpha's to touch.
	w, c := teamReq(t, u, http.MethodPost, "/x", nil, map[string]string{"id": a.ID, "sid": theirs.ID})
	apiTeamAgentScheduledMutate("run_now")(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-agent run = %d", w.Code)
	}
	w, c = teamReq(t, u, http.MethodDelete, "/x", nil, map[string]string{"id": a.ID, "sid": theirs.ID})
	apiTeamAgentScheduledDelete(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-agent delete = %d", w.Code)
	}

	// Another user sees nothing of alpha.
	if code, _ := listScheduled(t, &entity.User{ID: "u2"}, a.ID); code != http.StatusNotFound {
		t.Fatalf("other user list = %d", code)
	}
}

// Create lands in the main chat (made on first use); pause, resume and
// delete act on the row.
func TestTeamScheduledCreateAndMutate(t *testing.T) {
	withTeamWorld(t)
	withScheduleStore(t)
	u := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "alpha", "p1")
	seedAgentSession(t, a, "main-a", true)
	id := map[string]string{"id": a.ID}

	w, c := teamReq(t, u, http.MethodPost, "/x", map[string]any{"message": "", "every": "1h"}, id)
	apiTeamAgentScheduledCreate(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty message = %d", w.Code)
	}
	w, c = teamReq(t, u, http.MethodPost, "/x", map[string]any{"message": "hi", "every": "1h", "destination": "slack"}, id)
	apiTeamAgentScheduledCreate(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("slack destination = %d", w.Code)
	}
	w, c = teamReq(t, u, http.MethodPost, "/x", map[string]any{"message": "Check the queue", "every": "1h"}, id)
	apiTeamAgentScheduledCreate(c)
	var row struct {
		ID, SessionID, Destination, Kind string
	}
	_ = json.Unmarshal(w.Body.Bytes(), &struct {
		ID          *string `json:"id"`
		SessionID   *string `json:"session_id"`
		Destination *string `json:"destination"`
		Kind        *string `json:"kind"`
	}{&row.ID, &row.SessionID, &row.Destination, &row.Kind})
	if w.Code != http.StatusOK || row.SessionID != "main-a" || row.Destination != "main" || row.Kind != entity.ScheduledKindRecurring {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}

	sid := map[string]string{"id": a.ID, "sid": row.ID}
	w, c = teamReq(t, u, http.MethodPost, "/x", nil, sid)
	apiTeamAgentScheduledMutate("pause")(c)
	if m, _ := globalSchedule.Get(context.Background(), row.ID); w.Code != http.StatusOK || !m.Paused {
		t.Fatalf("pause = %d %+v", w.Code, m)
	}
	w, c = teamReq(t, u, http.MethodPost, "/x", nil, sid)
	apiTeamAgentScheduledMutate("resume")(c)
	if m, _ := globalSchedule.Get(context.Background(), row.ID); w.Code != http.StatusOK || m.Paused {
		t.Fatalf("resume = %d %+v", w.Code, m)
	}
	w, c = teamReq(t, u, http.MethodPatch, "/x", map[string]any{"message": "Check the queue twice"}, sid)
	apiTeamAgentScheduledMutate("edit")(c)
	if m, _ := globalSchedule.Get(context.Background(), row.ID); w.Code != http.StatusOK || m.Message != "Check the queue twice" {
		t.Fatalf("edit = %d %s", w.Code, w.Body)
	}
	w, c = teamReq(t, u, http.MethodDelete, "/x", nil, sid)
	apiTeamAgentScheduledDelete(c)
	if _, err := globalSchedule.Get(context.Background(), row.ID); w.Code != http.StatusOK || err != schedule.ErrNotFound {
		t.Fatalf("delete = %d %v", w.Code, err)
	}
}

// Disable holds the agent's schedules, enable releases them, and a row
// paused by hand stays paused through both. Deleting the agent removes them.
func TestTeamScheduledFollowsAgentLifecycle(t *testing.T) {
	withTeamWorld(t)
	withScheduleStore(t)
	u := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "alpha", "p1")
	seedAgentSession(t, a, "main-a", true)
	running := seedSchedule(t, "main-a", true)
	manual := seedSchedule(t, "main-a", true)
	ctx := context.Background()
	if err := globalSchedule.SetPaused(ctx, manual.ID, true, time.Time{}); err != nil {
		t.Fatal(err)
	}
	id := map[string]string{"id": a.ID}
	patch := func(disabled bool) {
		w, c := teamReq(t, u, http.MethodPatch, "/x", map[string]any{"disabled": disabled}, id)
		apiTeamAgentUpdate(c)
		if w.Code != http.StatusOK {
			t.Fatalf("patch disabled=%v: %d %s", disabled, w.Code, w.Body)
		}
	}
	patch(true)
	if m, _ := globalSchedule.Get(ctx, running.ID); !m.Paused || !m.HeldByAgent {
		t.Fatalf("disable did not hold: %+v", m)
	}
	patch(false)
	if m, _ := globalSchedule.Get(ctx, running.ID); m.Paused {
		t.Fatalf("enable did not release: %+v", m)
	}
	if m, _ := globalSchedule.Get(ctx, manual.ID); !m.Paused {
		t.Fatalf("hand-paused row woke up: %+v", m)
	}

	w, c := teamReq(t, u, http.MethodDelete, "/x", nil, id)
	apiTeamAgentDelete(c)
	if w.Code != http.StatusOK {
		t.Fatalf("delete agent = %d %s", w.Code, w.Body)
	}
	for _, sid := range []string{running.ID, manual.ID} {
		if _, err := globalSchedule.Get(ctx, sid); err != schedule.ErrNotFound {
			t.Fatalf("schedule %s survived its agent: %v", sid, err)
		}
	}
}

func TestTeamSessionPolicySave(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "alpha", "p1")
	id := map[string]string{"id": a.ID}
	for _, bad := range []map[string]any{{"compact": "off"}, {"idle_hours": 0}, {"idle_hours": 169}} {
		w, c := teamReq(t, u, http.MethodPatch, "/x", bad, id)
		apiTeamAgentSessionSave(c)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%v = %d", bad, w.Code)
		}
	}
	w, c := teamReq(t, u, http.MethodPatch, "/x", map[string]any{"compact": "idle", "idle_hours": 6, "summarise_threads": true}, id)
	apiTeamAgentSessionSave(c)
	if w.Code != http.StatusOK {
		t.Fatalf("save = %d %s", w.Code, w.Body)
	}
	p, _ := globalTeam.Get(context.Background(), a.ID)
	if got := team.DecodeSessionPolicy(p.SessionPolicy); got != (team.SessionPolicy{Compact: "idle", IdleHours: 6, SummariseThreads: true}) {
		t.Fatalf("stored = %+v", got)
	}
	// No main chat yet: compact now has nothing to compact.
	w, c = teamReq(t, u, http.MethodPost, "/x", nil, id)
	apiTeamAgentCompact(c)
	if w.Code != http.StatusConflict {
		t.Fatalf("compact without a main chat = %d", w.Code)
	}
	w, c = teamReq(t, &entity.User{ID: "u2"}, http.MethodGet, "/x", nil, id)
	apiTeamAgentSessionGet(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("other user = %d", w.Code)
	}
}
