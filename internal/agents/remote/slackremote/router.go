package slackremote

import (
	"sync"

	"github.com/rs/zerolog/log"
)

// Router is the shared listener's dispatch: the Slack events wick already
// receives are handed to Dispatch once, and each goes to the turns waiting
// in that thread (or that DM). One router serves every Slack remote agent;
// nothing listens per agent.
type Router struct {
	mu    sync.Mutex
	turns map[string]map[*tracker]struct{}
}

// FromEvent is a message event as the Slack channel received it, flattened
// for Dispatch: a message_changed carries the edited message.
func FromEvent(channel, subtype, ts, threadTS, user, botID, text string, edited *Message) (Message, bool) {
	switch subtype {
	case "", "bot_message", "thread_broadcast", "file_share", "me_message":
		return Message{Channel: channel, TS: ts, ThreadTS: threadTS, User: user, BotID: botID, Text: text}, true
	case "message_changed":
		if edited == nil {
			return Message{}, false
		}
		m := *edited
		m.Channel, m.Edited = channel, true
		return m, true
	}
	return Message{}, false
}

// Shared is the process-wide router the Slack channel feeds.
var Shared = NewRouter()

func NewRouter() *Router { return &Router{turns: map[string]map[*tracker]struct{}{}} }

func threadKey(channel, ts string) string { return channel + "|" + ts }

func (r *Router) keys(t *tracker) []string {
	k := []string{threadKey(t.channel, t.threadTS)}
	if t.topLevel {
		k = append(k, threadKey(t.channel, "*"))
	}
	return k
}

func (r *Router) add(t *tracker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range r.keys(t) {
		if r.turns[k] == nil {
			r.turns[k] = map[*tracker]struct{}{}
		}
		r.turns[k][t] = struct{}{}
	}
}

func (r *Router) remove(t *tracker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range r.keys(t) {
		delete(r.turns[k], t)
		if len(r.turns[k]) == 0 {
			delete(r.turns, k)
		}
	}
}

// Waiting is how many turns wait for a reply.
func (r *Router) Waiting() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[*tracker]bool{}
	for _, ts := range r.turns {
		for t := range ts {
			seen[t] = true
		}
	}
	return len(seen)
}

// Dispatch routes one message (new or edited). It never blocks the event
// loop: a turn whose buffer is full drops the event and catches up on its
// next poll, since the tracker keeps the whole reply.
func (r *Router) Dispatch(m Message) {
	r.mu.Lock()
	var targets []*tracker
	add := func(k string) {
		for t := range r.turns[k] {
			targets = append(targets, t)
		}
	}
	th := m.ThreadTS
	if th == "" {
		th = m.TS
	}
	add(threadKey(m.Channel, th))
	if m.ThreadTS == "" || m.ThreadTS == m.TS {
		add(threadKey(m.Channel, "*"))
	}
	r.mu.Unlock()
	seen := map[*tracker]bool{}
	for _, t := range targets {
		if seen[t] {
			continue
		}
		seen[t] = true
		for _, ev := range t.observe(m) {
			select {
			case t.events <- ev:
			default:
				log.Debug().Str("channel", m.Channel).Msg("slackremote: turn buffer full, event dropped")
			}
		}
	}
}

// wickIDs holds the user and bot ids of every Slack identity wick posts
// as, noted by the Slack channel as it learns them.
var wickIDs sync.Map

// NoteWickID records id as one of wick's own Slack identities.
func NoteWickID(id string) {
	if id != "" {
		wickIDs.Store(id, struct{}{})
	}
}

// WickIDs lists what NoteWickID recorded.
func WickIDs() []string {
	var out []string
	wickIDs.Range(func(k, _ any) bool {
		out = append(out, k.(string))
		return true
	})
	return out
}
