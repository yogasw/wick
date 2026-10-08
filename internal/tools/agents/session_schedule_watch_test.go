package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// watchPanelSteps is the pre-filled example of the session panel's Watch tab.
var watchPanelSteps = []map[string]any{
	{"kind": "connector", "tool_id": "conn:3f0cfe86/get_pipeline", "params": map[string]any{"uuid": "x"}},
	{"kind": "check", "rules": []map[string]any{{"path": "state.name", "op": "equals", "value": "COMPLETED"}}},
}

func withWatchPanel(t *testing.T, owner string) string {
	t.Helper()
	withTeamWorld(t)
	withScheduleStore(t)
	if _, err := globalMgr.CreateSession(context.Background(), session.CreateOptions{
		ID: "s1", Origin: session.OriginUI, UserID: owner,
	}); err != nil {
		t.Fatal(err)
	}
	return "s1"
}

// withBashGate makes every agent's Bash held to gate rules and only "admin"
// an admin — the case where bash steps must be refused for everyone else.
func withBashGate(t *testing.T) {
	t.Helper()
	schedule.SetBashPolicy(func(context.Context, string, string) schedule.BashAccess { return schedule.BashRestricted })
	schedule.SetBashAdminCheck(func(_ context.Context, uid string) bool { return uid == "admin" })
	t.Cleanup(func() { schedule.SetBashPolicy(nil); schedule.SetBashAdminCheck(nil) })
}

func createPanelSchedule(t *testing.T, u *entity.User, sid string, body map[string]any) (int, scheduleVM, string) {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPost, "/sessions/"+sid+"/schedules", body, map[string]string{"id": sid})
	sessionSchedulesCreateUI(c)
	var vm scheduleVM
	_ = json.Unmarshal(w.Body.Bytes(), &vm)
	return w.Code, vm, w.Body.String()
}

// A watch made from the panel defaults to every 10s for 2h, belongs to the
// caller and targets the session.
func TestSessionPanelWatchCreate_Defaults(t *testing.T) {
	sid := withWatchPanel(t, "u1")
	code, vm, raw := createPanelSchedule(t, &entity.User{ID: "u1"}, sid, map[string]any{
		"type": "watch", "steps": watchPanelSteps, "message": "pipeline done",
	})
	if code != http.StatusOK {
		t.Fatalf("create = %d %s", code, raw)
	}
	if vm.Type != entity.ScheduledTypeWatch || vm.OwnerUserID != "u1" || vm.SessionID != sid || vm.StepCount != 2 {
		t.Fatalf("vm = %+v", vm)
	}
	if vm.IntervalMs != schedule.MinWatchInterval.Milliseconds() {
		t.Fatalf("interval = %d", vm.IntervalMs)
	}
	m, _ := globalSchedule.Get(context.Background(), vm.ID)
	if m.EndsAt == nil || m.EndsAt.Sub(m.CreatedAt) < schedule.WatchDefaultTimeout-time.Minute {
		t.Fatalf("ends_at = %v (created %v)", m.EndsAt, m.CreatedAt)
	}
}

// The owner is the caller, not the session's owner: a watch runs as whoever
// made it.
func TestSessionPanelWatchCreate_OwnerIsCaller(t *testing.T) {
	sid := withWatchPanel(t, "u1")
	code, vm, raw := createPanelSchedule(t, &entity.User{ID: "boss", IsOwner: true}, sid, map[string]any{
		"type": "watch", "steps": watchPanelSteps, "message": "done",
	})
	if code != http.StatusOK || vm.OwnerUserID != "boss" {
		t.Fatalf("create = %d owner=%q %s", code, vm.OwnerUserID, raw)
	}
	// A message schedule keeps the session owner.
	code, vm, raw = createPanelSchedule(t, &entity.User{ID: "boss", IsOwner: true}, sid, map[string]any{
		"message": "hi", "every": "1h",
	})
	if code != http.StatusOK || vm.OwnerUserID != "u1" || vm.Type != entity.ScheduledTypeMessage {
		t.Fatalf("message create = %d %+v %s", code, vm, raw)
	}
}

