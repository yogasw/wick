package delegation

import (
	"strings"
	"sync"

	"github.com/yogasw/wick/internal/agents/event"
)

// TurnObserver assembles an agent's streamed turn and hands the finished
// text to the mention router.
//
// It is the ONLY place agent output is scanned for mentions. Leaders and
// sub-agents both spawn through the same pool and the same factory
// callbacks, so one observer covers every agent in the process; scanning
// again inside the delegation run loop would dispatch each mention twice.
//
// Deliberately not the channel registry's buffer: that one holds only the
// first few bytes of a reply, long enough to decide whether a turn is
// silent. A mention can appear anywhere in a turn.
type TurnObserver struct {
	mu  sync.Mutex
	buf map[string]*strings.Builder
	// route receives the whole turn once it ends. Called on the caller's
	// goroutine — the wiring decides whether to detach.
	route func(sessionID, agentName, text string)
}

// NewTurnObserver builds an observer that calls route at each turn end.
func NewTurnObserver(route func(sessionID, agentName, text string)) *TurnObserver {
	return &TurnObserver{buf: map[string]*strings.Builder{}, route: route}
}

// OnEvent accumulates text and flushes on a completed turn.
func (o *TurnObserver) OnEvent(sessionID, agentName string, ev event.AgentEvent) {
	if o == nil || o.route == nil || sessionID == "" {
		return
	}
	key := sessionID + "\x00" + agentName

	switch ev.Type {
	case event.TextDelta:
		if ev.Text == "" {
			return
		}
		o.mu.Lock()
		b := o.buf[key]
		if b == nil {
			b = &strings.Builder{}
			o.buf[key] = b
		}
		b.WriteString(ev.Text)
		o.mu.Unlock()

	case event.Done:
		o.mu.Lock()
		b := o.buf[key]
		delete(o.buf, key)
		o.mu.Unlock()
		if b == nil {
			return
		}
		// A Done with nothing buffered is a status-only turn end — and,
		// on the exit path, a SECOND Done for a turn already flushed.
		// Routing there would re-dispatch every mention in it.
		text := b.String()
		if strings.TrimSpace(text) == "" {
			return
		}
		o.route(sessionID, agentName, text)
	}
}
