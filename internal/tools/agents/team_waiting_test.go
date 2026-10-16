package agents

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
)

func TestTeamWaitingSessions(t *testing.T) {
	got := teamWaitingSessions([]teamlink.LiveTask{
		{FromSession: "cap", Working: true},
		{FromSession: "asks", Working: false},
		{FromSession: "", Working: true},
	})
	if len(got) != 1 || !got["cap"] {
		t.Fatalf("waiting = %v", got)
	}
}

// Either source finishing must not clear the other's live work.
func TestSidebarWorkMarker(t *testing.T) {
	cases := []struct {
		waiting bool
		child   string
		want    string
	}{
		{true, "", "working"},         // child stopped, teammate still working
		{false, "working", "working"}, // teammate settled, child still working
		{false, "spawning", "spawning"},
		{false, "", ""}, // both gone
	}
	for _, c := range cases {
		if got := sidebarWorkMarker(c.waiting, c.child); got != c.want {
			t.Errorf("marker(%v, %q) = %q, want %q", c.waiting, c.child, got, c.want)
		}
	}
}

func TestRosterTaskMovedFirst(t *testing.T) {
	v := teamlink.TaskView{TaskID: "t-first-1", State: "working"}
	if moved, first := rosterTaskMovedFirst(v); !moved || !first {
		t.Fatalf("sent: moved=%v first=%v", moved, first)
	}
	if moved, _ := rosterTaskMovedFirst(v); moved {
		t.Fatal("a repeat of the same state moved")
	}
	v.State = "completed"
	if moved, first := rosterTaskMovedFirst(v); !moved || first {
		t.Fatalf("settled: moved=%v first=%v", moved, first)
	}
}

