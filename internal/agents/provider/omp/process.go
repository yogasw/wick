package omp

import (
	"io"
	"os/exec"
)

// process implements provider.Process. The prompt is written to the
// child's stdin once at spawn and the pipe closed (both CLIs read a piped
// stdin as the prompt), so Stdin() hands the agent a no-op writer.
type process struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	env    []string // wick-injected env (masked), for the spawn log
	// scopeUnit is the systemd scope this agent runs inside, empty when
	// unwrapped. Read on the exit path to tell an OOM kill from any other
	// SIGKILL.
	scopeUnit string
	// realBin / realArgv are omp's own binary and arguments even when
	// cmd holds the scope wrapper. The spawn log exists so an operator can
	// reproduce a spawn by hand; reporting the wrapper would send them
	// after the wrong command.
	realBin  string
	realArgv []string
	// onExit runs once after Wait returns (MCP token revocation).
	onExit func()
}

// ScopeUnit implements provider.ScopedProcess. Empty when this spawn was
// not wrapped in a memory-limited scope.
func (p *process) ScopeUnit() string { return p.scopeUnit }

func (p *process) Stdout() io.Reader     { return p.stdout }
func (p *process) Stdin() io.WriteCloser { return noopWriteCloser{} }
func (p *process) Env() []string         { return p.env }
func (p *process) Wait() error {
	err := p.cmd.Wait()
	if p.onExit != nil {
		p.onExit()
		p.onExit = nil
	}
	return err
}
func (p *process) Pid() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}
func (p *process) Binary() string {
	// The real binary, not the scope wrapper — see process.realBin.
	if p.realBin != "" {
		return p.realBin
	}
	if p.cmd == nil {
		return ""
	}
	return p.cmd.Path
}
func (p *process) Argv() []string {
	// The real arguments, not the scope wrapper's — see process.realArgv.
	if p.realArgv != nil {
		out := make([]string, len(p.realArgv))
		copy(out, p.realArgv)
		return out
	}
	if p.cmd == nil || len(p.cmd.Args) <= 1 {
		return nil
	}
	out := make([]string, len(p.cmd.Args)-1)
	copy(out, p.cmd.Args[1:])
	return out
}
func (p *process) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// noopWriteCloser discards writes; the prompt already went in at spawn.
type noopWriteCloser struct{}

func (noopWriteCloser) Write(b []byte) (int, error) { return len(b), nil }
func (noopWriteCloser) Close() error                { return nil }
