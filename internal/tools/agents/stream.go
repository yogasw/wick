package agents

import (
	"encoding/json"
	"sync"

	"github.com/yogasw/wick/internal/agents/event"
)

// Event is one SSE payload pushed to browser subscribers.
type Event struct {
	SessionID string `json:"session_id"`
	AgentName string `json:"agent_name"`
	Type      string `json:"type"`
	Data      string `json:"data"`
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
	ch := make(chan Event, 64)
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
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, key := range []string{sessionID, ""} {
		for _, ch := range b.subs[key] {
			select {
			case ch <- payload:
			default:
			}
		}
	}
}
