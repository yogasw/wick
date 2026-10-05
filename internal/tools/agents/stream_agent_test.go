package agents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// withAgentSignals wires the change hook and a fresh broadcaster onto the
// team world, and returns the global bus subscription.
func withAgentSignals(t *testing.T) <-chan Event {
	t.Helper()
	withTeamWorld(t)
	prev := globalBcast
	globalBcast = NewBroadcaster()
	globalTeam.SetChangeHook(agentChangeHook)
	ch, unsub := globalBcast.Subscribe("")
	t.Cleanup(func() { unsub(); globalBcast = prev })
	return ch
}

// teamWorldDB reopens withTeamWorld's shared in-memory database, for the
// tag rows no team API writes.
func teamWorldDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// signalled is who, among uids, receives an agent_changed for agentID in
// evs — read the way /stream/sessions reads it.
func signalled(evs []Event, agentID string, uids ...string) map[string]bool {
	out := map[string]bool{}
	for _, ev := range evs {
		if ev.Type != evAgentChanged {
			continue
		}
		for _, uid := range uids {
			if data, ok := projectAgentSignal(ev, uid); ok {
				if data != `{"agent_id":"`+agentID+`"}` {
					panic("signal carries more than the id: " + data)
				}
				out[uid] = true
			}
		}
	}
	return out
}

func wantSignalled(t *testing.T, step string, got map[string]bool, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: signalled %v, want %v", step, got, want)
	}
	for _, uid := range want {
		if !got[uid] {
			t.Fatalf("%s: signalled %v, want %v", step, got, want)
		}
	}
}

func TestAgentChangedReachesOnlyWhoSeesTheAgent(t *testing.T) {
	ch := withAgentSignals(t)
	ctx := context.Background()
	users := []string{"u1", "bob", "carol", "dave"}
	for _, uid := range users {
		t.Cleanup(addStreamViewer(uid))
	}
	seedTeamProject(t, "p1", "u1")
	p := seedTeamAgent(t, "u1", "helper", "p1")
	wantSignalled(t, "create", signalled(drain(ch), p.ID, users...), "u1")

	// A hand share reaches the owner and the new recipient, not carol.
	if err := globalTeam.AddShare(ctx, p.ID, "bob", "u1"); err != nil {
		t.Fatal(err)
	}
	wantSignalled(t, "share", signalled(drain(ch), p.ID, users...), "u1", "bob")

	// A tag share: dave holds a filter tag the agent is shared to.
	db := teamWorldDB(t)
	if err := db.Create(&entity.Tag{ID: "t-team", Name: "team", IsFilter: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&entity.UserTag{UserID: "dave", TagID: "t-team"}).Error; err != nil {
		t.Fatal(err)
	}
	saved := TrackTeamAgentChange(ctx, p.ID)
	if err := db.Create(&entity.ToolTag{ToolPath: team.TagSharePath(p.ID), TagID: "t-team"}).Error; err != nil {
		t.Fatal(err)
	}
	saved()
	wantSignalled(t, "tag share", signalled(drain(ch), p.ID, users...), "u1", "bob", "dave")

	// An edit reaches everyone who sees it.
	p.Handle = "helper-2"
	if err := globalTeam.Update(ctx, &p); err != nil {
		t.Fatal(err)
	}
	wantSignalled(t, "update", signalled(drain(ch), p.ID, users...), "u1", "bob", "dave")

	// Unsharing still tells bob, whose roster loses it.
	if err := globalTeam.RemoveShare(ctx, p.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	wantSignalled(t, "unshare", signalled(drain(ch), p.ID, users...), "u1", "bob", "dave")

	// So does dropping the tag, for dave.
	saved = TrackTeamAgentChange(ctx, p.ID)
	if err := db.Where("tool_path = ?", team.TagSharePath(p.ID)).Delete(&entity.ToolTag{}).Error; err != nil {
		t.Fatal(err)
	}
	saved()
	wantSignalled(t, "untag", signalled(drain(ch), p.ID, users...), "u1", "dave")

	// Delete reaches the recipients it had at the time.
	if err := globalTeam.AddShare(ctx, p.ID, "bob", "u1"); err != nil {
		t.Fatal(err)
	}
	drain(ch)
	if err := globalTeam.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	wantSignalled(t, "delete", signalled(drain(ch), p.ID, users...), "u1", "bob")
}

// Nobody connected, nothing published.
func TestAgentChangedSkippedWithoutViewers(t *testing.T) {
	ch := withAgentSignals(t)
	seedTeamProject(t, "p1", "u1")
	seedTeamAgent(t, "u1", "helper", "p1")
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("published %+v with no viewer", got)
	}
}

// A failed write signals nobody.
func TestAgentChangedNotSentOnFailedWrite(t *testing.T) {
	ch := withAgentSignals(t)
	t.Cleanup(addStreamViewer("u1"))
	if err := globalTeam.Delete(context.Background(), "missing"); err == nil {
		t.Fatal("delete of a missing agent succeeded")
	}
	for _, ev := range drain(ch) {
		if ev.Type == evAgentChanged {
			t.Fatalf("failed write signalled: %+v", ev)
		}
	}
}

// End to end over GET /stream/sessions: the recipient gets the bare
// signal, a stranger gets nothing.
func TestSessionsStreamAgentChanged(t *testing.T) {
	withAgentSignals(t)
	seedTeamProject(t, "p1", "u1")
	p := seedTeamAgent(t, "u1", "helper", "p1")

	open := func(uid string) (*httptest.ResponseRecorder, func()) {
		ctx, cancel := context.WithCancel(context.Background())
		r := httptest.NewRequest(http.MethodGet, "/stream/sessions", nil).WithContext(ctx)
		r = r.WithContext(login.WithUser(r.Context(), &entity.User{ID: uid}, nil))
		w := httptest.NewRecorder()
		c := tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); sessionsLifecycleSSE(c) }()
		return w, func() { cancel(); wg.Wait() }
	}
	bobW, closeBob := open("bob")
	carolW, closeCarol := open("carol")
	time.Sleep(50 * time.Millisecond)

	if err := globalTeam.AddShare(context.Background(), p.ID, "bob", "u1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	closeBob()
	closeCarol()

	if want := "event: agent_changed\ndata: {\"agent_id\":\"" + p.ID + "\"}\n\n"; !strings.Contains(bobW.Body.String(), want) {
		t.Fatalf("bob stream = %q", bobW.Body.String())
	}
	if strings.Contains(carolW.Body.String(), "agent_changed") {
		t.Fatalf("carol got a signal for an agent she cannot see: %q", carolW.Body.String())
	}
	if ids := streamViewerIDs(); len(ids) != 0 {
		t.Fatalf("viewers left after close: %v", ids)
	}
}

// A group change reaches its owner only.
func TestGroupChangedReachesOwnerOnly(t *testing.T) {
	ch := withAgentSignals(t)
	publishGroupChanged("g1", "u1")
	got := drain(ch)
	if len(got) != 1 {
		t.Fatalf("signals = %+v", got)
	}
	if data, ok := projectAgentSignal(got[0], "u1"); !ok || data != `{"group_id":"g1"}` {
		t.Fatalf("owner got %q %v", data, ok)
	}
	if _, ok := projectAgentSignal(got[0], "bob"); ok {
		t.Fatal("bob got another user's group signal")
	}
}
