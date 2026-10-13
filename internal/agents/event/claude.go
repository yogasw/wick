package event

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	// tools pairs tool_use with tool_result so a result is classified
	// with its call's context (see display.go).
	tools toolCalls

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

	// partialThinkingEmitted tracks whether any thinking_delta has been
	// streamed in the current turn. When true, the trailing `assistant`
	// frame's thinking block is suppressed — same dedup logic as text.
	partialThinkingEmitted bool

	// lastLevel is the context reading from the most recent `assistant`
	// frame: input + cache read + cache write of that ONE request, which
	// is exactly what the model had in its window.
	//
	// It has to be remembered here because the `result` frame cannot
	// answer the question. Its .usage sums every request the turn made,
	// so a turn that looped through twenty tool calls re-counts the same
	// cached prefix twenty times — measured on a 3-tool-call turn, the
	// result said 134,763 where the window actually held 34,673. Read
	// as a level, that sum climbs past 100% and keeps going.
	lastLevel int

	// synthKind / synthReset carry what this turn's synthetic message
	// (see isSyntheticText) said about the account, for the result frame
	// that ends the turn: claude's error result does not always repeat
	// the wording — error_during_execution leaves .result empty.
	synthKind  ErrKind
	synthReset time.Time
	// synthCoded: that message carried claude's own error code, which
	// then outranks whatever the result's wording suggests.
	synthCoded bool

	// spentModel / spentCost are the previous `result` frame's per-model
	// counters and total_cost_usd. Claude keeps ONE process alive for the
	// whole session and those fields are running totals of that process,
	// not of the turn: two turns 5 s apart read 20.29M and 20.41M cache
	// read, $6.33 and $6.37, for +461 output tokens. A turn's own spend
	// is the difference from this baseline (see turnSpend). The parser is
	// built fresh per spawn, so the baseline lives exactly as long as the
	// process whose totals it tracks.
	spentModel map[string]claudeModelUsage
	spentCost  float64
}

// claudeSyntheticModel is the model id claude stamps on an assistant
// message it wrote itself instead of calling the API — the limit notice,
// an API error it is relaying, "No response requested.".
const claudeSyntheticModel = "<synthetic>"

// claudeErrorKinds maps the error code claude puts on a message it wrote
// itself (`"error":"rate_limit"` next to `"isApiErrorMessage":true`) to
// what it says about the account. A code not listed — invalid_request
// (an image it could not read), max_output_tokens, unknown — is about
// the request, not the account: ErrNone.
var claudeErrorKinds = map[string]ErrKind{
	"rate_limit":            ErrLimit,
	"rate_limit_error":      ErrLimit,
	"billing_error":         ErrLimit,
	"authentication_failed": ErrAuth,
	"authentication_error":  ErrAuth,
	"oauth_org_not_allowed": ErrAuth,
	"permission_error":      ErrAuth,
	"overloaded":            ErrUnavailable,
	"overloaded_error":      ErrUnavailable,
	"server_error":          ErrUnavailable,
	"api_error":             ErrUnavailable,
}

// errorCode reads an `error` field that is either the bare code
// ("rate_limit") or an API error object ({"type":"rate_limit_error"}).
func errorCode(b json.RawMessage) string {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return ""
	}
	var code string
	if b[0] == '"' {
		_ = json.Unmarshal(b, &code)
		return code
	}
	var obj struct {
		Type  string `json:"type"`
		Error *struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &obj) == nil {
		if obj.Error != nil && obj.Error.Type != "" {
			return obj.Error.Type
		}
		return obj.Type
	}
	return ""
}

// claudeSyntheticMarkers are the notices claude writes itself on an
// account problem, matched only when a frame carries neither the
// synthetic model id nor an error code (an older CLI).
// Narrower than ClassifyError's markers on purpose: this runs on assistant
// text, where the model may well be talking ABOUT a 429. Lower-case.
var claudeSyntheticMarkers = []string{
	"you've hit your", "you have hit your", "usage limit reached",
	"invalid api key", "please run /login", "oauth token has expired",
	"api error: 4", "api error: 5", "credit balance is too low",
	"failed to authenticate",
}

