package slackremote

import (
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/remote"
)

// Router is the shared listener's dispatch: the Slack events wick already
// receives are handed to Dispatch once, and each goes to the turns waiting
// in that thread (or that DM). One router serves every Slack remote agent;
// nothing listens per agent.
type Router struct {
	mu    sync.Mutex
	turns map[string]map[*tracker]struct{}
	// past keeps each ended turn's tracker until pastTTL, so the remote's
	// late messages in its thread are still known as its own (Owns).
	past map[*tracker]time.Time
}

// pastTTL is how long a thread a remote turn ran in stays known after it.
var pastTTL = 24 * time.Hour

// FromEvent is a message event as the Slack channel received it, flattened
// for Dispatch: a message_changed carries the edited message, a
// message_deleted the one removed.
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
	case "message_deleted":
		// edited is the message as it was before; its TS is the one gone.
		if edited == nil {
			return Message{}, false
		}
		m := *edited
		m.Channel, m.Deleted = channel, true
		return m, true
	}
	return Message{}, false
}

// Shared is the process-wide router the Slack channel feeds.
var Shared = NewRouter()

func NewRouter() *Router {
	return &Router{turns: map[string]map[*tracker]struct{}{}, past: map[*tracker]time.Time{}}
}

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
	r.past[t] = time.Now().Add(pastTTL)
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

// Owns reports whether m is a remote agent's message in a thread one of
// its turns runs — or ran, within pastTTL — in. Such a message is the
// remote's reply (or a late one), never a new message to wick: the Slack
// channel must not dispatch it to an agent.
func (r *Router) Owns(m Message) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	nowT := time.Now()
	for t, until := range r.past {
		if nowT.After(until) {
			delete(r.past, t)
			continue
		}
		if t.owns(m) {
			return true
		}
	}
	for _, ts := range r.turns {
		for t := range ts {
			if t.owns(m) {
				return true
			}
		}
	}
	return false
}

// DispatchReaction routes a reaction added to or removed from message ts
// to the turn that posted it: ⏳ there says the remote works.
func (r *Router) DispatchReaction(channel, ts, user, name string, added bool) {
	r.mu.Lock()
	var targets []*tracker
	for _, set := range r.turns {
		for t := range set {
			if t.channel == channel && t.sentTS == ts {
				targets = append(targets, t)
			}
		}
	}
	r.mu.Unlock()
	for _, t := range targets {
		t.send(t.react(ts, user, name, added))
	}
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
		t.send(t.observe(m))
	}
}

// send hands evs to the turn without blocking.
func (t *tracker) send(evs []remote.Event) {
	for _, ev := range evs {
		select {
		case t.events <- ev:
		default:
			log.Debug().Str("channel", t.channel).Msg("slackremote: turn buffer full, event dropped")
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
