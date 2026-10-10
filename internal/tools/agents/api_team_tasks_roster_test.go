package agents

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
)

// teamMainChat opens owner's main chat with agentID and returns its id.
func teamMainChat(t *testing.T, owner *entity.User, agentID string) string {
	t.Helper()
	w, c := teamReq(t, owner, http.MethodPost, "/", map[string]any{}, map[string]string{"id": agentID})
	apiTeamAgentChat(c)
	var chat struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &chat)
	if w.Code != http.StatusOK || chat.SessionID == "" {
		t.Fatalf("chat: %d %s", w.Code, w.Body)
	}
	return chat.SessionID
}

// A Team task the main chat sent that is still open (here a question back
// to it) shows on the sender's row as waiting on that teammate, like the
// tray; a settled or canceled one does not. Background sub-agents stay
// apart.
func TestRosterTeamWaitingFromOpenTasks(t *testing.T) {
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	main := teamMainChat(t, owner, p.ID)

	seedUserTasks(t, p.ID, map[string]seedTask{
		"t-done":   {main, a2a.TaskStateCompleted, ""},
		"t-cancel": {main, a2a.TaskStateCanceled, ""},
	})
	var it TeamAgentItem
	fillChatState(&it, owner.ID, p.ID, nil, teamLiveNow())
	if it.MainSessionID != main || it.TeamWaiting != nil || it.SubagentsWorking != nil {
		t.Fatalf("settled tasks: main=%q waiting=%v subagents=%v", it.MainSessionID, it.TeamWaiting, it.SubagentsWorking)
	}

	seedUserTasks(t, p.ID, map[string]seedTask{
		"t-ask":    {main, a2a.TaskStateInputRequired, ""},
		"t-done":   {main, a2a.TaskStateCompleted, ""},
		"t-other":  {"sess-other", a2a.TaskStateInputRequired, ""},
		"t-cancel": {main, a2a.TaskStateCanceled, ""},
	})
	it = TeamAgentItem{}
	fillChatState(&it, owner.ID, p.ID, nil, teamLiveNow())
	if !slices.Equal(it.TeamWaiting, []string{"anton"}) || it.SubagentsWorking != nil {
		t.Fatalf("open task: waiting=%v subagents=%v, want [anton] and none", it.TeamWaiting, it.SubagentsWorking)
	}
}

// signalledAgents is the agents uid's roster is told changed in evs.
func signalledAgents(evs []Event, uid string) []string {
	var out []string
	for _, ev := range evs {
		if ev.Type != evAgentChanged {
			continue
		}
		if data, ok := projectAgentSignal(ev, uid); ok {
			var sig agentSignal
			_ = json.Unmarshal([]byte(data), &sig)
			out = append(out, sig.AgentID)
		}
	}
	slices.Sort(out)
	return out
}

// A Team task's change refetches the roster of both ends: the sending
// agent (its main chat) and the teammate. A chat that is not a main one
// signals only the teammate.
func TestTeamTaskChangedSignalsBothRows(t *testing.T) {
	ch := withAgentSignals(t)
	owner := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", owner.ID)
	sender := seedTeamAgent(t, owner.ID, "helper", "p1")
	mate := seedTeamAgent(t, owner.ID, "anton", "p1")
	main := teamMainChat(t, owner, sender.ID)
	t.Cleanup(addStreamViewer(owner.ID))
	drain(ch)

	TeamTaskChanged(main, mate.ID)
	want := []string{sender.ID, mate.ID}
	slices.Sort(want)
	if got := signalledAgents(drain(ch), owner.ID); !slices.Equal(got, want) {
		t.Fatalf("main chat: signalled %v, want %v", got, want)
	}
	TeamTaskChanged("sess-not-main", mate.ID)
	if got := signalledAgents(drain(ch), owner.ID); !slices.Equal(got, []string{mate.ID}) {
		t.Fatalf("not main: signalled %v, want [%s]", got, mate.ID)
	}
	if got := signalledAgents(drain(ch), "eve"); got != nil {
		t.Fatalf("another user signalled %v", got)
	}
}

// fakeNames names every sending agent "Captain" and reads the viewer of a
// sending session from views.
func fakeNames(views map[string]string, reads *int) *teamTaskNames {
	return &teamTaskNames{
		name: func(string) string {
			if reads != nil {
				*reads++
			}
			return "Captain"
		},
		viewer: func(sessionID string) string { return views[sessionID] },
	}
}

