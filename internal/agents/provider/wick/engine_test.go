package wick

import (
	"context"
	"iter"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/yogasw/wick/internal/agents/event"
)

// fakeModel is a scripted LLM: it returns the queued responses in
// order, one per GenerateContent call, so a test can drive a multi-step
// tool loop deterministically without a network.
type fakeModel struct {
	responses []*LLMResponse
	calls     int
	lastReq   *LLMRequest
}

func (f *fakeModel) Name() string { return "fake" }

func (f *fakeModel) GenerateContent(ctx context.Context, req *LLMRequest, stream bool) iter.Seq2[*LLMResponse, error] {
	return func(yield func(*LLMResponse, error) bool) {
		f.lastReq = req
		i := f.calls
		f.calls++
		if i >= len(f.responses) {
			yield(&LLMResponse{Content: &genai.Content{Role: genai.RoleModel}}, nil)
			return
		}
		yield(f.responses[i], nil)
	}
}

func textResp(s string) *LLMResponse {
	return &LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: s}}}}
}

func toolCallResp(name string, args map[string]any) *LLMResponse {
	return &LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{
		{FunctionCall: &genai.FunctionCall{ID: "call_1", Name: name, Args: args}},
	}}}
}

// collectParsed runs the emitted stream-json lines through the REAL
// ClaudeParser — the whole point of the bridge is that these lines are
// consumed identically to a claude subprocess.
func collectParsed(t *testing.T, lines []string) []event.AgentEvent {
	t.Helper()
	p := event.NewClaudeParser()
	var evs []event.AgentEvent
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		ev, err := p.Parse(ln)
		if err != nil {
			t.Fatalf("parser rejected line %q: %v", ln, err)
		}
		if ev.Type != event.Unknown {
			evs = append(evs, ev)
		}
	}
	return evs
}

func newTestEngine(f LLM, tools []toolDef) (*engine, *[]string) {
	var lines []string
	emit := func(b []byte) { lines = append(lines, string(b)) }
	eng := newEngine(f, "fake", "you are a test agent", &genai.GenerateContentConfig{}, tools, nil, 0, emit)
	return eng, &lines
}

// TestEngineTextTurn: a plain text answer emits init → text → done and
// the ClaudeParser turns those into SessionStart, TextDelta, Done.
func TestEngineTextTurn(t *testing.T) {
	f := &fakeModel{responses: []*LLMResponse{textResp("hello world")}}
	eng, lines := newTestEngine(f, nil)
	eng.start()
	eng.runTurn(context.Background(), "hi")

	evs := collectParsed(t, *lines)
	if len(evs) < 3 {
		t.Fatalf("want >=3 events, got %d: %+v", len(evs), evs)
	}
	if evs[0].Type != event.SessionStart {
		t.Errorf("first event = %v, want SessionStart", evs[0].Type)
	}
	var gotText, gotDone bool
	for _, e := range evs {
		if e.Type == event.TextDelta && strings.Contains(e.Text, "hello world") {
			gotText = true
		}
		if e.Type == event.Done {
			gotDone = true
		}
	}
	if !gotText {
		t.Errorf("missing TextDelta with body; events=%+v", evs)
	}
	if !gotDone {
		t.Errorf("missing Done; events=%+v", evs)
	}
}

// TestEngineToolUseEmittedBeforeDispatch is the P0b guard: the engine
// MUST emit the tool_use line before invoking the (potentially long-
// running) tool handler. That ordering is what keeps a long foreground
// tool alive: the reader (agent.go) sees the ToolUse event and pauses the
// idle-kill timer (toolInFlight=true) for the whole time the tool blocks;
// only when the ToolResult arrives does the timer restart. If the engine
// ever dispatched first and emitted tool_use after, a `sleep 300` would
// look like 300s of silent stdout and the pool would idle-kill the
// session mid-tool. This test locks that ordering in.
func TestEngineToolUseEmittedBeforeDispatch(t *testing.T) {
	var linesAtDispatch int
	var linesRef *[]string

	slow := toolDef{
		decl: &genai.FunctionDeclaration{Name: "slow"},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			// Snapshot how many lines were emitted by the time the handler
			// runs. The tool_use line must already be among them.
			linesAtDispatch = len(*linesRef)
			return "ok", false
		},
	}
	f := &fakeModel{responses: []*LLMResponse{
		toolCallResp("slow", map[string]any{}),
		textResp("finished"),
	}}
	eng, lines := newTestEngine(f, []toolDef{slow})
	linesRef = lines
	eng.start()
	eng.runTurn(context.Background(), "run slow")

	if linesAtDispatch == 0 {
		t.Fatal("handler ran before any line was emitted — tool_use not sent first")
	}
	// The last line emitted before dispatch must be the tool_use frame.
	pre := (*lines)[:linesAtDispatch]
	last := pre[len(pre)-1]
	if !strings.Contains(last, `"type":"tool_use"`) || !strings.Contains(last, `"name":"slow"`) {
		t.Errorf("line before dispatch is not the tool_use frame:\n%s", last)
	}
	// And the real parser must classify it as a ToolUse (what the reader
	// keys the idle-pause off of).
	evs := collectParsed(t, pre)
	var sawToolUse bool
	for _, e := range evs {
		if e.Type == event.ToolUse && e.ToolName == "slow" {
			sawToolUse = true
		}
	}
	if !sawToolUse {
		t.Errorf("parser did not see ToolUse before dispatch; events=%+v", evs)
	}
}

