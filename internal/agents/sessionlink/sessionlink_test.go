package sessionlink

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
)

func newLayout(t *testing.T) config.Layout {
	t.Helper()
	l := config.NewLayout(t.TempDir())
	if err := l.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return l
}

func mk(t *testing.T, l config.Layout, id, label string) {
	t.Helper()
	if _, err := session.Create(context.Background(), l, session.CreateOptions{ID: id, Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}
	s, _ := session.Load(l, id)
	s.Meta.Label = label
	if err := session.SaveMeta(l, id, s.Meta); err != nil {
		t.Fatal(err)
	}
}

func setStatus(t *testing.T, l config.Layout, id string, st session.Status) {
	t.Helper()
	s, _ := session.Load(l, id)
	s.Meta.Status = st
	s.Meta.LastActive = time.Now().UTC()
	if err := session.SaveMeta(l, id, s.Meta); err != nil {
		t.Fatal(err)
	}
}

func TestLinkIsBothSidesAndIdempotent(t *testing.T) {
	l := newLayout(t)
	mk(t, l, "S-main", "Main thread")
	mk(t, l, "S-card", "Card thread")
	if err := Link(l, "S-main", "S-card", "card from this thread"); err != nil {
		t.Fatal(err)
	}
	if err := Link(l, "S-card", "S-main", ""); err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]string{{"S-main", "S-card"}, {"S-card", "S-main"}} {
		peers, err := Peers(l, p[0])
		if err != nil || len(peers) != 1 || peers[0].Session != p[1] || peers[0].Why != "card from this thread" {
			t.Fatalf("%s peers = %+v, %v", p[0], peers, err)
		}
	}
	if err := Link(l, "S-main", "S-main", "x"); err != ErrSelf {
		t.Fatalf("self link: %v", err)
	}
	if err := Unlink(l, "S-card", "S-main"); err != nil {
		t.Fatal(err)
	}
	if peers, _ := Peers(l, "S-main"); len(peers) != 0 {
		t.Fatalf("unlink left %+v", peers)
	}
}

// The Team pairing field reads as a link without being touched.
func TestLinkedFromSessionCounts(t *testing.T) {
	l := newLayout(t)
	mk(t, l, "S-asker", "Asker")
	mk(t, l, "S-chat", "Chat")
	s, _ := session.Load(l, "S-chat")
	s.Meta.LinkedFromSession = "S-asker"
	_ = session.SaveMeta(l, "S-chat", s.Meta)
	peers, _ := Peers(l, "S-chat")
	if len(peers) != 1 || peers[0].Session != "S-asker" {
		t.Fatalf("peers = %+v", peers)
	}
}

// Only a linked session working right now makes the turn note.
func TestNoteOnlyRunning(t *testing.T) {
	l := newLayout(t)
	mk(t, l, "S-a", "Main thread")
	mk(t, l, "S-b", "Card thread")
	_ = Link(l, "S-a", "S-b", "same ticket T-1")
	if n := Note(l, "S-a", time.Now()); n != "" {
		t.Fatalf("idle peer made a note: %q", n)
	}
	setStatus(t, l, "S-b", session.StatusRunning)
	n := Note(l, "S-a", time.Now())
	if !strings.HasPrefix(n, "[linked]") || !strings.Contains(n, "Card thread (S-b) — WORKING NOW") || !strings.Contains(n, "same ticket T-1") {
		t.Fatalf("note = %q", n)
	}
	if p := Prompt(l, "S-a"); !strings.Contains(p, "## Linked sessions") || !strings.Contains(p, "Card thread (S-b)") {
		t.Fatalf("prompt = %q", p)
	}
	if p := Prompt(l, "S-lonely"); p != "" {
		t.Fatalf("missing session must give no prompt: %q", p)
	}
}

