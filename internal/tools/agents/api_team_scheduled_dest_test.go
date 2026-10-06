package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/entity"
)

func TestScheduleRunsIn(t *testing.T) {
	at := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	fired := func(id string, n int) store.ConversationTurn {
		return store.ConversationTurn{Role: "system", Kind: store.KindScheduledFired, TurnID: id,
			Timestamp: at.Add(time.Duration(n) * time.Hour), Extras: map[string]string{"schedule_id": "s1"}}
	}
	turns := []store.ConversationTurn{
		fired("t1", 0),
		{Role: "user", Text: "digest"},
		{Role: "assistant", Text: "done"},
		{Role: "system", Kind: store.KindScheduledFired, Extras: map[string]string{"schedule_id": "other"}},
		{Role: "assistant", Text: "answer to the other schedule"},
		fired("t2", 1),
		{Role: "system", IsError: true, Text: "provider crashed\ntrace"},
		fired("t3", 2),
	}
	got := scheduleRunsIn(turns, "s1", "main-a")
	if len(got) != 3 {
		t.Fatalf("runs = %+v", got)
	}
	want := []string{"ok", "failed", "running"}
	for i, r := range got {
		if r.Status != want[i] || r.SessionID != "main-a" {
			t.Errorf("run %d = %+v, want %s", i, r, want[i])
		}
	}
	if got[1].Error != "provider crashed" || got[0].TurnID != "t1" {
		t.Errorf("run detail = %+v", got)
	}
}

// The drawer edits time (cron validated server-side), message and
// destination in place; Telegram is refused without a connection, and the
// run history comes back newest first.
func TestTeamScheduledEditAndRuns(t *testing.T) {
	withTeamWorld(t)
	withScheduleStore(t)
	u := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "alpha", "p1")
	seedAgentSession(t, a, "main-a", true)
	m := seedSchedule(t, "main-a", true)
	sid := map[string]string{"id": a.ID, "sid": m.ID}
	ctx := context.Background()

	w, c := teamReq(t, u, http.MethodPatch, "/x", map[string]any{"cron": "0 9 * * 1", "message": "Weekly", "destination": "main"}, sid)
	apiTeamAgentScheduledMutate("edit")(c)
	got, _ := globalSchedule.Get(ctx, m.ID)
	if w.Code != http.StatusOK || got.Cron != "0 9 * * 1" || got.IntervalMs != 0 || got.Message != "Weekly" || got.SessionID != "main-a" {
		t.Fatalf("edit = %d %s %+v", w.Code, w.Body, got)
	}
	for _, body := range []map[string]any{
		{"cron": "61 * * * *"},
		{"destination": "telegram"},
		{"destination": "slack"},
	} {
		w, c = teamReq(t, u, http.MethodPatch, "/x", body, sid)
		apiTeamAgentScheduledMutate("edit")(c)
		if w.Code != http.StatusBadRequest {
			t.Errorf("edit %v = %d %s, want 400", body, w.Code, w.Body)
		}
	}

	path := globalLayout.SessionConversation("main-a")
	for i, turn := range []store.ConversationTurn{
		{Role: "system", Kind: store.KindScheduledFired, TurnID: "f1", Timestamp: time.Now().Add(-2 * time.Hour), Extras: map[string]string{"schedule_id": m.ID}},
		{Role: "assistant", Text: "ok", Timestamp: time.Now().Add(-2 * time.Hour)},
		{Role: "system", Kind: store.KindScheduledFired, TurnID: "f2", Timestamp: time.Now().Add(-time.Hour), Extras: map[string]string{"schedule_id": m.ID}},
	} {
		if err := storage.AppendJSONL(path, "wick-conv-v1", "main-a", turn); err != nil {
			t.Fatal(i, err)
		}
	}
	w, c = teamReq(t, u, http.MethodGet, "/x", nil, sid)
	apiTeamAgentScheduledRuns(c)
	var resp struct {
		Items []scheduleRunVM `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != http.StatusOK || len(resp.Items) != 2 || resp.Items[0].TurnID != "f2" || resp.Items[0].Status != "running" || resp.Items[1].Status != "ok" {
		t.Fatalf("runs = %d %s", w.Code, w.Body)
	}
}

// A schedule aimed at a Slack thread session reads back as a "slack"
// destination with its channel; without a Slack connection the
// destination is refused before anything is posted.
func TestTeamScheduledSlackDestination(t *testing.T) {
	withTeamWorld(t)
	withScheduleStore(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "alpha", "p1")
	seedAgentSession(t, a, "main-a", true)
	thread := agentSlackSessionPrefix(a.ID) + "1700000000.000200"
	seedAgentSession(t, a, thread, false)
	sess, err := session.Load(globalLayout, thread)
	if err != nil {
		t.Fatal(err)
	}
	sess.Meta.ChannelRef = &session.ChannelRef{Channel: "slack", ChatID: "C0123ABCD", ThreadID: "1700000000.000200"}
	if err := session.SaveMeta(globalLayout, thread, sess.Meta); err != nil {
		t.Fatal(err)
	}
	_ = globalMgr.RefreshSession(thread)

	vm := agentScheduleToVM(*seedSchedule(t, thread, true), "main-a", a.ID)
	if vm.Destination != scheduleDestSlack || vm.SlackChannel != "C0123ABCD" {
		t.Errorf("vm = %+v", vm)
	}
	if got := scheduleSlackChannelOf(thread, "someone-else"); got != "" {
		t.Errorf("another agent's thread = %q", got)
	}
	if got := scheduleSlackChannelOf("main-a", a.ID); got != "" {
		t.Errorf("main chat = %q", got)
	}
	if chs := agentSlackChannels(a, agentSlackTarget{bound: []string{"C0999ZZZZ", "C0123ABCD", "#general"}}); len(chs) != 2 || chs[0].ID != "C0123ABCD" || chs[1].ID != "C0999ZZZZ" {
		t.Errorf("channels = %+v", chs)
	}
	if _, _, err := scheduleDestination(a, scheduleDestSlack, "", "C0123ABCD"); err == nil || !strings.Contains(err.Error(), "no active Slack connection") {
		t.Errorf("slack without a connection = %v", err)
	}
	if _, _, err := scheduleDestination(a, "email", "", ""); err == nil {
		t.Error("unknown destination accepted")
	}
}
