package pool

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Wait states: what a session waits on besides its own running turn, so
// whoever waits for it (a delegation, a Team task, the person) can show
// "Restarting · retry 1/2" or "Queued · #2" instead of silence. Kept in
// memory only: a restart of wick ends both.

// WaitState is what a session's next answer waits on. Zero = nothing
// (running, idle, or unknown).
type WaitState struct {
	// RestartAttempt is the idle-kill retry under way (1-based) and
	// RestartMax how many there are; 0 when the session is not being
	// restarted. Cleared at the restarted process's first output.
	RestartAttempt, RestartMax int
	// QueuePos is the session's place in the pool queue (1 = next),
	// 0 when it is not queued.
	QueuePos int
}

// Note is w in one line for an agent waiting on the session, "" for
// none: "target restarting after an idle timeout, retry 1/2".
func (w WaitState) Note() string {
	switch {
	case w.RestartAttempt > 0:
		return fmt.Sprintf("target restarting after an idle timeout, retry %d/%d", w.RestartAttempt, w.RestartMax)
	case w.QueuePos > 0:
		return fmt.Sprintf("target queued for a free slot (#%d)", w.QueuePos)
	}
	return ""
}

// waitStates is the Pool's part for wait states. Its own mutex, so the
// per-event NoteOutput never contends with p.mu.
type waitStates struct {
	waitMu sync.Mutex
	// restarting is the idle retry under way per session (attempt).
	restarting map[string]int
	// gaveUp is the reason the retries of a session stopped, read once by
	// TakeGiveUp for the Done of that exit.
	gaveUp map[string]string
	// onWaitChange is told when any wait state changed; nil = nobody.
	onWaitChange func()
}

// SetOnWaitChange registers f, called (on its own goroutine) whenever a
// session starts or stops restarting or the queue moves. f re-reads
// WaitOf for what it shows.
func (p *Pool) SetOnWaitChange(f func()) {
	p.waitMu.Lock()
	p.onWaitChange = f
	p.waitMu.Unlock()
}

// waitChanged fires onWaitChange. Never call it with p.mu held: the
// listener reads WaitOf, which takes it.
func (p *Pool) waitChanged() {
	p.waitMu.Lock()
	f := p.onWaitChange
	p.waitMu.Unlock()
	if f != nil {
		go f()
	}
}

// markRestarting records idle retry attempt of sessionID.
func (p *Pool) markRestarting(sessionID string, attempt int) {
	p.waitMu.Lock()
	if p.restarting == nil {
		p.restarting = map[string]int{}
	}
	p.restarting[sessionID] = attempt
	delete(p.gaveUp, sessionID)
	p.waitMu.Unlock()
	p.waitChanged()
}

// markGaveUp records why the idle retries of sessionID stopped.
func (p *Pool) markGaveUp(sessionID, reason string) {
	p.waitMu.Lock()
	delete(p.restarting, sessionID)
	if p.gaveUp == nil {
		p.gaveUp = map[string]string{}
	}
	p.gaveUp[sessionID] = reason
	p.waitMu.Unlock()
	p.waitChanged()
}

// NoteOutput tells the pool sessionID's agent produced output: a restart
// under way has come back. Cheap when nothing restarts, so it can run on
// every event.
func (p *Pool) NoteOutput(sessionID string) {
	p.waitMu.Lock()
	_, ok := p.restarting[sessionID]
	if ok {
		delete(p.restarting, sessionID)
	}
	p.waitMu.Unlock()
	if ok {
		p.waitChanged()
	}
}

// TakeGiveUp returns, and forgets, why the idle retries of sessionID
// stopped ("" = they did not): the Done of that exit carries it, so the
// waiting side fails with a plain reason instead of an empty answer.
func (p *Pool) TakeGiveUp(sessionID string) string {
	p.waitMu.Lock()
	defer p.waitMu.Unlock()
	r := p.gaveUp[sessionID]
	delete(p.gaveUp, sessionID)
	return r
}

// WaitOf is what sessionID's next answer waits on.
func (p *Pool) WaitOf(sessionID string) WaitState {
	var w WaitState
	p.waitMu.Lock()
	if n := p.restarting[sessionID]; n > 0 {
		w.RestartAttempt, w.RestartMax = n, maxIdleRetries
	}
	p.waitMu.Unlock()
	p.mu.Lock()
	for i, q := range p.queue {
		if q.sessionID == sessionID {
			w.QueuePos = i + 1
			break
		}
	}
	p.mu.Unlock()
	return w
}

// ProviderOf is the provider instance sessionID's agent runs on, as the
// spawn gate resolves it ("" when the session cannot be read).
func (p *Pool) ProviderOf(sessionID, agentName string) (pType, pName string) {
	return p.providerForSession(sessionID, agentName)
}

// idleGiveUpReason is the plain reason an idle-killed turn ends with
// once its retries are spent: "stopped after 2 retries: no output for
// 20m". halted: crash recovery stopped the agent before they ran out.
func idleGiveUpReason(retries int, window time.Duration, halted bool) string {
	if halted {
		return "stopped: the agent kept exiting, so automatic restarts were halted"
	}
	return fmt.Sprintf("stopped after %d retries: no output for %s", retries, shortWindow(window))
}

// shortWindow renders d as "20m", "2m", "90s", "1h".
func shortWindow(d time.Duration) string {
	if d < time.Second {
		return d.String()
	}
	s := d.Round(time.Second).String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
