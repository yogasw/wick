package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
)

// Spawner is a provider.Spawner with no process: Spawn returns a Process
// whose turns go to Source.
type Spawner struct{ Source Source }

// heartbeatEvery keeps the pool's idle timer from reaping a session whose
// remote is silent mid-turn.
var heartbeatEvery = 20 * time.Second

// pullSteps is the pull backoff: the wait before each Fetch while nothing
// new arrives, reset to the first step whenever something does. Pull only
// runs while a turn waits.
var pullSteps = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second}

// pushGrace is how long a push source gets, after the turn's deadline, to
// close with its own terminal event before the runner reports the timeout.
var pushGrace = 2 * time.Second

// RetryAfterError asks the runner to wait After before the next Fetch — a
// rate limit's Retry-After.
type RetryAfterError struct {
	After time.Duration
	Err   error
}

func (e *RetryAfterError) Error() string { return e.Err.Error() }
func (e *RetryAfterError) Unwrap() error { return e.Err }

// TimeoutMessage is the error of a turn that ran past max.
func TimeoutMessage(max time.Duration) string {
	return fmt.Sprintf("The remote agent did not finish within %s.", max)
}

// Spawn starts the turn loop.
func (s Spawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	if s.Source == nil {
		return nil, errors.New("remote: no source")
	}
	pr, pw := io.Pipe()
	runCtx, cancel := context.WithCancel(ctx)
	p := &process{
		r: pr, w: pw, msgs: make(chan string, 16), ctx: runCtx, cancel: cancel,
		done: make(chan struct{}), src: s.Source, dir: opt.SessionDir,
	}
	go p.loop(opt)
	return p, nil
}

// process fakes a subprocess around the turn loop the way the built-in
// wick provider does: user messages arrive on Stdin as stream-json
// envelopes, replies leave on Stdout as claude-shaped stream-json lines,
// so the parser, store, SSE, Slack bridge and mention router need not
// know the agent is remote.
type process struct {
	r      *io.PipeReader
	w      *io.PipeWriter
	wmu    sync.Mutex
	msgs   chan string
	mmu    sync.RWMutex
	closed bool
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
	src    Source
	dir    string
}

func (p *process) Stdout() io.Reader     { return p.r }
func (p *process) Stdin() io.WriteCloser { return stdin{p} }
func (p *process) Pid() int              { return 0 }
func (p *process) Binary() string        { return p.src.Label() }
func (p *process) Argv() []string        { return nil }
func (p *process) Env() []string         { return nil }

func (p *process) Wait() error {
	<-p.done
	p.closePipe()
	return nil
}

func (p *process) Kill() error {
	p.cancel()
	p.closeMsgs()
	p.closePipe()
	return nil
}

func (p *process) closePipe() { p.once.Do(func() { _ = p.w.Close() }) }

func (p *process) closeMsgs() {
	p.mmu.Lock()
	defer p.mmu.Unlock()
	if !p.closed {
		p.closed = true
		close(p.msgs)
	}
}

func (p *process) send(text string) {
	p.mmu.RLock()
	defer p.mmu.RUnlock()
	if p.closed {
		return
	}
	select {
	case p.msgs <- text:
	case <-p.ctx.Done():
	}
}

type stdin struct{ p *process }

func (s stdin) Write(b []byte) (int, error) {
	if t := UserText(b); t != "" {
		s.p.send(t)
	}
	return len(b), nil
}

func (s stdin) Close() error {
	s.p.closeMsgs()
	return nil
}

// UserText pulls the text out of a stream-json user envelope; anything
// else is taken as raw text.
func UserText(b []byte) string {
	var env struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(b, &env); err != nil || env.Type != "user" {
		return strings.TrimSpace(string(b))
	}
	var s string
	if json.Unmarshal(env.Message.Content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(env.Message.Content, &blocks)
	var sb strings.Builder
	for _, bl := range blocks {
		if bl.Type == "text" {
			sb.WriteString(bl.Text)
		}
	}
	return sb.String()
}

func (p *process) emit(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_, _ = p.w.Write(append(b, '\n'))
}

func (p *process) emitText(text string) {
	p.emit(map[string]any{"type": "assistant", "message": map[string]any{
		"role": "assistant", "content": []map[string]any{{"type": "text", "text": text}},
	}})
}

func (p *process) emitDone(result, note string) {
	line := map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": result}
	if note != "" {
		line["remote_note"] = note
	}
	p.emit(line)
}

func (p *process) emitError(msg string) {
	p.emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "result": msg})
}

