package opencode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yogasw/wick/internal/agents/event"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

// remote.go is one turn on a shared opencode server, shaped as a
// provider.Process so the pool and parser see what they see for `opencode
// run`: Stdout() yields run-format JSON lines (translate.go), Wait()
// returns once the session goes idle.
//
// Kill() aborts the opencode session (POST /session/:id/abort) and never
// touches the server, which other sessions are using. Pid() is 0 for the
// same reason: the agent's teardown signals the process GROUP of Pid(),
// and that group is the server.

const (
	abortTimeout = 5 * time.Second
	// killGrace is how long Kill waits for the aborted session to go idle
	// before it cuts the stream itself (the agent waits 5 s for EOF).
	killGrace = 3 * time.Second
)

// apiClient speaks the server's HTTP API for one directory.
type apiClient struct {
	base     string
	password string
	dir      string
	http     *http.Client
}

func (c *apiClient) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case []byte:
			rd = bytes.NewReader(b)
		default:
			j, err := json.Marshal(body)
			if err != nil {
				return err
			}
			rd = bytes.NewReader(j)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), rd)
	if err != nil {
		return err
	}
	req.SetBasicAuth(serverUser, c.password)
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return &httpError{status: resp.StatusCode, method: method, path: path, body: strings.TrimSpace(string(msg))}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (c *apiClient) url(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return c.base + path + sep + "directory=" + url.QueryEscape(c.dir)
}

type httpError struct {
	status       int
	method, path string
	body         string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("opencode server %s %s: %d %s", e.method, e.path, e.status, e.body)
}

func isNotFound(err error) bool {
	var h *httpError
	return errors.As(err, &h) && h.status == http.StatusNotFound
}

// turnSpec is what one turn sends.
type turnSpec struct {
	resumeID string
	title    string
	model    string // provider/model
	system   string
	prompt   string
	mcpName  string // per-session wick MCP server name, "" = none
	mcpURL   string
	mcpToken string
}

// sessionMCPName is the wick MCP server name for one wick session. Tool
// ids come out as <name>_<tool>, so it must stay [a-z0-9_].
func sessionMCPName(sessionID string) string {
	h := sha256.Sum256([]byte(sessionID))
	return mcpServerName + "_" + hex.EncodeToString(h[:])[:12]
}

// promptBody is the prompt_async body. tools is written by hand: opencode
// turns it into permission rules in key order and the LAST match wins, so
// "deny every wick_*" must come before "allow mine" — a Go map would sort
// the keys.
func promptBody(t turnSpec) []byte {
	var b bytes.Buffer
	b.WriteString(`{`)
	if prov, mod, ok := strings.Cut(t.model, "/"); ok {
		m, _ := json.Marshal(map[string]string{"providerID": prov, "modelID": mod})
		b.WriteString(`"model":`)
		b.Write(m)
		b.WriteString(`,`)
	}
	if t.system != "" {
		s, _ := json.Marshal(t.system)
		b.WriteString(`"system":`)
		b.Write(s)
		b.WriteString(`,`)
	}
	// Every session on the server registers its own wick MCP; only this
	// session's may be callable from this session.
	b.WriteString(`"tools":{"` + mcpServerName + `_*":false`)
	if t.mcpName != "" {
		b.WriteString(`,"` + t.mcpName + `_*":true`)
	}
	b.WriteString(`},`)
	parts, _ := json.Marshal([]map[string]string{{"type": "text", "text": t.prompt}})
	b.WriteString(`"parts":`)
	b.Write(parts)
	b.WriteString(`}`)
	return b.Bytes()
}

// remoteProcess implements provider.Process for one server turn.
type remoteProcess struct {
	pr     *io.PipeReader
	pw     *io.PipeWriter
	env    []string
	bin    string
	argv   []string
	dir    string
	done   chan struct{}
	cancel context.CancelFunc

	mu        sync.Mutex
	err       error
	client    *apiClient
	sessionID string
	prompted  bool
	killed    bool
	// killDone closes when the first Kill is through; a second Kill
	// waits on it, so no caller returns while an abort is in flight.
	killDone chan struct{}
	// finished: the session went idle — the turn is over on the server,
	// so a Kill has nothing left to abort.
	finished bool

	// injMu orders every prompt this turn sends: the first one and the
	// ones Inject adds, so they reach the session in the order they were
	// written. spec is the turn's prompt settings, reused for injections;
	// early holds messages injected before the first prompt went out (they
	// ride along in it); injected counts prompts added mid-turn.
	injMu    sync.Mutex
	spec     turnSpec
	early    []string
	injected int

	// orderMu serialises Inject calls while each waits for the server to
	// take in the previous prompt: prompt_async answers before the
	// message is stored, and prompts sent back to back are stored in
	// whatever order the server gets to them — a fresh server stored
	// "bravo", "charlie", then the first prompt "alpha", and the model
	// answered only the last one. sent counts prompts sent; userSeen the
	// user messages the event stream has shown.
	orderMu  sync.Mutex
	sent     int
	userSeen int
}

