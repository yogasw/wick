package agents

import (
	"encoding/json"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/event"
)

// subBuffer is per-subscriber channel depth. Sized for tool_use bursts
// where a single agent turn can fire 20-50 events back-to-back; 256
// gives slow clients (browser tab backgrounded, slow network) room to
// catch up before we start dropping. Drop-on-full is still the policy —
// a stuck client must never stall the agent reader goroutine.
const subBuffer = 256

// Event is one SSE payload pushed to browser subscribers. Type
// distinguishes agent stream events ("text_delta", "tool_use", ...)
// from lifecycle events ("lifecycle"); the latter carry PID +
// lifecycle label in Data so the UI can update the status badge
// without re-fetching the page.
type Event struct {
	SessionID string `json:"session_id"`
	AgentName string `json:"agent_name"`
	Type      string `json:"type"`
	Data      string `json:"data"`
	PID       int    `json:"pid,omitempty"`
	Lifecycle string `json:"lifecycle,omitempty"`
}

func (e Event) JSON() string {
	b, _ := json.Marshal(e)
	return string(b)
}

// Broadcaster fans out agent events to all subscribed SSE connections.
// Subscribe returns a receive channel and an unsub func. Publish is
// called from ClaudeFactory.OnEvent on every AgentEvent.
//
// subs is keyed by sessionID ("" = global subscribers that receive all
// events). Channels are buffered at 64 so a slow client never stalls
// the agent reader goroutine.
type Broadcaster struct {
	mu   sync.RWMutex
	subs map[string][]chan Event
}

// NewBroadcaster returns a ready Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: make(map[string][]chan Event)}
}

// Subscribe registers a listener for a specific session (or "" for all).
// The caller must call the returned unsub func when the SSE connection closes.
func (b *Broadcaster) Subscribe(sessionID string) (<-chan Event, func()) {
	ch := make(chan Event, subBuffer)
	b.mu.Lock()
	b.subs[sessionID] = append(b.subs[sessionID], ch)
	b.mu.Unlock()
	unsub := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.subs[sessionID]
		for i, c := range list {
			if c == ch {
				b.subs[sessionID] = append(list[:i], list[i+1:]...)
				break
			}
		}
		close(ch)
	}
	return ch, unsub
}

// Publish fires ev to all subscribers of sessionID and all global ("") subscribers.
// Non-blocking: a full channel's event is dropped rather than blocking.
func (b *Broadcaster) Publish(sessionID, agentName string, ev event.AgentEvent) {
	payload := Event{
		SessionID: sessionID,
		AgentName: agentName,
		Type:      ev.Type.String(),
		Data:      ev.Text,
	}
	if ev.ErrorMsg != "" {
		payload.Data = ev.ErrorMsg
	}
	b.fanout(sessionID, payload)
}

// PublishLifecycle pushes a lifecycle transition (Spawning, Killed)
// to subscribers. Idle/Working transitions are inferred from
// AgentEvent flow on the client side; only the bookend transitions —
// which never carry an AgentEvent — go through this channel.
func (b *Broadcaster) PublishLifecycle(sessionID, agentName, lifecycle string, pid int) {
	b.fanout(sessionID, Event{
		SessionID: sessionID,
		AgentName: agentName,
		Type:      "lifecycle",
		Lifecycle: lifecycle,
		PID:       pid,
	})
}

func (b *Broadcaster) fanout(sessionID string, payload Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, key := range []string{sessionID, ""} {
		for _, ch := range b.subs[key] {
			select {
			case ch <- payload:
			default:
				log.Warn().
					Str("session_id", payload.SessionID).
					Str("agent", payload.AgentName).
					Str("event_type", payload.Type).
					Int("buffer", subBuffer).
					Msg("sse: subscriber buffer full, dropping event")
			}
		}
	}
}
