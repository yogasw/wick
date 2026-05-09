package provider

import (
	"bytes"
	"context"
	"io"
	"sync"
)

// fakeSpawner is the test double for ClaudeSpawner. Each Spawn returns
// a Process whose stdout emits the canned `Lines` and whose stdin
// captures everything sent for assertions.
//
// The fake gives us full control over stream-json output without
// needing a real claude binary, but still exercises the agent's
// real reader/parser/state/store pipeline end to end.
type fakeSpawner struct {
	mu    sync.Mutex
	Lines [][]string // one slice per Spawn call (resume tests need 2)
	Calls int        // how many spawns have happened
	Last  *fakeProcess
}

func (s *fakeSpawner) Spawn(ctx context.Context, opt SpawnOptions) (Process, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.Calls
	s.Calls++

	var lines []string
	if idx < len(s.Lines) {
		lines = s.Lines[idx]
	}
	pr, pw := io.Pipe()
	proc := &fakeProcess{
		stdoutR:  pr,
		stdoutW:  pw,
		stdinBuf: &bytes.Buffer{},
		opt:      opt,
		done:     make(chan struct{}),
	}
	s.Last = proc

	// Emit canned lines then close stdout. Tests that need to assert
	// on cli_session_id / state changes rely on Done arriving in this
	// canned sequence.
	go func() {
		for _, line := range lines {
			if _, err := pw.Write([]byte(line + "\n")); err != nil {
				return
			}
		}
		_ = pw.Close()
	}()

	return proc, nil
}

type fakeProcess struct {
	stdoutR  *io.PipeReader
	stdoutW  *io.PipeWriter
	stdinMu  sync.Mutex
	stdinBuf *bytes.Buffer
	opt      SpawnOptions
	done     chan struct{}
	once     sync.Once
}

func (p *fakeProcess) Stdout() io.Reader     { return p.stdoutR }
func (p *fakeProcess) Stdin() io.WriteCloser { return &fakeStdin{p: p} }

// Wait blocks until Kill or until stdout writer closes. Mimics how
// real exec.Cmd.Wait blocks on subprocess exit.
func (p *fakeProcess) Wait() error {
	<-p.done
	return nil
}

func (p *fakeProcess) Kill() error {
	p.once.Do(func() {
		_ = p.stdoutR.Close()
		_ = p.stdoutW.Close()
		close(p.done)
	})
	return nil
}

// recordedStdin returns whatever the agent has written into stdin so
// far. Snapshot read — buffer keeps growing if more arrives.
func (p *fakeProcess) recordedStdin() ([]byte, error) {
	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	out := make([]byte, p.stdinBuf.Len())
	copy(out, p.stdinBuf.Bytes())
	return out, nil
}

// fakeStdin is a non-blocking WriteCloser. Real exec.Cmd stdin is
// buffered (the kernel pipe), so we mimic that with an in-memory
// buffer rather than io.Pipe (which would block writers without an
// active reader).
type fakeStdin struct{ p *fakeProcess }

func (f *fakeStdin) Write(b []byte) (int, error) {
	f.p.stdinMu.Lock()
	defer f.p.stdinMu.Unlock()
	return f.p.stdinBuf.Write(b)
}

func (f *fakeStdin) Close() error { return nil }

// keepAliveSpawner produces a process whose stdout never emits a line
// and never closes — used by the idle-TTL test to simulate "claude
// hung" so the timer is the only thing that ends the spawn.
type keepAliveSpawner struct {
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter

	mu   sync.Mutex
	last *fakeProcess
}

func (s *keepAliveSpawner) Spawn(ctx context.Context, opt SpawnOptions) (Process, error) {
	proc := &fakeProcess{
		stdoutR:  s.stdoutR,
		stdoutW:  s.stdoutW,
		stdinBuf: &bytes.Buffer{},
		opt:      opt,
		done:     make(chan struct{}),
	}
	s.mu.Lock()
	s.last = proc
	s.mu.Unlock()
	return proc, nil
}

// makePipePair creates a connected pipe — stdout reader the agent
// will block on, stdout writer the test never writes to.
func makePipePair() (*io.PipeReader, *io.PipeWriter) {
	return io.Pipe()
}