// injectOrderWait bounds how long an injection waits for the previous
// prompt to be taken in (a fresh server loads its catalog first, ~8 s
// on the 2 vCPU host); past it the prompt is sent anyway.
const injectOrderWait = 30 * time.Second

func newRemoteProcess(env []string, bin string, argv []string, dir string) *remoteProcess {
	pr, pw := io.Pipe()
	return &remoteProcess{pr: pr, pw: pw, env: env, bin: bin, argv: argv, dir: dir, done: make(chan struct{})}
}

func (p *remoteProcess) Stdout() io.Reader     { return p.pr }
func (p *remoteProcess) Stdin() io.WriteCloser { return noopWriteCloser{} }
func (p *remoteProcess) Env() []string         { return p.env }
func (p *remoteProcess) Pid() int              { return 0 }
func (p *remoteProcess) Binary() string        { return p.bin }
func (p *remoteProcess) Argv() []string        { return append([]string(nil), p.argv...) }

// TurnEnded reports that this turn is over on the server side: done is
// closed once the stream has finished, whatever the reader made of it.
func (p *remoteProcess) TurnEnded() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *remoteProcess) Wait() error {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Kill aborts the turn: before the prompt went out it just cancels; after,
// it asks the server to abort the session and waits for the idle that
// follows, cutting the stream after killGrace if none comes.
//
// It returns only once the abort is through. The next turn of this wick
// session prompts the SAME opencode session, and an abort still in flight
// when that prompt lands aborts the new turn instead — which then looks
// like a turn that failed on its own and drains the queue again. For the
// same reason a turn that already went idle is not aborted at all.
//
// Whoever kills a turn has stopped reading it, so the pipe is closed
// first: a frame the turn writes after that fails instead of blocking
// the turn (and its lease) forever.
func (p *remoteProcess) Kill() error {
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
	c, sid, prompted, finished := p.client, p.sessionID, p.prompted, p.finished
	p.mu.Unlock()
	_ = p.pr.CloseWithError(errTurnKilled)
	if finished {
		return nil
	}
	if !prompted || c == nil || sid == "" {
		p.cancel()
		return nil
	}
	if err := abortSession(c, sid); err != nil {
		log.Warn().Err(err).Str("session", sid).Msg("agents.opencode: abort failed; ending the turn")
	}
	select {
	case <-p.done:
	case <-time.After(killGrace):
		p.cancel()
	}
	return nil
}

// Busy reports whether the server still lists this turn's session as
// working. The agent's idle timer asks it before aborting a silent turn: a
// long tool or a sub-agent (its own session, whose frames are not this
// turn's) leaves the stream quiet while the work goes on. A turn that is
// over, killed, not yet prompted, or whose server does not answer is not
// busy, so the timer takes its usual course.
func (p *remoteProcess) Busy() bool {
	p.mu.Lock()
	c, sid, ok := p.client, p.sessionID, p.prompted && !p.killed && !p.finished
	p.mu.Unlock()
	return ok && c != nil && sid != "" && stillBusy(c, sid)
}

// errTurnOver is Inject's answer once the turn cannot take a message.
var errTurnOver = errors.New("opencode turn is over")

// Inject adds text to the turn that is running. opencode takes a prompt
// for a busy session as another user message of the loop already running
// (SessionPrompt.prompt → runner.ensureRunning joins it), so the reply to
// both comes out of this same stream — what claude does with input typed
// mid-turn. Before the first prompt is out the text is folded into it.
func (p *remoteProcess) Inject(text string) error {
	p.injMu.Lock()
	p.mu.Lock()
	if p.killed || p.finished {
		p.mu.Unlock()
		p.injMu.Unlock()
		return errTurnOver
	}
	if !p.prompted {
		p.early = append(p.early, text)
		p.mu.Unlock()
		p.injMu.Unlock()
		return nil
	}
	p.mu.Unlock()
	p.injMu.Unlock()

	// One injection at a time, each after the server has stored the
	// prompt before it (see orderMu). The wait holds no lock the event
	// loop needs, so the stream keeps being read meanwhile.
	p.orderMu.Lock()
	defer p.orderMu.Unlock()
	deadline := time.Now().Add(injectOrderWait)
	for {
		p.mu.Lock()
		over, taken := p.killed || p.finished, p.userSeen >= p.sent
		p.mu.Unlock()
		if over {
			return errTurnOver
		}
		if taken || time.Now().After(deadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	p.injMu.Lock()
	defer p.injMu.Unlock()
	p.mu.Lock()
	if p.killed || p.finished {
		p.mu.Unlock()
		return errTurnOver
	}
	c, sid, t := p.client, p.sessionID, p.spec
	p.mu.Unlock()
	t.prompt = text
	ctx, cancel := context.WithTimeout(context.Background(), abortTimeout)
	defer cancel()
	if err := c.do(ctx, http.MethodPost, "/session/"+sid+"/prompt_async", promptBody(t), nil); err != nil {
		return err
	}
	p.mu.Lock()
	p.injected++
	p.sent++
	p.mu.Unlock()
	return nil
}

// injectSettle is how long an idle that follows an injection waits before
// asking whether the session is really done: the injected prompt is
// processed asynchronously, and it can start its own run just after the
// one it was meant to join went idle.
const injectSettle = 300 * time.Millisecond

// Silence watchdog bounds (vars: shortened in tests).
var (
	silentTurnTimeout = 90 * time.Second
	// compactTimeout bounds one /compact (summarize) call.
	compactTimeout = 10 * time.Minute
	// apiRequestTimeout caps every other request to the server.
	apiRequestTimeout = 60 * time.Second
	silentTurnCheck   = 5 * time.Second
)

// stillBusy reports whether the server lists sid as working. Used only
// after an injection, where an idle frame may belong to the run that
// preceded the injected message's own.
func stillBusy(c *apiClient, sid string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), abortTimeout)
	defer cancel()
	var st map[string]struct {
		Type string `json:"type"`
	}
	if c.do(ctx, http.MethodGet, "/session/status", nil, &st) != nil {
		return false
	}
	s, ok := st[sid]
	return ok && s.Type != "" && s.Type != "idle"
}

// errNoReply ends a turn that went idle with nothing at all: no step,
// text, tool call or error. Reported as the turn's error so it is not
// taken for an empty answer.
var errNoReply = errors.New("opencode ended the turn without a reply (no text, tool call or error came back)")

// errTurnKilled is what a killed turn's Wait reports. It wraps
// context.Canceled because the agent reads that as a stop it asked for,
// not as a crash to recover from.
var errTurnKilled = fmt.Errorf("opencode turn aborted: %w", context.Canceled)

func abortSession(c *apiClient, sid string) error {
	ctx, cancel := context.WithTimeout(context.Background(), abortTimeout)
	defer cancel()
	return c.do(ctx, http.MethodPost, "/session/"+sid+"/abort", nil, nil)
}

func (p *remoteProcess) finish(err error) {
	p.mu.Lock()
	if p.err == nil {
		p.err = err
	}
	if p.killed {
		// Whatever the turn ended with, it ended because it was killed.
		p.err = errTurnKilled
	}
	p.mu.Unlock()
	_ = p.pw.Close()
	close(p.done)
}

func (p *remoteProcess) emit(b []byte) {
	_, _ = p.pw.Write(b)
}

// run drives one turn to the end. It owns the lease.
func (p *remoteProcess) run(ctx context.Context, l *lease, t turnSpec) {
	defer l.release()
	p.mu.Lock()
	p.spec = t
	p.mu.Unlock()
	err := p.turn(ctx, l, t)
	if err != nil && !p.wasKilled() {
		p.mu.Lock()
		sid := p.sessionID
		p.mu.Unlock()
		// The failure is reported IN the turn, as its error frame: the
		// chat shows it and the turn ends like any failed turn. Returning
		// it from Wait as well would make it an unexplained process exit,
		// and the pool would "recover" — restart the agent with a
		// "stopped unexpectedly" notice and a second reply to the same
		// message.
		p.emit(errorLine(sid, err.Error()))
		err = nil
	}
	p.finish(err)
}

func (p *remoteProcess) wasKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

func (p *remoteProcess) turn(ctx context.Context, l *lease, t turnSpec) error {
	if err := l.waitSlot(ctx); err != nil {
		return err
	}
	if p.dir != "" {
		// opencode takes a prompt for a directory that does not exist and
		// then ends the run without a word; say so instead.
		if st, err := os.Stat(p.dir); err != nil || !st.IsDir() {
			return fmt.Errorf("opencode workspace %s is not a directory", p.dir)
		}
	}
	c := p.clientFor(l)
	sid, err := ensureSession(ctx, c, t.resumeID, t.title)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.client, p.sessionID = c, sid
	p.mu.Unlock()
	if t.resumeID != "" && sid != t.resumeID {
		// Never a silent restart: the reply opens by saying the earlier
		// conversation is gone on this instance.
		p.emit(noticeLine(sid, lostSessionNotice))
	}
	// The model's context limit, from this running server (no process):
	// the meter's scale.
	// With it, whether the server compacts by itself (GET /config) — only
	// next to a known window: the panel shows the two together.
	if w, auto := contextState(ctx, c, t.model); w > 0 {
		p.emit(event.ContextStateLine(w, auto))
	}
	// "/compact": opencode's official compaction (POST
	// /session/{id}/summarize — what its TUI's /compact calls). As a plain
	// prompt it would only be text to the model. The turn ends right after.
	if compactPrompt(t.prompt) {
		return p.compactTurn(ctx, c, sid, t.model)
	}

	if t.mcpName != "" {
		cfg := map[string]any{"name": t.mcpName, "config": map[string]any{
			"type": "remote", "url": t.mcpURL, "enabled": true,
			"headers": map[string]string{"Authorization": "Bearer " + t.mcpToken},
		}}
		if err := c.do(ctx, http.MethodPost, "/mcp", cfg, nil); err != nil {
			// The turn still runs, just without wick's tools — same as a
			// run spawn whose MCP failed to connect.
			log.Warn().Err(err).Str("session", sid).Msg("agents.opencode: register wick MCP failed")
		}
		defer func() {
			dctx, cancel := context.WithTimeout(context.Background(), abortTimeout)
			defer cancel()
			_ = c.do(dctx, http.MethodPost, "/mcp/"+t.mcpName+"/disconnect", nil, nil)
		}()
	}

	// Subscribe before prompting, or the first frames are lost.
	resp, err := subscribe(ctx, c)
	if err != nil {
		return err
	}
	// The stream can be replaced (reconnect below): the watchdog and the
	// deferred close act on whichever body is current.
	var bodyMu sync.Mutex
	body := resp.Body
	closeBody := func() {
		bodyMu.Lock()
		_ = body.Close()
		bodyMu.Unlock()
	}
	defer closeBody()

	p.injMu.Lock()
	p.mu.Lock()
	if len(p.early) > 0 {
		t.prompt = strings.Join(append([]string{t.prompt}, p.early...), "\n\n")
		p.early = nil
	}
	p.mu.Unlock()
	if err := c.do(ctx, http.MethodPost, "/session/"+sid+"/prompt_async", promptBody(t), nil); err != nil {
		p.injMu.Unlock()
		return err
	}
	p.mu.Lock()
	p.prompted = true
	p.sent = 1
	killed := p.killed
	p.mu.Unlock()
	p.injMu.Unlock()
	if killed {
		// Kill landed between session and prompt: it only cancelled.
		_ = abortSession(c, sid)
	}

	tr := newTranslator(sid)
	finished := false
	// Silence watchdog: a model that streams NOTHING (no event of any kind
	// for silentTurnTimeout — seen with opencode/kimi-k3) must not leave the
	// user on a spinner. Any event is activity, so long reasoning that
	// streams deltas is never cut.
	var lastAct atomic.Int64
	lastAct.Store(time.Now().UnixNano())
	var silent atomic.Bool
	// reconnecting: the stream dropped and wick is waiting to resubscribe
	// to a session the server still runs. That pause is not the model's
	// silence, so the watchdog leaves it alone.
	var reconnecting atomic.Bool
	stopWatch, watchDone := make(chan struct{}), make(chan struct{})
	// The turn returns only once the watchdog is gone: an abort it already
	// started must land before the next turn prompts this session, or it
	// aborts that one instead.
	defer func() { close(stopWatch); <-watchDone }()
	go func() {
		defer close(watchDone)
		t := time.NewTicker(silentTurnCheck)
		defer t.Stop()
		for {
			select {
			case <-stopWatch:
				return
			case <-t.C:
				if reconnecting.Load() {
					lastAct.Store(time.Now().UnixNano())
					continue
				}
				if time.Since(time.Unix(0, lastAct.Load())) > silentTurnTimeout {
					silent.Store(true)
					_ = abortSession(c, sid)
					closeBody()
					return
				}
			}
		}
	}()
	onEvent := func(ev sseEvent) bool {
		lastAct.Store(time.Now().UnixNano())
		lines, done := tr.feed(ev)
		p.mu.Lock()
		p.userSeen = tr.userCount()
		p.mu.Unlock()
		for _, ln := range lines {
			p.emit(ln)
		}
		if done && p.moreInjected(c, sid) {
			done = false
		}
		finished = done
		return !done
	}
	cur := resp.Body
	for {
		err = readSSE(cur, onEvent)
		if finished || silent.Load() || ctx.Err() != nil || p.wasKilled() {
			break
		}
		// The stream ended but the turn did not. A dropped connection is
		// not the end of the work: the server may still be running it.
		reconnecting.Store(true)
		next, done, rerr := p.reconnect(ctx, c, sid, tr)
		reconnecting.Store(false)
		lastAct.Store(time.Now().UnixNano())
		if rerr != nil || done || next == nil {
			if rerr != nil {
				err = rerr
			}
			finished = done
			break
		}
		bodyMu.Lock()
		_ = body.Close()
		body = next.Body
		bodyMu.Unlock()
		cur = next.Body
	}
	// silent first: a turn the watchdog aborted is not a clean one, even
	// when its last frame raced the abort in.
	if silent.Load() {
		return fmt.Errorf("opencode: model %s sent nothing for %s (no reply, no error from the server); the turn was stopped — try another model", t.model, silentTurnTimeout)
	}
	if finished {
		if !tr.replied {
			return errNoReply
		}
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		err = errStreamClosed
	}
	return err
}

// errStreamClosed ends a turn whose event stream dropped and whose session
// the server no longer runs, without a finished answer to show for it.
var errStreamClosed = errors.New("opencode server closed the event stream mid-turn")

// Reconnect bounds for a dropped event stream (vars: shortened in tests).
// The wait doubles each attempt: 1s, 2s, 4s… so a server that is briefly
// unreachable is waited out, and one that is gone is given up on.
var (
	reconnectBackoff  = time.Second
	reconnectAttempts = 10
)

// subscribe opens GET /event.
func subscribe(ctx context.Context, c *apiClient) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/event"), nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(serverUser, c.password)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode server event stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("opencode server event stream: %d", resp.StatusCode)
	}
	return resp, nil
}

