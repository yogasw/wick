package event

import (
	"bytes"
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

	// `result` carries the turn's token accounting. .usage is the LAST
	// message's usage (what the context window currently holds);
	// .modelUsage is per-model totals for the whole turn and is only
	// read here for contextWindow.
	// `system subtype=compact_boundary` reports a compaction under
	// .compact_metadata.
	CompactMeta *claudeCompactMeta `json:"compact_metadata,omitempty"`

	Usage      *claudeUsage                `json:"usage,omitempty"`
	ModelUsage map[string]claudeModelUsage `json:"modelUsage,omitempty"`
	CostUSD    float64                     `json:"total_cost_usd,omitempty"`

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
// Its counts are turn-wide sums — right for "what did this turn spend",
// wrong for "how full is the window" (every iteration re-reads the same
// cached prefix, so the sum far exceeds what the window held).
type claudeModelUsage struct {
	ContextWindow       int `json:"contextWindow"`
	InputTokens         int `json:"inputTokens"`
	OutputTokens        int `json:"outputTokens"`
	CacheReadTokens     int `json:"cacheReadInputTokens"`
	CacheCreationTokens int `json:"cacheCreationInputTokens"`
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

	// Flows come from modelUsage, which totals the WHOLE turn; .usage is
	// only the last message and would under-report a turn that looped
	// through several tool calls. Level comes from .usage for the
	// opposite reason: modelUsage re-counts the cached prefix on every
	// iteration, so its sum is far larger than the window ever held.
	//
	// Picking the model: a turn can touch more than one (a cheap model
	// for a side task, the main one for the conversation). Take the
	// entry with the most input tokens — map iteration order is random,
	// so "first" would flap between turns.
	best := -1
	for name, mu := range r.ModelUsage {
		weight := mu.InputTokens + mu.CacheReadTokens + mu.CacheCreationTokens
		if weight > best {
			best = weight
			out.Model, out.Window = name, mu.ContextWindow
			out.Input, out.CacheRead, out.CacheWrite = mu.InputTokens, mu.CacheReadTokens, mu.CacheCreationTokens
			out.Output = mu.OutputTokens
		}
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
}

// UnmarshalJSON tolerates .content being a plain STRING instead of an
// array of blocks.
//
// Claude sends the string form for the summary it injects after a
// compaction ("This session is being continued from…") and for the
// <local-command-stdout> echo of a local slash command. Without this,
// json.Unmarshal fails on those lines, and a parse failure is turned
// into an Error event upstream — so a routine compaction would end the
// turn and post a "cannot unmarshal string" line into the user's
// conversation. Neither frame is something wick surfaces; they just
// have to decode without exploding.
func (m *claudeMessage) UnmarshalJSON(b []byte) error {
	var probe struct {
		Content json.RawMessage `json:"content"`
		Usage   *claudeUsage    `json:"usage"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	// Usage is decoded normally — only .content needs the two shapes.
	m.Usage = probe.Usage
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
		// If --include-partial-messages already streamed the text via
		// stream_event content_block_delta, suppress the trailing
		// `assistant` frame's text — it's the same content concatenated,
		// emitting it again would double-render in the UI bubble and
		// double the assistant turn body in conversation.jsonl.
		if p.partialTextEmitted {
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
		return live(AgentEvent{
			Type: TextDelta,
			Text: buf.String(),
			Raw:  trimmed,
		})

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
		if raw.IsError {
			p.lastLevel = 0
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
		u := raw.tokenUsage()
		if u != nil && p.lastLevel > 0 {
			u.ContextUsed = p.lastLevel
		}
		// The next turn measures itself; carrying this one's level over
		// would report a stale window if that turn reports none.
		p.lastLevel = 0
		return AgentEvent{
			Type:      Done,
			SessionID: p.sessionID,
			Raw:       trimmed,
			Usage:     u,
		}, nil
	}

	// Pass-through for anything else (rate_limit_event, status, etc.) —
	// store them in raw.jsonl but don't drive downstream state.
	return AgentEvent{Type: Unknown, Raw: trimmed}, nil
}

// SessionID returns the captured CLI session ID, or "" if no `system
// init` event has been seen yet.
func (p *ClaudeParser) SessionID() string { return p.sessionID }
