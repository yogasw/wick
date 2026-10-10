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
	// TaskStopShared: the running turn was the task's, but a message a
	// person sent (WithPersonMessage) reached the session while it ran —
	// queued behind it, or written into it by a provider that appends — so
	// it was left running to answer that message; whatever it reports for
	// the task is ignored.
	TaskStopShared
)

// droppedTTL is how long a task canceled before the gate saw it stays
// remembered: its turn asks for the gate right after the cancel, or never
// (it already ran).
const droppedTTL = 10 * time.Minute

// stopWait caps how long a message waits for a task's turn being stopped
// in its session, so it reaches the session after the stop rather than
// the turn being stopped. The stop ends that wait as soon as it returns;
// the cap only lets messages through a stop that never does, and is set
// well above a stop of every agent of a session.
const stopWait = 2 * time.Minute

// TaskStopper is optionally implemented by Turns: it stops the turn
// serving task id in sessionID, or drops the task while it still waits
// for one, and never stops a turn that belongs to anything else. A
// turn a message a person sent reached while it ran is left running
// (TaskStopShared), so that message is still answered. by names who
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

type personMessageKey struct{}

// WithPersonMessage tags ctx as carrying a message a person sent (typed
// in the composer, a channel, the CLI, a card click), set where such a
// message enters. Only those keep a task's turn they reach from being
// stopped (TaskStopShared). Untagged messages — a sub-agent's result, a
// teammate's reply, a notice wick posts — are most often the running
// task's own work coming back, so a cancel stops the turn as before.
func WithPersonMessage(ctx context.Context) context.Context {
	return context.WithValue(ctx, personMessageKey{}, true)
}

func personMessage(ctx context.Context) bool {
	v, _ := ctx.Value(personMessageKey{}).(bool)
	return v
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
	// stopWait is the cap of NoteMessage's wait for a stop (stopWait).
	// Tests shorten it.
	stopWait time.Duration

	mu       sync.Mutex
	sessions map[string]*gateSession
	// dropped are tasks canceled before they asked for their turn, with
	// when; pruned after droppedTTL.
	dropped map[a2a.TaskID]time.Time
	// stopping holds, per session, a channel closed once the task turn
	// being stopped there is (StopWith); messages wait for it.
	stopping map[string]chan struct{}
}

type gateSession struct {
	owner   a2a.TaskID
	waiting []*gateWaiter
	// shared: a message a person sent reached the session while the
	// owner's turn runs (NoteMessage). Cleared with the owner.
	shared bool
	// inflight counts messages on their way into the session (between
	// NoteMessage and its done), persons those of them a person sent.
	// No task takes the session while one is on its way, and a person's
	// keeps the owner's turn from being stopped.
	inflight, persons int
}

type gateWaiter struct {
	id       a2a.TaskID
	canceled chan struct{}
}

// NewTurnGate returns a gate over busy (see TurnGate).
func NewTurnGate(busy func(sessionID string) bool) *TurnGate {
	return &TurnGate{busy: busy, poll: 250 * time.Millisecond, stopWait: stopWait, sessions: map[string]*gateSession{}, dropped: map[a2a.TaskID]time.Time{}, stopping: map[string]chan struct{}{}}
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
	g.pruneDroppedLocked(time.Now())
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
		if s.owner == "" && len(s.waiting) > 0 && s.waiting[0] == w && !busy && s.inflight == 0 {
			s.owner, s.waiting, s.shared = id, s.waiting[1:], false
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
		s.owner, s.shared = "", false
		g.pruneLocked(sessionID, s)
	}
}

// pruneLocked forgets sessionID once s holds nothing any more.
func (g *TurnGate) pruneLocked(sessionID string, s *gateSession) {
	if s.owner == "" && len(s.waiting) == 0 && s.inflight == 0 && g.sessions[sessionID] == s {
		delete(g.sessions, sessionID)
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
			g.pruneLocked(sessionID, s)
			return true
		}
	}
	return false
}