// reconnect picks a turn back up after its event stream ended before the
// turn did. While the server still runs the session, it resubscribes and
// replays what the gap hid (resync); once the session is no longer running,
// the stored parts say how the turn ended. It returns the new stream, or
// done=true when the turn finished in the gap, or the error that ends it.
func (p *remoteProcess) reconnect(ctx context.Context, c *apiClient, sid string, tr *translator) (*http.Response, bool, error) {
	ended := func() (bool, error) {
		p.resync(ctx, c, sid, tr)
		if !tr.stopped {
			return false, errStreamClosed
		}
		if p.moreInjected(c, sid) {
			return false, nil // an injected prompt keeps it running
		}
		return true, nil
	}
	wait := reconnectBackoff
	for attempt := 0; attempt < reconnectAttempts; attempt++ {
		if !stillBusy(c, sid) {
			if done, err := ended(); done || err != nil {
				return nil, done, err
			}
		}
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
		if p.wasKilled() {
			return nil, false, nil
		}
		resp, err := subscribe(ctx, c)
		if err != nil {
			log.Warn().Err(err).Str("session", sid).Int("attempt", attempt+1).Msg("agents.opencode: event stream reconnect failed")
			continue
		}
		// Subscribed first, then caught up: a part stored between the two
		// arrives on both paths and is passed on once (translator.emitted).
		p.resync(ctx, c, sid, tr)
		if !stillBusy(c, sid) {
			// It ended while we were away; the idle frame is not coming.
			resp.Body.Close()
			if done, err := ended(); done || err != nil {
				return nil, done, err
			}
			continue
		}
		log.Info().Str("session", sid).Int("attempt", attempt+1).Msg("agents.opencode: event stream reconnected mid-turn")
		return resp, false, nil
	}
	return nil, false, fmt.Errorf("opencode server event stream dropped mid-turn and did not come back after %d reconnect attempts", reconnectAttempts)
}

