package wick

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/genai"
)

// interactions.go persists a structured record of every outbound model
// call to <SessionDir>/wick-interactions.jsonl. Unlike the CLI providers'
// "Reproduce this argv" story (meaningless in-process), the wick session
// log is about WHY the model answered as it did: the exact request sent
// (system prompt, message history, tools) and the response it produced,
// per call. Secrets never appear here — the request carries no API key
// (that's in the transport header, logged masked elsewhere).
//
// This is the durable data behind next-target B (the wick-specific spawn
// detail view). One JSONL line per model call; the FE reads it to render
// an interaction list with request/response + latency + tokens.

// interactionRecord is one model call.
type interactionRecord struct {
	Seq   int    `json:"seq"`
	Kind  string `json:"kind"` // "generate" | "compaction"
	Model string `json:"model"`
	// ModelID is the WickModel.ID this call ran under, if the engine had
	// one (empty in tests / headless paths). Used to look up the model's
	// config (base_url, kind, api_format) on demand when reconstructing a
	// request for the "copy as curl" view — the config itself is never
	// duplicated into this log.
	ModelID      string           `json:"model_id,omitempty"`
	LatencyMs    int64            `json:"latency_ms"`
	System       string           `json:"system,omitempty"`   // truncated
	Tools        []string         `json:"tools,omitempty"`    // offered tool names
	Request      []interactionMsg `json:"request"`            // sent message history (truncated)
	Response     string           `json:"response,omitempty"` // assistant text
	ToolCalls    []string         `json:"tool_calls,omitempty"`
	PromptTokens int              `json:"prompt_tokens,omitempty"`
	OutputTokens int              `json:"output_tokens,omitempty"`
	CachedTokens int              `json:"cached_tokens,omitempty"`
	Error        string           `json:"error,omitempty"`
}

// interactionMsg is one message in the sent request, summarized.
type interactionMsg struct {
	Role     string `json:"role"`
	Text     string `json:"text,omitempty"`
	ToolCall string `json:"tool_call,omitempty"`
	ToolResp string `json:"tool_resp,omitempty"`
}

const interactionTextCap = 4000

// newInteractionSink returns a sink that appends records to
// <sessionDir>/wick-interactions.jsonl. Returns nil (no-op) when
// sessionDir is empty (tests / headless paths).
//
// Seq is seeded from the file's current line count so it keeps
// incrementing across respawns of the SAME session (idle-kill, then a
// later message resumes with a fresh engine) instead of resetting to 1
// each time — a reset would produce duplicate Seq values across the
// file, which broke the FE's keyed list rendering.
func newInteractionSink(sessionDir string) func(interactionRecord) {
	if strings.TrimSpace(sessionDir) == "" {
		return nil
	}
	path := filepath.Join(sessionDir, "wick-interactions.jsonl")
	seq := countLines(path)
	return func(rec interactionRecord) {
		seq++
		rec.Seq = seq
		line, err := json.Marshal(rec)
		if err != nil {
			return
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = f.Write(append(line, '\n'))
	}
}

// countLines returns the number of newline-terminated lines already in
// path, or 0 if it doesn't exist / can't be read.
func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	return n
}

// summarizeRequest converts the sent contents into truncated per-message
// records so the FE can show the exact prompt without storing megabytes.
func summarizeRequest(contents []*genai.Content) []interactionMsg {
	out := make([]interactionMsg, 0, len(contents))
	for _, c := range contents {
		if c == nil {
			continue
		}
		m := interactionMsg{Role: c.Role}
		var text strings.Builder
		for _, p := range c.Parts {
			switch {
			case p == nil:
			case p.FunctionCall != nil:
				m.ToolCall = p.FunctionCall.Name
			case p.FunctionResponse != nil:
				m.ToolResp = capText(responseText(p.FunctionResponse.Response))
			case p.Text != "":
				text.WriteString(p.Text)
			}
		}
		m.Text = capText(text.String())
		out = append(out, m)
	}
	return out
}

// responseSummary extracts the assistant text + tool-call names from a
// model response for the record.
func responseSummary(resp *LLMResponse) (text string, toolCalls []string) {
	if resp == nil || resp.Content == nil {
		return "", nil
	}
	var b strings.Builder
	for _, p := range resp.Content.Parts {
		switch {
		case p == nil:
		case p.FunctionCall != nil:
			toolCalls = append(toolCalls, p.FunctionCall.Name)
		case p.Text != "" && !p.Thought:
			b.WriteString(p.Text)
		}
	}
	return capText(b.String()), toolCalls
}

func capText(s string) string {
	if len(s) > interactionTextCap {
		return s[:interactionTextCap] + "…(truncated)"
	}
	return s
}
