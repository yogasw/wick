package omp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yogasw/wick/internal/agents/event"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider/cliserver"
)

// rpc_turn.go is one turn on a session's omp RPC process, shaped as a
// provider.Process so the pool and event.OMPParser see what they see for
// `omp -p --mode json`: Stdout() yields the same JSON lines (a session
// header, then the AgentSessionEvents), Wait() returns once the prompt's
// work has settled.
//
// Inject = `steer` (the message joins the run that is going, as with
// claude typed mid-turn). Kill = `abort`: the process stays up for the
// session's next turn, whatever was queued inside omp goes with the abort.
// Pid() is 0: the agent's teardown signals the process GROUP of Pid(), and
// that group is the RPC process the next turn reuses.

const (
	// DefaultServerIdle is the idle window when the instance sets none.
	DefaultServerIdle = cliserver.DefaultIdle
	// rpcMaxAge retires a process before the MCP token baked into its env
	// (12 h TTL) can run out under a session that never goes idle.
	rpcMaxAge = 6 * time.Hour
	killGrace = 3 * time.Second
	// mcpRevokeGrace mirrors pool.mcpRevokeGrace: a tool call can still be
	// in flight when the process goes.
	mcpRevokeGrace = 90 * time.Second
)

// rpcPromptWait bounds a turn from its slot to its prompt going out: the
// steps before it (new_session, get_state, pin) are each bounded well
// inside it. A var so tests can shorten it.
var rpcPromptWait = 2 * time.Minute

var rpcServers = newRPCManager()

func newRPCManager() *cliserver.Manager[*rpcConn] {
	m := cliserver.New[*rpcConn]("omp", 1)
	m.OnStop = func(_ string, c *rpcConn) { revokeLater(c.token) }
	return m
}

// ShutdownServers kills every omp RPC process and auth broker (wick
// shutdown / upgrade).
func ShutdownServers() { rpcServers.Shutdown(); ShutdownBrokers() }

// revoker is the pool's RevokeMCPToken (SetMCPTokenRevoker); nil = tokens
// just expire at their TTL.
var (
	revokerMu sync.Mutex
	revoker   func(string)
)

// SetMCPTokenRevoker wires the per-session MCP token revocation. omp owns
// its tokens' lifetime because in server mode a token lives as long as the
// RPC process that baked it into its env, not as long as one turn.
func SetMCPTokenRevoker(fn func(string)) {
	revokerMu.Lock()
	revoker = fn
	revokerMu.Unlock()
}

func revokeLater(tok string) {
	revokerMu.Lock()
	fn := revoker
	revokerMu.Unlock()
	if fn == nil || tok == "" {
		return
	}
	time.AfterFunc(mcpRevokeGrace, func() { fn(tok) })
}

// rpcControlFrames are protocol frames with no `-p --mode json` twin.
var rpcControlFrames = map[string]bool{
	"response": true, "ready": true, "prompt_result": true, "session_settled": true,
	"rpc_chunk": true, "available_commands_update": true, "extension_ui_request": true,
	"host_tool_call": true, "host_tool_cancel": true, "host_uri_request": true, "host_uri_cancel": true,
	"subagent_lifecycle": true, "subagent_progress": true, "subagent_event": true,
	"command_output": true, "session_info_update": true, "config_update": true,
}

// translateFrame turns one RPC event frame into the line `-p --mode json`
// prints for it (print-mode.ts printableEvent); ok=false drops it.
func translateFrame(line []byte) ([]byte, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(line, &m) != nil {
		return nil, false
	}
	var typ string
	_ = json.Unmarshal(m["type"], &typ)
	if typ == "" || rpcControlFrames[typ] {
		return nil, false
	}
	delete(m, "messageId") // RPC stamps message frames; print mode does not
	switch typ {
	case "message_update":
		// The streamed snapshot (message, partial) is dropped: only the
		// delta matters, the full message follows in message_end.
		delete(m, "message")
		var ev map[string]json.RawMessage
		if json.Unmarshal(m["assistantMessageEvent"], &ev) == nil {
			var et string
			_ = json.Unmarshal(ev["type"], &et)
			if et == "done" || et == "error" {
				ev = map[string]json.RawMessage{"type": ev["type"], "reason": ev["reason"]}
			} else {
				delete(ev, "partial")
			}
			m["assistantMessageEvent"], _ = json.Marshal(ev)
		}
	case "tool_stream_update":
		m = map[string]json.RawMessage{"type": m["type"], "toolCallId": m["toolCallId"], "toolName": m["toolName"]}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, false
	}
	return append(b, '\n'), true
}

