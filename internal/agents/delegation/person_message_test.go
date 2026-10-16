package delegation

import (
	"context"
	"fmt"
	"testing"

	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
)

// gateSteerer hands every delivery to a team task gate the way the pool's
// OnSend does, so what the gate makes of a message is observable.
type gateSteerer struct{ g *teamlink.TurnGate }

func (s gateSteerer) SendToChild(ctx context.Context, childSessionID, _, _ string) error {
	done, err := s.g.NoteMessage(ctx, childSessionID, "user")
	if err != nil {
		return err
	}
	done(true)
	return nil
}

// taskTurnIn starts team task "t1"'s turn in sessionID on a fresh gate and
// returns the cancel's outcome once deliver has run.
func taskTurnIn(t *testing.T, sessionID string, deliver func(*teamlink.TurnGate) error) teamlink.TaskStop {
	t.Helper()
	g := teamlink.NewTurnGate(nil)
	release, err := g.Acquire(context.Background(), sessionID, "t1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := deliver(g); err != nil {
		t.Fatal(err)
	}
	out, err := g.StopWith(sessionID, "t1", func() (teamlink.TaskStop, error) { return teamlink.TaskStopRunning, nil })
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// seedIdleWorker is seedLiveWorker with @worker between turns, so a
// message to it is delivered at once.
func seedIdleWorker(t *testing.T, r *Repo) {
	t.Helper()
	seedLiveWorker(t, r)
	w, err := r.Get(context.Background(), "d1")
	if err != nil {
		t.Fatal(err)
	}
	w.Status = entity.DelegationDone
	if err := r.SaveDelegationForTest(context.Background(), w); err != nil {
		t.Fatal(err)
	}
}

// A peer's message reaching a team task's turn is agent work: the cancel
// still stops that turn.
func TestAgentInboxMessageLeavesTaskTurnStoppable(t *testing.T) {
	s, r := routerService(t)
	seedIdleWorker(t, r)
	out := taskTurnIn(t, "sub-d1", func(g *teamlink.TurnGate) error {
		s.Steerer = gateSteerer{g}
		_, err := s.SendMessage(context.Background(), SendInput{
			RootID: "root-1", FromHandle: entity.LeaderHandle, ToHandle: "worker",
			Body: "here is the file list", Kind: entity.MessageTell,
		})
		return err
	})
	if out != teamlink.TaskStopRunning {
		t.Fatalf("stop = %v, want running: an agent's message is not a person's", out)
	}
}

// An agent's own @mention is agent work too.
func TestAgentMentionLeavesTaskTurnStoppable(t *testing.T) {
	s, r := routerService(t)
	seedIdleWorker(t, r)
	out := taskTurnIn(t, "sub-d1", func(g *teamlink.TurnGate) error {
		s.Steerer = gateSteerer{g}
		// The mention must really reach the turn, or the stop below
		// proves nothing.
		ds := s.Route(context.Background(), RouteInput{SessionID: "parent", FromHandle: entity.LeaderHandle, Text: "@worker keep going"})
		if len(ds) != 1 || ds[0].Err != "" {
			return fmt.Errorf("route = %+v, want one delivered dispatch", ds)
		}
		return nil
	})
	if out != teamlink.TaskStopRunning {
		t.Fatalf("stop = %v, want running", out)
	}
}

// A person's @mention reaching a team task's turn keeps it running.
func TestPersonMentionSharesTaskTurn(t *testing.T) {
	s, r := routerService(t)
	seedIdleWorker(t, r)
	out := taskTurnIn(t, "sub-d1", func(g *teamlink.TurnGate) error {
		s.Steerer = gateSteerer{g}
		s.Route(context.Background(), RouteInput{SessionID: "parent", FromHandle: entity.LeaderHandle, Human: true, Text: "@worker what have you got?"})
		return nil
	})
	if out != teamlink.TaskStopShared {
		t.Fatalf("stop = %v, want shared: a person's mention reached the turn", out)
	}
}

// A take-over is a person typing into the sub-agent.
func TestTakeOverSharesTaskTurn(t *testing.T) {
	s, r, _ := newService(t)
	p := seedProfile(t, r, "researcher")
	p.AllowTakeOver = true
	if err := r.SaveProfile(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	seedDelegation(t, r, "d1", "root-1", entity.DelegationRunning, 1)
	out := taskTurnIn(t, "sub-d1", func(g *teamlink.TurnGate) error {
		s.Steerer = gateSteerer{g}
		return s.TakeOver(context.Background(), "d1", "user-1", "focus on v4", false)
	})
	if out != teamlink.TaskStopShared {
		t.Fatalf("stop = %v, want shared", out)
	}
}
