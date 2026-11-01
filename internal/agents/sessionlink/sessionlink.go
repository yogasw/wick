// Package sessionlink ties sessions that handle the same matter together,
// so each knows the others exist, what they are doing right now, and can
// read their latest turns — instead of a person forwarding between them.
//
// A link comes from one of four places:
//
//   - an explicit pair in session.Meta.Links, written to both sides (Link):
//     by an agent through wick_session_link, or by wick itself;
//   - a Team pairing, session.Meta.LinkedFromSession (read as a link, never
//     rewritten here: Team routing owns that field);
//   - a thread an agent opened and announced with LinkThread: the session
//     born in that thread later is linked to the one that opened it the
//     first time either side is looked at;
//   - nothing else. Tickets link through Link when a session joins one.
//
// Everything here reads and writes session files directly; callers that
// cache sessions refresh them afterwards.
package sessionlink

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
)

// Peer states.
const (
	StateRunning = "running"
	StateIdle    = "idle"
	// StatePending: a thread was opened and announced, nobody replied in
	// it yet, so no session exists there.
	StatePending = "pending"
)

// Peer is one linked session as an agent or the UI sees it.
type Peer struct {
	Session    string    `json:"session,omitempty"`
	Title      string    `json:"title"`
	AgentID    string    `json:"agent_id,omitempty"`
	Why        string    `json:"why"`
	State      string    `json:"state"`
	LastActive time.Time `json:"last_active,omitempty"`
	// Thread is "<channel>:<chat>/<thread>" for a pending thread.
	Thread string `json:"thread,omitempty"`
}

// ErrSelf refuses linking a session to itself.
var ErrSelf = errors.New("a session cannot be linked to itself")

// writeMu serialises the read-modify-write of meta files and the pending
// file within this process.
var writeMu sync.Mutex

// Link ties a and b together, on both sides. Linking an existing pair
// again only refreshes its reason.
func Link(layout config.Layout, a, b, why string) error {
	if a == "" || b == "" {
		return errors.New("both session ids are required")
	}
	if a == b {
		return ErrSelf
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	sa, err := session.Load(layout, a)
	if err != nil {
		return fmt.Errorf("session %s: %w", a, err)
	}
	sb, err := session.Load(layout, b)
	if err != nil {
		return fmt.Errorf("session %s: %w", b, err)
	}
	now := time.Now().UTC()
	if err := session.SaveMeta(layout, a, withLink(sa.Meta, b, why, now)); err != nil {
		return err
	}
	return session.SaveMeta(layout, b, withLink(sb.Meta, a, why, now))
}

func withLink(m session.Meta, other, why string, at time.Time) session.Meta {
	for i, l := range m.Links {
		if l.Session == other {
			if why != "" {
				m.Links[i].Why = why
			}
			return m
		}
	}
	m.Links = append(m.Links, session.SessionLink{Session: other, Why: why, At: at})
	return m
}

// Unlink removes the pair from both sides. A side that is gone is skipped.
func Unlink(layout config.Layout, a, b string) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	for _, p := range [][2]string{{a, b}, {b, a}} {
		s, err := session.Load(layout, p[0])
		if err != nil {
			continue
		}
		kept := s.Meta.Links[:0]
		for _, l := range s.Meta.Links {
			if l.Session != p[1] {
				kept = append(kept, l)
			}
		}
		s.Meta.Links = kept
		if err := session.SaveMeta(layout, p[0], s.Meta); err != nil {
			return err
		}
	}
	return nil
}

// pendingThread is a thread an agent opened from a session, waiting for
// the session a reply there will start.
type pendingThread struct {
	Origin string    `json:"origin"`
	Why    string    `json:"why,omitempty"`
	At     time.Time `json:"at"`
}

// pendingTTL drops announced threads nobody answered in this long.
const pendingTTL = 14 * 24 * time.Hour

func pendingPath(layout config.Layout) string {
	return filepath.Join(layout.BaseDir, "session-link-threads.json")
}

func threadKey(channel, chatID, threadID string) string {
	return channel + ":" + chatID + "/" + threadID
}

func loadPending(layout config.Layout) map[string]pendingThread {
	out := map[string]pendingThread{}
	b, err := os.ReadFile(pendingPath(layout))
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	cut := time.Now().Add(-pendingTTL)
	for k, v := range out {
		if v.At.Before(cut) {
			delete(out, k)
		}
	}
	return out
}

func savePending(layout config.Layout, m map[string]pendingThread) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := pendingPath(layout) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, pendingPath(layout))
}

