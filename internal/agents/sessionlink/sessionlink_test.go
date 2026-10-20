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
