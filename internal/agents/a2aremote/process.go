package a2aremote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
)

// Runtime is what one remote agent session needs: its settings, the auth
// in the clear (held only in memory for the life of the spawn) and the
// host policy.
type Runtime struct {
	Config Config
	Auth   PlainAuth
	Guard  Guard
}

// Spawner is a provider.Spawner with no process: Spawn returns a Process
// whose turns are A2A calls.
type Spawner struct{ Runtime Runtime }

// pollInterval spaces GetTask calls while a non-streaming task works.
var pollInterval = time.Second

// heartbeatEvery keeps the pool's idle timer from reaping a session whose
// remote is silent mid-turn.
var heartbeatEvery = 20 * time.Second

// stateFile holds the session's A2A conversation state in its session dir.
const stateFile = "a2a-remote.json"

// State is a session's A2A conversation: one wick session = one contextId.
// TaskID is kept while the remote waits for input, so the next message
// continues that task.
type State struct {
	ContextID     string    `json:"context_id,omitempty"`
	TaskID        string    `json:"task_id,omitempty"`
	InputRequired bool      `json:"input_required,omitempty"`
	LastState     string    `json:"last_state,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// LoadState reads dir's state; zero when there is none.
func LoadState(dir string) State {
	var st State
	if dir == "" {
		return st
	}
	if b, err := os.ReadFile(filepath.Join(dir, stateFile)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func saveState(dir string, st State) {
	if dir == "" {
		return
	}
	st.UpdatedAt = time.Now().UTC()
	b, _ := json.Marshal(st)
	if err := os.WriteFile(filepath.Join(dir, stateFile), b, 0o600); err != nil {
		log.Warn().Err(err).Msg("a2aremote: save session state")
	}
}

// Spawn starts the turn loop.
func (s Spawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	pr, pw := io.Pipe()
	runCtx, cancel := context.WithCancel(ctx)
	p := &process{
		r: pr, w: pw, msgs: make(chan string, 16), ctx: runCtx, cancel: cancel,
		done: make(chan struct{}), rt: s.Runtime, dir: opt.SessionDir,
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
	rt     Runtime
	dir    string
}

func (p *process) Stdout() io.Reader     { return p.r }
func (p *process) Stdin() io.WriteCloser { return stdin{p} }
func (p *process) Pid() int              { return 0 }
func (p *process) Binary() string        { return "a2a-remote (" + hostOf(p.rt.Config.Card.Endpoint) + ")" }
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
	if t := userText(b); t != "" {
		s.p.send(t)
	}
	return len(b), nil
}

func (s stdin) Close() error {
	s.p.closeMsgs()
	return nil
}

// userText pulls the text out of a stream-json user envelope; anything
// else is taken as raw text.
func userText(b []byte) string {
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

func (p *process) emitDone(result string) {
	p.emit(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": result})
}

func (p *process) emitError(msg string) {
	p.emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "result": msg})
}

func (p *process) loop(opt provider.SpawnOptions) {
	defer close(p.done)
	st := LoadState(p.dir)
	sid := st.ContextID
	if sid == "" {
		sid = uuid.NewString()
	}
	p.emit(map[string]any{"type": "system", "subtype": "init", "session_id": sid})
	if opt.InitialMessage != "" {
		p.turn(&st, opt.InitialMessage)
	}
	for text := range p.msgs {
		p.turn(&st, text)
	}
}

// turn is one A2A call: the result is streamed when the card says it can
// stream, else sent with message/send and polled until it settles.
func (p *process) turn(st *State, text string) {
	cfg := p.rt.Config
	ctx, cancel := context.WithTimeout(p.ctx, cfg.Timeout())
	defer cancel()
	stopBeat := p.heartbeat(ctx)
	defer stopBeat()

	t := &turnState{p: p, st: st, max: cfg.MaxBytes(), cancel: cancel}
	err := p.call(ctx, t, text)
	// The state is saved before the closing line, so whoever reads the
	// turn's end also reads where the conversation stands.
	var end func()
	switch {
	case t.tooBig:
		end = p.finishError(t, fmt.Sprintf("The remote agent's reply passed the %d byte limit and was cut off.", t.max))
	case err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		end = p.finishError(t, fmt.Sprintf("The remote agent did not finish within %s.", cfg.Timeout()))
	case err != nil:
		if p.ctx.Err() != nil {
			return
		}
		end = p.finishError(t, "A2A call failed: "+err.Error())
	default:
		end = p.finish(t)
	}
	saveState(p.dir, *st)
	end()
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

func (p *process) call(ctx context.Context, t *turnState, text string) error {
	card, err := p.rt.Config.ParsedCard()
	if err != nil {
		return err
	}
	client, err := NewClient(ctx, p.rt.Guard, card, p.rt.Auth, t.max)
	if err != nil {
		return err
	}
	defer func() { _ = client.Destroy() }()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
	msg.ContextID = t.st.ContextID
	if t.st.InputRequired && t.st.TaskID != "" {
		msg.TaskID = a2a.TaskID(t.st.TaskID)
	}
	req := &a2a.SendMessageRequest{Message: msg}
	if card.Capabilities.Streaming {
		for ev, err := range client.SendStreamingMessage(ctx, req) {
			if err != nil {
				return err
			}
			t.event(ev)
			if t.tooBig {
				return ErrTooLarge
			}
		}
	} else {
		out, err := client.SendMessage(ctx, req)
		if err != nil {
			return err
		}
		t.event(out)
	}
	// A task that is still working (a non-streaming send that returned
	// early, or a stream that closed mid-task) is polled until it settles.
	for t.taskID != "" && !t.settled() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
		task, err := client.GetTask(ctx, &a2a.GetTaskRequest{ID: a2a.TaskID(t.taskID)})
		if err != nil {
			return err
		}
		t.event(task)
		if t.tooBig {
			return ErrTooLarge
		}
	}
	return nil
}

// turnState follows one turn's events.
type turnState struct {
	p       *process
	st      *State
	max     int64
	cancel  context.CancelFunc
	taskID  string
	state   a2a.TaskState
	message bool
	status  string // last status message text
	out     strings.Builder
	sent    int64
	tooBig  bool
	// artifacts maps an artifact id to the bytes of it already shown, so
	// a polled task that repeats its artifacts only adds what is new.
	artifacts map[a2a.ArtifactID]int
}

func (t *turnState) settled() bool {
	return t.message || t.state.Terminal() || t.state == a2a.TaskStateInputRequired || t.state == a2a.TaskStateAuthRequired
}

// write streams text to the chat, enforcing the size cap.
func (t *turnState) write(s string) {
	if s == "" || t.tooBig {
		return
	}
	if t.sent+int64(len(s)) > t.max {
		t.tooBig = true
		t.cancel()
		return
	}
	t.sent += int64(len(s))
	t.out.WriteString(s)
	t.p.emitText(s)
}

func (t *turnState) context(id string) {
	if id != "" {
		t.st.ContextID = id
	}
}

func (t *turnState) artifact(a *a2a.Artifact, appendTo bool) {
	if a == nil {
		return
	}
	if t.artifacts == nil {
		t.artifacts = map[a2a.ArtifactID]int{}
	}
	text := partsText(a.Parts)
	if appendTo {
		t.artifacts[a.ID] += len(text)
		t.write(text)
		return
	}
	// A full artifact: show only the part not shown yet.
	seen := t.artifacts[a.ID]
	if seen < len(text) {
		t.artifacts[a.ID] = len(text)
		t.write(text[seen:])
	}
}

func (t *turnState) event(ev any) {
	switch v := ev.(type) {
	case *a2a.Message:
		t.context(v.ContextID)
		t.message = true
		t.write(MessageText(v))
	case *a2a.Task:
		t.context(v.ContextID)
		t.taskID = string(v.ID)
		for _, a := range v.Artifacts {
			t.artifact(a, false)
		}
		t.setStatus(v.Status)
	case *a2a.TaskStatusUpdateEvent:
		t.context(v.ContextID)
		t.taskID = string(v.TaskID)
		t.setStatus(v.Status)
	case *a2a.TaskArtifactUpdateEvent:
		t.context(v.ContextID)
		t.taskID = string(v.TaskID)
		t.artifact(v.Artifact, true)
	}
}

func (t *turnState) setStatus(s a2a.TaskStatus) {
	t.state = s.State
	if txt := MessageText(s.Message); txt != "" {
		t.status = txt
	}
}

// finish ends a turn that returned: the question of an input-required
// task is the reply and its task is kept for the next message; a failed
// task is an error; anything else completes with what was streamed, or
// the final status message when nothing was.
func (p *process) finish(t *turnState) func() {
	st := t.st
	st.LastState = string(t.state)
	if t.message {
		st.LastState = "message"
	}
	switch t.state {
	case a2a.TaskStateInputRequired:
		st.TaskID, st.InputRequired = t.taskID, true
		if t.status != "" {
			if t.out.Len() > 0 {
				t.write("\n\n")
			}
			t.write(t.status)
		}
		return func() { p.emitDone(t.out.String()) }
	case a2a.TaskStateFailed, a2a.TaskStateRejected, a2a.TaskStateCanceled, a2a.TaskStateAuthRequired:
		msg := t.status
		if msg == "" {
			msg = "remote task ended as " + strings.ToLower(strings.TrimPrefix(string(t.state), "TASK_STATE_"))
		}
		return p.finishError(t, "Remote agent: "+msg)
	}
	st.TaskID, st.InputRequired = "", false
	if t.out.Len() == 0 && t.status != "" {
		t.write(t.status)
	}
	return func() { p.emitDone(t.out.String()) }
}

func (p *process) finishError(t *turnState, msg string) func() {
	t.st.TaskID, t.st.InputRequired = "", false
	t.st.LastState = string(t.state)
	return func() { p.emitError(msg) }
}
