package wick

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/agents/session"
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
	modelID   string
	sysPrompt string
	genCfg    *genai.GenerateContentConfig
	reasoning *ReasoningConfig
	// defaultReasoning is the session's configured reasoning baseline (what
	// setReasoning was last given). A runtime "/thinking off" clears
	// e.reasoning; "/thinking on" restores this. Distinct from e.reasoning,
	// which is the CURRENT (possibly toggled) request.
	defaultReasoning *ReasoningConfig
	tools            []toolDef
	toolByName       map[string]toolDef

	// maxTurns caps total tool-call rounds per user message. 0 = unlimited
	// (the consec-error + wall-clock guards below are the safety net).
	maxTurns int
	// maxConsecErr cuts the turn after this many consecutive all-error
	// tool rounds (a success resets the counter). <=0 → default 20.
	maxConsecErr int
	// maxTurnDur is the wall-clock ceiling for one turn. <=0 → default 1h.
	maxTurnDur time.Duration

	// sessionDir is where session-scoped state lives (goal.json, tool spill,
	// interactions). Empty in unit tests that don't need goal force-continue.
	sessionDir string

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

	// steer, when set, is drained (non-blocking) between tool-call rounds within
	// a turn: any user messages that arrived MID-TURN are injected into the
	// history before the next model call, so the user can steer/correct the agent
	// while it's still working instead of waiting for the whole turn to finish.
	// Same channel the spawn loop reads for next-turn messages; the engine peeks
	// it opportunistically. nil disables mid-turn steering.
	steer <-chan string

	// retryPolicy tunes per-call timeout + retry attempts, applied to every model
	// call. Zero value → sane defaults (3 attempts, 120s). Set from wick config.
	retryPolicy retryPolicy

	// stream, when true, drives each model call over SSE: text/thinking is
	// emitted token-by-token as it arrives (the parser's stream_event delta
	// path), so the UI renders live instead of all-at-once when the call
	// returns. The aggregated final response is still used for history +
	// calibration, so a streamed turn is otherwise identical. false = the
	// one-shot JSON path. Adapters without an SSE path (Gemini) ignore it.
	stream bool

	// streamedText / streamedThinking record whether the last model call
	// already emitted its text / thinking as live deltas. runTurn consults
	// them so it doesn't ALSO emit the aggregated block (which would
	// double-render in the wick UI — the parser only dedups its own two
	// frame shapes, not wick emitting both a delta line and a full textLine).
	streamedText     bool
	streamedThinking bool
}

// setStream toggles SSE streaming of model output for this engine.
func (e *engine) setStream(v bool) { e.stream = v }

// setSteer wires the mid-turn steering channel (the spawn's msgs channel).
func (e *engine) setSteer(ch <-chan string) { e.steer = ch }

// setRetryPolicy wires the operator-configured retry+timeout policy.
func (e *engine) setRetryPolicy(p retryPolicy) { e.retryPolicy = p }

// setWickSessionID wires the wick HTTP session id (distinct from the
// stream-json protocol's own sessionID) so gate checks route approval
// requests to the session the user is actually looking at.
func (e *engine) setWickSessionID(id string) { e.wickSessionID = id }

// setToolSpillDir wires the directory large tool results spill into.
// Also seeds sessionDir (goal.json lives next to tool-out/).
func (e *engine) setToolSpillDir(dir string) {
	e.toolSpillDir = dir
	e.sessionDir = dir
}

// setInteractionSink wires the per-spawn interaction recorder.
func (e *engine) setInteractionSink(fn func(interactionRecord)) { e.interactionSink = fn }

// setModelID records which WickModel.ID this engine is running, so
// interaction records carry a reference the config can be looked up
// from later — never the config itself.
func (e *engine) setModelID(id string) { e.modelID = id }

// setReasoning attaches the vendor-agnostic reasoning request applied to every
// model call this turn (nil = vendor default). The value is also snapshotted as
// the session's configured baseline so a runtime "/thinking on" can restore it
// after a "/thinking off".
func (e *engine) setReasoning(r *ReasoningConfig) {
	e.reasoning = r
	e.defaultReasoning = r
}

// setLoopGuards wires the operator-configured no-progress guards: cut the
// turn after maxConsecErr consecutive all-error tool rounds, or when the
// turn's wall clock exceeds maxTurnDur. Zero values keep the defaults
// (20 errors / 1h) applied in newEngine.
func (e *engine) setLoopGuards(maxConsecErr int, maxTurnDur time.Duration) {
	if maxConsecErr > 0 {
		e.maxConsecErr = maxConsecErr
	}
	if maxTurnDur > 0 {
		e.maxTurnDur = maxTurnDur
	}
}