// A thread announced from a session links the session born there, once.
func TestLinkThreadAdoptedByNewSession(t *testing.T) {
	l := newLayout(t)
	mk(t, l, "S-origin", "Origin")
	if err := LinkThread(l, "S-origin", "", "C1", "200.1", "asked ops"); err != nil {
		t.Fatal(err)
	}
	peers, _ := Peers(l, "S-origin")
	if len(peers) != 1 || peers[0].State != StatePending || peers[0].Thread != "slack:C1/200.1" {
		t.Fatalf("pending thread not listed: %+v", peers)
	}
	mk(t, l, "S-thread", "Thread")
	s, _ := session.Load(l, "S-thread")
	s.Meta.ChannelRef = &session.ChannelRef{Channel: "slack", ChatID: "C1", ThreadID: "200.1"}
	_ = session.SaveMeta(l, "S-thread", s.Meta)
	peers, _ = Peers(l, "S-thread")
	if len(peers) != 1 || peers[0].Session != "S-origin" || !strings.Contains(peers[0].Why, "asked ops") {
		t.Fatalf("thread session not linked: %+v", peers)
	}
	peers, _ = Peers(l, "S-origin")
	if len(peers) != 1 || peers[0].Session != "S-thread" {
		t.Fatalf("origin must now see the thread session, not the pending entry: %+v", peers)
	}
}

func TestReadLastTurns(t *testing.T) {
	l := newLayout(t)
	mk(t, l, "S-r", "Reader")
	for i, txt := range []string{"one", "two", "three"} {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		turn := store.ConversationTurn{TurnID: txt, Role: role, Text: txt, Timestamp: time.Now()}
		if err := storage.AppendJSONL(l.SessionConversation("S-r"), "wick-conv-v1", "S-r", turn); err != nil {
			t.Fatal(err)
		}
	}
	turns, err := Read(l, "S-r", 2, 100)
	if err != nil || len(turns) != 2 || turns[0].Text != "two" || turns[1].Text != "three" {
		t.Fatalf("turns = %+v, %v", turns, err)
	}
	if IsLinked(l, "S-r", "S-x") {
		t.Fatal("unexpected link")
	}
}

// A Team pairing writes a reason per side, and a session of another
// agent reads its asker through neither the pairing nor the link.
func TestReadableAcrossTeamDirection(t *testing.T) {
	l := newLayout(t)
	mk(t, l, "S-asker", "Asker")
	mk(t, l, "S-chat", "Chat")
	mk(t, l, "S-ticket", "Ticket peer")
	mk(t, l, "S-stranger", "Stranger")
	if err := LinkTeam(l, "S-asker", "S-chat"); err != nil {
		t.Fatal(err)
	}
	if err := Link(l, "S-chat", "S-ticket", "same ticket T-1"); err != nil {
		t.Fatal(err)
	}
	load := func(id string) session.Session {
		s, err := session.Load(l, id)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if w := load("S-asker").Meta.Links[0].Why; w != WhyTeamTo {
		t.Fatalf("asker side why = %q", w)
	}
	if w := load("S-chat").Meta.Links[0].Why; w != WhyTeamFrom {
		t.Fatalf("chat side why = %q", w)
	}
	cases := []struct {
		self, peer string
		want       bool
	}{
		{"S-chat", "S-asker", false},
		{"S-asker", "S-chat", true},
		{"S-chat", "S-ticket", true},
		{"S-chat", "S-stranger", false},
	}
	for _, c := range cases {
		if got := ReadableAcross(load(c.self), load(c.peer)); got != c.want {
			t.Errorf("ReadableAcross(%s, %s) = %v, want %v", c.self, c.peer, got, c.want)
		}
	}

	// A pre-split pairing: the chat that holds LinkedFromSession is the
	// one asked, whatever the shared reason says.
	mk(t, l, "S-old-asker", "Old asker")
	mk(t, l, "S-old-chat", "Old chat")
	if err := Link(l, "S-old-asker", "S-old-chat", WhyTeamLegacy); err != nil {
		t.Fatal(err)
	}
	s := load("S-old-chat")
	s.Meta.LinkedFromSession = "S-old-asker"
	_ = session.SaveMeta(l, "S-old-chat", s.Meta)
	if ReadableAcross(load("S-old-chat"), load("S-old-asker")) {
		t.Error("legacy: chat read its asker")
	}
	if !ReadableAcross(load("S-old-asker"), load("S-old-chat")) {
		t.Error("legacy: asker could not read the chat it messaged")
	}
	s.Meta.LinkedFromSession = ""
	_ = session.SaveMeta(l, "S-old-chat", s.Meta)
	if ReadableAcross(load("S-old-chat"), load("S-old-asker")) {
		t.Error("legacy, pairing moved on: chat read its asker")
	}
}
