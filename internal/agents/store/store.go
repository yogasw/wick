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
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
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

// TurnEvent is one tool_use, tool_result, or thinking event recorded
// within an assistant turn. Stored alongside the text so the UI can
// replay the full trace on reload.
type TurnEvent struct {
	Type      string    `json:"type"`                 // "tool_use" | "tool_result" | "thinking"
	ToolName  string    `json:"tool_name,omitempty"`  // tool_use only
	ToolInput string    `json:"tool_input,omitempty"` // tool_use only
	ToolUseID string    `json:"tool_use_id,omitempty"`
	IsError   bool      `json:"is_error,omitempty"` // tool_result only
	Text      string    `json:"text,omitempty"`     // tool_result body / thinking text
	At        time.Time `json:"at,omitempty"`       // when this event arrived
	EndAt     time.Time `json:"end_at,omitempty"`   // tool_result: when tool finished
}

// ConversationTurn is the on-disk shape of one user/assistant turn.
type ConversationTurn struct {
	Timestamp time.Time   `json:"ts"`
	Role      string      `json:"role"`            // "user" | "assistant" | "system"
	Agent     string      `json:"agent,omitempty"` // assistant turn only
	Source    string      `json:"source,omitempty"`
	Text      string      `json:"text"`
	Truncated bool        `json:"truncated,omitempty"`
	Events    []TurnEvent `json:"events,omitempty"` // tool/thinking trace
}

// Store collects events for one session+agent and persists them.
type Store struct {
	layout    config.Layout
	sessionID string
	agentName string

	// turnBuf accumulates TextDelta chunks; flushed on Done.
	turnBuf strings.Builder

	// eventBuf collects tool_use/tool_result/thinking events within the
	// current turn so they can be stored alongside the text.
	// mu guards eventBuf only — Apply is single-goroutine but
	// InFlightEvents is called from HTTP handler goroutines.
	mu       sync.RWMutex
	eventBuf []TurnEvent

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
		_ = s.appendInflight(InflightEntry{
			Type: "text_delta",
			Text: ev.Text,
			At:   s.now().UTC(),
		})
		return false, nil

	case event.Thinking:
		now := s.now().UTC()
		te := TurnEvent{
			Type: "thinking",
			Text: ev.Text,
			At:   now,
		}
		s.mu.Lock()
		s.eventBuf = append(s.eventBuf, te)
		s.mu.Unlock()
		_ = s.appendInflight(InflightEntry{
			Type: "thinking",
			Text: ev.Text,
			At:   now,
		})
		return false, nil

	case event.ToolUse:
		now := s.now().UTC()
		te := TurnEvent{
			Type:      "tool_use",
			ToolName:  ev.ToolName,
			ToolInput: ev.ToolInput,
			ToolUseID: ev.ToolUseID,
			At:        now,
		}
		s.mu.Lock()
		s.eventBuf = append(s.eventBuf, te)
		s.mu.Unlock()
		_ = s.appendInflight(InflightEntry{
			Type:      "tool_use",
			ToolName:  ev.ToolName,
			ToolInput: ev.ToolInput,
			ToolUseID: ev.ToolUseID,
			At:        now,
		})
		return false, nil

	case event.ToolResult:
		now := s.now().UTC()
		s.mu.Lock()
		for i := range s.eventBuf {
			if s.eventBuf[i].Type == "tool_use" && s.eventBuf[i].ToolUseID == ev.ToolUseID {
				s.eventBuf[i].EndAt = now
				break
			}
		}
		s.eventBuf = append(s.eventBuf, TurnEvent{
			Type:      "tool_result",
			ToolUseID: ev.ToolUseID,
			IsError:   ev.IsError,
			Text:      ev.Text,
			At:        now,
		})
		s.mu.Unlock()
		_ = s.appendInflight(InflightEntry{
			Type:      "tool_result",
			ToolUseID: ev.ToolUseID,
			IsError:   ev.IsError,
			Text:      ev.Text,
			At:        now,
		})
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

// InFlightEvents returns a snapshot of events buffered in the current
// turn that have not yet been flushed to disk (no Done received yet).
// Safe to call from any goroutine — returns a copy.
func (s *Store) InFlightEvents() []TurnEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.eventBuf) == 0 {
		return nil
	}
	out := make([]TurnEvent, len(s.eventBuf))
	copy(out, s.eventBuf)
	return out
}

// PartialText returns the assistant text accumulated so far for the
// in-flight turn (everything appended via TextDelta since the last
// flushAssistantTurn). Empty string when no turn is in progress.
//
// Used by the SSE snapshot endpoint so a page refresh mid-stream can
// repaint the partial bubble instead of waiting for the next delta or
// losing the text entirely until Done writes it to conversation.jsonl.
//
// Safe to call from any goroutine — returns a defensive copy.
func (s *Store) PartialText() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.turnBuf.Len() == 0 {
		return ""
	}
	return s.turnBuf.String()
}

// Flush is the explicit drain hook for callers that want to write
// whatever's buffered (e.g. subprocess crashed mid-stream, no Done
// arrived). Marks the turn as truncated since it didn't end naturally.
func (s *Store) Flush() error {
	s.mu.RLock()
	evEmpty := len(s.eventBuf) == 0
	s.mu.RUnlock()
	if s.turnBuf.Len() == 0 && evEmpty {
		return nil
	}
	return s.flushAssistantTurn(true)
}

