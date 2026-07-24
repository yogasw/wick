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
}

// setWickSessionID wires the wick HTTP session id (distinct from the
// stream-json protocol's own sessionID) so gate checks route approval
// requests to the session the user is actually looking at.
func (e *engine) setWickSessionID(id string) { e.wickSessionID = id }

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
		llm:        llm,
		modelName:  modelName,
		sysPrompt:  sysPrompt,
		genCfg:     genCfg,
		tools:      tools,
		toolByName: byName,
		maxTurns:   maxTurns,
		history:    history,
		emit:       emit,
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

		resp, err := e.generate(ctx)
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
			e.emit(toolResultLine(id, result, isErr))

			respParts = append(respParts, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					ID:       fc.ID,
					Name:     fc.Name,
					Response: map[string]any{"result": result},
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

	var out *LLMResponse
	var gotErr error
	for resp, err := range e.llm.GenerateContent(ctx, req, false) {
		if err != nil {
			gotErr = err
			break
		}
		out = resp
	}
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
	return out, nil
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
