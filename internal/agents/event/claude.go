package event

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
)

// ClaudeParser parses the Claude CLI `--output-format stream-json`
// stream when claude is run as `claude -p --verbose --input-format
// stream-json --output-format stream-json` (the long-lived headless
// mode used by ClaudeSpawner).
//
// Wire shape per turn:
//
//  1. {"type":"system","subtype":"hook_started", ...}        // optional, skip
//  2. {"type":"system","subtype":"hook_response", ...}       // optional, skip
//  3. {"type":"system","subtype":"init", "session_id":"...", ...}
//  4. {"type":"assistant","message":{"content":[
//     {"type":"text","text":"..."},
//     {"type":"tool_use","id":"t1","name":"Bash","input":{}}
//     ]}}
//  5. {"type":"user","message":{"content":[
//     {"type":"tool_result","tool_use_id":"t1","content":"..."}
//     ]}}                                                    // tool result wrapped as user msg
//  6. {"type":"result","subtype":"success","is_error":false,"result":"..."}
//  7. ... process stays alive, next turn starts at step 3 again
//
// Concurrency: not safe for concurrent use. One parser per subprocess.
type ClaudeParser struct {
	// sessionID is captured from the first `system subtype=init` event.
	// Claude tags every event with `session_id`, but we only emit
	// SessionStart once per process lifetime.
	sessionID string

	// sessionEmitted is true after the first SessionStart we returned.
	sessionEmitted bool

	// partialTextEmitted tracks whether any content_block_delta of type
	// text_delta has been emitted in the current turn. When true, the
	// trailing `assistant` frame's text content is suppressed (the FE
	// would otherwise see the same text twice — once as live deltas,
	// once as the final block). Cleared on Done/Error.
	partialTextEmitted bool
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

	// `stream_event` (only when --include-partial-messages is set)
	// wraps an Anthropic Messages-API streaming event in .event.
	Event *claudeStreamEvent `json:"event,omitempty"`
}

// claudeStreamEvent mirrors the Anthropic Messages API streaming event
// shape. We only model content_block_delta payloads — the start/stop/
// message_delta bookends are not actionable here (turn lifecycle is
// already covered by `system init` + `result`).
type claudeStreamEvent struct {
	Type  string             `json:"type"`            // message_start | content_block_start | content_block_delta | content_block_stop | message_delta | message_stop
	Index int                `json:"index,omitempty"` // content block index
	Delta *claudeStreamDelta `json:"delta,omitempty"`
}

// claudeStreamDelta carries the incremental payload. For text we get
// text_delta with .text; for thinking we get thinking_delta with
// .thinking; for tool_use input we get input_json_delta — we don't
// stream tool args, so input_json_delta is ignored (the final
// `assistant` frame surfaces tool_use as one ToolUse event).
type claudeStreamDelta struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
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

	case "stream_event":
		// Partial-message stream from --include-partial-messages. Only
		// content_block_delta with a text_delta or thinking_delta is
		// actionable; the rest (message_start, content_block_start,
		// message_delta usage, etc.) are bookends with no user-visible
		// payload.
		if raw.Event == nil {
			log.Debug().Msg("claude.parse: stream_event without inner event")
			return AgentEvent{Type: Unknown, Raw: trimmed}, nil
		}
		if raw.Event.Type != "content_block_delta" || raw.Event.Delta == nil {
			log.Debug().Str("inner_type", raw.Event.Type).Msg("claude.parse: stream_event bookend, skipping")
			return AgentEvent{Type: Unknown, Raw: trimmed}, nil
		}
		switch raw.Event.Delta.Type {
		case "text_delta":
			if raw.Event.Delta.Text == "" {
				return AgentEvent{Type: Unknown, Raw: trimmed}, nil
			}
			p.partialTextEmitted = true
			log.Debug().Int("len", len(raw.Event.Delta.Text)).Msg("claude.parse: stream text_delta")
			return AgentEvent{
				Type: TextDelta,
				Text: raw.Event.Delta.Text,
				Raw:  trimmed,
			}, nil
		case "thinking_delta":
			if raw.Event.Delta.Thinking == "" {
				return AgentEvent{Type: Unknown, Raw: trimmed}, nil
			}
			log.Debug().Int("len", len(raw.Event.Delta.Thinking)).Msg("claude.parse: stream thinking_delta")
			return AgentEvent{
				Type: Thinking,
				Text: raw.Event.Delta.Thinking,
				Raw:  trimmed,
			}, nil
		}
		log.Debug().Str("delta_type", raw.Event.Delta.Type).Msg("claude.parse: stream_event unknown delta type")
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
					// Suppress when stream_event thinking_delta already
					// streamed this text; otherwise emit as one block.
					if p.partialTextEmitted {
						return AgentEvent{Type: Unknown, Raw: trimmed}, nil
					}
					return AgentEvent{
						Type: Thinking,
						Text: b.Thinking,
						Raw:  trimmed,
					}, nil
				}
			}
		}
		// If --include-partial-messages already streamed the text via
		// stream_event content_block_delta, suppress the trailing
		// `assistant` frame's text — it's the same content concatenated,
		// emitting it again would double-render in the UI bubble and
		// double the assistant turn body in conversation.jsonl.
		if p.partialTextEmitted {
			return AgentEvent{Type: Unknown, Raw: trimmed}, nil
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
		//
		// Reset the partial-text guard so the next turn's `assistant`
		// frame is treated fresh (we may not get stream_event deltas
		// for short replies — claude can batch them).
		p.partialTextEmitted = false
		if raw.IsError {
			// error_during_execution puts the detail on stderr, leaving
			// .result empty — fall back to subtype so the error isn't blank.
			msg := raw.Result
			if msg == "" {
				msg = raw.Subtype
			}
			return AgentEvent{
				Type:     Error,
				ErrorMsg: msg,
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