// headerLine is the `{"type":"session",...}` line print mode opens with;
// the parser reads the session id from it.
func headerLine(sessionID, cwd string) []byte {
	b, _ := json.Marshal(map[string]string{"type": "session", "id": sessionID, "cwd": cwd})
	return append(b, '\n')
}

// errorLines end a turn with err the way a failed `-p` run does: an
// assistant message that stopped on error, then the terminal agent_end.
func errorLines(msg string) []byte {
	a, _ := json.Marshal(map[string]any{"type": "message_end", "message": map[string]any{
		"role": "assistant", "stopReason": "error", "errorMessage": msg, "content": []any{}}})
	b, _ := json.Marshal(map[string]any{"type": "agent_end", "messages": []any{}})
	return append(append(append(a, '\n'), b...), '\n')
}

// errTurnKilled wraps context.Canceled: the agent reads that as a stop it
// asked for, not as a crash to recover from.
var errTurnKilled = fmt.Errorf("omp turn aborted: %w", context.Canceled)

// errTurnOver is Inject's answer once the turn cannot take a message.
var errTurnOver = errors.New("omp turn is over")

// rpcTurnSpec is what one turn sends.
type rpcTurnSpec struct {
	prompt string
	cwd    string
	// fresh: the wick session has no omp session yet (no resume id) but
	// the process already served one — start a new omp session first.
	fresh bool
	// account pins one OAuth account of the model's provider for the omp
	// session (`/session pin <n>`); "" = Auto, omp rotates natively.
	account string
}

