package wick

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/session"
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

// errTool returns a toolDef whose handler always fails (isErr=true) —
// used to drive the consecutive-error guard.
func errTool(name string) toolDef {
	return toolDef{
		decl: &genai.FunctionDeclaration{Name: name},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			return "boom: " + name, true
		},
	}
}

func okTool(name string) toolDef {
	return toolDef{
		decl: &genai.FunctionDeclaration{Name: name},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			return "ok", false
		},
	}
}

// repeatResp returns n copies of a tool-call response for the fake model.
func repeatResp(n int, name string) []*LLMResponse {
	out := make([]*LLMResponse, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, toolCallResp(name, map[string]any{}))
	}
	return out
}

// doneText returns the turn's full streamed text and asserts the turn
// ended with a Done event (the parser drops doneLine's .result, so
// transcript text arrives via TextDelta only).
func doneText(t *testing.T, lines []string) string {
	t.Helper()
	var txt strings.Builder
	gotDone := false
	for _, e := range collectParsed(t, lines) {
		switch e.Type {
		case event.TextDelta:
			txt.WriteString(e.Text)
		case event.Done:
			gotDone = true
		}
	}
	if !gotDone {
		t.Fatal("no Done event")
	}
	return txt.String()
}

// TestEngineConsecErrorCut: N consecutive all-error rounds cut the turn
// with a visible reason in the done text.
func TestEngineConsecErrorCut(t *testing.T) {
	f := &fakeModel{responses: repeatResp(10, "bad")}
	eng, lines := newTestEngine(f, []toolDef{errTool("bad")})
	eng.setLoopGuards(3, 0)
	eng.start()
	eng.runTurn(context.Background(), "go")

	if f.calls != 3 {
		t.Errorf("model calls = %d, want 3 (cut at 3rd consecutive error)", f.calls)
	}
	txt := doneText(t, *lines)
	if !strings.Contains(txt, "3 consecutive tool errors") {
		t.Errorf("done text missing cut reason: %q", txt)
	}
}

// TestEngineConsecErrorReset: a success between errors resets the counter,
// so err,err,ok,err,err,ok... never hits maxConsecErr=3.
func TestEngineConsecErrorReset(t *testing.T) {
	resp := []*LLMResponse{
		toolCallResp("bad", map[string]any{}),
		toolCallResp("bad", map[string]any{}),
		toolCallResp("good", map[string]any{}),
		toolCallResp("bad", map[string]any{}),
		toolCallResp("bad", map[string]any{}),
		textResp("survived"),
	}
	f := &fakeModel{responses: resp}
	eng, lines := newTestEngine(f, []toolDef{errTool("bad"), okTool("good")})
	eng.setLoopGuards(3, 0)
	eng.start()
	eng.runTurn(context.Background(), "go")

	txt := doneText(t, *lines)
	if strings.Contains(txt, "turn cut") {
		t.Errorf("turn was cut despite counter reset: %q", txt)
	}
	if !strings.Contains(txt, "survived") {
		t.Errorf("expected final text, got %q", txt)
	}
}

// TestEngineMaxTurnsOptIn: explicit maxTurns still cuts, visibly.
func TestEngineMaxTurnsOptIn(t *testing.T) {
	f := &fakeModel{responses: repeatResp(10, "good")}
	var lines []string
	emit := func(b []byte) { lines = append(lines, string(b)) }
	eng := newEngine(f, "fake", "", &genai.GenerateContentConfig{}, []toolDef{okTool("good")}, nil, 3, emit)
	eng.start()
	eng.runTurn(context.Background(), "go")

	if f.calls != 3 {
		t.Errorf("model calls = %d, want 3", f.calls)
	}
	txt := doneText(t, lines)
	if !strings.Contains(txt, "max_turns cap (3)") {
		t.Errorf("done text missing max_turns reason: %q", txt)
	}
}

// TestEngineUnlimitedByDefault: maxTurns 0 = unlimited — 60 successful
// rounds (past the old 50 cap) run to completion without a cut.
func TestEngineUnlimitedByDefault(t *testing.T) {
	resp := repeatResp(60, "good")
	resp = append(resp, textResp("done after 60"))
	f := &fakeModel{responses: resp}
	eng, lines := newTestEngine(f, []toolDef{okTool("good")})
	eng.start()
	eng.runTurn(context.Background(), "go")

	txt := doneText(t, *lines)
	if strings.Contains(txt, "turn cut") {
		t.Errorf("unexpected cut: %q", txt)
	}
	if !strings.Contains(txt, "done after 60") {
		t.Errorf("expected completion text, got %q", txt)
	}
}