// TestEngineToolLoop: the model calls a tool, the engine executes it and
// feeds the result back, then the model answers. Verify the parser sees
// ToolUse → ToolResult → TextDelta → Done, and the tool actually ran.
func TestEngineToolLoop(t *testing.T) {
	ran := false
	echo := toolDef{
		decl: &genai.FunctionDeclaration{Name: "echo"},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			ran = true
			return "echoed: " + args["msg"].(string), false
		},
	}
	f := &fakeModel{responses: []*LLMResponse{
		toolCallResp("echo", map[string]any{"msg": "ping"}),
		textResp("done: ping"),
	}}
	eng, lines := newTestEngine(f, []toolDef{echo})
	eng.start()
	eng.runTurn(context.Background(), "use echo")

	if !ran {
		t.Fatal("tool handler was not invoked")
	}
	evs := collectParsed(t, *lines)
	var gotToolUse, gotToolResult, gotDone bool
	for _, e := range evs {
		switch e.Type {
		case event.ToolUse:
			if e.ToolName == "echo" {
				gotToolUse = true
			}
		case event.ToolResult:
			if strings.Contains(e.Text, "echoed: ping") {
				gotToolResult = true
			}
		case event.Done:
			gotDone = true
		}
	}
	if !gotToolUse {
		t.Errorf("missing ToolUse(echo); events=%+v", evs)
	}
	if !gotToolResult {
		t.Errorf("missing ToolResult with body; events=%+v", evs)
	}
	if !gotDone {
		t.Errorf("missing Done; events=%+v", evs)
	}
}

// TestEngineGateDeny: a denied tool call is short-circuited with the
// gate's reason and the handler never runs.
func TestEngineGateDeny(t *testing.T) {
	ran := false
	danger := toolDef{
		decl: &genai.FunctionDeclaration{Name: "danger"},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			ran = true
			return "should not run", false
		},
	}
	f := &fakeModel{responses: []*LLMResponse{
		toolCallResp("danger", nil),
		textResp("ok"),
	}}
	eng, lines := newTestEngine(f, []toolDef{danger})
	eng.gate = func(ctx context.Context, sessionID, tool string, args map[string]any) (bool, string) {
		return true, "blocked: danger not allowed"
	}
	eng.start()
	eng.runTurn(context.Background(), "do danger")

	if ran {
		t.Fatal("denied tool handler should NOT have run")
	}
	evs := collectParsed(t, *lines)
	var denyResult bool
	for _, e := range evs {
		if e.Type == event.ToolResult && e.IsError && strings.Contains(e.Text, "blocked") {
			denyResult = true
		}
	}
	if !denyResult {
		t.Errorf("missing error ToolResult carrying the gate reason; events=%+v", evs)
	}
}

// TestEngineGate_UsesWickSessionIDNotStreamSessionID is a regression
// test: dispatch used to pass e.sessionID (the stream-json protocol's
// own session id, an engine-local UUID set in start()) to the gate
// checker. That id matches nothing in the wick pool/UI, so
// ApprovalManager.RequestApproval fanned the SSE approval_request out
// to a session no tab was subscribed to — the modal never appeared and
// every non-whitelisted command silently timed out after 25s (seen
// live: repeated "command gate: timeout" for a plain `sleep 60`).
// setWickSessionID must be a distinct field, and dispatch must pass
// THAT to gate, not e.sessionID.
func TestEngineGate_UsesWickSessionIDNotStreamSessionID(t *testing.T) {
	danger := toolDef{
		decl: &genai.FunctionDeclaration{Name: "danger"},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			return "ran", false
		},
	}
	f := &fakeModel{responses: []*LLMResponse{
		toolCallResp("danger", nil),
		textResp("ok"),
	}}
	eng, _ := newTestEngine(f, []toolDef{danger})

	var gotSessionID string
	eng.gate = func(ctx context.Context, sessionID, tool string, args map[string]any) (bool, string) {
		gotSessionID = sessionID
		return false, ""
	}
	eng.setWickSessionID("wick-http-session-abc")
	eng.start() // sets e.sessionID to a fresh random UUID — must NOT leak into gate

	if eng.sessionID == "wick-http-session-abc" {
		t.Fatal("test setup invalid: stream sessionID collided with wickSessionID")
	}

	eng.runTurn(context.Background(), "do danger")

	if gotSessionID != "wick-http-session-abc" {
		t.Errorf("gate received sessionID %q, want the wick HTTP session id %q (got the stream-json id instead: %q)",
			gotSessionID, "wick-http-session-abc", eng.sessionID)
	}
}

// TestEngineErrorSurfaced: a model error becomes an Error event with a
// user-actionable message.
func TestEngineErrorSurfaced(t *testing.T) {
	f := &fakeModel{responses: []*LLMResponse{
		{ErrorCode: "401", ErrorMessage: "invalid api key"},
	}}
	eng, lines := newTestEngine(f, nil)
	eng.start()
	eng.runTurn(context.Background(), "hi")

	evs := collectParsed(t, *lines)
	var gotErr bool
	for _, e := range evs {
		if e.Type == event.Error && strings.Contains(strings.ToLower(e.ErrorMsg), "key") {
			gotErr = true
		}
	}
	if !gotErr {
		t.Errorf("missing Error event with key hint; events=%+v", evs)
	}
}
