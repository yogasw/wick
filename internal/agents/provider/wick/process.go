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

	// msgs carries user-message text extracted from Stdin writes into
	// the engine's turn loop. Closed by Kill / stdin EOF.
	msgs chan string

	// ctx/cancel bound the engine goroutine; Kill cancels.
	ctx    context.Context
	cancel context.CancelFunc

	// engineDone closes when the engine goroutine returns, so Wait can
	// close the pipe writer (EOF for the reader) exactly once.
	engineDone chan struct{}

	closeOnce     sync.Once
	msgsCloseOnce sync.Once
	label         string
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
		select {
		case w.p.msgs <- text:
		case <-w.p.ctx.Done():
		}
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
// observes ctx.Done() and returns, unblocking Wait.
func (p *wickProcess) Kill() error {
	p.cancel()
	p.closeMsgs()
	p.closePipe()
	return nil
}

func (p *wickProcess) closePipe() {
	p.closeOnce.Do(func() { _ = p.w.Close() })
}

func (p *wickProcess) closeMsgs() {
	// Guarded close: multiple Stdin.Close / Kill calls are possible.
	p.msgsCloseOnce.Do(func() { close(p.msgs) })
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