// LinkThread announces that origin opened a thread (channel "slack",
// chatID a channel id, threadID the new message's ts). The session a reply
// there starts is linked to origin the first time either is looked at.
func LinkThread(layout config.Layout, origin, channel, chatID, threadID, why string) error {
	if origin == "" || chatID == "" || threadID == "" {
		return errors.New("origin, chat and thread are required")
	}
	if channel == "" {
		channel = "slack"
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	m := loadPending(layout)
	m[threadKey(channel, chatID, threadID)] = pendingThread{Origin: origin, Why: why, At: time.Now().UTC()}
	return savePending(layout, m)
}

// adoptThread links id to the session that announced its thread, if one
// did, and forgets the announcement. Reports whether it linked.
func adoptThread(layout config.Layout, s session.Session) bool {
	ref := s.Meta.ChannelRef
	if ref == nil || ref.ChatID == "" || ref.ThreadID == "" {
		return false
	}
	key := threadKey(ref.Channel, ref.ChatID, ref.ThreadID)
	writeMu.Lock()
	m := loadPending(layout)
	pt, ok := m[key]
	if ok {
		delete(m, key)
		_ = savePending(layout, m)
	}
	writeMu.Unlock()
	if !ok || pt.Origin == s.ID {
		return false
	}
	why := "thread opened from that session"
	if pt.Why != "" {
		why += ": " + pt.Why
	}
	return Link(layout, s.ID, pt.Origin, why) == nil
}

// Peers lists id's linked sessions, newest activity first, then the
// threads it opened that nobody answered yet.
func Peers(layout config.Layout, id string) ([]Peer, error) {
	s, err := session.Load(layout, id)
	if err != nil {
		return nil, err
	}
	if adoptThread(layout, s) {
		if s, err = session.Load(layout, id); err != nil {
			return nil, err
		}
	}
	why := map[string]string{}
	var order []string
	add := func(sid, w string) {
		if sid == "" || sid == id {
			return
		}
		if _, seen := why[sid]; !seen {
			order = append(order, sid)
			why[sid] = w
		}
	}
	for _, l := range s.Meta.Links {
		add(l.Session, l.Why)
	}
	add(s.Meta.LinkedFromSession, "team message from that session")

	var out []Peer
	for _, sid := range order {
		ps, err := session.Load(layout, sid)
		if err != nil {
			continue // deleted since: nothing to show
		}
		st := StateIdle
		if ps.Meta.Status == session.StatusRunning {
			st = StateRunning
		}
		out = append(out, Peer{
			Session: sid, Title: titleOf(ps), AgentID: ps.Meta.AgentID,
			Why: why[sid], State: st, LastActive: ps.Meta.LastActive,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastActive.After(out[j].LastActive) })

	for k, pt := range loadPending(layout) {
		if pt.Origin == id {
			out = append(out, Peer{Title: "thread " + k + " (no reply yet)", Why: pt.Why, State: StatePending, Thread: k, LastActive: pt.At})
		}
	}
	return out, nil
}

func titleOf(s session.Session) string {
	if t := strings.TrimSpace(s.Meta.Label); t != "" {
		return t
	}
	return s.ID
}

// Note is the one-line [linked] block a turn of id opens with: only the
// linked sessions working right now, since those are the ones whose work
// this turn could repeat or collide with. "" when none is.
func Note(layout config.Layout, id string, now time.Time) string {
	peers, err := Peers(layout, id)
	if err != nil {
		return ""
	}
	var lines []string
	for _, p := range peers {
		if p.State != StateRunning {
			continue
		}
		line := fmt.Sprintf("- %s (%s) — WORKING NOW", p.Title, p.Session)
		if !p.LastActive.IsZero() {
			line += ", active " + ago(now.Sub(p.LastActive)) + " ago"
		}
		if p.Why != "" {
			line += " · linked: " + p.Why
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return "[linked] Linked sessions working on the same matter right now:\n" + strings.Join(lines, "\n") +
		"\nDo not redo what they are doing; read their result with wick_session_link action=read."
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	default:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
}

// Turn is one conversation turn as Read returns it.
type Turn struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// IsLinked reports whether peer is among id's linked sessions.
func IsLinked(layout config.Layout, id, peer string) bool {
	peers, err := Peers(layout, id)
	if err != nil {
		return false
	}
	for _, p := range peers {
		if p.Session == peer {
			return true
		}
	}
	return false
}

// Read returns peer's last n user/assistant turns, oldest first, each cut
// to maxChars. Callers check IsLinked (or ownership) first.
func Read(layout config.Layout, peer string, n, maxChars int) ([]Turn, error) {
	if n <= 0 {
		n = 6
	}
	if maxChars <= 0 {
		maxChars = 1500
	}
	var all []Turn
	err := storage.ReadJSONL(layout.SessionConversation(peer), func(line []byte) bool {
		var t store.ConversationTurn
		if json.Unmarshal(line, &t) != nil {
			return true
		}
		if (t.Role != "user" && t.Role != "assistant") || strings.TrimSpace(t.Text) == "" {
			return true
		}
		text := t.Text
		if r := []rune(text); len(r) > maxChars {
			text = string(r[:maxChars]) + "…"
		}
		all = append(all, Turn{Role: t.Role, Text: text, At: t.Timestamp})
		if len(all) > n*4 {
			all = all[len(all)-n:]
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// promptMax caps the sessions listed in the system prompt, most recently
// active first; wick_session_link list shows the rest.
const promptMax = 8

// Prompt is the "Linked sessions" section of id's system prompt: who is
// linked when the process starts. "" for a session with no links, so a
// session that never has any pays nothing.
func Prompt(layout config.Layout, id string) string {
	peers, err := Peers(layout, id)
	if err != nil || len(peers) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Linked sessions\n\n")
	b.WriteString("Other conversations handle the same matter as this one. A turn opens with a [linked] line while one of them is working: " +
		"do not redo its work, read its result with wick_session_link action=read. wick_session_link action=list shows them all with their state.\n")
	if len(peers) > promptMax {
		peers = peers[:promptMax]
	}
	for _, p := range peers {
		if p.Session == "" {
			fmt.Fprintf(&b, "- %s", p.Title)
		} else {
			fmt.Fprintf(&b, "- %s (%s)", p.Title, p.Session)
		}
		if p.Why != "" {
			b.WriteString(" · " + p.Why)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