// seedLiveHub wires a Hub over tasks written to disk as given (finished
// false = still running), adopted as if a draining process still ran them,
// and hands it to the handlers.
func seedLiveHub(t *testing.T, recs ...map[string]any) *teamlink.Hub {
	t.Helper()
	dir := t.TempDir()
	now := time.Now()
	for _, r := range recs {
		r["context_id"], r["agent_id"], r["to_name"], r["title"] = "ctx-"+r["task_id"].(string), "a-anton", "Anton", "check logs"
		r["started_at"], r["updated_at"] = now, now
		if r["to_handle"] == nil {
			r["to_handle"] = "anton"
		}
		b, _ := json.Marshal(r)
		if err := os.WriteFile(filepath.Join(dir, r["task_id"].(string)+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hub := teamlink.NewHub(teamDirectory{svc: globalTeam}, poolTurns{}, teamNotifier{})
	hub.PredecessorBusy = func(string) bool { return true }
	hub.CallerBusy = func(string) bool { return false }
	if err := hub.Persist(dir); err != nil {
		t.Fatal(err)
	}
	prev := globalTeamHub
	SetTeamHub(func() *teamlink.Hub { return hub })
	t.Cleanup(func() { globalTeamHub = prev })
	return hub
}

// teamChat opens owner's chat with a fresh agent "helper".
func teamChat(t *testing.T, owner *entity.User) (entity.AgentPersona, string) {
	t.Helper()
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
	return p, chat.SessionID
}

// Stop → "also stop teammates": exactly this chat's working task is
// canceled (and must succeed); its question and another chat's working
// task stay; another user gets 404.
func TestSessionTeamTasksCancelWorking(t *testing.T) {
	withTeamWorld(t)
	owner, eve := &entity.User{ID: "u1"}, &entity.User{ID: "eve"}
	p, sid := teamChat(t, owner)
	hub := seedLiveHub(t,
		map[string]any{"task_id": "t-live", "caller_agent_id": p.ID, "caller_session": sid, "state": string(a2a.TaskStateWorking), "finished": false},
		map[string]any{"task_id": "t-ask", "caller_agent_id": p.ID, "caller_session": sid, "state": string(a2a.TaskStateInputRequired), "finished": true, "reply": "which env?"},
		map[string]any{"task_id": "t-other", "caller_agent_id": p.ID, "caller_session": "other-chat", "state": string(a2a.TaskStateWorking), "finished": false},
	)
	if !teamWaitingSessions(hub.LiveTasks())[sid] {
		t.Fatalf("seeded task not working: %+v", hub.SentFrom(sid))
	}
	w, c := teamReq(t, eve, http.MethodPost, "/", nil, map[string]string{"id": sid})
	sessionTeamTasksCancelWorking(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("as another user: %d %s", w.Code, w.Body)
	}
	w, c = teamReq(t, owner, http.MethodPost, "/", nil, map[string]string{"id": sid})
	sessionTeamTasksCancelWorking(c)
	var out struct {
		Canceled int      `json:"canceled"`
		Failed   []string `json:"failed"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != http.StatusOK || out.Canceled != 1 || len(out.Failed) != 0 {
		t.Fatalf("cancel-working: %d %s", w.Code, w.Body)
	}
	states := map[string]string{}
	for _, v := range hub.SentFrom(sid) {
		states[v.TaskID] = v.State
	}
	if states["t-live"] != "canceled" || states["t-ask"] != "input_required" {
		t.Fatalf("states after cancel: %v", states)
	}
	if teamWaiting(sid) {
		t.Fatal("chat still reads as waiting after the cancel")
	}
	if !teamWaitingSessions(hub.LiveTasks())["other-chat"] {
		t.Fatal("another chat's working task was touched")
	}
}

// S2: a task adopted from disk after a restart is not "first" on its next
// change, so the thread gets no second start ping.
func TestRestartAdoptedTaskIsNotFirst(t *testing.T) {
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	p, sid := teamChat(t, owner)
	hub := seedLiveHub(t, map[string]any{"task_id": "t-adopt", "caller_agent_id": p.ID, "caller_session": sid, "state": string(a2a.TaskStateWorking), "finished": false})
	rosterTaskState.Delete("t-adopt")
	seedRosterTasks(hub.LiveTasks())
	if moved, first := rosterTaskMovedFirst(teamlink.TaskView{TaskID: "t-adopt", State: "working"}); moved || first {
		t.Fatalf("progress after restart: moved=%v first=%v, want neither", moved, first)
	}
	if moved, first := rosterTaskMovedFirst(teamlink.TaskView{TaskID: "t-adopt", State: "completed"}); !moved || first {
		t.Fatalf("settle after restart: moved=%v first=%v", moved, first)
	}
}

// B.1 default + S3: the setting reads on for an agent that never set it,
// the chat's team-tasks says so, and saving it off tells the server (the
// live banner repaints) and flips both reads.
func TestShowTeammatesSettingRoundTrip(t *testing.T) {
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	p, sid := teamChat(t, owner)
	read := func() (item, tasks bool) {
		t.Helper()
		w, c := teamReq(t, owner, http.MethodGet, "/", nil, map[string]string{"id": p.ID})
		apiTeamAgentGet(c)
		var a struct {
			Show bool `json:"channel_show_teammates"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &a)
		w, c = teamReq(t, owner, http.MethodGet, "/", nil, map[string]string{"id": sid})
		sessionTeamTasks(c)
		var tt struct {
			Show bool `json:"show_teammates"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &tt)
		return a.Show, tt.Show
	}
	if item, tasks := read(); !item || !tasks {
		t.Fatalf("default: item=%v team-tasks=%v, want on", item, tasks)
	}
	got := make(chan string, 1)
	SetOnTeamVisibilityChanged(func(agentID string) { got <- agentID })
	t.Cleanup(func() { onTeamVisibilityChanged.Store(nil) })
	w, c := teamReq(t, owner, http.MethodPatch, "/", map[string]any{"channel_show_teammates": false}, map[string]string{"id": p.ID})
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	select {
	case id := <-got:
		if id != p.ID {
			t.Fatalf("hook told %q, want %q", id, p.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("visibility hook not called")
	}
	if item, tasks := read(); item || tasks {
		t.Fatalf("after off: item=%v team-tasks=%v", item, tasks)
	}
	if !TeamHidesTeammates(sid) {
		t.Fatal("cached read still shows teammates after the change")
	}
}

// S5: publishes for one session are serialised with their read, so a
// stale "working" read cannot land after a later "".
func TestPublishTeamWaitingOrdered(t *testing.T) {
	b := NewBroadcaster()
	ch, unsub := b.Subscribe("")
	defer unsub()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	live := func() []teamlink.LiveTask {
		if calls.Add(1) == 1 {
			close(entered)
			<-release // the first read is slow and stale: still working
			return []teamlink.LiveTask{{FromSession: "cap", Working: true}}
		}
		return nil
	}
	go publishTeamWaiting(b, live, "cap")
	<-entered
	done := make(chan struct{})
	go func() { publishTeamWaiting(b, live, "cap"); close(done) }()
	time.Sleep(30 * time.Millisecond)
	close(release)
	<-done
	var last *Event
	for {
		select {
		case ev := <-ch:
			if ev.Type == evTeamWaiting {
				e := ev
				last = &e
			}
			continue
		case <-time.After(100 * time.Millisecond):
		}
		break
	}
	if last == nil || last.Data != "" {
		t.Fatalf("last team_waiting = %+v, want the cleared marker", last)
	}
}