// isSyntheticText reports whether an un-streamed assistant frame's text
// is claude's own notice rather than the model's: short, and worded the
// way claude words its limit/auth/API-error messages.
func isSyntheticText(text string) bool {
	if len(text) > 500 {
		return false
	}
	return containsAny(strings.ToLower(text), claudeSyntheticMarkers)
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
	// ReplaceText is the whole reply of a system remote_replace line.
	ReplaceText string `json:"replace_text,omitempty"`
	// RemoteNote is a remote agent's result line's note (how it ended).
	RemoteNote string `json:"remote_note,omitempty"`
	// Detail is a system remote_status line's label: what the remote
	// says it is doing ("reading the code…").
	Detail string `json:"detail,omitempty"`
	// QueueID / QueueState are a system remote_queue line's message and
	// where it stands (its text is in Text).
	QueueID    string `json:"queue_id,omitempty"`
	QueueState string `json:"queue_state,omitempty"`
	Text       string `json:"text,omitempty"`
	// LinkURL is a system remote_link line's URL.
	LinkURL string `json:"url,omitempty"`

	// APIError is the error code on an assistant message claude wrote
	// itself ("rate_limit", "authentication_failed", …), and
	// IsAPIErrorMessage the flag that comes with it. See claudeErrorKinds.
	APIError          json.RawMessage `json:"error,omitempty"`
	IsAPIErrorMessage bool            `json:"isApiErrorMessage,omitempty"`

	// `assistant` and `user` wrap content blocks under .message.content
	Message *claudeMessage `json:"message,omitempty"`

	// `result` carries the turn's token accounting. .usage is the LAST
	// message's usage (what the context window currently holds);
	// .modelUsage and total_cost_usd are per-model running totals of the
	// whole PROCESS, which spans many turns; turnSpend turns them into
	// this turn's share before anything reads them.
	// `system subtype=compact_boundary` reports a compaction under
	// .compact_metadata.
	CompactMeta *claudeCompactMeta `json:"compact_metadata,omitempty"`

	Usage      *claudeUsage                `json:"usage,omitempty"`
	ModelUsage map[string]claudeModelUsage `json:"modelUsage,omitempty"`
	CostUSD    float64                     `json:"total_cost_usd,omitempty"`
	// UsageScope "turn" says modelUsage / total_cost_usd already count
	// this turn alone (the wick engine writes it), so they are not
	// running totals to difference. Claude's own CLI never sets it.
	UsageScope string `json:"usage_scope,omitempty"`

	// modelTotal keeps the frame's running totals once turnSpend has
	// replaced ModelUsage with the turn's share; the model is picked by
	// these, not by who happened to work this turn.
	modelTotal map[string]claudeModelUsage

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

// claudeUsage is the token accounting of one message. The three input
// fields are disjoint — fresh input, newly written cache, cache read —
// so their sum is what the model saw as context for that message.
// claudeCompactMeta is the compact_boundary payload. The preserved-uuid
// lists are ignored: they address transcript entries wick does not index.
type claudeCompactMeta struct {
	Trigger       string `json:"trigger"`
	PreTokens     int    `json:"pre_tokens"`
	PostTokens    int    `json:"post_tokens"`
	DroppedTokens int    `json:"cumulative_dropped_tokens"`
	DurationMS    int    `json:"duration_ms"`
}

type claudeUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens"`

	// Iterations is present on the `result` frame only: one entry per
	// request the turn made. The LAST entry is the final request, which
	// is the one whose input describes the window as the turn ended.
	// Used as the fallback when no assistant frame carried usage.
	Iterations []claudeUsage `json:"iterations,omitempty"`
}

// level is what the model actually had in front of it for this one
// request: fresh input plus both halves of the cached prefix.
func (u claudeUsage) level() int {
	return u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens
}

