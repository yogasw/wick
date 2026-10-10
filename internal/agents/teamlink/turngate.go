package teamlink

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// ErrTaskCanceled is what a turn reports for a task canceled before its
// turn started: nothing ran for it.
var ErrTaskCanceled = errors.New("the task was canceled before its turn started")

// TaskStop is what TaskStopper found when it canceled a task's turn.
type TaskStop int

const (
	// TaskStopNotFound: the session holds nothing of the task — its turn
	// already ended, or it has not reached the session yet.
	TaskStopNotFound TaskStop = iota
	// TaskStopRunning: the running turn was the task's, and was stopped.
	TaskStopRunning
	// TaskStopQueued: the task was still waiting for its turn and was
	// dropped; the session was not running anything else.
	TaskStopQueued
	// TaskStopBehindOther: the task was waiting behind a turn of another
	// conversation (a person chatting with the teammate, another task);
	// it was dropped and that turn was left running.
	TaskStopBehindOther
	// TaskStopShared: the running turn was the task's, but a message from
	// someone else is queued behind it in the same session (and may be
	// merged into it), so it was left running; whatever it reports for
	// the task is ignored. Only a provider that queues messages apart can
	// tell; on one that appends them to the running turn the turn is
	// stopped (TaskStopRunning) with that message in it.
	TaskStopShared
)

// droppedTTL is how long a task canceled before the gate saw it stays
// remembered: its turn asks for the gate right after the cancel, or never
// (it already ran).
const droppedTTL = 10 * time.Minute

// TaskStopper is optionally implemented by Turns: it stops the turn
// serving task id in sessionID, or drops the task while it still waits
// for one, and never stops a turn that belongs to anything else. A
// message someone typed into the session while the task's turn runs is
// spared only where the provider queues it apart (TaskStopShared); where
// it is appended to that turn, it is stopped with it. by names who
// canceled.
type TaskStopper interface {
	StopTask(ctx context.Context, agent Peer, sessionID string, id a2a.TaskID, by string) (TaskStop, error)
}

type taskIDKey struct{}

// withTaskID tags ctx with the task a turn is run for.
func withTaskID(ctx context.Context, id a2a.TaskID) context.Context {
	return context.WithValue(ctx, taskIDKey{}, id)
}

// TaskIDFrom is the task a Turns call runs for, "" for a turn that is
// no team task.
func TaskIDFrom(ctx context.Context) a2a.TaskID {
	id, _ := ctx.Value(taskIDKey{}).(a2a.TaskID)
	return id
}

// TurnGate lines up the team tasks a session answers so each task's turn
// is known: a task waits until the session runs nothing else and no other
// task holds it, then owns the turn it starts. Canceling a task can then
// stop exactly that turn, or drop the task while it waits, and leave
// every other turn of the session alone.
type TurnGate struct {
	// busy reports whether a session runs, or is about to run, a turn
	// the gate did not start. Nil means never.
	busy func(sessionID string) bool
	// poll is how often a waiting task looks again. Tests shorten it.
	poll time.Duration

	mu       sync.Mutex
	sessions map[string]*gateSession
	// dropped are tasks canceled before they asked for their turn, with
	// when; pruned after droppedTTL.
	dropped map[a2a.TaskID]time.Time
}

type gateSession struct {
	owner   a2a.TaskID
	waiting []*gateWaiter
}

type gateWaiter struct {
	id       a2a.TaskID
	canceled chan struct{}
}

// NewTurnGate returns a gate over busy (see TurnGate).
func NewTurnGate(busy func(sessionID string) bool) *TurnGate {
	return &TurnGate{busy: busy, poll: 250 * time.Millisecond, sessions: map[string]*gateSession{}, dropped: map[a2a.TaskID]time.Time{}}
}

func (g *TurnGate) isBusy(sessionID string) bool { return g.busy != nil && g.busy(sessionID) }

// Acquire waits until task id may start its turn in sessionID, first come
// first served, and returns the release to call once that turn ended.
// ErrTaskCanceled when the task was canceled meanwhile. A turn with no
// task id is not gated.
func (g *TurnGate) Acquire(ctx context.Context, sessionID string, id a2a.TaskID) (func(), error) {
	if id == "" || sessionID == "" {
		return func() {}, nil
	}
	w := &gateWaiter{id: id, canceled: make(chan struct{})}
	g.mu.Lock()
	if _, ok := g.dropped[id]; ok {
		delete(g.dropped, id)
		g.mu.Unlock()
		return nil, ErrTaskCanceled
	}
	s := g.sessions[sessionID]
	if s == nil {
		s = &gateSession{}
		g.sessions[sessionID] = s
	}
	s.waiting = append(s.waiting, w)
	g.mu.Unlock()
	for {
		busy := g.isBusy(sessionID)
		g.mu.Lock()
		if s.owner == "" && len(s.waiting) > 0 && s.waiting[0] == w && !busy {
			s.owner, s.waiting = id, s.waiting[1:]
			g.mu.Unlock()
			var once sync.Once
			return func() { once.Do(func() { g.release(sessionID, id) }) }, nil
		}
		g.mu.Unlock()
		select {
		case <-w.canceled:
			return nil, ErrTaskCanceled
		case <-ctx.Done():
			g.mu.Lock()
			g.removeLocked(sessionID, w)
			g.mu.Unlock()
			return nil, ctx.Err()
		case <-time.After(g.poll):
		}
	}
}

func (g *TurnGate) release(sessionID string, id a2a.TaskID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.sessions[sessionID]; s != nil && s.owner == id {
		s.owner = ""
		if len(s.waiting) == 0 {
			delete(g.sessions, sessionID)
		}
	}
}

// removeLocked takes w out of sessionID's line; false when it is not in it.
func (g *TurnGate) removeLocked(sessionID string, w *gateWaiter) bool {
	s := g.sessions[sessionID]
	if s == nil {
		return false
	}
	for i, q := range s.waiting {
		if q == w {
			s.waiting = append(s.waiting[:i:i], s.waiting[i+1:]...)
			if s.owner == "" && len(s.waiting) == 0 {
				delete(g.sessions, sessionID)
			}
			return true
		}
	}
	return false
}

// Stop cancels task id in sessionID: TaskStopRunning when it owns the
// running turn (the caller stops that turn), TaskStopQueued or
// TaskStopBehindOther when it was dropped from the line, depending on
// whether something else is running there. A task the gate has not seen
// yet is remembered, so its Acquire refuses it.
func (g *TurnGate) Stop(sessionID string, id a2a.TaskID) TaskStop {
	if id == "" {
		return TaskStopNotFound
	}
	busy := g.isBusy(sessionID)
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.sessions[sessionID]
	if s != nil && s.owner == id {
		return TaskStopRunning
	}
	if s != nil {
		for _, w := range s.waiting {
			if w.id != id {
				continue
			}
			other := busy || s.owner != ""
			g.removeLocked(sessionID, w)
			close(w.canceled)
			if other {
				return TaskStopBehindOther
			}
			return TaskStopQueued
		}
	}
	now := time.Now()
	for d, at := range g.dropped {
		if now.Sub(at) > droppedTTL {
			delete(g.dropped, d)
		}
	}
	g.dropped[id] = now
	return TaskStopNotFound
}
