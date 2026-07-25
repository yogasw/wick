package wick

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

// engine drives one wick session: it owns the model adapter, the tool
// set, and the multi-turn agentic loop. It writes claude-shaped
// stream-json lines via emit so the existing parser/store/UI consume
// wick turns exactly like claude turns.
//
// The engine drives LLM directly rather than going through
// adk-go's llmagent+runner: it needs full control over history (rebuilt
// from wick's own conversation.jsonl), tool dispatch (gate enforcement),
// and the wire output. LLM is a clean 2-method interface, so
// every vendor adapter (Gemini native, OpenAI, Anthropic) plugs in the
// same way. See internal/planning/in-progress/wick-provider/plan.md.
type engine struct {
	llm       LLM
	modelName string
	// modelID is the WickModel.ID this engine is running (empty when the
	// caller didn't supply one, e.g. tests). Recorded on each interaction
	// so the config (base_url, kind, api_format, key) can be looked up
	// on-demand when reconstructing a request — never duplicated into the
	// log itself.
	modelID    string
	sysPrompt  string
	genCfg     *genai.GenerateContentConfig
	tools      []toolDef
	toolByName map[string]toolDef
	maxTurns   int

	// contextBudget is the token ceiling for the replayed history. When
	// the estimate nears it, runTurn triggers model-driven compaction
	// (compact.go). 0 = unbounded (no compaction).
	contextBudget int

	// tokenCalibration scales the cheap chars/4 estimate toward the
	// vendor's real PromptTokenCount. Seeded 1.0; after each successful
	// generate we nudge it so the estimate tracks reality (chars/4 can
	// undercount dense JSON / non-ASCII). Only ever raised, never lowered
	// below 1.0, so compaction errs on the side of firing early rather
	// than letting a real overflow through. See calibrateFromUsage.
	tokenCalibration float64

	// history is the prior conversation replayed from the store,
	// alternating user/model contents. New turns append to it.
	history []*genai.Content

	// gate, when set, is consulted before every tool call — not just
	// "shell" — since a command-gate policy should cover file writes,
	// connector executes, and any other tool with real side effects the
	// same way it covers shell commands. It takes ctx so a call needing
	// interactive approval can block on the SAME synchronous ApprovalManager
	// round-trip a CLI provider's gate binary uses (see
	// internal/agents/gate/manager.go's RequestApproval) — cancelling ctx
	// (session killed mid-approval) unblocks it as a deny. deny=true
	// short-circuits the call with reason as the tool result.
	gate func(ctx context.Context, sessionID, name string, args map[string]any) (deny bool, reason string)

	// emit writes one stream-json line (without trailing newline) to the
	// process pipe. Set by the process wiring.
	emit func([]byte)

	// interactionSink, when set, records each outbound model call
	// (request + response + latency + tokens) for the wick session log.
	// nil = not recorded. The sink itself stamps Seq (seeded from the
	// on-disk file so numbering stays unique across respawns of the
	// same session) — recordInteraction leaves it zero.
	interactionSink func(interactionRecord)

	// sessionID is the stream-json protocol's own session id (emitted in
	// the init line for the parser's SessionStart/resume plumbing) — an
	// engine-local UUID, NOT the wick HTTP session id from the URL
	// (/tools/agents/sessions/<id>). gate needs the LATTER (it's what
	// ApprovalManager routes SSE broadcasts and pool lookups by), so
	// wickSessionID carries that one separately. Conflating the two was
	// a real bug: the gate check silently fanned approval requests out
	// to a session id nothing was subscribed to, so the modal never
	// appeared and every non-whitelisted command just timed out.
	sessionID     string
	wickSessionID string

	// toolSpillDir is where an oversized tool result's full body is written
	// (<dir>/tool-out/<call_id>.txt) so the context copy can be truncated to
	// a head+tail preview + a pointer. Empty disables spill (result is still
	// truncated inline, just without a file to recover the rest). Set from
	// the spawn's SessionDir.
	toolSpillDir string
}

// setWickSessionID wires the wick HTTP session id (distinct from the
// stream-json protocol's own sessionID) so gate checks route approval
// requests to the session the user is actually looking at.
func (e *engine) setWickSessionID(id string) { e.wickSessionID = id }

// setToolSpillDir wires the directory large tool results spill into.
func (e *engine) setToolSpillDir(dir string) { e.toolSpillDir = dir }

// setInteractionSink wires the per-spawn interaction recorder.
func (e *engine) setInteractionSink(fn func(interactionRecord)) { e.interactionSink = fn }

// setModelID records which WickModel.ID this engine is running, so
// interaction records carry a reference the config can be looked up
// from later — never the config itself.
func (e *engine) setModelID(id string) { e.modelID = id }