// claudeModelUsage is one entry of the result frame's per-model totals.
// Claude sends them as running totals of the process; once turnSpend has
// taken the difference they are turn-wide sums — right for "what did
// this turn spend", wrong for "how full is the window" (every iteration
// re-reads the same cached prefix, so the sum far exceeds what the
// window held).
type claudeModelUsage struct {
	ContextWindow       int `json:"contextWindow"`
	InputTokens         int `json:"inputTokens"`
	OutputTokens        int `json:"outputTokens"`
	CacheReadTokens     int `json:"cacheReadInputTokens"`
	CacheCreationTokens int `json:"cacheCreationInputTokens"`
}

// turnSpend rewrites a `result` frame's modelUsage and total_cost_usd
// from the process's running totals into this turn's own share, and
// moves the baseline on. The first result of a process is taken whole.
// A model whose counter went DOWN was reset under us (a CLI reset, a
// model switch), so its new value is taken as it stands; the same goes
// for the cost. The context level is not touched: it never came from
// these counters.
func (p *ClaudeParser) turnSpend(r *claudeRaw) {
	if r.UsageScope == "turn" {
		return
	}
	if len(r.ModelUsage) > 0 {
		turn := make(map[string]claudeModelUsage, len(r.ModelUsage))
		for name, mu := range r.ModelUsage {
			prev, ok := p.spentModel[name]
			if ok && mu.InputTokens >= prev.InputTokens && mu.OutputTokens >= prev.OutputTokens &&
				mu.CacheReadTokens >= prev.CacheReadTokens && mu.CacheCreationTokens >= prev.CacheCreationTokens {
				d := mu
				d.InputTokens -= prev.InputTokens
				d.OutputTokens -= prev.OutputTokens
				d.CacheReadTokens -= prev.CacheReadTokens
				d.CacheCreationTokens -= prev.CacheCreationTokens
				turn[name] = d
				continue
			}
			turn[name] = mu
		}
		// Merged per model, never replaced: a model missing from one
		// frame still has its totals when it shows up again.
		if p.spentModel == nil {
			p.spentModel = make(map[string]claudeModelUsage, len(r.ModelUsage))
		}
		for name, mu := range r.ModelUsage {
			p.spentModel[name] = mu
		}
		r.modelTotal = r.ModelUsage
		r.ModelUsage = turn
	}
	cost := r.CostUSD
	if cost >= p.spentCost {
		r.CostUSD = cost - p.spentCost
	}
	if cost > 0 {
		p.spentCost = cost
	}
}

// contextUsage builds the end-of-turn context reading, or nil when the
// frame carried no usage.
//
// Picking the model: a turn can touch more than one (a cheap model for
// a side task, the main one for the conversation). The window we want
// belongs to whichever model carried the conversation, so we take the
// entry with the most input tokens rather than the first key — map
// iteration order is random, and "first" would flap between turns.
func (r claudeRaw) tokenUsage() *TokenUsage {
	if r.Usage == nil {
		return nil
	}
	// The level is the LAST request's input, never the turn's sum — see
	// ClaudeParser.lastLevel. .iterations, when the CLI sends it, holds
	// exactly that last request; the caller overrides this with what it
	// saw on the assistant frames, which is the more reliable source.
	level := r.Usage.level()
	if n := len(r.Usage.Iterations); n > 0 {
		level = r.Usage.Iterations[n-1].level()
	}
	if level == 0 && r.Usage.OutputTokens == 0 {
		return nil
	}
	out := &TokenUsage{ContextUsed: level, CostUSD: r.CostUSD}

	// Flows come from modelUsage, which (after turnSpend) totals the WHOLE
	// turn; .usage is only the last message and would under-report a turn
	// that looped through several tool calls. Level comes from .usage for
	// the opposite reason: modelUsage re-counts the cached prefix on every
	// iteration, so its sum is far larger than the window ever held.
	//
	// Picking the model: a turn can touch more than one (a cheap model
	// for a side task, the main one for the conversation). Take the
	// entry with the most input tokens in the running totals, so a turn
	// where only the side model worked keeps the main one; ties go to
	// the smaller name, since map iteration order is random. The flows
	// stay this turn's own share of the picked model.
	totals := r.modelTotal
	if totals == nil {
		totals = r.ModelUsage
	}
	best, pick := -1, ""
	for name, mu := range totals {
		weight := mu.InputTokens + mu.CacheReadTokens + mu.CacheCreationTokens
		if weight > best || (weight == best && name < pick) {
			best, pick = weight, name
		}
	}
	if best >= 0 {
		mu := r.ModelUsage[pick]
		out.Model, out.Window = pick, totals[pick].ContextWindow
		out.Input, out.CacheRead, out.CacheWrite = mu.InputTokens, mu.CacheReadTokens, mu.CacheCreationTokens
		out.Output = mu.OutputTokens
	}
	if best < 0 {
		// No modelUsage (a provider emitting claude-shaped lines may omit
		// it) — fall back to the last message's numbers.
		out.Input, out.CacheRead = r.Usage.InputTokens, r.Usage.CacheReadTokens
		out.CacheWrite, out.Output = r.Usage.CacheCreationTokens, r.Usage.OutputTokens
	}
	return out
}

