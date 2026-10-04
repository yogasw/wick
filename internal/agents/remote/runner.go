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

// PullSteps is the pull backoff: the wait before each Fetch while nothing
// new arrives, reset to the first step whenever something does. Pull only
// runs while a turn waits.
var PullSteps = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second}

// pushGrace is how long a push source gets, after the turn's deadline, to
// close with its own terminal event before the runner reports the timeout.
var pushGrace = 2 * time.Second

// now and idleTick are the idle window's clock; tests replace them.
var (
	now      = time.Now
	idleTick = time.Second
)

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
	return fmt.Sprintf("No reply from the remote agent after %s.", max)
}

// TimeoutHinter is a Source that can say why a turn may have gone
// unanswered ("this bot answers only when @mentioned"). "" = no hint.
type TimeoutHinter interface {
	TimeoutHint() string
}

// timeoutMessage is TimeoutMessage plus src's hint, if it has one.
func timeoutMessage(src Source, max time.Duration) string {
	msg := TimeoutMessage(max)
	if h, ok := src.(TimeoutHinter); ok {
		if hint := h.TimeoutHint(); hint != "" {
			msg += " " + hint
		}
	}
	return msg
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
		done: make(chan struct{}), src: s.Source, dir: opt.SessionDir, id: opt.SessionID,
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
	// next is a message that arrived during a grace window (graceAfter),
	// to send next; inputDone: msgs closed meanwhile. Loop-only.
	next      string
	inputDone bool
	id        string
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
// emitReplace swaps the reply streamed so far for text: the remote edited
// a message already passed on.
func (p *process) emitReplace(text string) {
	p.emit(map[string]any{"type": "system", "subtype": "remote_replace", "replace_text": text})
}

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
	next := opt.InitialMessage
	for {
		if next == "" {
			text, ok := <-p.msgs
			if !ok {
				return
			}
			next = text
		}
		cur := next
		p.next, next = "", ""
		p.turn(cur)
		if p.inputDone {
			return
		}
		next = p.next
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

	h, err := src.Send(ctx, Turn{Text: text, SessionDir: p.dir, SessionID: p.id})
	if err != nil {
		switch {
		case p.ctx.Err() != nil:
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			p.emitError(timeoutMessage(src, lim.Max))
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
		pullT = time.NewTimer(pullSteps(lim.Poll)[0])
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
	steps := pullSteps(lim.Poll)
	if lim.Idle > 0 {
		every := lim.Idle / 4
		if every > idleTick {
			every = idleTick
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
					p.emitError(timeoutMessage(src, lim.Max))
				default:
					t.handle(Event{Kind: EventDone})
				}
				return
			}
			if t.handle(ev) {
				if t.ok {
					p.graceAfter(h, push, puller, canPull, t.out.String(), lim)
				}
				return
			}
			step = 0
			resetPull(steps[0])
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
					if t.ok {
						p.graceAfter(h, push, puller, canPull, t.out.String(), lim)
					}
					return
				}
			}
			if len(evs) > 0 {
				step = 0
			} else if step < len(steps)-1 {
				step++
			}
			if wait < steps[step] {
				wait = steps[step]
			}
			resetPull(wait)
		case <-idleC:
			if t.sawText && !t.busy && now().Sub(t.last) >= lim.Idle {
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
			p.emitError(timeoutMessage(src, lim.Max))
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

// OnFollowUp, when set, also hands a follow-up reply to whoever asked the
// session's last turn (another agent over A2A). Set once at startup.
var OnFollowUp func(sessionID, text string)

// followUpQuiet is how long a late change must stay still before it is
// passed on, so a remote streaming by edits gives one follow-up, not ten.
var followUpQuiet = 3 * time.Second

// graceAfter keeps following a finished turn for lim.Grace: a message the
// remote sends — or an edit it makes — after its turn ended is passed on
// as a follow-up reply of its own (NoteFollowUp), the added part or the
// whole edited reply. A new message to send ends the window at once; it
// is kept in p.next for the loop.
func (p *process) graceAfter(h Handle, push <-chan Event, puller Puller, canPull bool, final string, lim Limits) {
	rs, ok := p.src.(Reopener)
	if lim.Grace <= 0 || !ok {
		return
	}
	rs.Reopen(h)
	deadline := time.NewTimer(lim.Grace)
	defer deadline.Stop()
	quiet := time.NewTimer(time.Hour)
	quiet.Stop()
	defer quiet.Stop()
	steps := pullSteps(lim.Poll)
	step := 0
	var pullC <-chan time.Time
	var pt *time.Timer
	if canPull {
		pt = time.NewTimer(steps[0])
		defer pt.Stop()
		pullC = pt.C
	}
	shown, pending := final, ""
	take := func(ev Event) bool {
		if (ev.Kind == EventText || ev.Kind == EventDone) && ev.Text != "" && ev.Text != shown {
			pending = ev.Text
			quiet.Reset(followUpQuiet)
			return true
		}
		return false
	}
	flush := func() {
		if pending == "" || pending == shown {
			return
		}
		text := pending
		if strings.HasPrefix(pending, shown) {
			text = strings.TrimLeft(pending[len(shown):], "\n")
		}
		if OnFollowUp != nil {
			OnFollowUp(p.id, text)
		}
		p.emitText(text)
		p.emitDone(text, NoteFollowUp)
		shown, pending = pending, ""
	}
	for {
		select {
		case text, ok := <-p.msgs:
			flush()
			if !ok {
				p.inputDone = true
			} else {
				p.next = text
			}
			return
		case ev, ok := <-push:
			if !ok {
				push = nil
				continue
			}
			take(ev)
		case <-pullC:
			changed := false
			if evs, nh, err := puller.Fetch(p.ctx, h); err == nil {
				h = nh
				for _, ev := range evs {
					changed = take(ev) || changed
				}
			}
			if changed {
				step = 0
			} else if step < len(steps)-1 {
				step++
			}
			pt.Reset(steps[step])
		case <-quiet.C:
			flush()
		case <-deadline.C:
			flush()
			return
		case <-p.ctx.Done():
			return
		}
	}
}

// pullSteps is PullSteps with no step longer than max (0 = no cap).
func pullSteps(max time.Duration) []time.Duration {
	if max <= 0 {
		return PullSteps
	}
	out := make([]time.Duration, 0, len(PullSteps))
	for _, d := range PullSteps {
		if d > max {
			d = max
		}
		out = append(out, d)
	}
	return out
}

// turnOut writes one turn's events as stream-json lines.
type turnOut struct {
	p       *process
	out     strings.Builder
	sawText bool
	// busy: the remote shows it is still working (a progress note, a
	// working status); the idle window does not run meanwhile.
	busy bool
	// ok: the turn ended with a reply (EventDone), not an error.
	ok   bool
	last time.Time
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
	t.last = now()
	switch ev.Kind {
	case EventTextDelta:
		t.sawText = t.sawText || ev.Text != ""
		t.write(ev.Text)
	case EventText:
		t.sawText = t.sawText || ev.Text != ""
		t.busy = false
		t.replace(ev.Text)
	case EventDraft:
		t.sawText = t.sawText || ev.Text != ""
		t.full = ev.Text
	case EventStatus:
		t.busy = ev.Status == StatusWorking || ev.Status == StatusThinking
		if ev.Status != StatusIdle {
			t.p.emitStatus(ev.Status, ev.Detail)
		}
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
		if ev.Text != "" {
			t.replace(ev.Text)
		} else if t.full != "" {
			t.grow(t.full)
		}
		final := t.out.String()
		if t.full != "" {
			final = t.full
		}
		t.p.emitDone(final, ev.Note)
		t.ok = true
		return true
	case EventError:
		t.p.emitError(ev.Text)
		return true
	}
	return false
}

// replace shows whole as the reply: the new part appended, or — when an
// edit changed what was already shown — the whole reply swapped in.
func (t *turnOut) replace(whole string) {
	shown := t.out.String()
	if strings.HasPrefix(whole, shown) {
		t.write(whole[len(shown):])
		t.full = ""
		return
	}
	t.out.Reset()
	t.out.WriteString(whole)
	t.full = ""
	t.p.emitReplace(whole)
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