// newEngine assembles an engine for one spawn.
func newEngine(llm LLM, modelName, sysPrompt string, genCfg *genai.GenerateContentConfig, tools []toolDef, history []*genai.Content, maxTurns int, emit func([]byte)) *engine {
	byName := make(map[string]toolDef, len(tools))
	for _, t := range tools {
		byName[t.decl.Name] = t
	}
	if maxTurns < 0 {
		maxTurns = 0 // 0 = unlimited; consec-error + wall-clock guards are the brake
	}
	return &engine{
		llm:              llm,
		modelName:        modelName,
		sysPrompt:        sysPrompt,
		genCfg:           genCfg,
		tools:            tools,
		toolByName:       byName,
		maxTurns:         maxTurns,
		maxConsecErr:     20,
		maxTurnDur:       time.Hour,
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

	// /thinking — runtime reasoning toggle (like Claude Code's /thinking).
	// Intercepted so it never reaches the model: flip the live reasoning
	// request and report the new state. Same last-non-empty-line matching as
	// /compact so a real message merely mentioning it isn't hijacked.
	if ok, arg := parseThinkingCommand(userText); ok {
		e.runThinkingCommand(arg)
		return
	}

	// Compact BEFORE appending the new turn so the model-driven summary
	// covers only settled history, and the fresh user message always
	// survives into the request.
	e.maybeCompact(ctx, e.contextBudget)

	e.history = append(e.history, currentUserContent(e.sessionDir, userText))

	var finalText strings.Builder
	turnStart := time.Now()
	consecErr := 0
	for turn := 0; ; turn++ {
		// Cancellation (manual Kill / Stop / session reap) wins over every
		// other guard — an OPEN goal must NEVER keep the loop alive past a
		// kill. Checked FIRST so the goal force-continue branches below
		// can't swallow a cancel that raced a wall-clock/error window.
		if ctx.Err() != nil {
			e.emit(doneLine(strings.TrimSpace(finalText.String())))
			return
		}
		if e.maxTurns > 0 && turn >= e.maxTurns {
			e.finishTruncated(&finalText, fmt.Sprintf("turn cut: max_turns cap (%d) reached", e.maxTurns))
			return
		}
		if time.Since(turnStart) > e.maxTurnDur {
			// Goal open → don't kill (same recovery style as consec-error).
			// Nudge and reset the window so the next check is another full
			// maxTurnDur away; a just-closed goal falls through to the cut.
			if session.HasOpenGoalDir(e.sessionDir) {
				msg := fmt.Sprintf("wall-clock limit (%s) exceeded — goal still OPEN, keep working (or todo{goal_done:true}/todo{goal_abandon:true})", e.maxTurnDur)
				e.emit(thinkingLine("↻ " + msg))
				e.history = append(e.history, genai.NewContentFromText("[wick] "+msg, genai.RoleUser))
				turnStart = time.Now()
				continue
			}
			e.finishTruncated(&finalText, fmt.Sprintf("turn cut: wall-clock limit (%s) exceeded", e.maxTurnDur))
			return
		}

		// Drain any messages that arrived MID-TURN and inject them so the next
		// model call sees the user's steer/correction without waiting for the
		// whole turn to end. Non-blocking: only messages already queued are taken.
		e.drainSteer()

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

		// Emit text + thinking, and collect any function calls. When streaming
		// already sent the text/thinking as live deltas this same call, the
		// aggregated block is NOT re-emitted (that would double-render) — but
		// finalText still accumulates the text so doneLine's .result is whole.
		var calls []*genai.FunctionCall
		for _, p := range content.Parts {
			switch {
			case p.FunctionCall != nil:
				calls = append(calls, p.FunctionCall)
			case p.Thought && p.Text != "":
				if !e.streamedThinking {
					e.emit(thinkingLine(p.Text))
				}
			case p.Text != "":
				if !e.streamedText {
					e.emit(textLine(p.Text))
				}
				finalText.WriteString(p.Text)
			}
		}

		// Record the assistant turn (with any tool calls) into history so
		// the next model call sees its own prior actions.
		e.history = append(e.history, &genai.Content{Role: genai.RoleModel, Parts: content.Parts})

		if len(calls) == 0 {
			// Goal latch (todo{goal:...}): while OPEN, plain text is a
			// progress report, not end-of-turn. Keep looping until the
			// model calls todo with goal_done/goal_abandon.
			if session.HasOpenGoalDir(e.sessionDir) {
				msg := "goal still OPEN — keep working (or todo{goal_done:true} / todo{goal_abandon:true})"
				if g, _ := session.LoadGoalDir(e.sessionDir); g != nil && g.Goal != "" {
					msg = fmt.Sprintf("goal still OPEN: %q — keep working toward it, or todo{goal_done:true}/todo{goal_abandon:true}", g.Goal)
				}
				e.emit(thinkingLine("↻ " + msg))
				e.history = append(e.history, genai.NewContentFromText("[wick] "+msg, genai.RoleUser))
				continue
			}
			e.emit(doneLine(strings.TrimSpace(finalText.String())))
			return
		}

		// Execute each tool call, emit tool_use/tool_result, and append a
		// function-response content for the next model call.
		respParts := make([]*genai.Part, 0, len(calls))
		batchAllErr := len(calls) > 0
		var lastErr string
		for _, fc := range calls {
			id := fc.ID
			if id == "" {
				id = "call_" + uuid.NewString()[:8]
			}
			e.emit(toolUseLine(id, fc.Name, marshalArgs(fc.Args)))

			// Heartbeat while the tool runs: a long shell/scrape can sit
			// silent for minutes, which looks identical to a hung process
			// to the idle-kill timer (resets only on pipe writes). Same
			// pattern as generateAttempt's model-call heartbeat.
			toolHBDone := make(chan struct{})
			go func() {
				t := time.NewTicker(15 * time.Second)
				defer t.Stop()
				for {
					select {
					case <-t.C:
						e.emit(heartbeatLine())
					case <-toolHBDone:
						return
					}
				}
			}()
			result, isErr := e.dispatch(ctx, fc.Name, fc.Args)
			close(toolHBDone)
			if isErr {
				lastErr = result
			} else {
				batchAllErr = false
			}
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

		// No-progress guard: only consecutive ALL-error rounds count; any
		// round with at least one successful call resets the counter
		// (parallel calls where one succeeds = progress).
		if batchAllErr {
			consecErr++
		} else {
			consecErr = 0
		}
		if consecErr >= e.maxConsecErr {
			// Goal open → don't kill (Claude-style: recover & keep
			// going). Nudge the model with the error streak and reset
			// the counter so it can try a different approach.
			if session.HasOpenGoalDir(e.sessionDir) {
				msg := fmt.Sprintf("%d consecutive tool errors (last: %.200s) — goal still OPEN, try a different approach (or todo{goal_done:true}/todo{goal_abandon:true})", consecErr, lastErr)
				e.emit(thinkingLine("↻ " + msg))
				e.history = append(e.history, genai.NewContentFromText("[wick] "+msg, genai.RoleUser))
				consecErr = 0
				continue
			}
			e.finishTruncated(&finalText, fmt.Sprintf("turn cut: %d consecutive tool errors, last error: %.200s", consecErr, lastErr))
			return
		}
	}
}

// finishTruncated ends a truncated turn VISIBLY: the reason is emitted as
// a text event (the parser drops doneLine's .result — text reaches the
// transcript only via textLine), then the turn closes with doneLine —
// otherwise a cut turn looks like a mysterious death to the user.
func (e *engine) finishTruncated(finalText *strings.Builder, reason string) {
	log.Warn().Str("reason", reason).Msg("wick.engine: turn truncated")
	marker := "\n\n[wick] " + reason
	e.emit(textLine(marker))
	finalText.WriteString(marker)
	e.emit(doneLine(strings.TrimSpace(finalText.String())))
}

// drainSteer pulls any messages already queued on the steer channel (arrived
// mid-turn) and appends them to the history as user turns, so the next model
// call in this turn sees the user's steer. Non-blocking — it never waits for a
// message. The message is already persisted to the store by pool.send when it
// arrived, so the transcript shows it; here we only feed it into the live model
// context. isCompactCommand messages are skipped (a mid-turn /compact would
// fight the in-progress turn; it's honored as its own next turn instead).
func (e *engine) drainSteer() {
	if e.steer == nil {
		return
	}
	for {
		select {
		case msg, ok := <-e.steer:
			if !ok {
				e.steer = nil
				return
			}
			if trimmed := strings.TrimSpace(msg); trimmed == "" || isCompactCommand(msg) {
				continue
			} else if ok, _ := parseThinkingCommand(trimmed); ok {
				// A mid-turn /thinking is honored as its own next turn (like
				// /compact), not injected into this turn's history.
				continue
			}
			e.emit(thinkingLine("↪ user added mid-turn: " + msg))
			e.history = append(e.history, genai.NewContentFromText(msg, genai.RoleUser))
		default:
			return
		}
	}
}

// generate calls the model once (non-streaming v1) and returns the
// aggregated response. Streaming token-by-token output is a documented
// follow-up (the aggregator double-emit hazard makes non-streaming the
// correct first cut).
func (e *engine) generate(ctx context.Context) (*LLMResponse, error) {
	return e.generateAttempt(ctx, "generate", 1, "")
}

// snapshotRequest builds the in-flight request snapshot the live-phase registry
// exposes to the FE (curl + viewer) BEFORE the record lands. Reuses the same
// truncating summarizers the finished record uses, so both render identically.
func (e *engine) snapshotRequest(kind string, cfg *genai.GenerateContentConfig, contents []*genai.Content) *modelCallRequest {
	r := &modelCallRequest{
		Kind:    kind,
		Model:   e.modelName,
		ModelID: e.modelID,
		System:  capText(systemText(cfg)),
		Request: summarizeRequest(contents),
	}
	for _, fd := range toolsFromConfig(cfg) {
		r.Tools = append(r.Tools, fd.Name)
	}
	return r
}

// generateAttempt calls the model once and returns the aggregated response. kind
// is "generate" or "compaction"; attempt is the 1-based retry number and reason
// a short label (both surfaced live so the FE can show "retrying (attempt 2 —
// rate limited)"). The vendor call runs under a child context whose cancel is
// published to the live-phase registry, so CancelModelCall can abort THIS call
// without killing the turn — generate returns an error the caller handles like
// any other, and the turn ends gracefully.
func (e *engine) generateAttempt(ctx context.Context, kind string, attempt int, reason string) (*LLMResponse, error) {
	// The runtime /thinking override (if the user toggled it this session)
	// wins over the configured baseline. Resolved per-call so a mid-session
	// toggle takes effect on the very next model call.
	reasoning := e.effectiveReasoning()
	cfg := e.effectiveConfig()
	applyGeminiThinking(cfg, reasoning)
	req := &LLMRequest{
		Model:     e.modelName,
		Contents:  e.history,
		Config:    cfg,
		Reasoning: reasoning,
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

	// Child context so just this model call can be cancelled (CancelModelCall)
	// without tearing down the turn/session.
	callCtx, cancelCall := context.WithCancel(ctx)
	defer cancelCall()

	snap := e.snapshotRequest(kind, cfg, req.Contents)
	sid := e.wickSessionID

	// Apply the operator's configured retry+timeout policy to this call (the
	// adapters read it from the context); nil config → sane defaults.
	callCtx = withRetryPolicy(callCtx, e.retryPolicy)

	// Surface adapter-level retries (429/5xx/transport, done inside the adapters
	// below the engine) live AND in the conversation: the interactions panel gets
	// a "retrying (attempt N)" badge, and the chat gets a system line so the user
	// sees "the model errored and is being retried" without opening the panel.
	// The snapshot is unchanged across a retry (same request), so we reuse snap.
	callCtx = withRetryNotify(callCtx, func(httpAttempt int, reason string) {
		a := attempt
		if httpAttempt > 1 {
			a = attempt + httpAttempt - 1
		}
		markModelCallRetry(sid, a, reason, snap, cancelCall)
		max := e.retryPolicy.attempts()
		e.emit(thinkingLine(fmt.Sprintf("⚠ Model call failed (%s) — retrying (attempt %d/%d)…", reason, a, max)))
	})

	// Mark the model-call phase with the request snapshot + attempt + cancel so
	// the interactions UI can label "waiting on the model", render/copy the
	// in-flight request, show a retry badge, and drive a per-call cancel. Cleared
	// as soon as the call returns.
	if attempt > 1 {
		markModelCallRetry(sid, attempt, reason, snap, cancelCall)
	} else {
		markModelCallStart(sid, attempt, snap, cancelCall)
	}

	// Reset the per-call streamed-flags: runTurn reads these to know whether
	// the text/thinking already went out as live deltas (so it must not
	// re-emit the aggregated blocks and double-render).
	e.streamedText = false
	e.streamedThinking = false

	var out *LLMResponse
	var gotErr error
	for resp, err := range e.llm.GenerateContent(callCtx, req, e.stream) {
		if err != nil {
			gotErr = err
			break
		}
		if resp.isDelta() {
			// Live streaming chunk — emit it now via the parser's delta path.
			// It is NOT folded into out; the adapter's final aggregated yield
			// (Content set, deltas empty) carries the authoritative turn body.
			if resp.TextDelta != "" {
				e.emit(textDeltaLine(resp.TextDelta))
				e.streamedText = true
			}
			if resp.ThinkingDelta != "" {
				e.emit(thinkingDeltaLine(resp.ThinkingDelta))
				e.streamedThinking = true
			}
			continue
		}
		out = resp
	}
	markModelCallDone(e.wickSessionID)
	close(heartbeatDone)
	e.recordInteraction(kind, cfg, req.Contents, out, time.Since(start).Milliseconds(), gotErr)
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

		// attempt+1 because this generate is the (attempt+1)-th try of the same
		// logical call; the live phase shows "retrying (attempt N — context full)".
		out, err = e.generateAttempt(ctx, "generate", attempt+1, "context full")
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
