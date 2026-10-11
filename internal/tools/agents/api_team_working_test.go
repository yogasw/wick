package agents

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/a2aserver"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
)

// teamNewChat opens another (non-main) chat of owner with agentID.
func teamNewChat(t *testing.T, owner *entity.User, agentID string) string {
	t.Helper()
	w, c := teamReq(t, owner, http.MethodPost, "/", map[string]any{"new": true}, map[string]string{"id": agentID})
	apiTeamAgentChat(c)
	var chat struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &chat)
	if w.Code != http.StatusOK || chat.SessionID == "" {
		t.Fatalf("new chat: %d %s", w.Code, w.Body)
	}
	return chat.SessionID
}

// The row lists the agent's running non-main chats oldest turn first,
// each named by who it works for (the Team task's sender, else "you");
// the main chat never joins them, and a finished one is the "Last" line.
func TestRosterWorkingSessions(t *testing.T) {
	owner, p, helperMain, capMain := receiverWorld(t)
	taskChat := teamNewChat(t, owner, p.ID)
	ownChat := teamNewChat(t, owner, p.ID)
	t0 := time.Now().Add(-5 * time.Minute)

	l := teamTurnsNow()
	l.lifecycles[helperMain], l.lifecycles[taskChat], l.lifecycles[ownChat] = "working", "working", "spawning"
	l.since = map[string]time.Time{ownChat: t0, taskChat: t0.Add(time.Minute)}
	names := &teamTaskNames{name: func(string) string { return "Captain" }, viewer: sessionUserOf}
	l.addTeamTasks([]teamlink.LiveTask{{FromSession: capMain, FromAgentID: "a-cap", ToID: p.ID, ToHandle: "helper", Chat: taskChat, Working: true}}, names)
	var it TeamAgentItem
	fillChatState(&it, owner.ID, p.ID, nil, l)
	if len(it.WorkingSessions) != 2 || it.WorkingSessions[0].ID != ownChat || it.WorkingSessions[0].Trigger != "you" ||
		it.WorkingSessions[1].ID != taskChat || it.WorkingSessions[1].Trigger != "Captain" || it.LastSession != nil {
		t.Fatalf("working = %+v last = %+v", it.WorkingSessions, it.LastSession)
	}

	// The older turn ends: the task chat is next, the finished one is Last.
	delete(l.lifecycles, ownChat)
	it = TeamAgentItem{}
	fillChatState(&it, owner.ID, p.ID, nil, l)
	if len(it.WorkingSessions) != 1 || it.WorkingSessions[0].ID != taskChat || it.LastSession == nil || it.LastSession.ID != ownChat {
		t.Fatalf("after end: working = %+v last = %+v", it.WorkingSessions, it.LastSession)
	}

	// Another person sees none of the owner's chats.
	it = TeamAgentItem{}
	fillChatState(&it, "bob", p.ID, nil, l)
	if it.WorkingSessions != nil || it.LastSession != nil {
		t.Fatalf("bob: %+v %+v", it.WorkingSessions, it.LastSession)
	}
}

// noteTurns keeps a turn's first sighting while it runs, prefers an older
// first in-flight event, and forgets a turn once it ends.
func TestNoteTurns(t *testing.T) {
	now := time.Now()
	turnSince.Lock()
	turnSince.at = map[string]turnMark{}
	turnSince.Unlock()
	got := noteTurns(map[string]string{"a": "working", "b": "idle"}, map[string]time.Time{"a": now.Add(-time.Minute)}, now)
	if !got["a"].Equal(now.Add(-time.Minute)) || len(got) != 1 {
		t.Fatalf("first = %v", got)
	}
	got = noteTurns(map[string]string{"a": "working"}, nil, now.Add(time.Hour))
	if !got["a"].Equal(now.Add(-time.Minute)) {
		t.Fatalf("kept = %v", got)
	}
	noteTurns(map[string]string{}, nil, now)
	got = noteTurns(map[string]string{"a": "spawning"}, nil, now.Add(2*time.Hour))
	if !got["a"].Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("restart = %v", got)
	}
}

// sideChat is a non-main chat as the registry holds it.
func sideChat(id string, origin session.Origin, status session.Status, lastActive time.Time) session.Session {
	return session.Session{ID: id, Meta: session.Meta{Origin: origin, Status: status, LastActive: lastActive, Label: "chat " + id}}
}

