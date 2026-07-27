package wick

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
	"google.golang.org/genai"
)

// streamingModel yields text deltas then a final aggregated response, the
// same contract a real SSE adapter follows.
type streamingModel struct {
	deltas []string
}

func (s *streamingModel) Name() string { return "stream-fake" }

func (s *streamingModel) GenerateContent(ctx context.Context, req *LLMRequest, stream bool) iter.Seq2[*LLMResponse, error] {
	return func(yield func(*LLMResponse, error) bool) {
		if !stream {
			// Non-streaming: one aggregated shot.
			yield(&LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: strings.Join(s.deltas, "")}}}}, nil)
			return
		}
		for _, d := range s.deltas {
			if !yield(&LLMResponse{TextDelta: d}, nil) {
				return
			}
		}
		yield(&LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: strings.Join(s.deltas, "")}}}}, nil)
	}
}

// TestEngineStreamingNoDoubleEmit: with streaming on, the engine emits each
// chunk as a stream_event text_delta AND the parser SUPPRESSES the trailing
// full assistant frame — so the assembled transcript text appears exactly
// once, not doubled. This is the double-emit guard the whole design hinges on.
func TestEngineStreamingNoDoubleEmit(t *testing.T) {
	f := &streamingModel{deltas: []string{"Hel", "lo ", "world"}}
	var lines []string
	emit := func(b []byte) { lines = append(lines, string(b)) }
	eng := newEngine(f, "stream-fake", "test", &genai.GenerateContentConfig{}, nil, nil, 0, emit)
	eng.setStream(true)
	eng.start()
	eng.runTurn(context.Background(), "hi")

	evs := collectParsed(t, lines)
	var text strings.Builder
	deltas := 0
	for _, e := range evs {
		if e.Type == event.TextDelta {
			text.WriteString(e.Text)
			deltas++
		}
	}
	if got := text.String(); got != "Hello world" {
		t.Fatalf("assembled text = %q, want %q (doubled = design broken)", got, "Hello world")
	}
	if deltas < 3 {
		t.Errorf("want at least 3 streamed TextDeltas, got %d", deltas)
	}
}