// NoteMessage tells the gate a message with role is on its way into
// sessionID; ctx is the sender's. done is called once the send is over,
// delivered telling whether the message reached the session (it may be
// refused). A message a person sent (WithPersonMessage, role "user") that
// reaches the session while a task's turn runs marks that turn shared, so
// a cancel leaves it running (TaskStopShared): on a provider that
// appends, the message is written into that turn and would go down with
// it. Until done, the message also keeps any task from taking the session
// and, if a person's, the running task's turn from being stopped, so no
// message is half-way in while either is decided.
//
// While a task's turn is being stopped there, NoteMessage blocks until
// the stop has finished (at most stopWait), so the message reaches the
// session after the stop, not the turn being stopped; ctx ending first
// refuses the message with its error.
func (g *TurnGate) NoteMessage(ctx context.Context, sessionID, role string) (done func(delivered bool), err error) {
	if sessionID == "" {
		return func(bool) {}, nil
	}
	var timeout <-chan time.Time
	g.mu.Lock()
	// Looked at again after each wait: another stop may have begun.
	for ch := g.stopping[sessionID]; ch != nil; ch = g.stopping[sessionID] {
		g.mu.Unlock()
		if timeout == nil {
			t := time.NewTimer(g.stopWait)
			defer t.Stop()
			timeout = t.C
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			ch = nil
		}
		g.mu.Lock()
		if ch == nil {
			break
		}
	}
	s := g.sessions[sessionID]
	if s == nil {
		s = &gateSession{}
		g.sessions[sessionID] = s
	}
	person := role == "user" && personMessage(ctx)
	s.inflight++
	if person {
		s.persons++
	}
	g.mu.Unlock()
	var once sync.Once
	return func(delivered bool) {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			s.inflight--
			if person {
				s.persons--
				if delivered && s.owner != "" {
					s.shared = true
				}
			}
			g.pruneLocked(sessionID, s)
		})
	}, nil
}

// Stop is StopWith with nothing to stop the turn: TaskStopRunning tells
// the caller to stop it.
func (g *TurnGate) Stop(sessionID string, id a2a.TaskID) TaskStop {
	out, _ := g.StopWith(sessionID, id, nil)
	return out
}

// StopWith cancels task id in sessionID. When it owns the running turn,
// that turn is TaskStopShared if a message a person sent reached it or is
// on its way (NoteMessage), and otherwise stopped with stop, whose
// outcome is returned (TaskStopRunning when stop is nil): the stop runs
// with the session's new messages held back (NoteMessage waits until stop
// returns), so none slips into the turn being stopped. A task still in line is
// dropped: TaskStopQueued or TaskStopBehindOther, depending on whether
// something else is running there. A task the gate has not seen yet is
// remembered, so its Acquire refuses it.
func (g *TurnGate) StopWith(sessionID string, id a2a.TaskID, stop func() (TaskStop, error)) (TaskStop, error) {
	if id == "" {
		return TaskStopNotFound, nil
	}
	busy := g.isBusy(sessionID)
	g.mu.Lock()
	s := g.sessions[sessionID]
	if s != nil && s.owner == id {
		if s.shared || s.persons > 0 {
			g.mu.Unlock()
			return TaskStopShared, nil
		}
		if stop == nil {
			g.mu.Unlock()
			return TaskStopRunning, nil
		}
		ch := make(chan struct{})
		g.stopping[sessionID] = ch
		g.mu.Unlock()
		defer func() {
			g.mu.Lock()
			if g.stopping[sessionID] == ch {
				delete(g.stopping, sessionID)
			}
			g.mu.Unlock()
			close(ch)
		}()
		return stop()
	}
	defer g.mu.Unlock()
	if s != nil {
		for _, w := range s.waiting {
			if w.id != id {
				continue
			}
			other := busy || s.owner != ""
			g.removeLocked(sessionID, w)
			close(w.canceled)
			if other {
				return TaskStopBehindOther, nil
			}
			return TaskStopQueued, nil
		}
	}
	now := time.Now()
	g.pruneDroppedLocked(now)
	g.dropped[id] = now
	return TaskStopNotFound, nil
}

// pruneDroppedLocked forgets the cancels older than droppedTTL, so a task
// that never asked for its turn does not stay remembered. g.mu is held.
func (g *TurnGate) pruneDroppedLocked(now time.Time) {
	for d, at := range g.dropped {
		if now.Sub(at) > droppedTTL {
			delete(g.dropped, d)
		}
	}
}
