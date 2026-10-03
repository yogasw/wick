package event

import (
	"encoding/json"
	"strings"
)

// OpencodeParser parses `opencode run --format json`. Every line is
// `{type, timestamp, sessionID, ...}` written by emit() in
// packages/opencode/src/cli/cmd/run.ts:
//
//	{"type":"step_start","sessionID":"ses_1","part":{"type":"step-start",...}}
//	{"type":"text","sessionID":"ses_1","part":{"type":"text","text":"full text","time":{"end":...}}}
//	{"type":"reasoning","sessionID":"ses_1","part":{"type":"reasoning","text":"..."}}   ← only with --thinking
//	{"type":"tool_use","sessionID":"ses_1","part":{"type":"tool","callID":"c1","tool":"bash",
//	    "state":{"status":"completed","input":{...},"output":"..."}}}                 ← or status "error" + "error"
//	{"type":"step_finish","sessionID":"ses_1","part":{"type":"step-finish","reason":"stop",
//	    "tokens":{"input":1,"output":2,"reasoning":0,"cache":{"read":3,"write":0}},"cost":0.01}}
//	{"type":"error","sessionID":"ses_1","error":{"name":"APIError","data":{"message":"..."}}}
//
// Quirks the parser papers over:
//   - text is emitted once, when the part is complete — no deltas.
//   - a tool is emitted only once it has finished, so one tool_use line
//     becomes ToolUse + ToolResult (ParseAll).
//   - there is no end-of-run frame: the process exits when the session goes
//     idle. A step ends with the model's finish reason; "tool-calls" means
//     another step follows, anything else is the end of the turn → Done.
//
// Concurrency: one parser per subprocess.
type OpencodeParser struct {
	// tools pairs calls with results for Display (see display.go).
	tools toolCalls
	instance       string
	sessionEmitted bool
	usage          TokenUsage
	sawUsage       bool
	errored        bool
	// announced: tool calls already reported as started (tool_running), so
	// their finished frame yields only the result.
	announced map[string]bool
	// window: the active model's context limit (wick's context line).
	window int
	// autoCompact: the provider's own auto-compact state (context line).
	autoCompact *bool
}

// NewOpencodeParser returns a parser for one opencode spawn of instance.
func NewOpencodeParser(instance string) *OpencodeParser {
	return &OpencodeParser{instance: instance}
}

type opencodeRaw struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionID,omitempty"`
	Part      *opencodePart   `json:"part,omitempty"`
	Error     json.RawMessage `json:"error,omitempty"`
}

type opencodePart struct {
	Type   string          `json:"type"`
	Text   string          `json:"text,omitempty"`
	CallID string          `json:"callID,omitempty"`
	Tool   string          `json:"tool,omitempty"`
	State  *opencodeState  `json:"state,omitempty"`
	Reason string          `json:"reason,omitempty"`
	Tokens *opencodeTokens `json:"tokens,omitempty"`
	Cost   float64         `json:"cost,omitempty"`
}