// emitStatus reports what the remote is doing. It is a system line, like
// the heartbeat, so a reader that does not know it skips it.
func (p *process) emitStatus(status, detail string) {
	line := map[string]any{"type": "system", "subtype": "remote_status", "status": status}
	if detail != "" {
		line["detail"] = detail
	}
	p.emit(line)
}

func (p *process) loop(opt provider.SpawnOptions) {
	defer close(p.done)
	sid := ""
	if r, ok := p.src.(Resumer); ok {
		sid = r.ResumeID(p.dir)
	}
	if sid == "" {
		sid = uuid.NewString()
	}
	p.emit(map[string]any{"type": "system", "subtype": "init", "session_id": sid})
	if opt.InitialMessage != "" {
		p.turn(opt.InitialMessage)
	}
	for text := range p.msgs {
		p.turn(text)
	}
}

func (p *process) heartbeat(ctx context.Context) func() {
	stop := make(chan struct{})
	go func() {
		tk := time.NewTicker(heartbeatEvery)
		defer tk.Stop()
		for {
			select {
			case <-tk.C:
				p.emit(map[string]any{"type": "system", "subtype": "heartbeat"})
			case <-stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return func() { close(stop) }
}

func listens(src Source, m ListenMode) bool {
	for _, l := range src.Listen() {
		if l == m {
			return true
		}
	}
	return false
}

// turn sends text and follows the reply until a terminal event, idle or
// the hard limit. Push and pull may run together: push answers at once,
// and pull, backing off while push keeps it informed, covers a push that
// never comes (an event the app cannot see).
func (p *process) turn(text string) {
	src := p.src
	lim := src.Limits()
	ctx, cancel := context.WithTimeout(p.ctx, lim.Max)
	defer cancel()
	stopBeat := p.heartbeat(ctx)
	defer stopBeat()

	h, err := src.Send(ctx, Turn{Text: text, SessionDir: p.dir})
	if err != nil {
		switch {
		case p.ctx.Err() != nil:
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			p.emitError(TimeoutMessage(lim.Max))
		default:
			p.emitError(err.Error())
		}
		return
	}
	defer func() { src.End(h) }()

	var push <-chan Event
	if ps, ok := src.(Pusher); ok && listens(src, ListenPush) {
		ch, err := ps.Receive(ctx, h)
		switch {
		case err == nil:
			push = ch
		case !errors.Is(err, ErrPushUnavailable):
			p.emitError(err.Error())
			return
		}
	}
	puller, canPull := src.(Puller)
	canPull = canPull && listens(src, ListenPull)
	if push == nil && !canPull {
		p.emitError("The remote agent has no way to send its reply back.")
		return
	}

	t := &turnOut{p: p}
	step := 0
	var pullC <-chan time.Time
	var pullT *time.Timer
	if canPull {
		pullT = time.NewTimer(pullSteps[0])
		defer pullT.Stop()
		pullC = pullT.C
	}
	resetPull := func(d time.Duration) {
		if pullT == nil {
			return
		}
		if !pullT.Stop() {
			select {
			case <-pullT.C:
			default:
			}
		}
		pullT.Reset(d)
	}
	var idleC <-chan time.Time
	if lim.Idle > 0 {
		every := lim.Idle / 4
		if every > time.Second {
			every = time.Second
		}
		tk := time.NewTicker(every)
		defer tk.Stop()
		idleC = tk.C
	}

	for {
		select {
		case ev, ok := <-push:
			if !ok {
				push = nil
				if canPull {
					continue
				}
				// A push source that closed without a terminal event.
				switch {
				case p.ctx.Err() != nil:
				case errors.Is(ctx.Err(), context.DeadlineExceeded):
					p.emitError(TimeoutMessage(lim.Max))
				default:
					t.handle(Event{Kind: EventDone})
				}
				return
			}
			if t.handle(ev) {
				return
			}
			step = 0
			resetPull(pullSteps[0])
		case <-pullC:
			evs, nh, err := puller.Fetch(ctx, h)
			wait := time.Duration(0)
			if err != nil {
				if ctx.Err() != nil {
					continue
				}
				var ra *RetryAfterError
				if errors.As(err, &ra) && ra.After > 0 {
					wait = ra.After
				}
				log.Debug().Err(err).Str("remote", src.Kind()).Msg("remote: fetch")
			} else {
				h = nh
			}
			for _, ev := range evs {
				if t.handle(ev) {
					return
				}
			}
			if len(evs) > 0 {
				step = 0
			} else if step < len(pullSteps)-1 {
				step++
			}
			if wait < pullSteps[step] {
				wait = pullSteps[step]
			}
			resetPull(wait)
		case <-idleC:
			if t.sawText && time.Since(t.last) >= lim.Idle {
				t.handle(Event{Kind: EventDone, Note: NoteNoMarker})
				return
			}
		case <-ctx.Done():
			if p.ctx.Err() != nil {
				return
			}
			if push != nil && p.drain(t, push) {
				return
			}
			p.emitError(TimeoutMessage(lim.Max))
			return
		}
	}
}

// drain gives a push source pushGrace to finish on its own after the
// deadline; true when it did.
func (p *process) drain(t *turnOut, push <-chan Event) bool {
	grace := time.NewTimer(pushGrace)
	defer grace.Stop()
	for {
		select {
		case ev, ok := <-push:
			if !ok {
				return false
			}
			if t.handle(ev) {
				return true
			}
		case <-grace.C:
			return false
		case <-p.ctx.Done():
			return true
		}
	}
}

// turnOut writes one turn's events as stream-json lines.
type turnOut struct {
	p       *process
	out     strings.Builder
	sawText bool
	last    time.Time
	// full is the latest whole reply from an EventText that did not grow
	// what was shown (an edit that rewrote it), used as the final result.
	full string
}

func (t *turnOut) write(s string) {
	if s == "" {
		return
	}
	t.out.WriteString(s)
	t.p.emitText(s)
}

// handle writes ev; true when it ended the turn.
func (t *turnOut) handle(ev Event) bool {
	t.last = time.Now()
	switch ev.Kind {
	case EventTextDelta:
		t.sawText = t.sawText || ev.Text != ""
		t.write(ev.Text)
	case EventText:
		t.sawText = t.sawText || ev.Text != ""
		t.grow(ev.Text)
	case EventStatus:
		t.p.emitStatus(ev.Status, ev.Detail)
	case EventAttachment:
		t.sawText = true
		line := "📎 " + ev.Name
		if ev.URL != "" {
			line = "📎 [" + ev.Name + "](" + ev.URL + ")"
		}
		if t.out.Len() > 0 && !strings.HasSuffix(t.out.String(), "\n") {
			line = "\n" + line
		}
		t.write(line + "\n")
	case EventDone:
		if whole := ev.Text; whole != "" || t.full != "" {
			if whole == "" {
				whole = t.full
			}
			t.grow(whole)
		}
		final := t.out.String()
		if t.full != "" {
			final = t.full
		}
		t.p.emitDone(final, ev.Note)
		return true
	case EventError:
		t.p.emitError(ev.Text)
		return true
	}
	return false
}

// grow shows the part of whole not shown yet. A whole that does not start
// with what was shown (a rewrite) is kept for the result only: the chat
// stream is append-only.
func (t *turnOut) grow(whole string) {
	shown := t.out.String()
	if strings.HasPrefix(whole, shown) {
		t.write(whole[len(shown):])
		t.full = ""
		return
	}
	t.full = whole
}