// TestEngineWallClockCut: exceeding maxTurnDur cuts the turn with a
// visible wall-clock reason.
func TestEngineWallClockCut(t *testing.T) {
	slow := toolDef{
		decl: &genai.FunctionDeclaration{Name: "slow"},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			time.Sleep(20 * time.Millisecond)
			return "ok", false
		},
	}
	f := &fakeModel{responses: repeatResp(50, "slow")}
	eng, lines := newTestEngine(f, []toolDef{slow})
	eng.setLoopGuards(0, 10*time.Millisecond)
	eng.start()
	eng.runTurn(context.Background(), "go")

	txt := doneText(t, *lines)
	if !strings.Contains(txt, "wall-clock limit") {
		t.Errorf("done text missing wall-clock reason: %q", txt)
	}
}

// TestEngineBatchPartialSuccessResets: a round with one err + one ok
// counts as progress (resets the counter).
func TestEngineBatchPartialSuccessResets(t *testing.T) {
	mixed := &LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{
		{FunctionCall: &genai.FunctionCall{ID: "c1", Name: "bad", Args: map[string]any{}}},
		{FunctionCall: &genai.FunctionCall{ID: "c2", Name: "good", Args: map[string]any{}}},
	}}}
	f := &fakeModel{responses: []*LLMResponse{mixed, mixed, mixed, textResp("fine")}}
	eng, lines := newTestEngine(f, []toolDef{errTool("bad"), okTool("good")})
	eng.setLoopGuards(2, 0)
	eng.start()
	eng.runTurn(context.Background(), "go")

	txt := doneText(t, *lines)
	if strings.Contains(txt, "turn cut") {
		t.Errorf("mixed batch should reset counter, got cut: %q", txt)
	}
}

// TestEngineGoalForceContinue: with an open goal, plain text does NOT end
// the turn — the engine loops until the model releases the latch.
func TestEngineGoalForceContinue(t *testing.T) {
	dir := t.TempDir()
	if err := session.OpenGoal(agentconfig.NewLayout(""), "", "x", ""); err == nil {
		// OpenGoal needs a real session id; write the latch via Dir form.
	}
	// Seed an open goal file directly under the session dir.
	writeOpenGoal(t, dir, "find 3 houses")

	// Model: text (no tools) → text → then completes the goal via a tool.
	completeGoal := toolDef{
		decl: &genai.FunctionDeclaration{Name: "done_it"},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			// Close the latch so the next plain-text round ends the turn.
			closeOpenGoal(t, dir)
			return "ok", false
		},
	}
	f := &fakeModel{responses: []*LLMResponse{
		textResp("still looking..."),              // would end turn, but goal open → continue
		textResp("found some, verifying..."),      // continue again
		toolCallResp("done_it", map[string]any{}), // closes the latch
		textResp("done: 3 houses"),                // goal closed → ends turn
	}}
	eng, lines := newTestEngine(f, []toolDef{completeGoal})
	eng.setToolSpillDir(dir) // seeds sessionDir so goal lookups resolve
	eng.start()
	eng.runTurn(context.Background(), "go")

	if f.calls < 4 {
		t.Errorf("expected force-continue past plain text (>=4 model calls), got %d", f.calls)
	}
	txt := doneText(t, *lines)
	if !strings.Contains(txt, "done: 3 houses") {
		t.Errorf("expected final text after goal closed, got %q", txt)
	}
}