// Handles are listed once per session; the teammate is noted, for the
// person whose chat sent the task, replying to its sender (the first task
// wins) or asking it; the person's own task names "you"; the sender's
// name is read once per agent; a sending chat with no known person tells
// nobody.
func TestAddTeamTasks(t *testing.T) {
	var l teamLive
	reads := 0
	l.addTeamTasks([]teamlink.LiveTask{
		{FromSession: "main-1", FromAgentID: "a-cap", ToID: "a-anton", ToHandle: "anton", Working: true},
		{FromSession: "main-1", FromAgentID: "a-cap", ToID: "a-anton", ToHandle: "anton", Working: true},
		{FromSession: "main-1", FromAgentID: "a-cap", ToID: "a-vera", ToHandle: "vera", Question: "which deploy?"},
		{FromSession: "main-2", FromAgentID: "a-x", Origin: teamlink.OriginUser, ToID: "a-jo", ToHandle: "jo", Working: true},
		{FromSession: "gone", FromAgentID: "a-cap", ToID: "a-kim", ToHandle: "kim", Working: true},
	}, fakeNames(map[string]string{"main-1": "u1", "main-2": "u2"}, &reads))
	if got := l.teamWaitingOf("main-1"); !slices.Equal(got, []string{"anton", "vera"}) {
		t.Fatalf("main-1 = %v", got)
	}
	if got := l.teamWaitingOf("main-3"); got != nil {
		t.Fatalf("main-3 = %v", got)
	}
	if l.replyingTo[viewKey("a-anton", "u1")] != "Captain" || l.replyingTo[viewKey("a-jo", "u2")] != "you" || len(l.replyingTo) != 2 {
		t.Fatalf("replyingTo = %v", l.replyingTo)
	}
	if l.replyingTo[viewKey("a-jo", "u1")] != "" {
		t.Fatal("another person's own task reads \"you\" for u1")
	}
	if l.teamAsks[viewKey("a-vera", "u1")] != (TeamAsk{To: "Captain", Question: "which deploy?"}) || len(l.teamAsks) != 1 {
		t.Fatalf("teamAsks = %v", l.teamAsks)
	}
	if reads != 1 {
		t.Fatalf("name read %d times, want 1", reads)
	}
}

// receiverWorld is an owner's helper agent, its main chat, and the
// owner's Captain main chat that sends it tasks.
func receiverWorld(t *testing.T) (owner *entity.User, helper entity.AgentPersona, helperMain, capMain string) {
	t.Helper()
	withTeamWorld(t)
	owner = &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", owner.ID)
	helper = seedTeamAgent(t, owner.ID, "helper", "p1")
	capAgent := seedTeamAgent(t, owner.ID, "captain", "p1")
	return owner, helper, teamMainChat(t, owner, helper.ID), teamMainChat(t, owner, capAgent.ID)
}

// readReceiver is the helper's row for userID with the given open and
// settled tasks, viewers read from the real sessions.
func readReceiver(userID, agentID string, open []teamlink.LiveTask, settled []teamlink.SettledTask) TeamAgentItem {
	l := teamTurnsNow()
	names := &teamTaskNames{name: func(string) string { return "Captain" }, viewer: sessionUserOf}
	l.addTeamTasks(open, names)
	l.addSettled(settled, names)
	var it TeamAgentItem
	fillChatState(&it, userID, agentID, nil, l)
	return it
}

// The teammate's row carries who it replies to and the question it asks,
// the question also as its attention preview; another agent's task does
// not touch it.
func TestRosterReceiverReplyingAndAsks(t *testing.T) {
	owner, p, _, capMain := receiverWorld(t)
	read := func(tasks ...teamlink.LiveTask) TeamAgentItem { return readReceiver(owner.ID, p.ID, tasks, nil) }
	it := read(teamlink.LiveTask{FromSession: capMain, FromAgentID: "a-cap", ToID: p.ID, ToHandle: "helper", Chat: "task-chat", Working: true})
	if it.ReplyingTo != "Captain" || it.TeamAsks != nil || it.NeedsAttention || it.Status != string(session.StatusIdle) {
		t.Fatalf("working: %+v", it)
	}
	it = read(teamlink.LiveTask{FromSession: capMain, FromAgentID: "a-cap", ToID: p.ID, ToHandle: "helper", Question: "which deploy?"})
	if it.ReplyingTo != "" || it.TeamAsks == nil || !it.NeedsAttention || it.AttentionPreview != "Asks Captain: which deploy?" || it.LastPreview != it.AttentionPreview {
		t.Fatalf("asks: %+v", it)
	}
	it = read(teamlink.LiveTask{FromSession: capMain, FromAgentID: "a-cap", ToID: "a-else", ToHandle: "else", Working: true})
	if it.ReplyingTo != "" || it.TeamAsks != nil || it.NeedsAttention {
		t.Fatalf("another agent's task: %+v", it)
	}
}