// The roster's last_any_active is the newest of all the agent's chats: a
// side chat that moved today wins over a main chat idle for two days.
func TestNewestActive(t *testing.T) {
	now := time.Now()
	main := sideChat("main", session.OriginUI, session.StatusIdle, now.Add(-48*time.Hour))
	main.Meta.AgentMain = true
	side := sideChat("side", session.OriginUI, session.StatusIdle, now.Add(-3*time.Hour))
	if got := newestActive([]session.Session{main, side}); got == nil || !got.Equal(side.Meta.LastActive) {
		t.Fatalf("newest = %v, want side %v", got, side.Meta.LastActive)
	}
	if got := newestActive([]session.Session{main}); got == nil || !got.Equal(main.Meta.LastActive) {
		t.Fatalf("main only = %v", got)
	}
	if got := newestActive([]session.Session{sideChat("x", session.OriginUI, session.StatusIdle, time.Time{})}); got != nil {
		t.Fatalf("never moved = %v", got)
	}

	// On the row it counts every chat, while last_active stays the main's:
	// the main chat idle for two days, the side chat newer.
	owner, p, helperMain, _ := receiverWorld(t)
	sideID := teamNewChat(t, owner, p.ID)
	reg := globalMgr.Registry()
	mainSess, ok := reg.Session(helperMain)
	if !ok {
		t.Fatal("main chat missing")
	}
	mainSess.Meta.LastActive = time.Now().Add(-48 * time.Hour)
	if err := session.SaveMeta(globalLayout, helperMain, mainSess.Meta); err != nil {
		t.Fatal(err)
	}
	_ = globalMgr.RefreshSession(helperMain)
	sideSess, ok := reg.Session(sideID)
	if !ok {
		t.Fatal("side chat missing")
	}
	var it TeamAgentItem
	fillChatState(&it, owner.ID, p.ID, nil, teamTurnsNow())
	if it.LastActive == nil || !it.LastActive.Equal(mainSess.Meta.LastActive) {
		t.Fatalf("last_active = %v, want main %v", it.LastActive, mainSess.Meta.LastActive)
	}
	if it.LastAnyActive == nil || !it.LastAnyActive.Equal(sideSess.Meta.LastActive) || !it.LastAnyActive.After(*it.LastActive) {
		t.Fatalf("last_any_active = %v, want side %v after last_active %v", it.LastAnyActive, sideSess.Meta.LastActive, it.LastActive)
	}
}

// A queued chat has not started: it is neither working nor Last, so it
// cannot take the click and then hand it on when it starts. Running turns
// sort by their start, one never seen yet starts now (no zero Since), and
// a chat no person opened names its source instead of "you".
func TestWorkingSessionsOfQueuedAndSources(t *testing.T) {
	now := time.Now()
	l := teamLive{
		lifecycles: map[string]string{"run-ui": "working", "run-slack": "working", "run-a2a": "spawning", "fresh": "working"},
		since:      map[string]time.Time{"run-ui": now.Add(-3 * time.Minute), "run-slack": now.Add(-2 * time.Minute), "run-a2a": now.Add(-time.Minute)},
		taskChats:  map[string]string{},
	}
	chats := []session.Session{
		sideChat("queued", session.OriginUI, session.StatusQueued, now.Add(-10*time.Minute)),
		sideChat("fresh", session.OriginSchedule, session.StatusIdle, time.Time{}),
		sideChat("run-a2a", session.Origin(a2aserver.Source), session.StatusIdle, now),
		sideChat("run-slack", session.OriginSlack, session.StatusIdle, now),
		sideChat("run-ui", session.OriginUI, session.StatusIdle, now),
	}
	working, last := workingSessionsOf(chats, "main", l, now)
	var ids, triggers, sources []string
	for _, w := range working {
		if w.Since == nil || w.Since.IsZero() {
			t.Fatalf("%s: no Since", w.ID)
		}
		ids, triggers, sources = append(ids, w.ID), append(triggers, w.Trigger), append(sources, w.Source)
	}
	if !slices.Equal(ids, []string{"run-ui", "run-slack", "run-a2a", "fresh"}) ||
		!slices.Equal(triggers, []string{"you", "", "", ""}) ||
		!slices.Equal(sources, []string{"", "Slack", "A2A", "Schedule"}) {
		t.Fatalf("working ids=%v triggers=%v sources=%v", ids, triggers, sources)
	}
	if last != nil {
		t.Fatalf("queued chat read as Last: %+v", last)
	}
}

