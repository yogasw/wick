package wick

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
)

// wickProcess is the in-process implementation of provider.Process. It
// fakes a subprocess around the in-process engine: an io.Pipe carries
// the engine's claude-shaped stream-json to the reader (Agent.run), and
// a channel carries user messages written to Stdin into the engine.
//
// The zero-value Pid (0) is deliberate — the factory's spawn-log start
// hook already tolerates PID 0 for providers whose process doesn't
// exist at Start() time.
type wickProcess struct {
	r   *io.PipeReader
	w   *io.PipeWriter
	env []string // wick-injected env (masked), for the spawn log

	// msgs carries user-message text extracted from Stdin writes (and
	// job-completion injects) into the engine's turn loop. Closed by Kill /
	// stdin EOF. msgsMu + msgsClosed serialise every send against the close
	// so a late inject/Write can never hit a send-on-closed race — senders
	// hold RLock and check msgsClosed; closeMsgs holds the write lock. (A
	// plain sync.Once close still races concurrent sends under -race.)
	msgs       chan string
	msgsMu     sync.RWMutex
	msgsClosed bool

	// ctx/cancel bound the engine goroutine; Kill cancels.
	ctx    context.Context
	cancel context.CancelFunc

	// engineDone closes when the engine goroutine returns, so Wait can
	// close the pipe writer (EOF for the reader) exactly once.
	engineDone chan struct{}

	closeOnce sync.Once
	label     string

	// bg holds this spawn's background shells. Reaped on Kill so a
	// detached `npm i` / dev server can't outlive the session. Set by
	// runEngine before the turn loop starts.
	bg *bgRegistry
	// jobs holds this spawn's async jobs. Cancelled on Kill so a job's
	// runner (and its process tree) doesn't outlive the session. Set in
	// Spawn so Kill can read it without racing the engine goroutine.
	jobs *jobManager
}

// injectTurn pushes a synthetic user turn into the engine's msgs channel.
// Used by the job manager's completion seam: when a job finishes it wakes
// the session with a [job-done …] turn. Safe after teardown — sendMsg
// drops it when the channel is closed.
func (p *wickProcess) injectTurn(text string) { p.sendMsg(text) }

// sendMsg delivers text to the engine's turn loop, serialised against
// closeMsgs so a concurrent close can never turn this into a
// send-on-closed panic/race. Returns without blocking forever: it bails
// when the channel is closed (session torn down) or ctx is cancelled.
// The RLock is held only across the send attempt — closeMsgs takes the
// write lock, so at most one of {any sender, the close} touches the
// channel's closed state at a time.
func (p *wickProcess) sendMsg(text string) {
	p.msgsMu.RLock()
	defer p.msgsMu.RUnlock()
	if p.msgsClosed {
		return
	}
	select {
	case p.msgs <- text:
	case <-p.ctx.Done():
	}
}

// stdinWriter adapts writes of the claude stream-json user-message
// envelope into plain text pushed onto the engine's msgs channel.
type stdinWriter struct {
	p *wickProcess
}

func (p *wickProcess) Stdout() io.Reader     { return p.r }
func (p *wickProcess) Stdin() io.WriteCloser { return stdinWriter{p: p} }
func (p *wickProcess) Env() []string         { return p.env }
func (p *wickProcess) Pid() int              { return 0 }
func (p *wickProcess) Binary() string        { return p.label }
func (p *wickProcess) Argv() []string        { return nil }

// Write decodes the stream-json user envelope and forwards the text.
// The default MessageEncoder (agent.go) sends
// `{"type":"user","message":{"role":"user","content":"..."}}\n`.
// Non-JSON or unexpected payloads are forwarded verbatim so a raw text
// send still reaches the engine.
func (w stdinWriter) Write(b []byte) (int, error) {
	text := extractUserText(b)
	if text != "" {
		w.p.sendMsg(text)
	}
	return len(b), nil
}

func (w stdinWriter) Close() error {
	w.p.closeMsgs()
	return nil
}

// Wait blocks until the engine goroutine finishes, then closes the pipe
// writer so the reader sees EOF (mirrors a subprocess exiting and its
// stdout closing). Idempotent.
func (p *wickProcess) Wait() error {
	<-p.engineDone
	p.closePipe()
	return nil
}

// Kill cancels the engine ctx and closes the pipe. The engine goroutine
// observes ctx.Done() and returns, unblocking Wait. Any background shells
// this spawn started are reaped so detached commands don't linger.
func (p *wickProcess) Kill() error {
	p.cancel()
	if p.jobs != nil {
		p.jobs.cancelAll()
	}
	if p.bg != nil {
		p.bg.killAll()
	}
	p.closeMsgs()
	p.closePipe()
	return nil
}

func (p *wickProcess) closePipe() {
	p.closeOnce.Do(func() { _ = p.w.Close() })
}

// closeMsgs closes the msgs channel exactly once, under the write lock so
// it's serialised against every in-flight sendMsg (no send-on-closed).
// Idempotent — multiple Stdin.Close / Kill calls are expected. Callers
// that also cancel ctx (Kill) should do so BEFORE this: a sender blocked
// on a full buffer escapes via <-ctx.Done() and releases its RLock, so
// this write lock isn't held off.
func (p *wickProcess) closeMsgs() {
	p.msgsMu.Lock()
	defer p.msgsMu.Unlock()
	if p.msgsClosed {
		return
	}
	p.msgsClosed = true
	close(p.msgs)
}

// extractUserText pulls the user text out of a stream-json user
// envelope. Falls back to the trimmed raw string for non-envelope input.
func extractUserText(b []byte) string {
	var env struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(b, &env); err != nil || env.Type != "user" {
		return strings.TrimSpace(string(b))
	}
	// content is usually a string; tolerate the array-of-blocks shape.
	var s string
	if err := json.Unmarshal(env.Message.Content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(env.Message.Content, &blocks); err == nil {
		var sb strings.Builder
		for _, bl := range blocks {
			if bl.Type == "text" {
				sb.WriteString(bl.Text)
			}
		}
		return sb.String()
	}
	log.Debug().Msg("wick.stdin: could not extract user text from envelope")
	return ""
}
