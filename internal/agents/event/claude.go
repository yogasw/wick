package event

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ClaudeParser parses the Claude CLI `--output-format stream-json`
// stream when claude is run as `claude -p --verbose --input-format
// stream-json --output-format stream-json` (the long-lived headless
// mode used by ClaudeSpawner).
//
// Wire shape per turn:
//
//	1. {"type":"system","subtype":"hook_started", ...}        // optional, skip
//	2. {"type":"system","subtype":"hook_response", ...}       // optional, skip
//	3. {"type":"system","subtype":"init", "session_id":"...", ...}
//	4. {"type":"assistant","message":{"content":[
//	       {"type":"text","text":"..."},
//	       {"type":"tool_use","id":"t1","name":"Bash","input":{}}
//	   ]}}
//	5. {"type":"user","message":{"content":[
//	       {"type":"tool_result","tool_use_id":"t1","content":"..."}
//	   ]}}                                                    // tool result wrapped as user msg
//	6. {"type":"result","subtype":"success","is_error":false,"result":"..."}
//	7. ... process stays alive, next turn starts at step 3 again
//
// Concurrency: not safe for concurrent use. One parser per subprocess.
type ClaudeParser struct {
	// sessionID is captured from the first `system subtype=init` event.
	// Claude tags every event with `session_id`, but we only emit
	// SessionStart once per process lifetime.
	sessionID string

	// sessionEmitted is true after the first SessionStart we returned.
	sessionEmitted bool
}

// NewClaudeParser returns a fresh parser ready to consume Claude
// stream-json lines.
func NewClaudeParser() *ClaudeParser { return &ClaudeParser{} }

// claudeRaw is the wire shape of one stream-json line. We model only
// the fields we use; unknown fields are ignored.
type claudeRaw struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	Result    string `json:"result,omitempty"`

	// `assistant` and `user` wrap content blocks under .message.content
	Message *claudeMessage `json:"message,omitempty"`
}

type claudeMessage struct {
	Content []claudeContentBlock `json:"content,omitempty"`
}

type claudeContentBlock struct {
	Type string `json:"type"`
	// text-block fields
	Text string `json:"text,omitempty"`
	// tool_use fields
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// thinking fields
	Thinking string `json:"thinking,omitempty"`
	// tool_result fields
	ToolUseID string `json:"tool_use_id,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	// tool_result.content can be a string OR an array of blocks; keep
	// the raw bytes so we don't fight Anthropic's polymorphism here.
	ResultContent json.RawMessage `json:"content,omitempty"`
}

// Parse decodes one line and returns the normalized event. Empty / ws-
// only lines yield (Unknown, nil) — caller can stream stdout without
// filtering.
//
// A single claude line frequently carries multiple semantic events
// (e.g. one assistant message with both text and tool_use blocks). We
// can't return more than one event per call, so we collapse: text
// content wins over tool_use for the headline event, but we still
// surface the tool via subsequent calls? No — claude emits one block
// type per assistant frame in practice; if both ever co-occur, the
// raw line is preserved so downstream consumers can re-parse.
func (p *ClaudeParser) Parse(line string) (AgentEvent, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return AgentEvent{}, nil
	}

	var raw claudeRaw
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return AgentEvent{}, fmt.Errorf("claude parse: %w", err)
	}

	switch raw.Type {
	case "system":
		// `init` carries the session_id we want for resume. Other
		// system subtypes (`hook_started`, `hook_response`,
		// `compaction`, ...) are noise from claude's lifecycle hooks
		// and don't map to anything user-visible.
		if raw.Subtype == "init" && raw.SessionID != "" {
			if !p.sessionEmitted {
				p.sessionID = raw.SessionID
				p.sessionEmitted = true
				return AgentEvent{
					Type:      SessionStart,
					SessionID: raw.SessionID,
					Raw:       trimmed,
				}, nil
			}
			// New `init` after the first means a follow-up turn within
			// the same long-lived process. Same session_id; nothing to
			// emit downstream.
			p.sessionID = raw.SessionID
		}
		return AgentEvent{Type: Unknown, Raw: trimmed}, nil

	case "assistant":
		// claude packs text + tool_use blocks into one frame. Iterate
		// to find the first interesting block. If both text and
		// tool_use are present we prefer tool_use (gate-relevant) and
		// drop text — the result event will carry the final assistant
		// text in .result, so we don't lose user-visible output.
		if raw.Message == nil {
			return AgentEvent{Type: Unknown, Raw: trimmed}, nil
		}
		for _, b := range raw.Message.Content {
			switch b.Type {
			case "tool_use":
				return AgentEvent{
					Type:      ToolUse,
					ToolName:  b.Name,
					ToolInput: string(b.Input),
					ToolUseID: b.ID,
					Raw:       trimmed,
				}, nil
			case "thinking":
				if b.Thinking != "" {
					return AgentEvent{
						Type: Thinking,
						Text: b.Thinking,
						Raw:  trimmed,
					}, nil
				}
			}
		}
		// No tool_use/thinking — return concatenated text as TextDelta.
		var buf strings.Builder
		for _, b := range raw.Message.Content {
			if b.Type == "text" {
				buf.WriteString(b.Text)
			}
		}
		if buf.Len() == 0 {
			return AgentEvent{Type: Unknown, Raw: trimmed}, nil
		}
		return AgentEvent{
			Type: TextDelta,
			Text: buf.String(),
			Raw:  trimmed,
		}, nil

	case "user":
		// In headless stream-json mode claude wraps tool_result blocks
		// as `user` messages. Surface them so the store can append a
		// tool-result line to commands.jsonl / raw.jsonl.
		if raw.Message == nil {
			return AgentEvent{Type: Unknown, Raw: trimmed}, nil
		}
		for _, b := range raw.Message.Content {
			if b.Type == "tool_result" {
				return AgentEvent{
					Type:      ToolResult,
					Text:      string(b.ResultContent),
					ToolUseID: b.ToolUseID,
					IsError:   b.IsError,
					Raw:       trimmed,
				}, nil
			}
		}
		return AgentEvent{Type: Unknown, Raw: trimmed}, nil

	case "result":
		// `result` ends the current turn. is_error=true means claude
		// itself failed (auth, rate limit, model error) — surface as
		// Error so the agent can react. .result holds the final
		// assistant text on success; we already streamed it via
		// TextDelta above so we don't re-emit here.
		if raw.IsError {
			return AgentEvent{
				Type:     Error,
				ErrorMsg: raw.Result,
				Raw:      trimmed,
			}, nil
		}
		return AgentEvent{
			Type:      Done,
			SessionID: p.sessionID,
			Raw:       trimmed,
		}, nil
	}

	// Pass-through for anything else (rate_limit_event, status, etc.) —
	// store them in raw.jsonl but don't drive downstream state.
	return AgentEvent{Type: Unknown, Raw: trimmed}, nil
}

// SessionID returns the captured CLI session ID, or "" if no `system
// init` event has been seen yet.
func (p *ClaudeParser) SessionID() string { return p.sessionID }
