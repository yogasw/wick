package agents

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/teamlink"
)

type nmDir struct{}

var nmPeers = []teamlink.Peer{
	{ID: "nm-cap", OwnerID: "nm", Handle: "captain", Name: "Captain", IsCaptain: true},
	{ID: "nm-anton", OwnerID: "nm", Handle: "anton", Name: "Anton"},
	{ID: "nm-vera", OwnerID: "nm", Handle: "vera", Name: "Vera"},
}

func (nmDir) Peers(context.Context, string) ([]teamlink.Peer, error) { return nmPeers, nil }
func (nmDir) Get(_ context.Context, id string) (teamlink.Peer, error) {
	for _, p := range nmPeers {
		if p.ID == id {
			return p, nil
		}
	}
	return teamlink.Peer{}, errors.New("nope")
}

// nmTurns: each teammate answers once its line is released.
type nmTurns struct{ anton, vera chan struct{} }

func (t nmTurns) Run(_ context.Context, a teamlink.Peer, _ string) (string, string, error) {
	if a.Handle == "vera" {
		<-t.vera
	} else {
		<-t.anton
	}
	return "sess-" + a.ID, a.Handle + ": done", nil
}

type nmNotify struct {
	mu  sync.Mutex
	got []string
}

func (n *nmNotify) Deliver(_ context.Context, s, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.got = append(n.got, s+"|"+text)
	return nil
}
func (n *nmNotify) Audit(context.Context, string, teamlink.Handoff) {}
func (n *nmNotify) count() int                                      { n.mu.Lock(); defer n.mu.Unlock(); return len(n.got) }

// A person's message into the sender's chat opens the fan-in barrier; a
// message nobody typed (a team delivery, REST, A2A) does not.
func TestNoteSessionMessageFlushesOnAPersonsMessage(t *testing.T) {
	vera, antonLine := make(chan struct{}), make(chan struct{})
	defer close(vera)
	n := &nmNotify{}
	hub := teamlink.NewHub(nmDir{}, nmTurns{anton: antonLine, vera: vera}, n)
	hub.BatchResults = func() bool { return true }
	t.Cleanup(hub.StopBatchSweep)
	prev := globalTeamHub
	SetTeamHub(func() *teamlink.Hub { return hub })
	t.Cleanup(func() { globalTeamHub = prev })

	ctx := context.Background()
	var anton string
	for _, to := range []string{"anton", "vera"} {
		res, err := hub.Send(ctx, teamlink.SendInput{CallerSession: "nm-sess", CallerAgentID: "nm-cap", To: to, Text: "check", Wait: -1})
		if err != nil {
			t.Fatal(err)
		}
		if to == "anton" {
			anton = res.TaskID
		}
	}
	close(antonLine) // after both were sent: its reply is held, not read inline
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if r, err := hub.GetTask(ctx, "nm-cap", anton); err == nil && r.State == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("anton never replied")
		}
	}
	note := func(c context.Context, role string) {
		done, err := NoteSessionMessage(c, "nm-sess", role)
		if err != nil {
			t.Fatal(err)
		}
		done(true)
	}
	note(ctx, "user")                                  // a team delivery or REST: no mark
	note(teamlink.WithPersonMessage(ctx), "assistant") // not a user turn
	time.Sleep(50 * time.Millisecond)
	if c := n.count(); c != 0 {
		t.Fatalf("flushed without a person's message: %d", c)
	}
	note(teamlink.WithPersonMessage(ctx), "user")
	for deadline := time.Now().Add(2 * time.Second); n.count() == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a person's message did not flush")
		}
	}
	n.mu.Lock()
	got := n.got[0]
	n.mu.Unlock()
	if !strings.Contains(got, "anton: done") || !strings.Contains(got, "Still working: Vera (@vera)") {
		t.Fatalf("flush = %s", got)
	}
}
