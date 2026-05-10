package gate

import (
	"encoding/json"
	"net"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// socketPathFor returns a short socket path inside a temp dir.
// Unix domain sockets have a tight path-length cap (~104 bytes on
// macOS, ~108 on linux) — t.TempDir() can blow past it on CI. Keep
// the leaf short; the test dir itself is the only variable.
func socketPathFor(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Windows accepts longer paths; t.TempDir() is fine.
		return filepath.Join(t.TempDir(), "g.sock")
	}
	return filepath.Join(t.TempDir(), "g.sock")
}

// dialAndSend opens a socket, encodes req, and decodes one response.
// On any error the test fails — these are happy-path helpers.
func dialAndSend(t *testing.T, sockPath string, req ApprovalRequest) ApprovalResponse {
	t.Helper()
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var resp ApprovalResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestListener_TimeoutBlocks(t *testing.T) {
	sockPath := socketPathFor(t)
	l, err := NewListener(ListenerOptions{
		SocketPath: sockPath,
		Timeout:    100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	defer l.Close()

	resp := dialAndSend(t, sockPath, ApprovalRequest{
		ID:        "req-1",
		SessionID: "S1",
		Cmd:       "rm -rf /tmp/x",
	})

	if resp.Decision != DecisionBlock {
		t.Errorf("Decision: got %q, want %q", resp.Decision, DecisionBlock)
	}
	if resp.Reason != "timeout" {
		t.Errorf("Reason: got %q, want timeout", resp.Reason)
	}
	if resp.ID != "req-1" {
		t.Errorf("ID echoed wrong: got %q", resp.ID)
	}
}

func TestListener_ResolveApprove(t *testing.T) {
	sockPath := socketPathFor(t)

	gotRequest := make(chan ApprovalRequest, 1)
	l, err := NewListener(ListenerOptions{
		SocketPath: sockPath,
		Timeout:    2 * time.Second,
		OnRequest:  func(r ApprovalRequest) { gotRequest <- r },
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	defer l.Close()

	respCh := make(chan ApprovalResponse, 1)
	go func() {
		respCh <- dialAndSend(t, sockPath, ApprovalRequest{
			ID:  "req-x",
			Cmd: "git status",
		})
	}()

	// Wait for the listener to register the pending request before
	// resolving — otherwise Resolve races the conn goroutine.
	select {
	case <-gotRequest:
	case <-time.After(2 * time.Second):
		t.Fatal("OnRequest not called within 2s")
	}

	if !l.Resolve("req-x", DecisionApproveOnce, "user clicked approve") {
		t.Fatal("Resolve returned false; pending should be present")
	}

	select {
	case resp := <-respCh:
		if resp.Decision != DecisionApproveOnce {
			t.Errorf("Decision: got %q, want %q", resp.Decision, DecisionApproveOnce)
		}
		if resp.ID != "req-x" {
			t.Errorf("ID: got %q", resp.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not receive response")
	}
}

func TestListener_ResolveUnknownID(t *testing.T) {
	sockPath := socketPathFor(t)
	l, err := NewListener(ListenerOptions{
		SocketPath: sockPath,
		Timeout:    100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	defer l.Close()

	if l.Resolve("does-not-exist", DecisionApproveOnce, "") {
		t.Error("Resolve(unknown-id) should return false")
	}
}

func TestListener_PendingSnapshot(t *testing.T) {
	sockPath := socketPathFor(t)

	registered := make(chan struct{}, 1)
	l, err := NewListener(ListenerOptions{
		SocketPath: sockPath,
		Timeout:    2 * time.Second,
		OnRequest:  func(_ ApprovalRequest) { registered <- struct{}{} },
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	defer l.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		dialAndSend(t, sockPath, ApprovalRequest{ID: "snap-1", Cmd: "ls"})
	}()

	// Wait for the listener to land the request in the pending map.
	select {
	case <-registered:
	case <-time.After(2 * time.Second):
		t.Fatal("OnRequest not called")
	}

	snap := l.PendingSnapshot()
	if len(snap) != 1 || snap[0].ID != "snap-1" {
		t.Errorf("PendingSnapshot: %+v", snap)
	}

	l.Resolve("snap-1", DecisionApproveOnce, "")
	wg.Wait()

	if got := l.PendingSnapshot(); len(got) != 0 {
		t.Errorf("expected empty after Resolve, got: %+v", got)
	}
}

func TestListener_CloseFailsPending(t *testing.T) {
	sockPath := socketPathFor(t)

	registered := make(chan struct{}, 1)
	l, err := NewListener(ListenerOptions{
		SocketPath: sockPath,
		Timeout:    5 * time.Second,
		OnRequest:  func(_ ApprovalRequest) { registered <- struct{}{} },
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}

	respCh := make(chan ApprovalResponse, 1)
	go func() {
		respCh <- dialAndSend(t, sockPath, ApprovalRequest{ID: "close-1", Cmd: "ls"})
	}()

	select {
	case <-registered:
	case <-time.After(2 * time.Second):
		t.Fatal("OnRequest not called")
	}

	_ = l.Close()

	select {
	case resp := <-respCh:
		if resp.Decision != DecisionBlock {
			t.Errorf("Decision after Close: got %q, want %q", resp.Decision, DecisionBlock)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not receive close response")
	}
}

func TestIsApprove(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{DecisionApproveOnce, true},
		{DecisionApproveSession, true},
		{DecisionApproveAlways, true},
		{DecisionBlock, false},
		{"", false},
		{"weird", false},
	}
	for _, c := range cases {
		if got := IsApprove(c.in); got != c.want {
			t.Errorf("IsApprove(%q): got %v, want %v", c.in, got, c.want)
		}
	}
}
