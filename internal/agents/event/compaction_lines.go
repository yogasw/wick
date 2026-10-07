package event

import "encoding/json"

// compaction_lines.go: the stream lines that say a compaction happened, for
// the CLIs whose own stream carries none wick could read as a turn:
//
//   - {"type":"compaction","trigger":"manual","tokensBefore":N,"tokensAfter":M}
//     written by wick itself when it ran the CLI's official compaction for
//     a "/compact" turn (omp RPC `compact`, opencode POST
//     /session/{id}/summarize), followed by the turn's end;
//   - omp's own auto_compaction_end, whose `result` is the CompactionResult
//     (tokensBefore / tokensAfter) — omp compacts by itself when the
//     context fills ("context-full").
//
// And {"type":"context","window":N}: the active model's context limit,
// read by wick from the running server (omp get_state.model.contextWindow,
// opencode /provider limit.context) so the meter can show a percentage,
// plus "autoCompact" when wick knows whether the provider compacts by
// itself (omp get_state.autoCompactionEnabled, opencode compaction.auto).

// CompactionLine builds wick's compaction line.
func CompactionLine(trigger string, before, after int) []byte {
	b, _ := json.Marshal(map[string]any{"type": "compaction", "trigger": trigger, "tokensBefore": before, "tokensAfter": after})
	return append(b, '\n')
}

// ContextLine builds wick's context-window line.
func ContextLine(window int) []byte { return ContextStateLine(window, nil) }

// ContextStateLine is ContextLine with the provider's auto-compact state
// (nil = unknown, left out).
func ContextStateLine(window int, autoCompact *bool) []byte {
	m := map[string]any{"type": "context", "window": window}
	if autoCompact != nil {
		m["autoCompact"] = *autoCompact
	}
	b, _ := json.Marshal(m)
	return append(b, '\n')
}

type compactionRaw struct {
	Trigger      string `json:"trigger"`
	TokensBefore int    `json:"tokensBefore"`
	TokensAfter  int    `json:"tokensAfter"`
	Window       int    `json:"window"`
	AutoCompact  *bool  `json:"autoCompact"`
	Result       *struct {
		TokensBefore int `json:"tokensBefore"`
		TokensAfter  int `json:"tokensAfter"`
	} `json:"result"`
}

// compactionInfo reads a compaction line (wick's or omp's
// auto_compaction_end) into CompactionInfo; trigger defaults to def.
func compactionInfo(line, def string) *CompactionInfo {
	var r compactionRaw
	_ = json.Unmarshal([]byte(line), &r)
	ci := &CompactionInfo{Trigger: def, PreTokens: r.TokensBefore, PostTokens: r.TokensAfter}
	if r.Trigger != "" {
		ci.Trigger = r.Trigger
	}
	if r.Result != nil {
		if ci.PreTokens == 0 {
			ci.PreTokens = r.Result.TokensBefore
		}
		if ci.PostTokens == 0 {
			ci.PostTokens = r.Result.TokensAfter
		}
	}
	if ci.PreTokens > ci.PostTokens && ci.PostTokens > 0 {
		ci.DroppedTokens = ci.PreTokens - ci.PostTokens
	}
	return ci
}

// compactionEvent is the Compaction event of a compaction line. It carries
// the "after" size as the context level too: the live meter keeps the
// last level the stream reported, so without a reading here it stays on
// the pre-compaction number until the next turn ends.
func compactionEvent(line, def string) AgentEvent {
	ci := compactionInfo(line, def)
	return AgentEvent{Type: Compaction, Raw: line, Compaction: ci, ContextUsed: ci.PostTokens}
}

// contextState reads the window and auto-compact state of a context line.
func contextState(line string) (int, *bool) {
	var r compactionRaw
	_ = json.Unmarshal([]byte(line), &r)
	return r.Window, r.AutoCompact
}