// readSSE: multi-line data, event names, comments/keep-alives, and a trailing
// event with no final blank line all decode correctly.
func TestReadSSE(t *testing.T) {
	raw := strings.Join([]string{
		": keep-alive comment",
		"event: message_start",
		`data: {"a":1}`,
		"",
		`data: line one`,
		`data: line two`,
		"",
		`data: [DONE]`, // no trailing blank line — must still flush
	}, "\n")

	var got []sseEvent
	err := readSSE(strings.NewReader(raw), func(ev sseEvent) bool {
		got = append(got, ev)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d: %+v", len(got), got)
	}
	if got[0].Name != "message_start" || got[0].Data != `{"a":1}` {
		t.Errorf("event 0 wrong: %+v", got[0])
	}
	if got[1].Data != "line one\nline two" {
		t.Errorf("multi-line data not concatenated with newline: %q", got[1].Data)
	}
	if got[2].Data != "[DONE]" {
		t.Errorf("trailing no-blank-line event lost: %+v", got[2])
	}
}

// readSSE stops early when onEvent returns false.
func TestReadSSE_StopEarly(t *testing.T) {
	raw := "data: a\n\ndata: b\n\ndata: c\n\n"
	n := 0
	_ = readSSE(strings.NewReader(raw), func(ev sseEvent) bool {
		n++
		return ev.Data != "a" // stop right after the first
	})
	if n != 1 {
		t.Fatalf("want 1 event before stop, got %d", n)
	}
}

// oaiStreamAggregator: text deltas concatenate, tool calls reassemble across
// index-keyed fragments, usage is captured — matching the non-streaming shape.
func TestOAIStreamAggregator(t *testing.T) {
	a := &oaiStreamAggregator{}

	// text arrives in two chunks
	if d := a.ingest(chunkText("Hel")); d != "Hel" {
		t.Fatalf("delta 1: %q", d)
	}
	if d := a.ingest(chunkText("lo")); d != "lo" {
		t.Fatalf("delta 2: %q", d)
	}
	// a tool call streamed piecemeal: id+name first, then args in fragments
	a.ingest(chunkToolStart(0, "call_1", "get_weather"))
	a.ingest(chunkToolArgs(0, `{"ci`))
	a.ingest(chunkToolArgs(0, `ty":"NYC"}`))
	a.ingest(chunkUsage(10, 5))

	out := a.final()
	if out.Content == nil || len(out.Content.Parts) != 2 {
		t.Fatalf("want text + 1 tool call, got %+v", out.Content)
	}
	if out.Content.Parts[0].Text != "Hello" {
		t.Errorf("text not concatenated: %q", out.Content.Parts[0].Text)
	}
	fc := out.Content.Parts[1].FunctionCall
	if fc == nil || fc.Name != "get_weather" || fc.ID != "call_1" {
		t.Fatalf("tool call header wrong: %+v", fc)
	}
	if fc.Args["city"] != "NYC" {
		t.Errorf("tool args not reassembled: %+v", fc.Args)
	}
	if out.UsageMetadata == nil || out.UsageMetadata.PromptTokenCount != 10 || out.UsageMetadata.CandidatesTokenCount != 5 {
		t.Errorf("usage not captured: %+v", out.UsageMetadata)
	}
}

// antStreamAggregator: text_delta emits live, thinking_delta emits live, a
// tool_use block reassembles from content_block_start + input_json_delta, and
// usage merges the message_start (input) + message_delta (output) halves.
func TestAntStreamAggregator(t *testing.T) {
	a := &antStreamAggregator{}

	a.ingest(&antStreamFrame{Type: "message_start", Message: &struct {
		Usage *antStreamUsage `json:"usage"`
	}{Usage: &antStreamUsage{InputTokens: 12, CacheReadTokens: 3}}})

	// text block
	a.ingest(&antStreamFrame{Type: "content_block_start", Index: 0, ContentBlock: &struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	}{Type: "text"}})
	if txt, _ := a.ingest(deltaFrame(0, "text_delta", "Hi ")); txt != "Hi " {
		t.Fatalf("text delta not surfaced: %q", txt)
	}
	if txt, _ := a.ingest(deltaFrame(0, "text_delta", "there")); txt != "there" {
		t.Fatalf("text delta 2: %q", txt)
	}

	// thinking block
	if _, th := a.ingest(deltaFrame(1, "thinking_delta", "hmm")); th != "hmm" {
		t.Fatalf("thinking delta not surfaced: %q", th)
	}

	// tool_use block: header then args fragments
	a.ingest(&antStreamFrame{Type: "content_block_start", Index: 2, ContentBlock: &struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	}{Type: "tool_use", ID: "toolu_1", Name: "search"}})
	a.ingest(deltaFrame(2, "input_json_delta", `{"q":`))
	a.ingest(deltaFrame(2, "input_json_delta", `"go"}`))

	a.ingest(&antStreamFrame{Type: "message_delta", Usage: &antStreamUsage{OutputTokens: 7}})

	out := a.final()
	// text part + one tool call (thinking is streamed live, not a Content part)
	if out.Content == nil || len(out.Content.Parts) != 2 {
		t.Fatalf("want text + tool call, got %+v", out.Content)
	}
	if out.Content.Parts[0].Text != "Hi there" {
		t.Errorf("text: %q", out.Content.Parts[0].Text)
	}
	fc := out.Content.Parts[1].FunctionCall
	if fc == nil || fc.Name != "search" || fc.ID != "toolu_1" || fc.Args["q"] != "go" {
		t.Fatalf("tool call wrong: %+v", fc)
	}
	if out.UsageMetadata.PromptTokenCount != 15 || out.UsageMetadata.CandidatesTokenCount != 7 || out.UsageMetadata.CachedContentTokenCount != 3 {
		t.Errorf("usage merge wrong: %+v", out.UsageMetadata)
	}
}

// ── chunk/frame builders ───────────────────────────────────────────────

func chunkText(s string) *oaiStreamChunk {
	c := &oaiStreamChunk{}
	c.Choices = append(c.Choices, struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	}{})
	c.Choices[0].Delta.Content = s
	return c
}

func chunkToolStart(idx int, id, name string) *oaiStreamChunk {
	c := chunkText("")
	tc := struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}{Index: idx, ID: id, Type: "function"}
	tc.Function.Name = name
	c.Choices[0].Delta.ToolCalls = append(c.Choices[0].Delta.ToolCalls, tc)
	return c
}

func chunkToolArgs(idx int, args string) *oaiStreamChunk {
	c := chunkText("")
	tc := struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}{Index: idx}
	tc.Function.Arguments = args
	c.Choices[0].Delta.ToolCalls = append(c.Choices[0].Delta.ToolCalls, tc)
	return c
}

func chunkUsage(prompt, completion int) *oaiStreamChunk {
	return &oaiStreamChunk{Usage: &oaiUsage{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion}}
}

func deltaFrame(idx int, deltaType, payload string) *antStreamFrame {
	f := &antStreamFrame{Type: "content_block_delta", Index: idx}
	f.Delta = &struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
	}{Type: deltaType}
	switch deltaType {
	case "text_delta":
		f.Delta.Text = payload
	case "thinking_delta":
		f.Delta.Thinking = payload
	case "input_json_delta":
		f.Delta.PartialJSON = payload
	}
	return f
}
