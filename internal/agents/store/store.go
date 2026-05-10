// Package store is the agents pipeline sink: it consumes AgentEvent
// values produced by a Parser and writes them to the on-disk session
// folder (conversation.jsonl, raw.jsonl, agents.json cli_session_id).
//
// The store buffers TextDelta chunks until Done, then writes one
// assistant turn — that matches how UI / Slack want to display
// messages and keeps conversation.jsonl one-line-per-turn instead of
// one-line-per-character.
//
// One Store per active session. Not safe for concurrent Apply from
// multiple goroutines; the agent lifecycle pipes events through a
// single reader goroutine.
package store

import (
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
)

// MaxAssistantTurnBytes caps one assistant turn's body before it gets
// written to conversation.jsonl. Anything beyond is truncated with a
// note pointing at raw.jsonl. Matches §13 cap.
const MaxAssistantTurnBytes = 32 * 1024

// ConversationTurn is the on-disk shape of one user/assistant turn.
type ConversationTurn struct {
	Timestamp time.Time `json:"ts"`
	Role      string    `json:"role"`            // "user" | "assistant" | "system"
	Agent     string    `json:"agent,omitempty"` // assistant turn only
	Source    string    `json:"source,omitempty"`
	Text      string    `json:"text"`
	Truncated bool      `json:"truncated,omitempty"`
}

// Store collects events for one session+agent and persists them.
type Store struct {
	layout    config.Layout
	sessionID string
	agentName string

	// turnBuf accumulates TextDelta chunks; flushed on Done.
	turnBuf strings.Builder

	// recordRaw mirrors every event line into raw.jsonl. Off by
	// default per design (raw is opt-in, retention agressive).
	recordRaw bool

	now func() time.Time
}

// Options configures a Store. AgentName ties assistant turns to the
// agents.json entry that emitted them.
type Options struct {
	Layout    config.Layout
	SessionID string
	AgentName string
	RecordRaw bool
	Now       func() time.Time // optional; defaults to time.Now
}

// New returns a Store ready to consume events. Caller owns the
// session/agent CRUD; the store only writes turns + cli_session_id.
func New(opt Options) *Store {
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	return &Store{
		layout:    opt.Layout,
		sessionID: opt.SessionID,
		agentName: opt.AgentName,
		recordRaw: opt.RecordRaw,
		now:       now,
	}
}

// AppendUserTurn records a user / system message before it's sent to
// the subprocess. Source is the transport label ("ui", "slack",
// "api"). Role is normally "user"; "system" for operator instructions.
func (s *Store) AppendUserTurn(role, source, text string) error {
	turn := ConversationTurn{
		Timestamp: s.now().UTC(),
		Role:      role,
		Source:    source,
		Text:      text,
	}
	return storage.AppendJSONL(
		s.layout.SessionConversation(s.sessionID),
		"wick-conv-v1",
		s.sessionID,
		turn,
	)
}

// Apply consumes one parser event. Returns true when an assistant turn
// was just flushed (caller may want to notify Slack / SSE).
//
// Side effects per event type:
//
//   - SessionStart    → persists cli_session_id into agents.json (if
//                        AgentName is set) so resume works after kill.
//   - TextDelta       → appended to turnBuf.
//   - Done / Error    → flush turnBuf as one assistant turn.
//   - Anything else   → optionally mirrored to raw.jsonl.
func (s *Store) Apply(ev event.AgentEvent) (bool, error) {
	if s.recordRaw && ev.Raw != "" {
		_ = storage.AppendJSONL(
			s.layout.SessionRaw(s.sessionID),
			"wick-raw-v1",
			s.sessionID,
			map[string]any{
				"ts":   s.now().UTC(),
				"type": ev.Type.String(),
				"raw":  ev.Raw,
			},
		)
	}

	switch ev.Type {
	case event.SessionStart:
		if ev.SessionID != "" && s.agentName != "" {
			if err := s.persistCLISessionID(ev.SessionID); err != nil {
				return false, err
			}
		}
		return false, nil

	case event.TextDelta:
		s.turnBuf.WriteString(ev.Text)
		return false, nil

	case event.Done:
		if err := s.flushAssistantTurn(false); err != nil {
			return false, err
		}
		return true, nil

	case event.Error:
		// On error we still want whatever partial text accumulated;
		// don't lose it.
		if err := s.flushAssistantTurn(false); err != nil {
			return false, err
		}
		return true, nil
	}

	return false, nil
}

// Flush is the explicit drain hook for callers that want to write
// whatever's buffered (e.g. subprocess crashed mid-stream, no Done
// arrived). Marks the turn as truncated since it didn't end naturally.
func (s *Store) Flush() error {
	if s.turnBuf.Len() == 0 {
		return nil
	}
	return s.flushAssistantTurn(true)
}

// flushAssistantTurn writes the buffered text as one assistant turn
// and resets the buffer. wasInterrupted=true sets Truncated on the
// turn — used by Flush when there was no Done event.
func (s *Store) flushAssistantTurn(wasInterrupted bool) error {
	if s.turnBuf.Len() == 0 {
		return nil
	}
	body := s.turnBuf.String()
	truncated := wasInterrupted
	if len(body) > MaxAssistantTurnBytes {
		body = body[:MaxAssistantTurnBytes] + "\n…(truncated, see raw.jsonl)"
		truncated = true
	}
	turn := ConversationTurn{
		Timestamp: s.now().UTC(),
		Role:      "assistant",
		Agent:     s.agentName,
		Text:      body,
		Truncated: truncated,
	}
	s.turnBuf.Reset()
	return storage.AppendJSONL(
		s.layout.SessionConversation(s.sessionID),
		"wick-conv-v1",
		s.sessionID,
		turn,
	)
}

// persistCLISessionID writes the captured CLI session ID into the
// matching entry of sessions/<id>/agents.json. The store reads, edits,
// writes — no concurrent writer because store + agent lifecycle share
// one goroutine.
func (s *Store) persistCLISessionID(id string) error {
	sess, err := session.Load(s.layout, s.sessionID)
	if err != nil {
		return err
	}
	updated := false
	for i := range sess.Agents {
		if sess.Agents[i].Name != s.agentName {
			continue
		}
		if sess.Agents[i].CLISessionID == id {
			return nil // already up to date
		}
		sess.Agents[i].CLISessionID = id
		sess.Agents[i].LastActive = s.now().UTC()
		updated = true
		break
	}
	if !updated {
		// No matching agent — caller might not have created it yet.
		// Don't fabricate one here; leave session.AddAgent in charge.
		return nil
	}
	return session.SaveAgents(s.layout, s.sessionID, sess.Agents)
}
