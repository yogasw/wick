package logintty

import (
	"errors"
	"os"

	pty "github.com/aymanbagabas/go-pty"
)

// ptyRunner adapts a go-pty pseudo-terminal + attached command to the
// runner interface. go-pty uses ConPTY on Windows and a classic pty
// pair on unix, so the same code path serves both.
type ptyRunner struct {
	p pty.Pty
	c *pty.Cmd
}

func (r *ptyRunner) Read(p []byte) (int, error)  { return r.p.Read(p) }
func (r *ptyRunner) Write(p []byte) (int, error) { return r.p.Write(p) }
func (r *ptyRunner) Resize(cols, rows int) error { return r.p.Resize(cols, rows) }

func (r *ptyRunner) Kill() error {
	if r.c.Process == nil {
		return errors.New("not started")
	}
	return r.c.Process.Kill()
}

func (r *ptyRunner) Wait() error {
	err := r.c.Wait()
	// Unblock the read loop — ConPTY reads don't return EOF until the
	// console handle closes.
	_ = r.p.Close()
	return err
}

// ptySpawn starts bin+args attached to a fresh PTY. env is the full
// environment (os.Environ + instance env).
func ptySpawn(bin string, args, env []string, cols, rows int) (runner, error) {
	p, err := pty.New()
	if err != nil {
		return nil, err
	}
	if err := p.Resize(cols, rows); err != nil {
		_ = p.Close()
		return nil, err
	}
	c := p.Command(bin, args...)
	c.Env = env
	if home, herr := os.UserHomeDir(); herr == nil {
		c.Dir = home
	}
	if err := c.Start(); err != nil {
		_ = p.Close()
		return nil, err
	}
	return &ptyRunner{p: p, c: c}, nil
}
