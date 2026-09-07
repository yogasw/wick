package logintty

import (
	"encoding/base64"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

// fakeProc is a runner backed by pipes so manager tests run without a
// real PTY.
type fakeProc struct {
	outR *io.PipeReader
	outW *io.PipeWriter

	mu      sync.Mutex
	input   []byte
	cols    int
	rows    int
	killed  bool
	waitCh  chan struct{}
	waitErr error
}

func newFakeProc() *fakeProc {
	r, w := io.Pipe()
	return &fakeProc{outR: r, outW: w, waitCh: make(chan struct{})}
}

func (f *fakeProc) Read(p []byte) (int, error) { return f.outR.Read(p) }
func (f *fakeProc) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.input = append(f.input, p...)
	return len(p), nil
}
func (f *fakeProc) Resize(cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cols, f.rows = cols, rows
	return nil
}
func (f *fakeProc) Kill() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.killed {
		f.killed = true
		f.outW.Close()
		close(f.waitCh)
	}
	return nil
}
func (f *fakeProc) Wait() error { <-f.waitCh; return f.waitErr }

func (f *fakeProc) emit(s string) { _, _ = f.outW.Write([]byte(s)) }

// finish simulates the process exiting on its own.
func (f *fakeProc) finish() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.killed {
		f.killed = true
		f.outW.Close()
		close(f.waitCh)
	}
}

func (f *fakeProc) wasKilled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.killed
}

func (f *fakeProc) inputStr() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.input)
}

// testManager returns a manager whose spawn hands out the given procs
// in order, plus the recorded spawn calls.
func testManager(now func() time.Time, procs ...*fakeProc) (*Manager, *[]string) {
	var calls []string
	i := 0
	spawn := func(bin string, args, env []string, cols, rows int) (runner, error) {
		calls = append(calls, bin)
		p := procs[i]
		i++
		return p, nil
	}
	return newManagerWith(spawn, now), &calls
}

func claudeIns() provider.Instance {
	// Point the account probe at a nonexistent config dir so tests never
	// read the developer's real ~/.claude credentials.
	return provider.Instance{
		Type: provider.TypeClaude,
		Name: "main",
		Env:  []string{"CLAUDE_CONFIG_DIR=" + filepath.Join(string(filepath.Separator), "wick-logintty-test-nonexistent")},
	}
}

// collect drains frames until the predicate matches or times out.
func collect(t *testing.T, ch chan Frame, match func(Frame) bool) Frame {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case fr := <-ch:
			if match(fr) {
				return fr
			}
		case <-deadline:
			t.Fatal("frame not received in time")
		}
	}
}