// newEngine assembles an engine for one spawn.
func newEngine(llm LLM, modelName, sysPrompt string, genCfg *genai.GenerateContentConfig, tools []toolDef, history []*genai.Content, maxTurns int, emit func([]byte)) *engine {
	byName := make(map[string]toolDef, len(tools))
	for _, t := range tools {
		byName[t.decl.Name] = t
	}
	if maxTurns <= 0 {
		maxTurns = 50 // safety cap on the tool-call loop, not user turns
	}
	return &engine{
		llm:              llm,
		modelName:        modelName,
		sysPrompt:        sysPrompt,
		genCfg:           genCfg,
		tools:            tools,
		toolByName:       byName,
		maxTurns:         maxTurns,
		history:          history,
		emit:             emit,
		tokenCalibration: 1.0,
	}
}

// setContextBudget sets the token ceiling that triggers compaction.
func (e *engine) setContextBudget(tokens int) { e.contextBudget = tokens }

// start emits the session-init line so the parser fires SessionStart and
// the store/resume plumbing has a session id.
func (e *engine) start() {
	if e.sessionID == "" {
		e.sessionID = uuid.NewString()
	}
	e.emit(initLine(e.sessionID))
}

// runTurn processes one user message: it appends the message to history
// and loops model→tools until the model returns no more tool calls or
// the safety cap is hit. Emits text/thinking/tool events throughout and
// a final result line.
//
// ctx cancellation (Kill / Stop) aborts the loop; the caller's process
// Wait then closes the pipe. On cancel mid-turn we still emit a result
// line so the UI shows the partial turn instead of hanging.
func (e *engine) runTurn(ctx context.Context, userText string) {
	// /compact — manual compaction command (like Claude Code's /compact).
	// Intercepted here so it never reaches the model as a prompt: force a
	// summary of the older history now and report the result, instead of
	// waiting for the budget threshold. A no-op-with-explanation when there
	// isn't enough history to fold.
	//
	// The pool may PREPEND a buffered non-user turn (e.g. the connector
	// reap "[system] …" notice) to the user's message, so the text can be
	// "[system] …\n/compact" rather than a bare "/compact". Match on the
	// last non-empty line so the command still fires; any buffered context
	// above it is dropped (it was context-only, and compaction summarizes
	// history anyway).
	if isCompactCommand(userText) {
		e.runManualCompact(ctx)
		return
	}

	// Compact BEFORE appending the new turn so the model-driven summary
	// covers only settled history, and the fresh user message always
	// survives into the request.
	e.maybeCompact(ctx, e.contextBudget)

	e.history = append(e.history, genai.NewContentFromText(userText, genai.RoleUser))

	var finalText strings.Builder
	for turn := 0; turn < e.maxTurns; turn++ {
		if ctx.Err() != nil {
			e.emit(doneLine(strings.TrimSpace(finalText.String())))
			return
		}

		// Re-check the budget every round, not just at turn start: history
		// balloons INSIDE this loop as each tool result is appended (a shell
		// or connector result can be tens of KB), so a turn with many tool
		// calls would otherwise blow past the budget mid-turn with no chance
		// to compact. maybeCompact is a no-op when under threshold.
		e.maybeCompact(ctx, e.contextBudget)

		resp, err := e.generateWithOverflowRecovery(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				e.emit(doneLine(strings.TrimSpace(finalText.String())))
				return
			}
			log.Warn().Err(err).Str("model", e.modelName).Msg("wick.engine: generate failed")
			e.emit(errorLine(vendorErrorMessage(err)))
			return
		}

		content := resp.Content
		if content == nil {
			// Empty candidate — treat as an empty successful turn.
			e.emit(doneLine(strings.TrimSpace(finalText.String())))
			return
		}

		// Emit text + thinking, and collect any function calls.
		var calls []*genai.FunctionCall
		for _, p := range content.Parts {
			switch {
			case p.FunctionCall != nil:
				calls = append(calls, p.FunctionCall)
			case p.Thought && p.Text != "":
				e.emit(thinkingLine(p.Text))
			case p.Text != "":
				e.emit(textLine(p.Text))
				finalText.WriteString(p.Text)
			}
		}

		// Record the assistant turn (with any tool calls) into history so
		// the next model call sees its own prior actions.
		e.history = append(e.history, &genai.Content{Role: genai.RoleModel, Parts: content.Parts})

		if len(calls) == 0 {
			e.emit(doneLine(strings.TrimSpace(finalText.String())))
			return
		}

		// Execute each tool call, emit tool_use/tool_result, and append a
		// function-response content for the next model call.
		respParts := make([]*genai.Part, 0, len(calls))
		for _, fc := range calls {
			id := fc.ID
			if id == "" {
				id = "call_" + uuid.NewString()[:8]
			}
			e.emit(toolUseLine(id, fc.Name, marshalArgs(fc.Args)))

			result, isErr := e.dispatch(ctx, fc.Name, fc.Args)
			// Emit the FULL result to the UI/store (the store spills large
			// events to its own file for display); only the copy that enters
			// the model's context is capped, so one giant result can't blow
			// the window (see toolresult.go).
			e.emit(toolResultLine(id, result, isErr))

			respParts = append(respParts, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					ID:       fc.ID,
					Name:     fc.Name,
					Response: map[string]any{"result": e.capToolResult(id, fc.Name, result)},
				},
			})
		}
		e.history = append(e.history, &genai.Content{Role: genai.RoleUser, Parts: respParts})
	}

	log.Warn().Int("cap", e.maxTurns).Msg("wick.engine: tool-call loop hit safety cap")
	e.emit(doneLine(strings.TrimSpace(finalText.String())))
}