// resync passes on the parts of this turn the server stored while wick was
// not listening: GET /session/{sid}/message, from the turn's first prompt
// on, through the translator, which drops what was already passed on and
// what is not finished yet.
func (p *remoteProcess) resync(ctx context.Context, c *apiClient, sid string, tr *translator) {
	var msgs []struct {
		Info struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"info"`
		Parts []json.RawMessage `json:"parts"`
	}
	if err := c.do(ctx, http.MethodGet, "/session/"+sid+"/message", nil, &msgs); err != nil {
		log.Warn().Err(err).Str("session", sid).Msg("agents.opencode: resync after a dropped stream failed")
		return
	}
	// The turn starts at its prompt: the first user message the stream
	// showed, else the last one stored. Earlier turns are history.
	start, last := -1, -1
	for i, m := range msgs {
		if m.Info.Role != "user" {
			continue
		}
		last = i
		if start < 0 && tr.userMsgs[m.Info.ID] {
			start = i
		}
	}
	if start < 0 {
		start = last
	}
	if start < 0 {
		return
	}
	for _, m := range msgs[start+1:] {
		if m.Info.Role != "assistant" {
			continue
		}
		for _, part := range m.Parts {
			props, _ := json.Marshal(map[string]json.RawMessage{"part": part})
			lines, _ := tr.feed(sseEvent{Type: "message.part.updated", Properties: props})
			for _, ln := range lines {
				p.emit(ln)
			}
		}
	}
}

// moreInjected is called on the idle that would end the turn. With no
// injection it just marks the turn finished. After one, it holds Inject
// off, lets the injected prompt settle, and keeps reading when the
// session is still working on it.
func (p *remoteProcess) moreInjected(c *apiClient, sid string) bool {
	p.injMu.Lock()
	defer p.injMu.Unlock()
	p.mu.Lock()
	n := p.injected
	p.mu.Unlock()
	if n > 0 {
		time.Sleep(injectSettle)
		if stillBusy(c, sid) {
			return true
		}
	}
	p.mu.Lock()
	p.finished = true
	p.mu.Unlock()
	return false
}

func (p *remoteProcess) clientFor(l *lease) *apiClient {
	return &apiClient{base: l.s.h.url, password: l.s.h.password, dir: p.dir, http: &http.Client{Timeout: apiRequestTimeout}}
}

// lostSessionNotice opens a reply whose resume id this instance's server
// does not have (history not copied here, or deleted).
const lostSessionNotice = "Note: this instance could not find the session's earlier opencode conversation, so this reply starts a new one — it does not remember the earlier turns.\n\n"

// ensureSession resumes resumeID when the server still has it, else
// creates a session. A title is always set: an untitled session costs an
// extra LLM call to name it (SessionPrompt.ensureTitle).
func ensureSession(ctx context.Context, c *apiClient, resumeID, title string) (string, error) {
	if resumeID != "" {
		var s struct {
			ID string `json:"id"`
		}
		err := c.do(ctx, http.MethodGet, "/session/"+resumeID, nil, &s)
		if err == nil && s.ID != "" {
			return s.ID, nil
		}
		if err != nil && !isNotFound(err) {
			return "", err
		}
		log.Info().Str("resume", resumeID).Msg("agents.opencode: resume session not found; starting a new one")
	}
	var s struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/session", map[string]string{"title": title}, &s); err != nil {
		return "", err
	}
	if s.ID == "" {
		return "", errors.New("opencode server created a session without an id")
	}
	return s.ID, nil
}

// compactPrompt reports a bare "/compact" turn.
func compactPrompt(prompt string) bool {
	return strings.EqualFold(strings.TrimSpace(prompt), "/compact")
}

// compactTurn summarizes the session with its own model and ends the turn.
// The effect is read back from the running server (GET
// /session/{id}/message): before = the context of the last assistant
// message, after = what the next turn starts from — the session's fixed
// overhead plus the new summary (see sessionTokens). No new summary →
// opencode compacted nothing, and the notice says so instead of claiming
// it did; so does a summarize opencode refuses for having nothing to do.
func (p *remoteProcess) compactTurn(ctx context.Context, c *apiClient, sid, model string) error {
	prev := sessionContext(ctx, c, sid)
	prov, id, _ := strings.Cut(model, "/")
	body := map[string]any{"providerID": prov, "modelID": id, "auto": false}
	// summarize answers only once the summary is written — minutes for a
	// big session on a slow model — so it gets its own client without the
	// 60s per-request cap, bounded by compactTimeout instead. With the cap
	// opencode finished the compaction while wick reported a timeout.
	sctx, cancel := context.WithTimeout(ctx, compactTimeout)
	defer cancel()
	long := *c
	long.http = &http.Client{}
	err := long.do(sctx, http.MethodPost, "/session/"+sid+"/summarize", body, nil)
	if err != nil && !compactNoop(err) {
		return fmt.Errorf("opencode compact: %w", err)
	}
	cur := sessionContext(ctx, c, sid)
	if err != nil || cur.summaryID == "" || cur.summaryID == prev.summaryID {
		p.emit(noticeLine(sid, "Nothing was compacted — opencode wrote no summary for this session (too little history?).\n"))
	} else {
		p.emit(event.CompactionLine("manual", prev.last, cur.overhead+cur.summary))
	}
	b, _ := json.Marshal(map[string]any{"type": "step_finish", "sessionID": sid, "part": map[string]any{"type": "step-finish", "reason": "stop", "sessionID": sid}})
	p.emit(append(b, '\n'))
	return nil
}

// compactNoop reports a summarize refused because there is nothing to
// compact — not a failure of the turn.
func compactNoop(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "already compacted") || strings.Contains(m, "nothing to compact") ||
		strings.Contains(m, "nothing to summarize")
}

// sessionTokens is what the session's messages say about its context.
type sessionTokens struct {
	// last is the context of the last (non-summary) assistant message.
	last int
	// overhead is the part of every request compaction cannot shrink —
	// system prompt + tool definitions — estimated as the SMALLEST
	// non-zero context of any non-summary assistant message: each
	// request is overhead + at least one user message, so the minimum is
	// the tightest bound the session itself offers (a real session read
	// 41.0k against 41.5–41.7k for the first turn after each summary,
	// which also carried that turn's prompt). Summary messages are left
	// out: the summarizer runs without the agent's tools.
	overhead int
	// summary is the size (output) of the newest summary message, and
	// summaryID its id ("" = none).
	summary   int
	summaryID string
}

// sessionContext reads the session's messages from the running server.
func sessionContext(ctx context.Context, c *apiClient, sid string) sessionTokens {
	var msgs []struct {
		Info struct {
			ID   string `json:"id"`
			Role string `json:"role"`
			// Summary is `true` on a compaction summary (assistant) but an
			// object ({"diffs":[]}) on user messages in opencode 1.18 — a
			// bool here fails the whole decode and hides every summary.
			Summary json.RawMessage `json:"summary"`
			Tokens  struct {
				Input  int `json:"input"`
				Output int `json:"output"`
				Cache  struct {
					Read  int `json:"read"`
					Write int `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		} `json:"info"`
	}
	var st sessionTokens
	if err := c.do(ctx, http.MethodGet, "/session/"+sid+"/message", nil, &msgs); err != nil {
		return st
	}
	for _, m := range msgs {
		if m.Info.Role != "assistant" {
			continue
		}
		t := m.Info.Tokens
		if string(m.Info.Summary) == "true" {
			st.summary, st.summaryID = t.Output, m.Info.ID
			continue
		}
		if n := t.Input + t.Cache.Read + t.Cache.Write; n > 0 {
			st.last = n
			if st.overhead == 0 || n < st.overhead {
				st.overhead = n
			}
		}
	}
	return st
}