// pinAccount pins account for the omp session before its prompt. omp's RPC
// `prompt` runs built-in slash commands (their text arrives as
// command_output frames), and `/session pin` binds an OAuth account of the
// CURRENT model's provider to this session id — so it is redone after
// new_session. A pin that does not take (unknown account, single-account
// provider, older omp) is logged and the turn runs on Auto: a wrong pin
// must never block the answer.
func pinAccount(ctx context.Context, c *rpcConn, sessionID, account string) {
	var out strings.Builder
	unsub := c.subscribe(func(_ []byte, f rpcFrame) {
		if f.Type == "command_output" {
			out.WriteString(f.Text)
		}
	})
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	f, err := c.call(pctx, map[string]any{"type": "prompt", "message": "/session pin " + account})
	cancel()
	unsub()
	text := strings.TrimSpace(out.String())
	if err != nil || !f.Success || !strings.HasPrefix(text, "Pinned ") {
		log.Warn().Err(err).Str("account", account).Str("omp", firstLineOf(text)).
			Msg("omp: account pin did not take; this turn runs on Auto")
		return
	}
	c.mu.Lock()
	c.pinned = sessionID + "#" + account
	c.mu.Unlock()
	log.Info().Str("account", account).Msg("omp: account pinned for the session")
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// rpcProcess implements provider.Process (and provider.Injector) for one turn.
type rpcProcess struct {
	pr     *io.PipeReader
	pw     *io.PipeWriter
	env    []string
	bin    string
	argv   []string
	done   chan struct{}
	cancel context.CancelFunc

	mu       sync.Mutex
	err      error
	conn     *rpcConn
	prompted bool
	killed   bool
	// killDone closes when the first Kill is through; a second Kill
	// waits on it, so no caller returns while an abort is in flight.
	killDone chan struct{}
	finished bool
	early    []string

	// injMu orders the first prompt and every steer.
	injMu sync.Mutex
}

func newRPCProcess(env []string, bin string, argv []string) *rpcProcess {
	pr, pw := io.Pipe()
	// cancel is a no-op until a turn replaces it: the failed-start path
	// never runs a turn, and Kill must not call a nil func there.
	return &rpcProcess{pr: pr, pw: pw, env: env, bin: bin, argv: argv, done: make(chan struct{}), cancel: func() {}}
}

func (p *rpcProcess) Stdout() io.Reader     { return p.pr }
func (p *rpcProcess) Stdin() io.WriteCloser { return noopWriteCloser{} }
func (p *rpcProcess) Env() []string         { return p.env }
func (p *rpcProcess) Pid() int              { return 0 }
func (p *rpcProcess) Binary() string        { return p.bin }
func (p *rpcProcess) Argv() []string        { return append([]string(nil), p.argv...) }

func (p *rpcProcess) Wait() error {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Kill aborts the turn and returns once the abort is through (the next
// turn prompts the same omp session; an abort landing after that prompt
// would abort the new turn instead). The pipe is closed first: whoever
// kills a turn has stopped reading it.
func (p *rpcProcess) Kill() error {
	p.mu.Lock()
	if p.killed {
		kd := p.killDone
		p.mu.Unlock()
		<-kd
		return nil
	}
	p.killed = true
	p.killDone = make(chan struct{})
	defer close(p.killDone)
	c, prompted, finished := p.conn, p.prompted, p.finished
	p.mu.Unlock()
	_ = p.pr.CloseWithError(errTurnKilled)
	if finished {
		return nil
	}
	if !prompted || c == nil {
		p.cancel()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcCallWait)
	defer cancel()
	if _, err := c.call(ctx, map[string]any{"type": "abort"}); err != nil {
		log.Warn().Err(err).Msg("agents.omp: abort failed; ending the turn")
	}
	select {
	case <-p.done:
	case <-time.After(killGrace):
		p.cancel()
	}
	return nil
}

// Inject steers the running turn with text: omp delivers it to the run
// that is going (rpc steer → session.steer). Before the prompt is out the
// text rides along in it.
func (p *rpcProcess) Inject(text string) error {
	p.injMu.Lock()
	defer p.injMu.Unlock()
	p.mu.Lock()
	if p.killed || p.finished {
		p.mu.Unlock()
		return errTurnOver
	}
	if !p.prompted {
		p.early = append(p.early, text)
		p.mu.Unlock()
		return nil
	}
	c := p.conn
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), rpcCallWait)
	defer cancel()
	_, err := c.call(ctx, map[string]any{"type": "steer", "message": text})
	return err
}

// busyCheckWait bounds Busy's get_state: the idle timer asks from its own
// goroutine and must not stall on an RPC that answers nothing.
var busyCheckWait = 5 * time.Second

// Busy reports whether omp still runs this turn's session: streaming,
// compacting, or holding async work (a background sub-agent) that will wake
// it again. The agent's idle timer asks before aborting a silent turn — a
// long tool leaves the frame stream quiet while the work goes on. A turn
// that is over, killed, not yet prompted, or whose RPC does not answer is
// not busy, so the timer takes its usual course.
func (p *rpcProcess) Busy() bool {
	p.mu.Lock()
	c, ok := p.conn, p.prompted && !p.killed && !p.finished
	p.mu.Unlock()
	if !ok || c == nil || c.dead() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), busyCheckWait)
	defer cancel()
	st, err := c.callWait(ctx, map[string]any{"type": "get_state"}, busyCheckWait)
	if err != nil {
		return false
	}
	var state struct {
		IsStreaming         bool `json:"isStreaming"`
		IsCompacting        bool `json:"isCompacting"`
		HasPendingAsyncWork bool `json:"hasPendingAsyncWork"`
	}
	if json.Unmarshal(st.Data, &state) != nil {
		return false
	}
	return state.IsStreaming || state.IsCompacting || state.HasPendingAsyncWork
}

func (p *rpcProcess) emit(b []byte) { _, _ = p.pw.Write(b) }

func (p *rpcProcess) finish(err error) {
	p.mu.Lock()
	if p.err == nil {
		p.err = err
	}
	if p.killed {
		p.err = errTurnKilled
	}
	p.finished = true
	p.mu.Unlock()
	_ = p.pw.Close()
	close(p.done)
}

// turnSink routes the shared reader's frames to one turn. It runs on the
// process's stdout reader, so no send may block past the turn's end: a
// surplus result after the turn stopped reading would wedge every later
// turn on the same process.
func turnSink(frames chan<- []byte, results chan<- rpcFrame, done <-chan struct{}) func(line []byte, f rpcFrame) {
	return func(line []byte, f rpcFrame) {
		switch f.Type {
		case "prompt_result", "session_settled":
			select {
			case results <- f:
			case <-done:
			}
		default:
			select {
			case frames <- line:
			case <-done:
			}
		}
	}
}

// run drives one turn to the end. It owns the lease.
func (p *rpcProcess) run(ctx context.Context, l *cliserver.Lease[*rpcConn], t rpcTurnSpec) {
	defer l.Release()
	err := p.turn(ctx, l, t)
	killed := func() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.killed }()
	if err != nil && !killed {
		// Reported IN the turn, as its error frames: returning it from Wait
		// would read as an unexplained exit and the pool would "recover".
		p.emit(errorLines(err.Error()))
		err = nil
	}
	p.finish(err)
}