// generate calls the model once (non-streaming v1) and returns the
// aggregated response. Streaming token-by-token output is a documented
// follow-up (the aggregator double-emit hazard makes non-streaming the
// correct first cut).
func (e *engine) generate(ctx context.Context) (*LLMResponse, error) {
	cfg := e.effectiveConfig()
	req := &LLMRequest{
		Model:    e.modelName,
		Contents: e.history,
		Config:   cfg,
	}
	start := time.Now()

	// A single non-streaming vendor call can take up to perCallTimeout
	// (120s) with no AgentEvent emitted in between — indistinguishable
	// from a hung process to the pool's idle-kill timer, which only
	// resets on pipe writes. Emit a silent heartbeat every few seconds
	// for the duration of the call so a slow-but-alive vendor response
	// doesn't get killed mid-flight (see heartbeatLine's doc comment).
	heartbeatDone := make(chan struct{})
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				e.emit(heartbeatLine())
			case <-heartbeatDone:
				return
			}
		}
	}()

	// Mark the model-call phase so the interactions UI can tell "waiting on the
	// model" from "running a tool" — the log alone can't, since a record only
	// lands when the call finishes. Cleared as soon as the call returns.
	markModelCallStart(e.wickSessionID)

	var out *LLMResponse
	var gotErr error
	for resp, err := range e.llm.GenerateContent(ctx, req, false) {
		if err != nil {
			gotErr = err
			break
		}
		out = resp
	}
	markModelCallDone(e.wickSessionID)
	close(heartbeatDone)
	e.recordInteraction("generate", cfg, req.Contents, out, time.Since(start).Milliseconds(), gotErr)
	if gotErr != nil {
		return nil, gotErr
	}
	if out == nil {
		return nil, errors.New("model returned no response")
	}
	if out.ErrorCode != "" || out.ErrorMessage != "" {
		return nil, fmt.Errorf("%s: %s", out.ErrorCode, out.ErrorMessage)
	}
	e.calibrateFromUsage(req.Contents, out)
	return out, nil
}

// calibratedEstimate is estimateTokens scaled by the learned calibration
// factor, so compaction fires against a number that tracks the vendor's
// real token count rather than the raw chars/4 heuristic.
func (e *engine) calibratedEstimate(contents []*genai.Content) int {
	f := e.tokenCalibration
	if f < 1.0 {
		f = 1.0
	}
	return int(float64(estimateTokens(contents)) * f)
}

// calibrateFromUsage nudges tokenCalibration toward realTokens/estimated
// using the vendor's reported PromptTokenCount. It only ratchets UP (never
// below 1.0): undercounting is the dangerous direction (it lets a real
// overflow through), so we bias the estimate to be conservative. The
// system prompt + tool declarations are part of the real prompt but not of
// e.history, so a raw ratio would overshoot; we cap the factor to a sane
// ceiling to avoid one weird response pinning it forever.
func (e *engine) calibrateFromUsage(sent []*genai.Content, out *LLMResponse) {
	if out == nil || out.UsageMetadata == nil {
		return
	}
	real := int(out.UsageMetadata.PromptTokenCount)
	est := estimateTokens(sent)
	if real <= 0 || est <= 0 {
		return
	}
	ratio := float64(real) / float64(est)
	if ratio < 1.0 {
		ratio = 1.0
	}
	if ratio > 3.0 {
		ratio = 3.0 // guardrail against a pathological single sample
	}
	// Exponential smoothing so one turn doesn't whipsaw the factor.
	const alpha = 0.3
	if e.tokenCalibration <= 0 {
		e.tokenCalibration = 1.0
	}
	e.tokenCalibration = (1-alpha)*e.tokenCalibration + alpha*ratio
}

// maxOverflowRetries bounds how many times an overflow error triggers an
// aggressive compact-and-retry before we give up. Each pass folds a large
// oldest slice, so 2 passes shrinks history a lot; more than that means
// the tail alone (recent turns + system) already exceeds the window and
// no amount of summarising the past will help.
const maxOverflowRetries = 2