// Last is the newest finished chat within the hour; older or expired ones
// are not.
func TestWorkingSessionsOfLast(t *testing.T) {
	now := time.Now()
	l := teamLive{lifecycles: map[string]string{}, since: map[string]time.Time{}, taskChats: map[string]string{"b": "Captain"}}
	chats := []session.Session{
		sideChat("a", session.OriginUI, session.StatusIdle, now.Add(-20*time.Minute)),
		sideChat("b", session.OriginUI, session.StatusIdle, now.Add(-5*time.Minute)),
		sideChat("old", session.OriginUI, session.StatusIdle, now.Add(-2*time.Hour)),
	}
	working, last := workingSessionsOf(chats, "main", l, now)
	if working != nil || last == nil || last.ID != "b" || last.Trigger != "Captain" {
		t.Fatalf("working=%v last=%+v", working, last)
	}
	if _, last = workingSessionsOf(chats[2:], "main", l, now); last != nil {
		t.Fatalf("expired chat read as Last: %+v", last)
	}
}

// A later first in-flight event is a new turn even when no read caught the
// chat idle in between: its start moves up instead of keeping the old one.
func TestNoteTurnsNewTurnWithoutIdleRead(t *testing.T) {
	now := time.Now()
	turnSince.Lock()
	turnSince.at = map[string]turnMark{}
	turnSince.Unlock()
	first := now.Add(-10 * time.Minute)
	noteTurns(map[string]string{"a": "working"}, map[string]time.Time{"a": first}, now.Add(-9*time.Minute))
	got := noteTurns(map[string]string{"a": "working"}, map[string]time.Time{"a": first}, now.Add(-5*time.Minute))
	if !got["a"].Equal(first) {
		t.Fatalf("same turn = %v, want %v", got["a"], first)
	}
	second := now.Add(-time.Minute)
	got = noteTurns(map[string]string{"a": "working"}, map[string]time.Time{"a": second}, now)
	if !got["a"].Equal(second) {
		t.Fatalf("next turn = %v, want %v", got["a"], second)
	}
}

// The activity stream names the agent only for the viewer's own non-main
// chat with it: not its main chat, not another person's chat.
func TestSideChatAgent(t *testing.T) {
	owner, p, helperMain, _ := receiverWorld(t)
	side := teamNewChat(t, owner, p.ID)
	of := sideChatAgent(owner.ID)
	if of(side) != p.ID || of(helperMain) != "" || of("nope") != "" {
		t.Fatalf("side=%q main=%q unknown=%q", of(side), of(helperMain), of("nope"))
	}
	if got := sideChatAgent("bob")(side); got != "" {
		t.Fatalf("bob reads %q", got)
	}
	tr := newActivityTracker(func(string) (string, bool) { return "", true }, func(string) bool { return true }, func(string) bool { return false })
	tr.sideAgent = of
	a, ok := tr.apply(Event{SessionID: side, Type: "lifecycle", Lifecycle: "working"})
	if !ok || a.AgentID != p.ID || a.Work != "thinking" {
		t.Fatalf("side activity = %+v %v", a, ok)
	}
	a, ok = tr.apply(Event{SessionID: side, Type: "lifecycle", Lifecycle: "idle"})
	if !ok || a.AgentID != p.ID || a.Work != "" {
		t.Fatalf("side end = %+v %v", a, ok)
	}
	if a, _ = tr.apply(Event{SessionID: helperMain, Type: "lifecycle", Lifecycle: "working"}); a.AgentID != "" {
		t.Fatalf("main chat carries agent_id %q", a.AgentID)
	}
	if !strings.Contains(sessionActivity{SessionID: "x", AgentID: "a1"}.JSON(), `"agent_id":"a1"`) || strings.Contains(sessionActivity{SessionID: "x"}.JSON(), "agent_id") {
		t.Fatal("agent_id JSON")
	}
}