// Validation errors come back as the shared rules word them (field path).
func TestSessionPanelWatchCreate_Rejects(t *testing.T) {
	sid := withWatchPanel(t, "u1")
	u := &entity.User{ID: "u1"}
	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"bad kind", map[string]any{"type": "watch", "steps": []map[string]any{{"kind": "go"}}, "message": "m"}, `steps[0].kind`},
		{"no steps", map[string]any{"type": "watch", "message": "m"}, "at least one step"},
		{"too fast", map[string]any{"type": "watch", "steps": watchPanelSteps, "every": "5s", "message": "m"}, "at least"},
		// No upper bound on timeout any more; only a bad or too-short one.
		{"timeout too short", map[string]any{"type": "watch", "steps": watchPanelSteps, "timeout": "5s", "message": "m"}, "timeout"},
		{"timeout not a duration", map[string]any{"type": "watch", "steps": watchPanelSteps, "timeout": "abc", "message": "m"}, "timeout"},
		{"project scope", map[string]any{"type": "watch", "steps": watchPanelSteps, "project_id": "p1", "session_mode": "new", "message": "m"}, "targets this session"},
		{"steps on message", map[string]any{"steps": watchPanelSteps, "every": "1h", "message": "m"}, "only for type=watch"},
		{"bad type", map[string]any{"type": "cron", "message": "m", "every": "1h"}, "type must be"},
	}
	for _, tc := range cases {
		code, _, raw := createPanelSchedule(t, u, sid, tc.body)
		if code != http.StatusBadRequest || !strings.Contains(raw, tc.want) {
			t.Errorf("%s: %d %s (want %q)", tc.name, code, raw, tc.want)
		}
	}
}

// Bash steps under a gated agent: refused for a non-admin, on create and on
// the dry run; an admin passes the create.
func TestSessionPanelWatch_BashGate(t *testing.T) {
	sid := withWatchPanel(t, "u1")
	withBashGate(t)
	bash := []map[string]any{{"kind": "bash", "script": "exit 0"}}

	code, _, raw := createPanelSchedule(t, &entity.User{ID: "u1"}, sid, map[string]any{"type": "watch", "steps": bash, "message": "m"})
	if code != http.StatusBadRequest || !strings.Contains(raw, "gate limits") {
		t.Fatalf("non-admin bash create = %d %s", code, raw)
	}
	w, c := teamReq(t, &entity.User{ID: "u1"}, http.MethodPost, "/x", map[string]any{"steps": bash}, map[string]string{"id": sid})
	sessionSchedulesWatchTestUI(c)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "gate limits") {
		t.Fatalf("non-admin bash dry run = %d %s", w.Code, w.Body)
	}

	code, vm, raw := createPanelSchedule(t, &entity.User{ID: "admin", IsOwner: true}, sid, map[string]any{"type": "watch", "steps": bash, "message": "m"})
	if code != http.StatusOK || vm.OwnerUserID != "admin" {
		t.Fatalf("admin bash create = %d %s", code, raw)
	}
}

// The live-watch cap applies to the panel too (fail-closed, per caller).
func TestSessionPanelWatchCreate_Cap(t *testing.T) {
	sid := withWatchPanel(t, "u1")
	u := &entity.User{ID: "u1"}
	for i := 0; i < schedule.MaxLiveWatchesPerUser; i++ {
		if code, _, raw := createPanelSchedule(t, u, sid, map[string]any{"type": "watch", "steps": watchPanelSteps, "message": "m"}); code != http.StatusOK {
			t.Fatalf("create %d = %d %s", i, code, raw)
		}
	}
	code, _, raw := createPanelSchedule(t, u, sid, map[string]any{"type": "watch", "steps": watchPanelSteps, "message": "m"})
	if code != http.StatusBadRequest || !strings.Contains(raw, "watch limit") {
		t.Fatalf("over cap = %d %s", code, raw)
	}
}

// The dry run validates before anything runs, and is refused on a session
// the caller cannot open.
func TestSessionPanelWatchTest_ValidatesAndScopes(t *testing.T) {
	sid := withWatchPanel(t, "u1")
	w, c := teamReq(t, &entity.User{ID: "u1"}, http.MethodPost, "/x", map[string]any{"steps": []map[string]any{{"kind": "check"}}}, map[string]string{"id": sid})
	sessionSchedulesWatchTestUI(c)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "steps[0]") {
		t.Fatalf("invalid dry run = %d %s", w.Code, w.Body)
	}
	w, c = teamReq(t, &entity.User{ID: "u2"}, http.MethodPost, "/x", map[string]any{"steps": watchPanelSteps}, map[string]string{"id": sid})
	sessionSchedulesWatchTestUI(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign dry run = %d %s", w.Code, w.Body)
	}
}