// TestEngineWallClockNudgeUnderGoal: wall-clock does NOT cut while a goal
// is open — it nudges and keeps going instead of truncating. The model
// eventually closes the goal, and the turn ends cleanly.
func TestEngineWallClockNudgeUnderGoal(t *testing.T) {
	dir := t.TempDir()
	writeOpenGoal(t, dir, "long job")
	// Close the goal from a tool on the 2nd round; the model then answers.
	round := 0
	step := toolDef{
		decl: &genai.FunctionDeclaration{Name: "step"},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			round++
			if round >= 2 {
				closeOpenGoal(t, dir)
			}
			return "ok", false
		},
	}
	f := &fakeModel{responses: []*LLMResponse{
		toolCallResp("step", map[string]any{}),
		toolCallResp("step", map[string]any{}), // closes goal
		textResp("finished"),
	}}
	eng, lines := newTestEngine(f, []toolDef{step})
	eng.setToolSpillDir(dir)
	// Window large enough that a normal round doesn't trip it, so the
	// nudge path is exercised only if we force it — here we assert the
	// turn completes (no cut) with the goal driving continuation.
	eng.setLoopGuards(0, time.Hour)
	eng.start()
	eng.runTurn(context.Background(), "go")

	txt := doneText(t, *lines)
	if strings.Contains(txt, "turn cut: wall-clock") {
		t.Errorf("wall-clock should NOT cut under open goal, got %q", txt)
	}
	if !strings.Contains(txt, "finished") {
		t.Errorf("expected completion text, got %q", txt)
	}
}

// TestEngineWallClockNudgeEmitted: with a tiny window + open goal, the
// engine emits the wall-clock nudge (thinking line) rather than cutting.
func TestEngineWallClockNudgeEmitted(t *testing.T) {
	dir := t.TempDir()
	writeOpenGoal(t, dir, "long job")
	round := 0
	slow := toolDef{
		decl: &genai.FunctionDeclaration{Name: "slow"},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			round++
			if round >= 4 {
				closeOpenGoal(t, dir) // let it finish eventually
			}
			time.Sleep(15 * time.Millisecond)
			return "ok", false
		},
	}
	f := &fakeModel{responses: append(repeatResp(6, "slow"), textResp("done"))}
	eng, lines := newTestEngine(f, []toolDef{slow})
	eng.setToolSpillDir(dir)
	eng.setLoopGuards(0, 5*time.Millisecond) // trips most rounds
	eng.start()
	eng.runTurn(context.Background(), "go")

	// The wall-clock guard NUDGED (thinking line) while the goal was open
	// rather than truncating — proving force-continue survives the timer.
	joined := strings.Join(*lines, "\n")
	if !strings.Contains(joined, "wall-clock limit") {
		t.Errorf("expected wall-clock nudge emitted, got:\n%s", joined)
	}
	nudges := strings.Count(joined, "↻ wall-clock limit")
	if nudges == 0 {
		t.Errorf("expected at least one wall-clock nudge (not a cut), got:\n%s", joined)
	}
}

func writeOpenGoal(t *testing.T, dir, goal string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "goal.json"),
		[]byte(`{"goal":`+strconv.Quote(goal)+`,"status":"open"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func closeOpenGoal(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "goal.json"),
		[]byte(`{"goal":"x","status":"done"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEngineKillWinsOverOpenGoal: a cancelled context ends the turn even
// with an open goal — kill must never be swallowed by force-continue.
// A text-only model + open goal would otherwise loop forever (each plain
// text re-nudges); the cancel must break it at the top-of-loop guard.
func TestEngineKillWinsOverOpenGoal(t *testing.T) {
	dir := t.TempDir()
	writeOpenGoal(t, dir, "never ending")

	ctx, cancel := context.WithCancel(context.Background())
	// A tool that cancels the turn on first call, then keeps "succeeding".
	killOnce := false
	killer := toolDef{
		decl: &genai.FunctionDeclaration{Name: "work"},
		handler: func(_ context.Context, _ map[string]any) (string, bool) {
			if !killOnce {
				killOnce = true
				cancel() // simulate manual Kill mid-turn
			}
			return "ok", false
		},
	}
	// Endless tool calls: without the ctx guard the open goal would loop
	// forever. repeatResp gives plenty; the cancel must stop it fast.
	f := &fakeModel{responses: repeatResp(1000, "work")}
	eng, lines := newTestEngine(f, []toolDef{killer})
	eng.setToolSpillDir(dir)
	eng.start()
	eng.runTurn(ctx, "go")

	// Turn ended (Done emitted) despite the open goal + endless tool calls,
	// and did NOT run anywhere near 1000 rounds.
	_ = doneText(t, *lines) // asserts a Done event exists
	if f.calls > 5 {
		t.Errorf("cancel did not stop the loop promptly: %d model calls", f.calls)
	}
}
