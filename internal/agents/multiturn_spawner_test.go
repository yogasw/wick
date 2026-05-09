package agents_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/yogasw/wick/internal/agents/provider"
)

// multiTurnSpawner is the stdin-driven fake claude used by the
// concurrent / multi-turn integration tests. Unlike scriptedSpawner
// (one canned dump per spawn) this one mimics real claude's long-
// lived behavior: each user envelope written to stdin triggers one
// "turn" of canned output events, then the process waits for the
// next stdin line.
//
// Turns are keyed per session via the workspace path the agent passes
// in SpawnOptions. Multiple sessions can share one spawner safely
// because each call to Spawn gets its own goroutine + per-process
// state — no cross-session interference.
//
// The spawner records every spawn (workspace, ResumeID, env) so tests
// can assert that the second spawn for a session was invoked with the
// captured cli_session_id (resume verification).
type multiTurnSpawner struct {
	mu sync.Mutex

	// turns maps workspace dir → list of turn scripts. Each call to
	// Spawn for that workspace consumes one turn list (allowing tests
	// to define different scripts for first spawn vs resume-spawn).
	turns map[string][][]turnScript

	// spawnLog records every Spawn call in arrival order — used to
	// assert resume IDs.
	spawnLog []spawnRecord

	// processes holds live procs so tests can introspect them.
	processes []*multiTurnProc
}

// turnScript is the events emitted in response to one user envelope.
// Each string is one stream-json line; the spawner adds the trailing
// newline. The first turn script is consumed on the first stdin write,
// the second on the second, and so on. Once all turn scripts are
// consumed, the process closes stdout (subprocess "exits cleanly").
type turnScript []string

// spawnRecord captures one Spawn invocation for assertions.
type spawnRecord struct {
	Workspace string
	ResumeID  string
	ExtraEnv  []string
}

// newMultiTurnSpawner builds an empty spawner. Tests then call
// SetTurns to script per-workspace turn output.
func newMultiTurnSpawner() *multiTurnSpawner {
	return &multiTurnSpawner{turns: map[string][][]turnScript{}}
}

// SetTurns installs the per-spawn turn scripts for one workspace.
// Each outer slice = one Spawn call (so [first-spawn-turns,
// resume-spawn-turns]). Each inner slice = one turn's events.
func (s *multiTurnSpawner) SetTurns(workspace string, perSpawn ...[]turnScript) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns[workspace] = perSpawn
}

// Spawn implements agent.Spawner.
func (s *multiTurnSpawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	s.mu.Lock()
	scripts := s.turns[opt.Workspace]
	if len(scripts) == 0 {
		s.mu.Unlock()
		return nil, fmt.Errorf("multiTurnSpawner: no script for workspace %q", opt.Workspace)
	}
	turns := scripts[0]
	s.turns[opt.Workspace] = scripts[1:]
	s.spawnLog = append(s.spawnLog, spawnRecord{
		Workspace: opt.Workspace,
		ResumeID:  opt.ResumeID,
		ExtraEnv:  append([]string(nil), opt.ExtraEnv...),
	})

	stdoutR, stdoutW := io.Pipe()
	stdinR, stdinW := io.Pipe()
	proc := &multiTurnProc{
		stdoutR:  stdoutR,
		stdoutW:  stdoutW,
		stdinR:   stdinR,
		stdinW:   stdinW,
		stdinBuf: &bytes.Buffer{},
		opt:      opt,
		done:     make(chan struct{}),
		turns:    turns,
	}
	s.processes = append(s.processes, proc)
	s.mu.Unlock()

	go proc.run()
	return proc, nil
}

// SpawnLog returns a copy of every Spawn invocation seen.
func (s *multiTurnSpawner) SpawnLog() []spawnRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]spawnRecord, len(s.spawnLog))
	copy(out, s.spawnLog)
	return out
}

// SpawnsFor returns spawn records filtered by workspace.
func (s *multiTurnSpawner) SpawnsFor(workspace string) []spawnRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []spawnRecord
	for _, r := range s.spawnLog {
		if r.Workspace == workspace {
			out = append(out, r)
		}
	}
	return out
}

// multiTurnProc is one live "subprocess" — drains stdin line-by-line
// and emits one turnScript per line. After all turns are consumed,
// stdout closes and Wait unblocks, mirroring real claude exiting on
// EOF.
type multiTurnProc struct {
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter

	stdinMu  sync.Mutex
	stdinBuf *bytes.Buffer
	opt      provider.SpawnOptions
	done     chan struct{}
	turns    []turnScript
	once     sync.Once
}

// run is the per-process goroutine: read one stdin line → emit one
// turn → repeat. Closes stdout when scripts are exhausted or stdin is
// closed by the agent.
func (p *multiTurnProc) run() {
	defer p.cleanup()
	scanner := bufio.NewScanner(p.stdinR)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	turnIdx := 0
	for scanner.Scan() {
		// Mirror the input into the recorded buffer so tests can
		// assert the user envelope wire format.
		line := scanner.Bytes()
		p.stdinMu.Lock()
		p.stdinBuf.Write(line)
		p.stdinBuf.WriteByte('\n')
		p.stdinMu.Unlock()

		if turnIdx >= len(p.turns) {
			// Out of canned output — agent over-sent. Real claude
			// would just keep waiting; we close to signal "no more".
			break
		}
		for _, evt := range p.turns[turnIdx] {
			if _, err := p.stdoutW.Write([]byte(evt + "\n")); err != nil {
				return
			}
		}
		turnIdx++
	}
}

// cleanup is the single-shot teardown. Multiple callers (Kill, normal
// exit, ctx cancel) converge here.
func (p *multiTurnProc) cleanup() {
	p.once.Do(func() {
		_ = p.stdoutW.Close()
		_ = p.stdoutR.Close()
		_ = p.stdinW.Close()
		_ = p.stdinR.Close()
		close(p.done)
	})
}

func (p *multiTurnProc) Stdout() io.Reader     { return p.stdoutR }
func (p *multiTurnProc) Stdin() io.WriteCloser { return &multiTurnStdin{p: p} }
func (p *multiTurnProc) Wait() error           { <-p.done; return nil }
func (p *multiTurnProc) Kill() error           { p.cleanup(); return nil }

// recordedStdin returns the envelopes the agent wrote so tests can
// assert format / content.
func (p *multiTurnProc) recordedStdin() string {
	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	return p.stdinBuf.String()
}

// multiTurnStdin forwards writes into the pipe so the run goroutine's
// scanner sees them. We don't use the raw pipe writer because we want
// Close to be a no-op (agent.Stop closes stdin; we want stdout to
// keep draining if there are more queued events).
type multiTurnStdin struct{ p *multiTurnProc }

func (s *multiTurnStdin) Write(b []byte) (int, error) { return s.p.stdinW.Write(b) }
func (s *multiTurnStdin) Close() error                { return nil }
