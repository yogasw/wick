//go:build linux

package terminal

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

func TestManagerStartCloseKillsTree(t *testing.T) {
	exe, _ := os.Executable()
	childFile := filepath.Join(t.TempDir(), "child")
	m := NewManager()
	var released bool
	m.Wrap = func(bin string, args []string) (string, []string, func()) {
		return bin, args, func() { released = true }
	}
	s, err := m.Start(StartRequest{
		Instance: provider.Instance{Type: provider.TypeOpencode, Name: "scratch"},
		Command:  Command{Key: "auth-list", Label: "opencode auth list", Args: []string{"auth", "list"}},
		GottyBin: exe,
		CmdBin:   "/bin/true",
		BasePath: "/api/providers/opencode/scratch/terminal/",
		User:     "test@example.com",
		Env:      append(os.Environ(), "FAKE_GOTTY=1", "FAKE_GOTTY_CHILD="+childFile),
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Get(s.ID) != s || s.BasePath != "/api/providers/opencode/scratch/terminal/"+s.ID+"/" {
		t.Fatalf("session not registered / base path %q", s.BasePath)
	}
	var child int
	for i := 0; i < 50 && child == 0; i++ {
		b, _ := os.ReadFile(childFile)
		child, _ = strconv.Atoi(string(b))
		time.Sleep(50 * time.Millisecond)
	}
	if child == 0 || !alive(child) {
		t.Fatal("fake gotty child never started")
	}
	pid := s.Pid()
	s.Close("test")
	<-s.Done()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (alive(pid) || alive(child)) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) || alive(child) {
		t.Fatalf("processes left: gotty %v child %v", alive(pid), alive(child))
	}
	if !released || m.Count() != 0 {
		t.Fatalf("scope released=%v open=%d", released, m.Count())
	}
}

func TestManagerIdleAndShutdown(t *testing.T) {
	exe, _ := os.Executable()
	m := NewManager()
	m.IdleTimeout = 300 * time.Millisecond
	start := func() *Session {
		s, err := m.Start(StartRequest{
			Instance: provider.Instance{Type: provider.TypeOMP, Name: "scratch"},
			Command:  Command{Key: "usage"},
			GottyBin: exe, CmdBin: "/bin/true", BasePath: "/t/",
			Env: append(os.Environ(), "FAKE_GOTTY=1", "FAKE_GOTTY_CHILD="+filepath.Join(t.TempDir(), "c")),
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := start()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("idle session was not closed")
	}

	m.IdleTimeout = time.Hour
	a, b := start(), start()
	_ = m.Shutdown(nil)
	for _, x := range []*Session{a, b} {
		select {
		case <-x.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("shutdown left a session open")
		}
	}
}

func TestManagerCap(t *testing.T) {
	m := NewManager()
	for i := 0; i < MaxSessions; i++ {
		m.sessions[strconv.Itoa(i)] = &Session{}
	}
	if _, err := m.Start(StartRequest{}); err != ErrTooMany {
		t.Fatalf("err = %v", err)
	}
}

var _ = syscall.SIGKILL