type opencodeState struct {
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input,omitempty"`
	Output string          `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// opencodeTokens is Session.getUsage's shape
// (packages/opencode/src/session/session.ts): input excludes the cache,
// output excludes reasoning.
type opencodeTokens struct {
	Input     int `json:"input"`
	Output    int `json:"output"`
	Reasoning int `json:"reasoning"`
	Cache     struct {
		Read  int `json:"read"`
		Write int `json:"write"`
	} `json:"cache"`
}

// Parse returns the FIRST event of the line; the agent loop uses
// ParseAll, this exists to satisfy Parser.
func (p *OpencodeParser) Parse(line string) (AgentEvent, error) {
	evs, err := p.ParseAll(line)
	if err != nil || len(evs) == 0 {
		return AgentEvent{}, err
	}
	return evs[0], nil
}

// ParseAll decodes one opencode line into its events, in order.
func (p *OpencodeParser) ParseAll(line string) ([]AgentEvent, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return []AgentEvent{{}}, nil
	}
	if trimmed[0] != '{' {
		return []AgentEvent{{Type: Thinking, Text: trimmed, Raw: trimmed}}, nil
	}
	var raw opencodeRaw
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return []AgentEvent{{Type: Thinking, Text: trimmed, Raw: trimmed}}, nil
	}
	var out []AgentEvent
	// Every frame carries sessionID; the first one is the resume handle.
	if !p.sessionEmitted && raw.SessionID != "" {
		p.sessionEmitted = true
		out = append(out, AgentEvent{Type: SessionStart, SessionID: raw.SessionID, Raw: trimmed})
	}
	out = append(out, p.events(raw, trimmed)...)
	for i := range out {
		p.tools.decorate(&out[i])
	}
	return out, nil
}

func (p *OpencodeParser) events(raw opencodeRaw, trimmed string) []AgentEvent {
	part := raw.Part
	switch raw.Type {
	case "context":
		w, auto := contextState(trimmed)
		if w > 0 {
			p.window = w
		}
		if auto != nil {
			p.autoCompact = auto
		}
		return []AgentEvent{{Raw: trimmed}}
	case "compaction":
		// wick ran opencode's summarize for a /compact turn.
		return []AgentEvent{compactionEvent(trimmed, "manual")}
	case "text":
		if part == nil || part.Text == "" {
			return nil
		}
		return []AgentEvent{{Type: TextDelta, Text: part.Text, Raw: trimmed}}
	case "reasoning":
		if part == nil || part.Text == "" {
			return nil
		}
		return []AgentEvent{{Type: Thinking, Text: part.Text, Raw: trimmed}}
	case "tool_running":
		// wick-added frame: the tool has started but not finished. Emitting
		// ToolUse now lets the agent hold its idle timer while it runs.
		if part == nil || part.State == nil || part.CallID == "" {
			return nil
		}
		if p.announced == nil {
			p.announced = map[string]bool{}
		}
		p.announced[part.CallID] = true
		return []AgentEvent{{Type: ToolUse, ToolName: part.Tool, ToolUseID: part.CallID, ToolInput: toolInput(part.State.Input), Raw: trimmed}}
	case "tool_use":
		if part == nil || part.State == nil {
			return []AgentEvent{{Type: Trace, Text: trimmed, Raw: trimmed}}
		}
		in := toolInput(part.State.Input)
		res := AgentEvent{Type: ToolResult, Text: part.State.Output, ToolUseID: part.CallID, Raw: trimmed}
		if part.State.Status == "error" {
			res.IsError = true
			res.Text = part.State.Error
		}
		if p.announced[part.CallID] {
			delete(p.announced, part.CallID)
			return []AgentEvent{res}
		}
		return []AgentEvent{
			{Type: ToolUse, ToolName: part.Tool, ToolUseID: part.CallID, ToolInput: in, Raw: trimmed},
			res,
		}
	case "step_start":
		return nil
	case "step_finish":
		if part == nil {
			return nil
		}
		level := 0
		if t := part.Tokens; t != nil {
			p.sawUsage = true
			p.usage.Input += t.Input
			p.usage.Output += t.Output + t.Reasoning
			p.usage.CacheRead += t.Cache.Read
			p.usage.CacheWrite += t.Cache.Write
			level = t.Input + t.Cache.Read + t.Cache.Write
			p.usage.ContextUsed = level
		}
		p.usage.CostUSD += part.Cost
		if part.Reason == "tool-calls" || part.Reason == "tool_calls" {
			return []AgentEvent{{Raw: trimmed, ContextUsed: level}}
		}
		if p.errored {
			// The error already ended the turn; a Done now would drain the
			// queue a second time.
			p.reset()
			return []AgentEvent{{Raw: trimmed}}
		}
		ev := AgentEvent{Type: Done, Raw: trimmed}
		if p.sawUsage {
			u := p.usage
			u.Window = p.window
			u.AutoCompact = p.autoCompact
			ev.Usage = &u
		}
		p.reset()
		return []AgentEvent{ev}
	case "error":
		if p.errored {
			return []AgentEvent{{Type: Warning, ErrorMsg: opencodeErrorText(raw.Error), Raw: trimmed}}
		}
		p.errored = true
		msg := accountErrorMsg("opencode", p.instance, opencodeErrorText(raw.Error))
		return []AgentEvent{{Type: Error, ErrorMsg: msg, Raw: trimmed}}
	}
	return []AgentEvent{{Type: Trace, Text: trimmed, Raw: trimmed}}
}

func (p *OpencodeParser) reset() {
	p.usage = TokenUsage{}
	p.sawUsage = false
	p.errored = false
}

// opencodeErrorText pulls the human message out of an opencode NamedError
// (`{"name":"APIError","data":{"message":"..."}}`), falling back to the
// name, then the raw JSON.
func opencodeErrorText(b json.RawMessage) string {
	if len(b) == 0 {
		return "opencode error"
	}
	var e struct {
		Name    string `json:"name"`
		Message string `json:"message"`
		Data    struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &e) == nil {
		switch {
		case e.Data.Message != "":
			return e.Data.Message
		case e.Message != "":
			return e.Message
		case e.Name != "":
			return e.Name
		}
	}
	var s string
	if json.Unmarshal(b, &s) == nil && s != "" {
		return s
	}
	return string(b)
}

// toolInput is a tool call's input as the event carries it: empty for none.
func toolInput(raw json.RawMessage) string {
	in := string(raw)
	if in == "null" || in == "{}" {
		return ""
	}
	return in
}