func (p *rpcProcess) turn(ctx context.Context, l *cliserver.Lease[*rpcConn], t rpcTurnSpec) error {
	if err := l.WaitSlot(ctx); err != nil {
		return err
	}
	// Bounded: a turn that holds the slot but never gets its prompt out
	// (its stream unread, an RPC that answers nothing) is killed — Kill
	// closes the stream, so whatever it blocks on returns and the lease
	// goes back. Without it the process counts as busy forever and is
	// neither reaped nor yielded.
	wait := rpcPromptWait
	guard := time.AfterFunc(wait, func() {
		p.mu.Lock()
		prompted := p.prompted
		p.mu.Unlock()
		if !prompted {
			log.Warn().Int("rpc_pid", l.H.Pid()).Dur("after", wait).
				Msg("agents.omp: turn never sent its prompt; ending it")
			_ = p.Kill()
		}
	})
	defer guard.Stop()
	c := l.H
	if t.fresh {
		if _, err := c.call(ctx, map[string]any{"type": "new_session"}); err != nil {
			return err
		}
	}
	st, err := c.call(ctx, map[string]any{"type": "get_state"})
	if err != nil {
		return err
	}
	var state struct {
		SessionID string `json:"sessionId"`
		// The active model (packages/ai Model): its contextWindow is the
		// meter's scale — no extra process, the RPC is already running.
		Model *struct {
			ContextWindow int `json:"contextWindow"`
		} `json:"model"`
		// Whether omp compacts by itself once the window fills; shown in
		// the context panel next to the manual button.
		AutoCompactionEnabled *bool `json:"autoCompactionEnabled"`
	}
	_ = json.Unmarshal(st.Data, &state)
	c.mu.Lock()
	c.sessionID = state.SessionID
	c.turns++
	needPin := t.account != "" && c.pinned != state.SessionID+"#"+t.account
	c.mu.Unlock()
	if needPin {
		pinAccount(ctx, c, state.SessionID, t.account)
	}

	// Frames of this turn, in order; the reader goroutine never blocks on
	// a slow consumer for long (buffered), and emit is the only sink.
	frames := make(chan []byte, 256)
	results := make(chan rpcFrame, 4)
	unsub := c.subscribe(turnSink(frames, results, p.done))
	defer unsub()
	p.emit(headerLine(state.SessionID, t.cwd))
	window := 0
	if state.Model != nil {
		window = state.Model.ContextWindow
	}
	if window > 0 || state.AutoCompactionEnabled != nil {
		p.emit(event.ContextStateLine(window, state.AutoCompactionEnabled))
	}
	// "/compact": omp's official RPC `compact` (its slash command sent as
	// a prompt compacts too, but emits no turn — the turn never ended and
	// the UI kept spinning). The result's token counts become the notice,
	// then the turn ends.
	if instr, ok := compactPrompt(t.prompt); ok {
		// No prompt goes out: the watchdog above would end a compaction
		// at 2m; compactTurn has its own bound (rpcCompactWait).
		guard.Stop()
		return p.compactTurn(ctx, c, instr)
	}

	p.injMu.Lock()
	p.mu.Lock()
	if p.killed {
		// Killed before the prompt went out (stopped, or the guard
		// above): sending it now would start a run nobody reads.
		p.mu.Unlock()
		p.injMu.Unlock()
		return errTurnKilled
	}
	p.conn = c
	if len(p.early) > 0 {
		t.prompt = strings.Join(append([]string{t.prompt}, p.early...), "\n\n")
		p.early = nil
	}
	p.mu.Unlock()
	// streamingBehavior steer: if a run is somehow still going (a turn
	// killed while its abort was in flight) the prompt joins it rather
	// than failing with "already streaming".
	id, ch, err := c.send(map[string]any{"type": "prompt", "message": t.prompt, "streamingBehavior": "steer"})
	if err == nil {
		select {
		case f := <-ch:
			if !f.Success {
				err = fmt.Errorf("omp rpc prompt: %s", f.errText())
			}
		case <-c.done:
			err = errors.New("omp rpc process exited")
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err != nil {
		p.injMu.Unlock()
		return err
	}
	p.mu.Lock()
	p.prompted = true
	killed := p.killed
	p.mu.Unlock()
	p.injMu.Unlock()
	if killed {
		_, _ = c.call(context.Background(), map[string]any{"type": "abort"})
	}

	sawEnd := false
	var result *rpcFrame
	for {
		select {
		case line := <-frames:
			out, ok := translateFrame(line)
			if !ok {
				continue
			}
			if strings.Contains(string(out), `"type":"agent_end"`) {
				sawEnd = true
			}
			p.emit(out)
			continue
		case f := <-results:
			if f.Type == "prompt_result" && f.ID == id {
				r := f
				result = &r
				if !f.SessionSettled {
					continue // background work may still wake it; session_settled follows
				}
			} else if f.Type != "session_settled" || result == nil {
				continue
			}
		case <-c.done:
			return errors.New("omp rpc process exited mid-turn")
		case <-ctx.Done():
			return ctx.Err()
		}
		break
	}
	// Flush frames that arrived before the result.
	for {
		select {
		case line := <-frames:
			if out, ok := translateFrame(line); ok {
				if strings.Contains(string(out), `"type":"agent_end"`) {
					sawEnd = true
				}
				p.emit(out)
			}
			continue
		default:
		}
		break
	}
	p.mu.Lock()
	p.finished = true
	p.mu.Unlock()
	if result.Status == "error" && !sawEnd {
		// Failed before the agent ran (model unavailable, not logged in):
		// no agent_end carried the error, so say it the parser's way.
		msg := result.errText()
		if msg == "" {
			msg = "omp prompt failed"
		}
		p.emit(errorLines(msg))
	}
	return nil
}

// compactPrompt reports a "/compact [instructions]" turn and its
// instructions.
func compactPrompt(prompt string) (string, bool) {
	p := strings.TrimSpace(prompt)
	if !strings.EqualFold(p, "/compact") && !strings.HasPrefix(strings.ToLower(p), "/compact ") {
		return "", false
	}
	return strings.TrimSpace(p[len("/compact"):]), true
}

// compactTurn runs omp's RPC `compact` and ends the turn with its result
// (a compaction line the parser turns into the "Compacted: X → Y" notice,
// then agent_end), or with omp's own error. A compact omp refuses because
// there is nothing to do is a notice, not an error.
func (p *rpcProcess) compactTurn(ctx context.Context, c *rpcConn, instructions string) error {
	cmd := map[string]any{"type": "compact"}
	if instructions != "" {
		cmd["customInstructions"] = instructions
	}
	end, _ := json.Marshal(map[string]any{"type": "agent_end", "messages": []any{}})
	end = append(end, '\n')
	f, err := c.callWait(ctx, cmd, rpcCompactWait)
	if err == nil && !f.Success {
		err = fmt.Errorf("omp compact: %s", f.errText())
	}
	if err != nil {
		notice, ok := compactNoop(err.Error())
		if !ok {
			return err
		}
		p.emit(textLine(notice))
		p.emit(end)
		return nil
	}
	var res struct {
		TokensBefore int `json:"tokensBefore"`
		TokensAfter  int `json:"tokensAfter"`
	}
	_ = json.Unmarshal(f.Data, &res)
	// The context the next turn starts from: get_state's contextUsage
	// counts system prompt + tools + what survived, which tokensAfter
	// does not always (omp leaves it out when it compacts remotely).
	after := res.TokensAfter
	if n := stateContextTokens(ctx, c); n > 0 {
		after = n
	}
	p.emit(event.CompactionLine("manual", res.TokensBefore, after))
	p.emit(end)
	return nil
}

// compactNoop reads omp's refusals to compact a session there is nothing
// to compact in ("Already compacted" right after a compact, "Nothing to
// compact (session too small)") into the neutral notice for the turn.
func compactNoop(msg string) (string, bool) {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "already compacted"):
		return "Nothing to compact — already compacted, no new history since the last compact.\n", true
	case strings.Contains(m, "nothing to compact"):
		return "Nothing to compact — the session is too small to compact yet.\n", true
	}
	return "", false
}

// stateContextTokens is get_state's contextUsage.tokens (0 = unknown).
func stateContextTokens(ctx context.Context, c *rpcConn) int {
	st, err := c.call(ctx, map[string]any{"type": "get_state"})
	if err != nil || !st.Success {
		return 0
	}
	var state struct {
		ContextUsage *struct {
			Tokens int `json:"tokens"`
		} `json:"contextUsage"`
	}
	if json.Unmarshal(st.Data, &state) != nil || state.ContextUsage == nil {
		return 0
	}
	return state.ContextUsage.Tokens
}

// textLine is assistant text wick writes into the turn itself.
func textLine(text string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "message_update",
		"assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": text}})
	return append(b, '\n')
}
