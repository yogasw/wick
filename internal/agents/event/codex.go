package event

import (
	"encoding/json"
	"strings"

	"github.com/rs/zerolog/log"
)

// CodexParser parses the `codex exec --json` newline-delimited JSON stream.
//
// Actual wire shape from codex 0.129+ --json:
//
//	{"type":"thread.started","thread_id":"<uuid>"}
//	{"type":"turn.started"}
//	{"type":"item.created","item":{"id":"...","type":"function_call","name":"...","call_id":"..."}}
//	{"type":"item.updated","item":{"id":"...","type":"agent_message","text":"partial..."}}    ← streaming snapshot
//	{"type":"item.completed","item":{"id":"...","type":"agent_message","text":"full text"}}
//	{"type":"turn.completed","usage":{...}}
//	{"type":"error","message":"..."}
//
// Session ends when process exits. thread_id is used as session ID for --resume.
//
// Streaming semantics:
//   - item.updated for agent_message carries the FULL text so far (snapshot,
//     not chunk). We diff against the last-seen text per item.id and emit
//     only the appended tail as a TextDelta so consumers (FE, store) append
//     naturally without dedup work.
//   - item.completed for agent_message carries the final full text. We emit
//     only the remaining tail (if any) — usually empty because item.updated
//     already streamed everything.
//
// Concurrency: not safe for concurrent use. One parser per subprocess.
type CodexParser struct {
	sessionEmitted bool
	// agentMsgText tracks the most recent text snapshot per item.id for
	// agent_message items so item.updated emits only the appended tail
	// (delta semantics) instead of re-sending the whole string.
	agentMsgText map[string]string
}

// NewCodexParser returns a fresh parser ready to consume codex --json lines.
func NewCodexParser() *CodexParser {
	return &CodexParser{agentMsgText: map[string]string{}}
}

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
	// command_execution fields (codex Bash-like shell tool)
	Command          string `json:"command,omitempty"`
	AggregatedOutput string `json:"aggregated_output,omitempty"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	Status           string `json:"status,omitempty"`
	// web_search fields (codex built-in browser tool)
	Query  string             `json:"query,omitempty"`
	Action *codexWebSearchAct `json:"action,omitempty"`
}

// codexWebSearchAct is the action sub-object on a web_search item.
// item.started carries action.type="other" with no queries; the matching
// item.completed has action.type="search" with action.queries populated.
type codexWebSearchAct struct {
	Type    string   `json:"type"`
	Queries []string `json:"queries,omitempty"`
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

	case "item.updated":
		// Streaming snapshot: codex sends the full text-so-far on every
		// update. Diff against the previous snapshot and emit only the
		// new tail so FE.appendDelta appends naturally without dedup.
		if raw.Item == nil {
			return AgentEvent{Type: Unknown, Raw: trimmed}, nil
		}
		item := raw.Item
		log.Debug().Str("item_type", item.Type).Str("id", item.ID).Int("text_len", len(item.Text)).Msg("codex.parse: item.updated")
		if item.Type == "agent_message" && item.Text != "" {
			prev := p.agentMsgText[item.ID]
			delta := diffTail(prev, item.Text)
			if delta == "" {
				return AgentEvent{Type: Unknown, Raw: trimmed}, nil
			}
			p.agentMsgText[item.ID] = item.Text
			return AgentEvent{
				Type: TextDelta,
				Text: delta,
				Raw:  trimmed,
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
				prev := p.agentMsgText[item.ID]
				delta := diffTail(prev, item.Text)
				delete(p.agentMsgText, item.ID)
				if delta == "" {
					// All text already streamed via item.updated; nothing
					// new to emit. Don't return TextDelta with empty text
					// (FE would render an empty bubble).
					return AgentEvent{Type: Unknown, Raw: trimmed}, nil
				}
				return AgentEvent{
					Type: TextDelta,
					Text: delta,
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
		case "command_execution":
			// Result of a codex shell call — aggregated_output is the
			// stdout/stderr combined; non-zero exit_code flips IsError.
			isErr := item.ExitCode != nil && *item.ExitCode != 0
			return AgentEvent{
				Type:      ToolResult,
				Text:      item.AggregatedOutput,
				ToolUseID: item.ID,
				IsError:   isErr,
				Raw:       trimmed,
			}, nil
		case "web_search":
			// Web search completed — surface the resolved queries (or the
			// top-level query if action.queries is empty) so the operator
			// sees what codex actually searched for in the trace.
			text := item.Query
			if item.Action != nil && len(item.Action.Queries) > 0 {
				text = strings.Join(item.Action.Queries, "\n")
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
		case "command_execution":
			// Codex shell tool. item.started carries the full command;
			// the completion arrives as a separate item.completed with
			// aggregated_output + exit_code, so emit ToolUse here only.
			return AgentEvent{
				Type:      ToolUse,
				ToolName:  "Shell",
				ToolUseID: item.ID,
				ToolInput: item.Command,
				Raw:       trimmed,
			}, nil
		case "web_search":
			// Codex built-in web search. item.started has query="" +
			// action.type="other"; emit ToolUse so the FE renders a card
			// immediately. The matching item.completed carries the actual
			// query / action.queries and surfaces as ToolResult below.
			return AgentEvent{
				Type:      ToolUse,
				ToolName:  "WebSearch",
				ToolUseID: item.ID,
				ToolInput: item.Query,
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

// diffTail returns the suffix of `cur` that comes after `prev` when `cur`
// starts with `prev`. Used to convert codex's snapshot-style item.updated
// payloads into incremental TextDelta chunks the FE can append directly.
//
// If `cur` does not start with `prev` (the model rewrote earlier text —
// rare but observed when codex retries a partial response), the full
// `cur` is returned so the consumer always sees the latest text. The FE
// turn rendering should be able to handle that as a re-emit; conversation
// log writers may end up with a duplicated tail but the final text is
// correct.
func diffTail(prev, cur string) string {
	if cur == "" || cur == prev {
		return ""
	}
	if prev == "" {
		return cur
	}
	if len(cur) > len(prev) && cur[:len(prev)] == prev {
		return cur[len(prev):]
	}
	return cur
}
