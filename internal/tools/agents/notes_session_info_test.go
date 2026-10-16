package agents

import (
	"os"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/entity"
)

func TestSessionPeople(t *testing.T) {
	turns := []store.ConversationTurn{
		{Role: "user", Text: "typed before senders were recorded"},
		{Role: "assistant", Text: "reply"},
		{Role: "user", Sender: &store.Sender{ID: "u-owner", WickUserID: "u-owner", Channel: "ui", Name: "Yoga"}},
		{Role: "user", Sender: &store.Sender{ID: "U0104", Channel: "slack", Name: "Hana"}},
		{Role: "system", Text: "operator note"},
		{Role: "user", Sender: &store.Sender{ID: "U0104", Channel: "slack"}},
		{Role: "user", Sender: &store.Sender{ID: "U0104", Channel: "slack"}},
	}
	got := sessionPeople(turns, "u-owner")
	if len(got) != 2 {
		t.Fatalf("people = %+v, want 2", got)
	}
	// Most messages first: Hana (3) before the owner (2, one unattributed).
	if got[0].ID != "slack:U0104" || got[0].Messages != 3 || got[0].Name != "Hana" || got[0].Owner {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].ID != "u-owner" || got[1].Messages != 2 || !got[1].Owner {
		t.Errorf("second = %+v", got[1])
	}
}

func TestSessionPeopleNoOwnerSkipsAnonymous(t *testing.T) {
	got := sessionPeople([]store.ConversationTurn{{Role: "user", Text: "x"}}, "")
	if len(got) != 0 {
		t.Fatalf("people = %+v, want none", got)
	}
}

// Agents are counted over the whole conversation, keyed by agent and
// owner like the rail's list; turns with no owner are not another person's
// agent and do not count.
func TestSessionAgents(t *testing.T) {
	turns := []store.ConversationTurn{
		{Role: "assistant", Speaker: &store.Speaker{AgentID: "a1", Handle: "captain"}},
		{Role: "assistant", Speaker: &store.Speaker{AgentID: "a1", Handle: "captain", OwnerID: "hana", OwnerHandle: "hana", Via: "group"}},
		{Role: "user", Text: "x"},
		{Role: "assistant", Speaker: &store.Speaker{AgentID: "a1", Handle: "captain", OwnerID: "dana"}},
		{Role: "assistant", Speaker: &store.Speaker{AgentID: "a1", Handle: "captain", OwnerID: "hana"}},
	}
	got := sessionAgents(turns)
	if len(got) != 2 || got[0].OwnerID != "hana" || got[0].Replies != 2 || got[0].OwnerHandle != "hana" ||
		got[1].OwnerID != "dana" || got[1].Replies != 1 {
		t.Fatalf("agents = %+v", got)
	}
}

// A member's backing session in a group or ticket room is theirs alone:
// another project member who names its id gets no Info, people or agents.
func TestAddSessionInfoOnlyForWhoCanOpenTheSession(t *testing.T) {
	withSessionWorld(t, []seededSession{{id: "s-own", userID: "alice"}})
	alice := &entity.User{ID: "alice", Role: entity.RoleUser, Approved: true}
	backing := session.Session{ID: "s-backing", Meta: session.Meta{ProjectID: "p1", UserID: "bob", GroupSessionID: "g1"}}
	_, c := userCtx(t, alice, "/api/notes?session_id=s-backing", nil)
	out := map[string]any{}
	addSessionInfo(c, out, backing)
	for _, k := range []string{"session_info", "session_people", "session_agents"} {
		if _, ok := out[k]; ok {
			t.Errorf("%s handed to a non-owner of a backing session", k)
		}
	}

	own, ok := globalMgr.Registry().Session("s-own")
	if !ok {
		t.Fatal("s-own missing")
	}
	out = map[string]any{}
	addSessionInfo(c, out, own)
	if _, ok := out["session_people"]; !ok {
		t.Fatal("the owner gets no session_people")
	}
}

// The tally is cached per session and re-read once the conversation file
// changes, so a reload of the rail does not parse an unchanged history.
func TestTallySessionFollowsTheFile(t *testing.T) {
	withSessionWorld(t, []seededSession{{id: "s1", userID: "alice"}})
	sess, _ := globalMgr.Registry().Session("s1")
	path := globalLayout.SessionConversation("s1")
	write := func(lines ...string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"role":"user","ts":"2026-08-01T00:00:00Z"}`)
	people, _ := tallySession(sess)
	if len(people) != 1 || people[0].Messages != 1 {
		t.Fatalf("first read: %+v", people)
	}
	people[0].Name = "mutated"
	if again, _ := tallySession(sess); again[0].Name == "mutated" {
		t.Fatal("cached people were handed out by reference")
	}
	write(`{"role":"user","ts":"2026-08-01T00:00:00Z"}`, `{"role":"user","ts":"2026-08-01T00:00:01Z"}`)
	if people, _ = tallySession(sess); len(people) != 1 || people[0].Messages != 2 {
		t.Fatalf("after a write: %+v", people)
	}
}
