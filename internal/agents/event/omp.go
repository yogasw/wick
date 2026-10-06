package event

import (
	"encoding/json"
	"strings"
)

// OMPParser parses `omp -p --mode json` (oh-my-pi print mode). Each line
// is one AgentSessionEvent shaped by printableEvent
// (packages/coding-agent/src/modes/print-mode.ts):
//
//	{"type":"session","id":"<sid>","cwd":"...",...}            ← header, first line
//	{"type":"agent_start"}
//	{"type":"turn_start"}
//	{"type":"message_start","message":{"role":"assistant",...}}
//	{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Hi"}}
//	{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","contentIndex":0,"delta":"..."}}
//	{"type":"message_end","message":{"role":"assistant","usage":{...},"stopReason":"stop",...}}
//	{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{...}}
//	{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":{"content":[...]},"isError":false}
//	{"type":"turn_end",...}
//	{"type":"agent_end","messages":[...],"isTerminal":false?}
//
// Text arrives as real deltas (message_update drops the snapshot), so
// nothing needs diffing. The run ends at agent_end unless isTerminal is
// explicitly false (a queued continuation follows). Usage is summed over
// every assistant message_end of the run; the context level is the last
// one's input side.
//
// Concurrency: one parser per subprocess.
type OMPParser struct {
	// tools pairs calls with results for Display (see display.go).
	tools          toolCalls
	instance       string
	sessionEmitted bool
	usage          TokenUsage
	sawUsage       bool
	lastErr        string
	// window: the active model's context limit (wick's context line).
	window int
	// autoCompact: the provider's own auto-compact state (context line).
	autoCompact *bool
}

// NewOMPParser returns a parser for one omp spawn of instance (named in
// account-limit errors).
func NewOMPParser(instance string) *OMPParser { return &OMPParser{instance: instance} }

type ompRaw struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	// Message is an object on message_* frames and a string on notice.
	Message    json.RawMessage `json:"message,omitempty"`
	Assistant  *ompStreamEvent `json:"assistantMessageEvent,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
	Result     *ompToolResult  `json:"result,omitempty"`
	IsError    bool            `json:"isError,omitempty"`
	IsTerminal *bool           `json:"isTerminal,omitempty"`
	// notice / auto_retry_* / auto_compaction_end
	Level        string `json:"level,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	Aborted      bool   `json:"aborted,omitempty"`
}

type ompStreamEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta,omitempty"`
}