// A shared agent's recipient reads none of the owner's Team tasks on its
// row — not replying, not the question, not the settled reply — and the
// owner's own row still does. "you" never means someone else.
func TestRosterSharedRecipientSeesNoOwnerTasks(t *testing.T) {
	owner, p, _, capMain := receiverWorld(t)
	bob := &entity.User{ID: "bob"}
	if code := shareWith(t, owner, p.ID, bob.ID); code != http.StatusOK {
		t.Fatalf("share: %d", code)
	}
	bobMain := teamMainChat(t, bob, p.ID)
	later := time.Now().Add(time.Hour)
	cases := map[string]struct {
		open    []teamlink.LiveTask
		settled []teamlink.SettledTask
	}{
		"replying": {open: []teamlink.LiveTask{{FromSession: capMain, FromAgentID: "a-cap", ToID: p.ID, ToHandle: "helper", Chat: "task-chat", Working: true}}},
		"own task": {open: []teamlink.LiveTask{{FromSession: capMain, Origin: teamlink.OriginUser, ToID: p.ID, ToHandle: "helper", Chat: "task-chat", Working: true}}},
		"asks":     {open: []teamlink.LiveTask{{FromSession: capMain, FromAgentID: "a-cap", ToID: p.ID, ToHandle: "helper", Question: "secret question"}}},
		"settled":  {settled: []teamlink.SettledTask{{FromSession: capMain, FromAgentID: "a-cap", ToID: p.ID, Chat: "task-chat", Summary: "secret reply", At: later}}},
	}
	for name, c := range cases {
		bobRow := readReceiver(bob.ID, p.ID, c.open, c.settled)
		if bobRow.MainSessionID != bobMain {
			t.Fatalf("%s: bob's row reads main %q, want %q", name, bobRow.MainSessionID, bobMain)
		}
		if bobRow.ReplyingTo != "" || bobRow.TeamAsks != nil || bobRow.NeedsAttention || strings.Contains(bobRow.LastPreview, "secret") || strings.HasPrefix(bobRow.LastPreview, "Replied") {
			t.Fatalf("%s: recipient sees the owner's task: %+v", name, bobRow)
		}
		ownRow := readReceiver(owner.ID, p.ID, c.open, c.settled)
		if ownRow.ReplyingTo == "" && ownRow.TeamAsks == nil && !strings.HasPrefix(ownRow.LastPreview, "Replied") {
			t.Fatalf("%s: owner's row lost it: %+v", name, ownRow)
		}
	}
	// A task bob's own chat sent reads "you" on bob's row only.
	open := []teamlink.LiveTask{{FromSession: bobMain, Origin: teamlink.OriginUser, ToID: p.ID, ToHandle: "helper", Chat: "task-chat", Working: true}}
	if it := readReceiver(bob.ID, p.ID, open, nil); it.ReplyingTo != "you" {
		t.Fatalf("bob's own task: %+v", it)
	}
	if it := readReceiver(owner.ID, p.ID, open, nil); it.ReplyingTo != "" {
		t.Fatalf("bob's task on the owner's row: %+v", it)
	}
}