// generateWithOverflowRecovery wraps generate with a self-healing loop for
// the one error the model can recover from on its own: the prompt exceeds
// the vendor's context window. Instead of killing the turn (which forced
// the user to start over), it aggressively compacts the oldest history —
// the model summarises it, preserving decisions/facts/paths, Claude
// /compact style — and retries. Any other error (bad key, 404, network)
// passes straight through unchanged.
func (e *engine) generateWithOverflowRecovery(ctx context.Context) (*LLMResponse, error) {
	out, err := e.generate(ctx)
	if err == nil || !isContextOverflowError(err) {
		return out, err
	}
	for attempt := 1; attempt <= maxOverflowRetries; attempt++ {
		before := len(e.history)
		if !e.compactAggressively(ctx) {
			// Nothing left to fold (history already just the recent tail +
			// summary). Summarising more won't shrink the request — return
			// the original overflow so the user sees the real ceiling.
			log.Warn().Msg("wick.engine: context overflow but history can't compact further")
			return nil, err
		}
		log.Info().
			Int("attempt", attempt).
			Int("history_before", before).
			Int("history_after", len(e.history)).
			Msg("wick.engine: context overflow — compacted and retrying")
		// Tell the user the turn is being rescued, not stalled/dead. Emitted
		// as a thinking line (not assistant text) so it shows the recovery is
		// happening without polluting the final answer text.
		e.emit(thinkingLine("Context was full — auto-summarized earlier conversation to keep going."))

		out, err = e.generate(ctx)
		if err == nil {
			return out, nil
		}
		if !isContextOverflowError(err) {
			return nil, err
		}
	}
	return nil, err
}

// isContextOverflowError reports whether err is the vendor rejecting the
// request for exceeding its context window — the recoverable case. Matches
// the phrasings the major providers use (Anthropic "prompt is too long",
// OpenAI "maximum context length", generic "maximum prompt length" /
// "too many tokens", and HTTP 413).
func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	needles := []string{
		"maximum prompt length",
		"maximum context length",
		"context length exceeded",
		"context_length_exceeded",
		"prompt is too long",
		"too many tokens",
		"reduce the length",
		"413",
	}
	for _, n := range needles {
		if strings.Contains(low, n) {
			return true
		}
	}
	return false
}

// recordInteraction writes one model-call record to the interaction sink
// (the wick session log: why the model answered as it did). No-op when
// no sink is wired.
func (e *engine) recordInteraction(kind string, cfg *genai.GenerateContentConfig, contents []*genai.Content, out *LLMResponse, latencyMs int64, callErr error) {
	if e.interactionSink == nil {
		return
	}
	rec := interactionRecord{
		Kind:      kind,
		Model:     e.modelName,
		ModelID:   e.modelID,
		LatencyMs: latencyMs,
		System:    capText(systemText(cfg)),
		Request:   summarizeRequest(contents),
	}
	for _, fd := range toolsFromConfig(cfg) {
		rec.Tools = append(rec.Tools, fd.Name)
	}
	if callErr != nil {
		rec.Error = callErr.Error()
	}
	if out != nil {
		if out.ErrorMessage != "" {
			rec.Error = strings.TrimSpace(out.ErrorCode + " " + out.ErrorMessage)
		}
		rec.Response, rec.ToolCalls = responseSummary(out)
		if u := out.UsageMetadata; u != nil {
			rec.PromptTokens = int(u.PromptTokenCount)
			rec.OutputTokens = int(u.CandidatesTokenCount)
			rec.CachedTokens = int(u.CachedContentTokenCount)
		}
	}
	e.interactionSink(rec)
}

// effectiveConfig clones the base gen config and attaches the system
// instruction + tool declarations. Tools/system are set here (not on the
// stored config) so the static prefix stays stable for vendor caching.
func (e *engine) effectiveConfig() *genai.GenerateContentConfig {
	cfg := &genai.GenerateContentConfig{}
	if e.genCfg != nil {
		c := *e.genCfg
		cfg = &c
	}
	if e.sysPrompt != "" {
		cfg.SystemInstruction = genai.NewContentFromText(e.sysPrompt, genai.RoleUser)
	}
	cfg.Tools = declarations(e.tools)
	return cfg
}

// dispatch runs one tool call: gate check first, then the handler.
func (e *engine) dispatch(ctx context.Context, name string, args map[string]any) (string, bool) {
	if e.gate != nil {
		if deny, reason := e.gate(ctx, e.wickSessionID, name, args); deny {
			if reason == "" {
				reason = "blocked by command gate"
			}
			return reason, true
		}
	}
	t, ok := e.toolByName[name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", name), true
	}
	return t.handler(ctx, args)
}