type ompMessage struct {
	Role         string    `json:"role"`
	Model        string    `json:"model,omitempty"`
	StopReason   string    `json:"stopReason,omitempty"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
	Usage        *ompUsage `json:"usage,omitempty"`
}

// ompUsage mirrors packages/catalog/src/types.ts Usage: input is the
// NON-cached share, disjoint from cacheRead/cacheWrite (Anthropic style).
type ompUsage struct {
	Input         int `json:"input"`
	Output        int `json:"output"`
	CacheRead     int `json:"cacheRead"`
	CacheWrite    int `json:"cacheWrite"`
	ContextTokens int `json:"contextTokens,omitempty"`
}

type ompToolResult struct {
	Content []struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		Data     string `json:"data,omitempty"`
		MimeType string `json:"mimeType,omitempty"`
	} `json:"content,omitempty"`
}

func (r *ompToolResult) text() string {
	if r == nil {
		return ""
	}
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		} else if c.Data != "" {
			// An image block (omp's read of a .png) used to vanish here;
			// keep the block array so Classify can surface it.
			if b, err := json.Marshal(r.Content); err == nil {
				return string(b)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// Parse decodes one omp line.
func (p *OMPParser) Parse(line string) (AgentEvent, error) {
	ev, err := p.parse(line)
	if err == nil {
		p.tools.decorate(&ev)
	}
	return ev, err
}

func (p *OMPParser) parse(line string) (AgentEvent, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return AgentEvent{}, nil
	}
	if trimmed[0] != '{' {
		// Stray stdout text (warnings) — keep visible, never forward.
		return AgentEvent{Type: Thinking, Text: trimmed, Raw: trimmed}, nil
	}
	var raw ompRaw
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return AgentEvent{Type: Thinking, Text: trimmed, Raw: trimmed}, nil
	}
	switch raw.Type {
	case "session":
		if !p.sessionEmitted && raw.ID != "" {
			p.sessionEmitted = true
			return AgentEvent{Type: SessionStart, SessionID: raw.ID, Raw: trimmed}, nil
		}
		return AgentEvent{Raw: trimmed}, nil

	case "message_update":
		if raw.Assistant == nil || raw.Assistant.Delta == "" {
			return AgentEvent{Raw: trimmed}, nil
		}
		switch raw.Assistant.Type {
		case "text_delta":
			return AgentEvent{Type: TextDelta, Text: raw.Assistant.Delta, Raw: trimmed}, nil
		case "thinking_delta":
			return AgentEvent{Type: Thinking, Text: raw.Assistant.Delta, Raw: trimmed}, nil
		}
		return AgentEvent{Raw: trimmed}, nil

	case "message_end":
		var m *ompMessage
		if len(raw.Message) > 0 && raw.Message[0] == '{' {
			_ = json.Unmarshal(raw.Message, &m)
		}
		if m == nil || m.Role != "assistant" {
			return AgentEvent{Raw: trimmed}, nil
		}
		level := 0
		if u := m.Usage; u != nil {
			p.sawUsage = true
			p.usage.Input += u.Input
			p.usage.Output += u.Output
			p.usage.CacheRead += u.CacheRead
			p.usage.CacheWrite += u.CacheWrite
			level = u.Input + u.CacheRead + u.CacheWrite
			if u.ContextTokens > 0 {
				level = u.ContextTokens
			}
			p.usage.ContextUsed = level
			if m.Model != "" {
				p.usage.Model = m.Model
			}
		}
		if m.StopReason == "error" || m.StopReason == "aborted" {
			p.lastErr = m.ErrorMessage
			if p.lastErr == "" {
				p.lastErr = "request " + m.StopReason
			}
		} else {
			p.lastErr = ""
		}
		return AgentEvent{Raw: trimmed, ContextUsed: level}, nil

	case "tool_execution_start":
		in := string(raw.Args)
		if in == "null" || in == "{}" {
			in = ""
		}
		return AgentEvent{Type: ToolUse, ToolName: raw.ToolName, ToolUseID: raw.ToolCallID, ToolInput: in, Raw: trimmed}, nil

	case "tool_execution_end":
		return AgentEvent{Type: ToolResult, Text: raw.Result.text(), ToolUseID: raw.ToolCallID, IsError: raw.IsError, Raw: trimmed}, nil

	case "agent_end":
		if raw.IsTerminal != nil && !*raw.IsTerminal {
			// A queued continuation follows in this same process.
			return AgentEvent{Type: Trace, Text: trimmed, Raw: trimmed}, nil
		}
		if p.lastErr != "" {
			// One end-of-turn event only: Error already ends the turn, a
			// Done after it would drain the queue twice.
			msg := accountErrorMsg("omp", p.instance, p.lastErr)
			p.reset()
			return AgentEvent{Type: Error, ErrorMsg: msg, Raw: trimmed}, nil
		}
		ev := AgentEvent{Type: Done, Raw: trimmed}
		if p.sawUsage {
			u := p.usage
			u.Window = p.window
			u.AutoCompact = p.autoCompact
			ev.Usage = &u
		}
		p.reset()
		return ev, nil

	case "auto_compaction_end":
		if raw.Aborted {
			return AgentEvent{Type: Trace, Text: trimmed, Raw: trimmed}, nil
		}
		return compactionEvent(trimmed, "auto"), nil

	case "compaction":
		// wick ran omp's RPC `compact` for a /compact turn.
		return compactionEvent(trimmed, "manual"), nil

	case "context":
		w, auto := contextState(trimmed)
		if w > 0 {
			p.window = w
		}
		if auto != nil {
			p.autoCompact = auto
		}
		return AgentEvent{Raw: trimmed}, nil

	case "auto_retry_start":
		return AgentEvent{Type: Warning, ErrorMsg: accountErrorMsg("omp", p.instance, raw.ErrorMessage), Raw: trimmed}, nil

	case "notice":
		if raw.Level == "error" || raw.Level == "warning" {
			var msg string
			_ = json.Unmarshal(raw.Message, &msg)
			return AgentEvent{Type: Warning, ErrorMsg: accountErrorMsg("omp", p.instance, msg), Raw: trimmed}, nil
		}
		return AgentEvent{Type: Trace, Text: trimmed, Raw: trimmed}, nil
	}
	if ompControlFrames[raw.Type] {
		return AgentEvent{Raw: trimmed}, nil
	}
	return AgentEvent{Type: Trace, Text: trimmed, Raw: trimmed}, nil
}

func (p *OMPParser) reset() {
	p.usage = TokenUsage{}
	p.sawUsage = false
	p.lastErr = ""
}

// ompControlFrames carry nothing user-facing.
var ompControlFrames = map[string]bool{
	"agent_start": true, "turn_start": true, "turn_end": true,
	"message_start": true, "tool_execution_update": true, "tool_stream_update": true,
	"auto_compaction_start": true, "auto_retry_end": true, "model_changed": true,
	"thinking_level_changed": true, "config_warnings_changed": true,
}