type claudeMessage struct {
	Content []claudeContentBlock `json:"content,omitempty"`

	// Usage is the per-request accounting on an `assistant` frame — the
	// only place the true context level is reported. See lastLevel.
	Usage *claudeUsage `json:"usage,omitempty"`

	// Model is the model that wrote the message; claudeSyntheticModel
	// when claude wrote it itself.
	Model string `json:"model,omitempty"`

	// Error is the same code as claudeRaw.APIError, when a CLI puts it on
	// the message instead of the line.
	Error json.RawMessage `json:"error,omitempty"`
}

// UnmarshalJSON tolerates two string-shaped frames that would otherwise
// fail the whole line: .message itself being a plain STRING, and
// .message.content being a plain STRING instead of an array of blocks.
//
// Claude sends the content string form for the summary it injects after
// a compaction ("This session is being continued from…") and for the
// <local-command-stdout> echo of a local slash command. It sends the
// whole .message as a string on some error/notice frames. Without this,
// json.Unmarshal fails on those lines, and a parse failure is turned
// into an Error event upstream — so a routine compaction would end the
// turn and post a "cannot unmarshal string" line into the user's
// conversation. None of these frames is something wick surfaces; they
// just have to decode without exploding.
func (m *claudeMessage) UnmarshalJSON(b []byte) error {
	// .message as a bare string — keep the text, drop nothing else.
	if t := bytes.TrimSpace(b); len(t) > 0 && t[0] == '"' {
		var text string
		if err := json.Unmarshal(t, &text); err != nil {
			return err
		}
		if text != "" {
			m.Content = []claudeContentBlock{{Type: "text", Text: text}}
		}
		return nil
	}
	var probe struct {
		Content json.RawMessage `json:"content"`
		Usage   *claudeUsage    `json:"usage"`
		Model   string          `json:"model"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	// Usage is decoded normally — only .content needs the two shapes.
	m.Usage = probe.Usage
	m.Model = probe.Model
	m.Error = probe.Error
	trimmed := bytes.TrimSpace(probe.Content)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		m.Content = []claudeContentBlock{{Type: "text", Text: text}}
		return nil
	}
	return json.Unmarshal(trimmed, &m.Content)
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
//
// Every tool event leaves with a Display: claude's tool_result content is
// a JSON string literal or a content-block array, which Classify unwraps.
func (p *ClaudeParser) Parse(line string) (AgentEvent, error) {
	ev, err := p.parse(line)
	if err == nil {
		p.tools.decorate(&ev)
	}
	return ev, err
}

func (p *ClaudeParser) parse(line string) (AgentEvent, error) {
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
		// A compaction is the one system frame worth surfacing: the
		// conversation just lost messages. Claude also emits the
		// replacement summary as a `user` frame and a
		// `<local-command-stdout>Compacted</local-command-stdout>` echo,
		// both of which fall through to Unknown below — this marker is
		// what the UI shows in their place.
		if raw.Subtype == "compact_boundary" && raw.CompactMeta != nil {
			m := raw.CompactMeta
			return AgentEvent{
				Type: Compaction,
				Raw:  trimmed,
				Compaction: &CompactionInfo{
					Trigger:       m.Trigger,
					PreTokens:     m.PreTokens,
					PostTokens:    m.PostTokens,
					DroppedTokens: m.DroppedTokens,
					DurationMS:    m.DurationMS,
				},
			}, nil
		}
		// `init` carries the session_id we want for resume. Other
		// system subtypes (`hook_started`, `hook_response`,
		// `compaction`, ...) are noise from claude's lifecycle hooks
		// and don't map to anything user-visible.
		// A remote agent's runner rewrites the reply it streamed when the
		// remote edited a message already passed on.
		if raw.Subtype == "remote_replace" {
			return AgentEvent{Type: TextReplace, Text: raw.ReplaceText, Raw: trimmed}, nil
		}
		// Its progress label shows as the turn's thinking — what the
		// remote is doing, never part of the reply.
		if raw.Subtype == "remote_queue" && raw.QueueID != "" {
			return AgentEvent{Type: RemoteQueue, Queue: &QueueInfo{ID: raw.QueueID, State: raw.QueueState, Text: raw.Text}, Raw: trimmed}, nil
		}
		if raw.Subtype == "remote_link" && raw.LinkURL != "" {
			return AgentEvent{Type: RemoteLink, Text: raw.LinkURL, Raw: trimmed}, nil
		}
		if raw.Subtype == "remote_status" && raw.Detail != "" {
			return AgentEvent{Type: Thinking, Text: raw.Detail + "\n", Raw: trimmed}, nil
		}
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
			p.partialThinkingEmitted = true
			return AgentEvent{
				Type: Thinking,
				Text: raw.Event.Delta.Thinking,
				Raw:  trimmed,
			}, nil
		}
		log.Debug().Str("delta_type", raw.Event.Delta.Type).Msg("claude.parse: stream_event unknown delta type")
		return AgentEvent{Type: Unknown, Raw: trimmed}, nil

	case "assistant":
		// Every assistant frame reports the window as it stood for that
		// one request. Remember the newest; the `result` frame that ends
		// the turn cannot tell us this (its .usage is a turn-wide sum).
		frameLevel := 0
		if raw.Message != nil && raw.Message.Usage != nil {
			if lvl := raw.Message.Usage.level(); lvl > 0 {
				p.lastLevel, frameLevel = lvl, lvl
			}
		}
		// live stamps that reading onto whatever this frame turns into,
		// so the meter can move DURING the turn instead of only when it
		// ends. Every exit below goes through it — including the Unknown
		// ones, which is the whole point: the frame that carries the
		// newest level is often the one whose text was already streamed
		// and is therefore suppressed.
		live := func(ev AgentEvent) (AgentEvent, error) {
			ev.ContextUsed = frameLevel
			return ev, nil
		}
		// claude packs text + tool_use blocks into one frame. Iterate
		// to find the first interesting block. If both text and
		// tool_use are present we prefer tool_use (gate-relevant) and
		// drop text — the result event will carry the final assistant
		// text in .result, so we don't lose user-visible output.
		if raw.Message == nil {
			return live(AgentEvent{Type: Unknown, Raw: trimmed})
		}
		for _, b := range raw.Message.Content {
			switch b.Type {
			case "tool_use":
				return live(AgentEvent{
					Type:      ToolUse,
					ToolName:  b.Name,
					ToolInput: string(b.Input),
					ToolUseID: b.ID,
					Raw:       trimmed,
				})
			case "thinking":
				if b.Thinking != "" {
					// Suppress when stream_event thinking_delta already
					// streamed this text; otherwise emit as one block.
					if p.partialThinkingEmitted {
						return live(AgentEvent{Type: Unknown, Raw: trimmed})
					}
					return live(AgentEvent{
						Type: Thinking,
						Text: b.Thinking,
						Raw:  trimmed,
					})
				}
			}
		}
		// A message claude wrote itself is never streamed as deltas, so
		// the suppression below must not swallow it.
		code := errorCode(raw.APIError)
		if code == "" {
			code = errorCode(raw.Message.Error)
		}
		synthModel := raw.Message.Model == claudeSyntheticModel || code != "" || raw.IsAPIErrorMessage
		// If --include-partial-messages already streamed the text via
		// stream_event content_block_delta, suppress the trailing
		// `assistant` frame's text — it's the same content concatenated,
		// emitting it again would double-render in the UI bubble and
		// double the assistant turn body in conversation.jsonl.
		if p.partialTextEmitted && !synthModel {
			return live(AgentEvent{Type: Unknown, Raw: trimmed})
		}
		// No tool_use/thinking — return concatenated text as TextDelta.
		var buf strings.Builder
		for _, b := range raw.Message.Content {
			if b.Type == "text" {
				buf.WriteString(b.Text)
			}
		}
		if buf.Len() == 0 {
			return live(AgentEvent{Type: Unknown, Raw: trimmed})
		}
		ev := AgentEvent{
			Type: TextDelta,
			Text: buf.String(),
			Raw:  trimmed,
		}
		// claude announces a limit as an assistant message of its own
		// before the error result: shown as text, but not output the
		// model produced.
		// The wording alone counts only on a frame that names no model:
		// a real reply may well talk about a 401 or a quota.
		if synthModel || (raw.Message.Model == "" && isSyntheticText(ev.Text)) {
			if code != "" {
				// claude's own code decides; the wording only dates it.
				ev.ErrKind = claudeErrorKinds[code]
				if ev.ErrKind == ErrLimit {
					if at, ok := ParseResetAt(ev.Text, nowFunc()); ok {
						ev.ResetAt = at
					}
				}
				// A code that says nothing about the account (invalid_request:
				// claude carries on) must not mask how the turn ends.
				if ev.ErrKind != ErrNone {
					p.synthCoded = true
				}
			} else {
				ev = classifyErr(ev, ev.Text)
			}
			ev.Synthetic = true
			if ev.ErrKind != ErrNone {
				p.synthKind, p.synthReset = ev.ErrKind, ev.ResetAt
			}
		}
		return live(ev)

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
		p.partialThinkingEmitted = false
		synthKind, synthReset, synthCoded := p.synthKind, p.synthReset, p.synthCoded
		p.synthKind, p.synthReset, p.synthCoded = ErrNone, time.Time{}, false
		// Error results carry the same running totals (error_max_turns,
		// error_during_execution report what was spent before stopping),
		// so they move the baseline too and hand their own share to the
		// ledger: otherwise that spend is lost, or charged to the next
		// turn.
		p.turnSpend(&raw)
		u := raw.tokenUsage()
		if u != nil && p.lastLevel > 0 {
			u.ContextUsed = p.lastLevel
		}
		// The next turn measures itself; carrying this one's level over
		// would report a stale window if that turn reports none.
		p.lastLevel = 0
		if raw.IsError {
			// error_during_execution puts the detail on stderr, leaving
			// .result empty — fall back to subtype so the error isn't blank.
			msg := raw.Result
			if msg == "" {
				msg = raw.Subtype
			}
			ev := classifyErr(AgentEvent{
				Type:     Error,
				ErrorMsg: msg,
				Raw:      trimmed,
			}, msg)
			// The result may say less than the notice before it did, and
			// a coded notice outranks the wording either way.
			if synthCoded || ev.ErrKind == ErrNone {
				ev.ErrKind = synthKind
			}
			if ev.ResetAt.IsZero() && ev.ErrKind == synthKind {
				ev.ResetAt = synthReset
			}
			if ev.ErrKind != ErrLimit {
				ev.ResetAt = time.Time{}
			}
			ev.Usage = u
			return ev, nil
		}
		// A turn whose only reply was a limit notice can still end in a
		// "success" result; carry the notice's kind so the end of the
		// turn says what stopped it.
		return AgentEvent{
			Type:       Done,
			SessionID:  p.sessionID,
			Raw:        trimmed,
			Usage:      u,
			RemoteNote: raw.RemoteNote,
			ErrKind:    synthKind,
			ResetAt:    synthReset,
		}, nil
	}

	// Pass-through for anything else (rate_limit_event, status, etc.) —
	// store them in raw.jsonl but don't drive downstream state.
	return AgentEvent{Type: Unknown, Raw: trimmed}, nil
}

// SessionID returns the captured CLI session ID, or "" if no `system
// init` event has been seen yet.
func (p *ClaudeParser) SessionID() string { return p.sessionID }