// A teammate's newest finished Team task becomes its preview while the
// main chat has not moved since: replied or declined, naming the sender;
// the person's own task drops the reply they already have; an open task
// outranks it.
func TestRosterReceiverSettledPreview(t *testing.T) {
	owner, p, main, capMain := receiverWorld(t)
	later := time.Now().Add(time.Hour)
	read := func(settled teamlink.SettledTask, open ...teamlink.LiveTask) TeamAgentItem {
		settled.FromSession, settled.ToID = capMain, p.ID
		return readReceiver(owner.ID, p.ID, open, []teamlink.SettledTask{settled})
	}
	if it := read(teamlink.SettledTask{FromAgentID: "a-cap", Chat: "task-chat", Summary: "3 rooms hit the same 401", At: later}); it.LastPreview != "Replied to Captain: 3 rooms hit the same 401" {
		t.Fatalf("replied: %q", it.LastPreview)
	}
	if it := read(teamlink.SettledTask{FromAgentID: "a-cap", Chat: "task-chat", Declined: true, Summary: "no access to the repo", At: later}); it.LastPreview != "Declined Captain's task — no access to the repo" {
		t.Fatalf("declined: %q", it.LastPreview)
	}
	if it := read(teamlink.SettledTask{Origin: teamlink.OriginUser, Chat: "task-chat", Summary: "the reply", At: later}); it.LastPreview != "Replied to you" {
		t.Fatalf("person's own task: %q", it.LastPreview)
	}
	if it := read(teamlink.SettledTask{Origin: teamlink.OriginUser, Chat: "task-chat", Declined: true, Summary: "no access", At: later}); it.LastPreview != "Declined your task — no access" {
		t.Fatalf("person's own declined: %q", it.LastPreview)
	}
	if it := read(teamlink.SettledTask{FromAgentID: "a-cap", Chat: "task-chat", Summary: "old"}); strings.HasPrefix(it.LastPreview, "Replied") {
		t.Fatalf("main chat moved since: %q", it.LastPreview)
	}
	// In the main chat itself: its own reply is within the grace; talk
	// after that hides it.
	sess, _ := globalMgr.Registry().Session(main)
	if it := read(teamlink.SettledTask{FromAgentID: "a-cap", Chat: main, Summary: "in main", At: sess.Meta.LastActive.Add(-time.Second)}); it.LastPreview != "Replied to Captain: in main" {
		t.Fatalf("main-chat task: %q", it.LastPreview)
	}
	if it := read(teamlink.SettledTask{FromAgentID: "a-cap", Chat: main, Summary: "in main", At: sess.Meta.LastActive.Add(-time.Minute)}); strings.HasPrefix(it.LastPreview, "Replied") {
		t.Fatalf("owner chatted after the main-chat task: %q", it.LastPreview)
	}
	open := teamlink.LiveTask{FromSession: capMain, FromAgentID: "a-cap", ToID: p.ID, ToHandle: "helper", Working: true}
	if it := read(teamlink.SettledTask{FromAgentID: "a-cap", Chat: "task-chat", Summary: "x", At: later}, open); strings.HasPrefix(it.LastPreview, "Replied") || it.ReplyingTo != "Captain" {
		t.Fatalf("open task: %+v", it)
	}
}

// The roster is signalled when a task's state moves, not on a "Needs
// you" refresh that repeats it; settling always signals.
func TestRosterTaskMoved(t *testing.T) {
	v := teamlink.TaskView{TaskID: "t-moved-1", State: "working"}
	if !rosterTaskMoved(v) || rosterTaskMoved(v) {
		t.Fatal("working: first signals, repeat does not")
	}
	v.State = "input_required"
	if !rosterTaskMoved(v) || rosterTaskMoved(v) {
		t.Fatal("input_required: first signals, refresh does not")
	}
	v.State = "completed"
	if !rosterTaskMoved(v) {
		t.Fatal("settled does not signal")
	}
	v.State = "working"
	if !rosterTaskMoved(v) {
		t.Fatal("reopened task does not signal")
	}
}

// An open task the Hub dropped without settling leaves no entry forever:
// entries older than rosterTaskKeep are swept on the next signal.
func TestRosterTaskStateSwept(t *testing.T) {
	rosterTaskState.Store("t-stale-1", rosterSeen{state: "working", at: time.Now().Add(-rosterTaskKeep - time.Minute)})
	rosterTaskState.Store("t-fresh-1", rosterSeen{state: "working", at: time.Now()})
	t.Cleanup(func() { rosterTaskState.Delete("t-fresh-1"); rosterTaskState.Delete("t-sweep-1") })
	rosterTaskMoved(teamlink.TaskView{TaskID: "t-sweep-1", State: "working"})
	if _, ok := rosterTaskState.Load("t-stale-1"); ok {
		t.Fatal("stale entry kept")
	}
	if _, ok := rosterTaskState.Load("t-fresh-1"); !ok {
		t.Fatal("fresh entry swept")
	}
}

// sessionUserOf without a manager reads nobody instead of panicking.
func TestSessionUserOfNoManager(t *testing.T) {
	prev := globalMgr
	globalMgr = nil
	t.Cleanup(func() { globalMgr = prev })
	if got := sessionUserOf("s-1"); got != "" {
		t.Fatalf("sessionUserOf = %q", got)
	}
}
