package wick

import (
	"encoding/json"
)

// emit.go builds the claude `--output-format stream-json` lines the
// ClaudeParser (internal/agents/event/claude.go) already understands.
// The in-process engine writes these to the process pipe so the entire
// downstream machinery — parser, store, state machine, SSE, spawn-log —
// works byte-identically to a real claude subprocess. This file is the
// single source of truth for the wire shape; keep it aligned with the
// parser's expectations (see the wire-shape comment atop claude.go).

// initLine is the first line of a session: `system/init` carrying the
// session id. The parser emits SessionStart from it exactly once.
func initLine(sessionID string) []byte {
	return mustLine(map[string]any{
		"type":       "system",
		"subtype":    "init",
		"session_id": sessionID,
	})
}

// textLine wraps assistant text as an `assistant` frame with a single
// text block → TextDelta.
func textLine(text string) []byte {
	return assistantLine([]map[string]any{{"type": "text", "text": text}})
}

// thinkingLine wraps a reasoning block → Thinking.
func thinkingLine(text string) []byte {
	return assistantLine([]map[string]any{{"type": "thinking", "thinking": text}})
}

// textDeltaLine wraps an incremental text chunk as a `stream_event`
// content_block_delta → the parser's TextDelta (append) path. Emitting
// these token-by-token makes wick stream live like a real claude
// --include-partial-messages run; the parser then SUPPRESSES the trailing
// full `assistant` text frame (partialTextEmitted), so there's no
// double-render — this is the whole reason the engine can stream safely.
func textDeltaLine(text string) []byte {
	return streamDeltaLine("text_delta", "text", text)
}

// thinkingDeltaLine is textDeltaLine's reasoning counterpart → Thinking
// (append). The trailing full thinking block is likewise suppressed.
func thinkingDeltaLine(text string) []byte {
	return streamDeltaLine("thinking_delta", "thinking", text)
}

// streamDeltaLine builds one Anthropic-shaped content_block_delta frame the
// ClaudeParser recognizes (see claude.go's stream_event case). field is the
// delta payload key ("text" or "thinking") matching deltaType.
func streamDeltaLine(deltaType, field, text string) []byte {
	return mustLine(map[string]any{
		"type": "stream_event",
		"event": map[string]any{
			"type": "content_block_delta",
			"delta": map[string]any{
				"type": deltaType,
				field:  text,
			},
		},
	})
}

// toolUseLine wraps a tool call → ToolUse. input is the raw JSON args
// object; toolUseID correlates with the later tool_result.
func toolUseLine(id, name string, input json.RawMessage) []byte {
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	return assistantLine([]map[string]any{{
		"type":  "tool_use",
		"id":    id,
		"name":  name,
		"input": input,
	}})
}

// toolResultLine wraps a tool result as a `user` frame (claude's
// headless convention) → ToolResult.
func toolResultLine(toolUseID, content string, isError bool) []byte {
	return mustLine(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": toolUseID,
				"content":     content,
				"is_error":    isError,
			}},
		},
	})
}

// doneLine ends a turn successfully → Done. The final assistant text is
// carried in .result; the engine also streams it via textLine, and the
// parser dedups, so this mainly signals turn completion.
func doneLine(result string) []byte {
	return mustLine(map[string]any{
		"type":     "result",
		"subtype":  "success",
		"is_error": false,
		"result":   result,
	})
}

// errorLine ends a turn with a failure → Error, surfaced in the UI.
func errorLine(msg string) []byte {
	return mustLine(map[string]any{
		"type":     "result",
		"subtype":  "error_during_execution",
		"is_error": true,
		"result":   msg,
	})
}

// heartbeatLine is a no-op system frame emitted periodically while a
// model call is in flight. A single non-streaming vendor call can take
// up to perCallTimeout (120s) with no AgentEvent to emit, which looks
// identical to a stuck session to the pool's idle-kill timer (it only
// watches for pipe writes, e.g. agent.go's stdout scanner) — so a slow
// vendor response and a genuinely hung process race the same clock, and
// the idle-kill can fire mid-call. The parser drops unrecognized system
// subtypes (see claude.go), so this resets the idle timer without
// surfacing anything in the UI.
func heartbeatLine() []byte {
	return mustLine(map[string]any{
		"type":    "system",
		"subtype": "heartbeat",
	})
}

func assistantLine(content []map[string]any) []byte {
	return mustLine(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"content": content,
		},
	})
}

// mustLine marshals v to a single JSON line. Marshal of these fixed
// shapes cannot fail; on the impossible error we return an empty object
// line so the reader never blocks on malformed bytes.
func mustLine(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