// flushAssistantTurn writes the buffered text as one assistant turn
// and resets the buffer. wasInterrupted=true sets Truncated on the
// turn — used by Flush when there was no Done event.
func (s *Store) flushAssistantTurn(wasInterrupted bool) error {
	if s.turnBuf.Len() == 0 && len(s.eventBuf) == 0 {
		return nil
	}
	body := s.turnBuf.String()
	truncated := wasInterrupted
	if len(body) > MaxAssistantTurnBytes {
		body = body[:MaxAssistantTurnBytes] + "\n…(truncated, see raw.jsonl)"
		truncated = true
	}
	s.mu.Lock()
	evSnap := s.eventBuf
	s.eventBuf = nil
	s.mu.Unlock()
	turn := ConversationTurn{
		Timestamp: s.now().UTC(),
		Role:      "assistant",
		Agent:     s.agentName,
		Text:      body,
		Truncated: truncated,
		Events:    evSnap,
	}
	s.turnBuf.Reset()
	if err := storage.AppendJSONL(
		s.layout.SessionConversation(s.sessionID),
		"wick-conv-v1",
		s.sessionID,
		turn,
	); err != nil {
		return err
	}
	// Turn safely persisted in conversation.jsonl → drop the inflight
	// mirror so a crash AFTER this point doesn't replay an already-saved
	// turn on next boot. Missing-file is fine; ignore any other error
	// (the canonical record is already on disk).
	if err := os.Remove(s.layout.SessionInflight(s.sessionID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		// non-fatal: log left to caller's discretion via raw.jsonl audit
	}
	return nil
}

// InflightEntry is one line of inflight.jsonl. Mirrors TurnEvent +
// text_delta chunks so a crash mid-stream leaves a full replay log on
// disk. Provider-agnostic — claude TextDelta, codex item.updated, and
// future CLIs all serialise through the same shape via store.Apply.
type InflightEntry struct {
	Type      string    `json:"type"` // "text_delta" | "thinking" | "tool_use" | "tool_result"
	Text      string    `json:"text,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
	ToolInput string    `json:"tool_input,omitempty"`
	ToolUseID string    `json:"tool_use_id,omitempty"`
	IsError   bool      `json:"is_error,omitempty"`
	At        time.Time `json:"at,omitempty"`
}

// appendInflight writes one entry to the session's inflight.jsonl.
// Best-effort: a transient disk error here loses one replay frame on
// crash but never blocks the live event pipeline. Caller passes the
// already-built entry so the struct is identical to what consumers
// see at replay time.
func (s *Store) appendInflight(e InflightEntry) error {
	return storage.AppendJSONL(
		s.layout.SessionInflight(s.sessionID),
		"wick-inflight-v1",
		s.sessionID,
		e,
	)
}

// LoadInflight reads inflight.jsonl for a session and returns every
// entry in order. Used at boot/snapshot time to repaint a turn that
// was mid-stream when the process died. Missing file is treated as
// "no inflight" (returns nil, nil) so callers don't need to stat.
func LoadInflight(layout config.Layout, sessionID string) ([]InflightEntry, error) {
	var out []InflightEntry
	err := storage.ReadJSONL(layout.SessionInflight(sessionID), func(line []byte) bool {
		var e InflightEntry
		if jerr := json.Unmarshal(line, &e); jerr != nil {
			return true // skip malformed lines, keep reading
		}
		if e.Type == "" {
			return true
		}
		out = append(out, e)
		return true
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return out, nil
}

// RecoverInflight merges a leftover inflight.jsonl (turn was mid-stream
// when the previous wick process died) into conversation.jsonl as one
// truncated assistant turn, then deletes the inflight file. Called from
// registry boot so the next chat continues from a consistent history
// instead of branching off a partial turn.
//
// agentName goes onto the assistant turn record so the UI groups it
// under the right agent; pass session.Meta.ActiveAgent or the first
// entry of session.Agents.
//
// Returns true when a recovery write actually happened (caller may want
// to log). Missing file or empty entries → (false, nil). Any disk error
// in append OR delete propagates so the caller knows the file is still
// there (registry boot can choose to keep going).
func RecoverInflight(layout config.Layout, sessionID, agentName string, now func() time.Time) (bool, error) {
	entries, err := LoadInflight(layout, sessionID)
	if err != nil {
		return false, err
	}
	if len(entries) == 0 {
		// File missing or empty — clean up empty file if present and bail.
		_ = os.Remove(layout.SessionInflight(sessionID))
		return false, nil
	}
	if now == nil {
		now = time.Now
	}
	var body strings.Builder
	var events []TurnEvent
	for _, e := range entries {
		switch e.Type {
		case "text_delta":
			body.WriteString(e.Text)
		case "thinking", "tool_use", "tool_result":
			events = append(events, TurnEvent{
				Type:      e.Type,
				ToolName:  e.ToolName,
				ToolInput: e.ToolInput,
				ToolUseID: e.ToolUseID,
				IsError:   e.IsError,
				Text:      e.Text,
				At:        e.At,
			})
		}
	}
	if body.Len() == 0 && len(events) == 0 {
		_ = os.Remove(layout.SessionInflight(sessionID))
		return false, nil
	}
	text := body.String()
	if len(text) > MaxAssistantTurnBytes {
		text = text[:MaxAssistantTurnBytes] + "\n…(truncated, see raw.jsonl)"
	}
	turn := ConversationTurn{
		Timestamp: now().UTC(),
		Role:      "assistant",
		Agent:     agentName,
		Text:      text,
		Truncated: true,
		Events:    events,
	}
	if err := storage.AppendJSONL(
		layout.SessionConversation(sessionID),
		"wick-conv-v1",
		sessionID,
		turn,
	); err != nil {
		return false, err
	}
	if err := os.Remove(layout.SessionInflight(sessionID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return true, err
	}
	return true, nil
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
		// Keep ProviderSessions in sync so switch-back can resume.
		if sess.Agents[i].ProviderSessions == nil {
			sess.Agents[i].ProviderSessions = map[string]string{}
		}
		sess.Agents[i].ProviderSessions[sess.Agents[i].Provider] = id
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
