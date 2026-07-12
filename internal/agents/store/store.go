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
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
)

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

// Attachment is one file uploaded with a user turn. The file content
// lives under <SessionDir>/uploads/<StoredName>; URL is the path the UI
// uses to fetch it (served via /tools/agents/sessions/<id>/uploads/...).
// AbsPath is the on-disk path passed to the CLI subprocess so it can
// Read the file via tool calls.
type Attachment struct {
	Name       string `json:"name"`               // original filename (display)
	StoredName string `json:"stored_name"`        // filename under uploads dir
	URL        string `json:"url,omitempty"`      // GET path for the UI
	AbsPath    string `json:"abs_path,omitempty"` // absolute disk path for CLI
	MIME       string `json:"mime,omitempty"`
	Size       int64  `json:"size,omitempty"`
}

// Artifact is a file produced by an assistant turn, derived from the turn's
// trace at read time (never persisted). URL serves bytes inline (images, pdf);
// DownloadURL forces a download. Kind drives how the UI previews it.
type Artifact struct {
	Name        string `json:"name"`
	Path        string `json:"path"` // relative to session cwd, forward slashes
	URL         string `json:"url"`
	DownloadURL string `json:"download_url"`
	Kind        string `json:"kind"` // image | pdf | html | file
	MIME        string `json:"mime,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

// ConversationTurn is the on-disk shape of one user/assistant turn.
// Events are NOT stored here — they live in thinking/<TurnID>.json so
// conversation.jsonl stays small regardless of tool payload size.
type ConversationTurn struct {
	TurnID      string       `json:"turn_id,omitempty"`
	Timestamp   time.Time    `json:"ts"`
	Role        string       `json:"role"`               // "user" | "assistant" | "system"
	Agent       string       `json:"agent,omitempty"`    // assistant turn only
	Provider    string       `json:"provider,omitempty"` // assistant turn only — "type/name" snapshot at turn time
	Source      string       `json:"source,omitempty"`
	Text        string       `json:"text"`
	Truncated   bool         `json:"truncated,omitempty"`
	Interrupted bool         `json:"interrupted,omitempty"` // true when killed before Done — distinct from text-cap truncation
	HasTrace    bool         `json:"has_trace,omitempty"`   // true when thinking/<TurnID>.json exists
	Events      []TurnEvent  `json:"events,omitempty"`      // legacy: populated only when reading old turns
	Attachments []Attachment `json:"attachments,omitempty"`  // user turn only
	HasArtifact bool         `json:"has_artifact,omitempty"` // assistant turn — true when Artifacts derived
	Artifacts   []Artifact   `json:"artifacts,omitempty"`    // assistant turn, derived read-time
	IsError     bool         `json:"is_error,omitempty"`     // system turn — provider/runtime error, render as a failure

	// Kind tags a structured system turn so the UI can render it specially
	// and callers can identify it (e.g. "provider_switch"). Empty for a
	// plain system message. Extras carries arbitrary metadata for that kind
	// (provider_switch: from, to, note) without growing the schema per feature.
	Kind   string            `json:"kind,omitempty"`   // system turn only
	Extras map[string]string `json:"extras,omitempty"` // system turn only
}

// SystemTurnKind values for ConversationTurn.Kind.
const KindProviderSwitch = "provider_switch"

// TurnTraceIndex is the lightweight index written to thinking/<turn_id>.json.
// Events below the inline threshold have their Text embedded here.
// Events at or above the threshold have Text omitted and Large=true —
// UI must fetch thinking/<turn_id>/<event_id>.json separately.
type TurnTraceIndex struct {
	TurnID string            `json:"turn_id"`
	Events []TurnEventIndex  `json:"events"`
}

// TurnEventIndex is one row in the trace index.
type TurnEventIndex struct {
	EventID   string    `json:"event_id"`
	Type      string    `json:"type"`
	ToolName  string    `json:"tool_name,omitempty"`
	ToolUseID string    `json:"tool_use_id,omitempty"`
	IsError   bool      `json:"is_error,omitempty"`
	At        time.Time `json:"at,omitempty"`
	EndAt     time.Time `json:"end_at,omitempty"`
	// Inline payload — present only when size < threshold.
	Text      string `json:"text,omitempty"`
	ToolInput string `json:"tool_input,omitempty"`
	// Large=true means payload lives in <event_id>.json, fetch on demand.
	Large bool  `json:"large,omitempty"`
	Size  int64 `json:"size,omitempty"`
}

// TurnEventPayload is written to thinking/<turn_id>/<event_id>.json
// for large events.
type TurnEventPayload struct {
	EventID   string `json:"event_id"`
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ToolInput string `json:"tool_input,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// DefaultTraceInlineBytes is the fallback threshold when no config is set.
// Events with combined text+tool_input payload below this are stored inline
// in the trace index; larger events get their own file.
const DefaultTraceInlineBytes = 10 * 1024

// Store collects events for one session+agent and persists them.
type Store struct {
	layout    config.Layout
	sessionID string
	agentName string
	provider  string // "type/name" — stamped onto each assistant turn

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

	// traceInlineBytes is the per-event payload threshold in bytes.
	// Events larger than this are written to a separate file.
	// 0 = use DefaultTraceInlineBytes.
	traceInlineBytes int
	// traceEventMaxBytes caps per-event file payload. 0 = no cap.
	traceEventMaxBytes int

	now func() time.Time
}

// Options configures a Store. AgentName ties assistant turns to the
// agents.json entry that emitted them. Provider is "type/name" and is
// stamped on every assistant turn so the UI can render which model
// produced it even after the active provider switches.
type Options struct {
	Layout           config.Layout
	SessionID        string
	AgentName        string
	Provider         string
	RecordRaw        bool
	TraceInlineBytes   int             // 0 = DefaultTraceInlineBytes
	TraceEventMaxBytes int             // 0 = no cap on per-event file size
	Now                func() time.Time // optional; defaults to time.Now
}

// New returns a Store ready to consume events. Caller owns the
// session/agent CRUD; the store only writes turns + cli_session_id.
func New(opt Options) *Store {
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	inlineBytes := opt.TraceInlineBytes
	if inlineBytes <= 0 {
		inlineBytes = DefaultTraceInlineBytes
	}
	return &Store{
		layout:             opt.Layout,
		sessionID:          opt.SessionID,
		agentName:          opt.AgentName,
		provider:           opt.Provider,
		recordRaw:          opt.RecordRaw,
		traceInlineBytes:   inlineBytes,
		traceEventMaxBytes: opt.TraceEventMaxBytes,
		now:                now,
	}
}

// AppendUserTurn records a user / system message before it's sent to
// the subprocess. Source is the transport label ("ui", "slack",
// "api"). Role is normally "user"; "system" for operator instructions.
func (s *Store) AppendUserTurn(role, source, text string) error {
	return s.AppendUserTurnWithAttachments(role, source, text, nil)
}

// AppendUserTurnWithAttachments is AppendUserTurn plus a list of
// uploaded files. The attachments are persisted alongside the text so
// the UI can re-render thumbnails / file chips after reload.
func (s *Store) AppendUserTurnWithAttachments(role, source, text string, atts []Attachment) error {
	turn := ConversationTurn{
		Timestamp:   s.now().UTC(),
		Role:        role,
		Source:      source,
		Text:        text,
		Attachments: atts,
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
		s.mu.Lock()
		s.turnBuf.WriteString(ev.Text)
		s.mu.Unlock()
		_ = s.appendInflight(InflightEntry{
			Type: "text_delta",
			Text: ev.Text,
			At:   s.now().UTC(),
		})
		return false, nil

	case event.Thinking:
		now := s.now().UTC()
		s.mu.Lock()
		if n := len(s.eventBuf); n > 0 && s.eventBuf[n-1].Type == "thinking" {
			s.eventBuf[n-1].Text += ev.Text
		} else {
			s.eventBuf = append(s.eventBuf, TurnEvent{
				Type: "thinking",
				Text: ev.Text,
				At:   now,
			})
		}
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
		// Persist the error itself as a system turn so it survives a reload
		// and renders in history — otherwise it's only a transient SSE event
		// that vanishes (the user just sees "reconnecting…"). Skip when the
		// error carries no message (nothing useful to show).
		if msg := strings.TrimSpace(ev.ErrorMsg); msg != "" {
			if err := s.appendErrorTurn(msg); err != nil {
				return false, err
			}
		}
		return true, nil

	case event.Warning:
		// Non-fatal error the CLI reported mid-stream — record it as an error
		// turn so it shows in history, but do NOT flush/end the turn: the
		// subprocess is still running and more output will follow.
		if msg := strings.TrimSpace(ev.ErrorMsg); msg != "" {
			if err := s.appendErrorTurn(msg); err != nil {
				return false, err
			}
		}
		return false, nil

	case event.Trace:
		// Unrecognized frame — keep it in the turn's trace (expandable in
		// the UI) instead of the main thread. Buffered like other trace
		// events; flushed with the turn. Never ends the turn.
		raw := ev.Text
		if raw == "" {
			raw = ev.Raw
		}
		if strings.TrimSpace(raw) != "" {
			now := s.now().UTC()
			s.mu.Lock()
			s.eventBuf = append(s.eventBuf, TurnEvent{Type: "raw", Text: raw, At: now})
			s.mu.Unlock()
			_ = s.appendInflight(InflightEntry{Type: "raw", Text: raw, At: now})
		}
		return false, nil
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
	bufEmpty := s.turnBuf.Len() == 0
	s.mu.RUnlock()
	if bufEmpty && evEmpty {
		return nil
	}
	return s.flushAssistantTurn(true)
}

// flushAssistantTurn writes the buffered text as one assistant turn
// and resets the buffer. wasInterrupted=true sets Truncated on the
// turn — used by Flush when there was no Done event.
//
// Events (tool_use, tool_result, thinking) are written to
// thinking/<turn_id>.json so conversation.jsonl stays lean.
func (s *Store) flushAssistantTurn(wasInterrupted bool) error {
	if s.turnBuf.Len() == 0 && len(s.eventBuf) == 0 {
		return nil
	}
	body := s.turnBuf.String()
	truncated := wasInterrupted
	s.mu.Lock()
	evSnap := s.eventBuf
	s.eventBuf = nil
	s.mu.Unlock()

	now := s.now().UTC()
	turnID := fmt.Sprintf("%d", now.UnixNano())

	// Write trace index + per-event files before the conversation entry
	// that references them.
	hasTrace := false
	if len(evSnap) > 0 {
		hasTrace = s.writeTraceIndex(turnID, evSnap)
	}

	turn := ConversationTurn{
		TurnID:      turnID,
		Timestamp:   now,
		Role:        "assistant",
		Agent:       s.agentName,
		Provider:    s.provider,
		Text:        body,
		Truncated:   truncated,
		Interrupted: wasInterrupted,
		HasTrace:    hasTrace,
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

// appendErrorTurn writes a provider/runtime error as a system turn to
// conversation.jsonl so it renders in history (IsError flags it for the UI
// to style as a failure). Called from the Error event path after any
// partial assistant text has been flushed.
// AppendErrorTurn persists a system error turn to conversation.jsonl. Used by
// callers outside the normal event flow — e.g. the pool when a spawn fails
// before the agent starts — to record the failure as inline history.
func (s *Store) AppendErrorTurn(msg string) error { return s.appendErrorTurn(msg) }

func (s *Store) appendErrorTurn(msg string) error {
	now := s.now().UTC()
	turn := ConversationTurn{
		TurnID:    fmt.Sprintf("%d", now.UnixNano()),
		Timestamp: now,
		Role:      "system",
		Agent:     s.agentName,
		Provider:  s.provider,
		Text:      msg,
		IsError:   true,
	}
	return storage.AppendJSONL(
		s.layout.SessionConversation(s.sessionID),
		"wick-conv-v1",
		s.sessionID,
		turn,
	)
}

// writeTraceIndex writes thinking/<turn_id>.json (the index) and, for
// events whose payload exceeds s.traceInlineBytes, a separate file at
// thinking/<turn_id>/<event_id>.json. Returns true when the index was
// successfully written.
// writeTraceIndexStatic is the package-level variant for callers without a *Store.
func writeTraceIndexStatic(layout config.Layout, sessionID, turnID string, evs []TurnEvent, inlineBytes int) bool {
	s := &Store{layout: layout, sessionID: sessionID, traceInlineBytes: inlineBytes}
	return s.writeTraceIndex(turnID, evs)
}

func (s *Store) writeTraceIndex(turnID string, evs []TurnEvent) bool {
	turnDir := s.layout.SessionThinkingTurnDir(s.sessionID, turnID)
	if err := os.MkdirAll(turnDir, 0o755); err != nil {
		return false
	}
	idx := TurnTraceIndex{TurnID: turnID}
	for i, ev := range evs {
		eventID := fmt.Sprintf("e%d", i)
		payload := ev.Text + ev.ToolInput
		row := TurnEventIndex{
			EventID:   eventID,
			Type:      ev.Type,
			ToolName:  ev.ToolName,
			ToolUseID: ev.ToolUseID,
			IsError:   ev.IsError,
			At:        ev.At,
			EndAt:     ev.EndAt,
		}
		if len(payload) >= s.traceInlineBytes {
			// Apply per-event max cap before writing to file.
			text := ev.Text
			toolInput := ev.ToolInput
			truncated := false
			if s.traceEventMaxBytes > 0 {
				if len(text) > s.traceEventMaxBytes {
					text = text[:s.traceEventMaxBytes]
					truncated = true
				}
				if len(toolInput) > s.traceEventMaxBytes {
					toolInput = toolInput[:s.traceEventMaxBytes]
					truncated = true
				}
			}
			ep := TurnEventPayload{
				EventID:   eventID,
				Type:      ev.Type,
				Text:      text,
				ToolInput: toolInput,
				Truncated: truncated,
			}
			if data, err := json.Marshal(ep); err == nil {
				_ = os.WriteFile(s.layout.SessionThinkingEvent(s.sessionID, turnID, eventID), data, 0o644)
			}
			row.Large = true
			row.Size = int64(len(payload))
		} else {
			row.Text = ev.Text
			row.ToolInput = ev.ToolInput
		}
		idx.Events = append(idx.Events, row)
	}
	data, err := json.Marshal(idx)
	if err != nil {
		return false
	}
	return os.WriteFile(s.layout.SessionThinking(s.sessionID, turnID), data, 0o644) == nil
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
// entry of session.Agents. provider is "type/name" of the agent that
// owned the inflight stream — stamped onto the recovered turn so the UI
// can label it like any other assistant turn.
//
// Returns true when a recovery write actually happened (caller may want
// to log). Missing file or empty entries → (false, nil). Any disk error
// in append OR delete propagates so the caller knows the file is still
// there (registry boot can choose to keep going).
func RecoverInflight(layout config.Layout, sessionID, agentName, provider string, now func() time.Time) (bool, error) {
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
	ts := now().UTC()
	turnID := fmt.Sprintf("%d", ts.UnixNano())
	hasTrace := writeTraceIndexStatic(layout, sessionID, turnID, events, DefaultTraceInlineBytes)
	turn := ConversationTurn{
		TurnID:      turnID,
		Timestamp:   ts,
		Role:        "assistant",
		Agent:       agentName,
		Provider:    provider,
		Text:        text,
		Truncated:   true,
		Interrupted: true,
		HasTrace:    hasTrace,
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