func TestStartReturnsExistingRunningSession(t *testing.T) {
	proc := newFakeProc()
	m, calls := testManager(nil, proc, newFakeProc())
	s1, err := m.Start(claudeIns(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := m.Start(claudeIns(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if s1.ID != s2.ID {
		t.Fatalf("second Start must attach to running session: %s vs %s", s1.ID, s2.ID)
	}
	if len(*calls) != 1 {
		t.Fatalf("spawn called %d times, want 1", len(*calls))
	}
	proc.finish()
}

func TestStartRefusesUnsupportedType(t *testing.T) {
	m, _ := testManager(nil)
	if _, err := m.Start(provider.Instance{Type: provider.TypeWick, Name: "wick"}, ""); err == nil {
		t.Fatal("wick must be refused")
	}
	if _, err := m.Start(provider.Instance{Type: provider.TypeCodex, Name: "x"}, "codex"); err == nil {
		t.Fatal("codex not wired yet, must be refused")
	}
}

func TestOutputBroadcastsLinkAndReplaysToLateSubscriber(t *testing.T) {
	proc := newFakeProc()
	m, _ := testManager(nil, proc)
	s, err := m.Start(claudeIns(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	live := make(chan Frame, 64)
	s.Attach(live)
	defer s.Detach(live)

	proc.emit("open https://claude.ai/oauth/authorize?code=1\r\n")

	out := collect(t, live, func(f Frame) bool { return f.T == "out" })
	if data, _ := base64.StdEncoding.DecodeString(out.Data); len(data) == 0 {
		t.Fatal("out frame must carry pty bytes")
	}
	link := collect(t, live, func(f Frame) bool { return f.T == "link" })
	if link.URL != "https://claude.ai/oauth/authorize?code=1" {
		t.Fatalf("link = %q", link.URL)
	}

	late := make(chan Frame, 64)
	snapshot := s.Attach(late)
	defer s.Detach(late)
	var gotLink, gotOut bool
	for _, f := range snapshot {
		switch f.T {
		case "link":
			gotLink = f.URL == "https://claude.ai/oauth/authorize?code=1"
		case "out":
			gotOut = f.Data != ""
		}
	}
	if !gotLink || !gotOut {
		t.Fatalf("late snapshot missing replay: link=%v out=%v (%v)", gotLink, gotOut, snapshot)
	}
	proc.finish()
}

func TestExpiryKillsProcess(t *testing.T) {
	now, advance := fakeClock(time.Unix(1000, 0))
	proc := newFakeProc()
	m, _ := testManager(now, proc)
	s, err := m.Start(claudeIns(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan Frame, 64)
	s.Attach(ch)
	defer s.Detach(ch)

	advance(DefaultTTL + time.Second)
	s.enforceTTL()

	fr := collect(t, ch, func(f Frame) bool { return f.T == "state" && f.State != StateRunning })
	if fr.State != StateExpired {
		t.Fatalf("state = %q, want expired", fr.State)
	}
	if !proc.wasKilled() {
		t.Fatal("process must be killed on expiry")
	}
}

func TestExtendPushesDeadline(t *testing.T) {
	now, advance := fakeClock(time.Unix(1000, 0))
	proc := newFakeProc()
	m, _ := testManager(now, proc)
	if _, err := m.Start(claudeIns(), "claude"); err != nil {
		t.Fatal(err)
	}
	advance(4 * time.Minute)
	fr, ok := m.Extend(provider.TypeClaude, "main")
	if !ok {
		t.Fatal("extend must succeed")
	}
	if fr.RemainingS != int((6 * time.Minute).Seconds()) {
		t.Fatalf("RemainingS = %d, want 360", fr.RemainingS)
	}
	proc.finish()
}

func TestExitRunsAccountProbe(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"tok","subscriptionType":"max"}}`)
	writeFile(t, filepath.Join(dir, ".claude.json"),
		`{"oauthAccount":{"emailAddress":"dev@abc.com"}}`)

	proc := newFakeProc()
	m, _ := testManager(nil, proc)
	ins := claudeIns()
	ins.Env = []string{"CLAUDE_CONFIG_DIR=" + dir}
	s, err := m.Start(ins, "claude")
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan Frame, 64)
	s.Attach(ch)
	defer s.Detach(ch)

	proc.finish()

	fr := collect(t, ch, func(f Frame) bool { return f.T == "state" && f.State == StateExited })
	if fr.Account == nil || !fr.Account.Connected || fr.Account.Email != "dev@abc.com" {
		t.Fatalf("exit frame account = %+v", fr.Account)
	}
}

func TestInputAndResizeReachProcess(t *testing.T) {
	proc := newFakeProc()
	m, _ := testManager(nil, proc)
	s, err := m.Start(claudeIns(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Input([]byte("1\r")); err != nil {
		t.Fatal(err)
	}
	if err := s.Resize(100, 40); err != nil {
		t.Fatal(err)
	}
	if got := proc.inputStr(); got != "1\r" {
		t.Fatalf("input = %q", got)
	}
	proc.mu.Lock()
	cols := proc.cols
	proc.mu.Unlock()
	if cols != 100 {
		t.Fatalf("cols = %d, want 100", cols)
	}
	proc.finish()
}

func TestNewStartAfterExitReplacesSession(t *testing.T) {
	p1, p2 := newFakeProc(), newFakeProc()
	m, _ := testManager(nil, p1, p2)
	s1, err := m.Start(claudeIns(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	p1.finish()
	// Wait for exit to land.
	ch := make(chan Frame, 64)
	s1.Attach(ch)
	collect(t, ch, func(f Frame) bool { return f.T == "state" && f.State == StateExited })
	s1.Detach(ch)

	s2, err := m.Start(claudeIns(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if s2.ID == s1.ID {
		t.Fatal("exited session must be replaced by a fresh one")
	}
	p2.finish()
}
