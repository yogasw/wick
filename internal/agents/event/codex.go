package event

import (
	"encoding/json"
	"strings"

	"github.com/rs/zerolog/log"
)

// CodexParser parses the `codex exec --json` newline-delimited JSON stream.
//
// Actual wire shape from codex 0.129 --json:
//
//	{"type":"thread.started","thread_id":"<uuid>"}
//	{"type":"turn.started"}
//	{"type":"item.created","item":{"id":"...","type":"function_call","name":"...","call_id":"..."}}
//	{"type":"item.created","item":{"id":"...","type":"function_call_output","call_id":"...","output":"..."}}
//	{"type":"item.completed","item":{"id":"...","type":"agent_message","text":"..."}}
//	{"type":"turn.completed","usage":{...}}
//	{"type":"error","message":"..."}
//
// Session ends when process exits. thread_id is used as session ID for --resume.
//
// Concurrency: not safe for concurrent use. One parser per subprocess.
type CodexParser struct {
	sessionEmitted bool
}

// NewCodexParser returns a fresh parser ready to consume codex --json lines.
func NewCodexParser() *CodexParser { return &CodexParser{} }

type codexRaw struct {
	Type     string      `json:"type"`
	ThreadID string      `json:"thread_id,omitempty"`
	Item     *codexItem  `json:"item,omitempty"`
	Message  string      `json:"message,omitempty"`
}

type codexItem struct {
	ID     string `json:"id,omitempty"`
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	// function_call fields
	Name   string `json:"name,omitempty"`
	CallID string `json:"call_id,omitempty"`
	// function_call_output fields
	Output string `json:"output,omitempty"`
	// mcp_tool_call fields
	Server    string          `json:"server,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Result    *codexMCPResult `json:"result,omitempty"`
}

type codexMCPResult struct {
	Content []codexMCPContent `json:"content,omitempty"`
}

type codexMCPContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Parse decodes one codex --json line into an AgentEvent.
// Blank/whitespace lines return (Unknown, nil).
func (p *CodexParser) Parse(line string) (AgentEvent, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return AgentEvent{}, nil
	}

	log.Debug().Str("raw", trimmed).Msg("codex.parse: raw line")

	// Non-JSON lines (startup messages, taskkill output, warnings) surface
	// as Thinking events so they appear in the UI trace but are never
	// forwarded to the provider or sent to Slack/REST channels.
	if trimmed[0] != '{' && trimmed[0] != '[' {
		log.Debug().Str("line", trimmed).Msg("codex.parse: non-JSON line → trace")
		return AgentEvent{Type: Thinking, Text: trimmed, Raw: trimmed}, nil
	}
	var raw codexRaw
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		log.Warn().Str("line", trimmed).Err(err).Msg("codex.parse: unmarshal failed → trace")
		return AgentEvent{Type: Thinking, Text: trimmed, Raw: trimmed}, nil
	}

	log.Debug().Str("type", raw.Type).Str("thread_id", raw.ThreadID).Msg("codex.parse: decoded")

	switch raw.Type {
	case "thread.started":
		log.Debug().Str("thread_id", raw.ThreadID).Bool("already_emitted", p.sessionEmitted).Msg("codex.parse: thread.started")
		if !p.sessionEmitted && raw.ThreadID != "" {
			p.sessionEmitted = true
			return AgentEvent{
				Type:      SessionStart,
				SessionID: raw.ThreadID,
				Raw:       trimmed,
			}, nil
		}
		return AgentEvent{Type: Unknown, Raw: trimmed}, nil

	case "item.completed":
		if raw.Item == nil {
			return AgentEvent{Type: Unknown, Raw: trimmed}, nil
		}
		item := raw.Item
		log.Debug().Str("item_type", item.Type).Str("id", item.ID).Msg("codex.parse: item.completed")
		switch item.Type {
		case "agent_message":
			if item.Text != "" {
				return AgentEvent{
					Type: TextDelta,
					Text: item.Text,
					Raw:  trimmed,
				}, nil
			}
		case "function_call_output":
			return AgentEvent{
				Type:      ToolResult,
				Text:      item.Output,
				ToolUseID: item.CallID,
				Raw:       trimmed,
			}, nil
		case "mcp_tool_call":
			// completed mcp tool call = tool result
			text := ""
			if item.Result != nil {
				var parts []string
				for _, c := range item.Result.Content {
					if c.Type == "text" && c.Text != "" {
						parts = append(parts, c.Text)
					}
				}
				text = strings.Join(parts, "\n")
			}
			return AgentEvent{
				Type:      ToolResult,
				Text:      text,
				ToolUseID: item.ID,
				Raw:       trimmed,
			}, nil
		}
		return AgentEvent{Type: Unknown, Raw: trimmed}, nil

	case "item.created", "item.started":
		if raw.Item == nil {
			return AgentEvent{Type: Unknown, Raw: trimmed}, nil
		}
		item := raw.Item
		log.Debug().Str("item_type", item.Type).Str("name", item.Name).Str("tool", item.Tool).Msg("codex.parse: item.created/started")
		switch item.Type {
		case "function_call":
			fcInput := string(item.Arguments)
			if fcInput == "null" || fcInput == "{}" {
				fcInput = ""
			}
			return AgentEvent{
				Type:      ToolUse,
				ToolName:  item.Name,
				ToolUseID: item.CallID,
				ToolInput: fcInput,
				Raw:       trimmed,
			}, nil
		case "mcp_tool_call":
			name := item.Tool
			if item.Server != "" {
				name = item.Server + "." + item.Tool
			}
			toolInput := string(item.Arguments)
			if toolInput == "null" || toolInput == "{}" {
				toolInput = ""
			}
			return AgentEvent{
				Type:      ToolUse,
				ToolName:  name,
				ToolUseID: item.ID,
				ToolInput: toolInput,
				Raw:       trimmed,
			}, nil
		}
		return AgentEvent{Type: Unknown, Raw: trimmed}, nil

	case "turn.completed":
		log.Debug().Msg("codex.parse: turn.completed")
		return AgentEvent{Type: Done, Raw: trimmed}, nil

	case "error":
		log.Debug().Str("message", raw.Message).Msg("codex.parse: error event")
		return AgentEvent{
			Type:     Error,
			ErrorMsg: raw.Message,
			Raw:      trimmed,
		}, nil
	}

	log.Debug().Str("type", raw.Type).Msg("codex.parse: unknown type, pass-through")
	return AgentEvent{Type: Unknown, Raw: trimmed}, nil
}
