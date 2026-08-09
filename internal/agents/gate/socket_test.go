package gate

import (
	"encoding/json"
	"net"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
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

// ── Wait policy over the real socket ────────────────────────────────

// TestListener_WaitsWhileViewerPresent: with the approval deadline off,
// a prompt must outlive the fixed timeout for as long as a browser is
// watching. This is the wire-level version — the gate binary's own
// connection is what has to stay open.
func TestListener_WaitsWhileViewerPresent(t *testing.T) {
	sockPath := socketPathFor(t)
	l, err := NewListener(ListenerOptions{
		SocketPath: sockPath,
		Timeout:    100 * time.Millisecond, // must not be what decides this
		Policy: func(ApprovalRequest) WaitPolicy {
			return WaitPolicy{
				TimeoutEnabled: false,
				Grace:          100 * time.Millisecond,
				HasViewer:      func() bool { return true },
			}
		},
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	defer l.Close()

	done := make(chan ApprovalResponse, 1)
	go func() {
		done <- dialAndSend(t, sockPath, ApprovalRequest{ID: "wv-1", SessionID: "S1", Cmd: "sleep 999"})
	}()

	select {
	case resp := <-done:
		t.Fatalf("returned early: decision=%q reason=%q", resp.Decision, resp.Reason)
	case <-time.After(600 * time.Millisecond):
	}

	// Still pending, so a decision can still land.
	if !l.Resolve("wv-1", DecisionApproveOnce, "user clicked") {
		t.Fatal("Resolve reported the request was gone while a viewer was watching")
	}
	select {
	case resp := <-done:
		if resp.Decision != DecisionApproveOnce {
			t.Errorf("Decision: got %q, want %q", resp.Decision, DecisionApproveOnce)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gate connection never received the decision")
	}
}

// TestListener_BlocksWhenViewerGone: nobody left to answer means the
// command is blocked — the wait is conditional, not unconditional.
func TestListener_BlocksWhenViewerGone(t *testing.T) {
	sockPath := socketPathFor(t)
	expired := make(chan string, 1)
	l, err := NewListener(ListenerOptions{
		SocketPath: sockPath,
		Timeout:    time.Hour, // proves the block came from viewer loss
		Policy: func(ApprovalRequest) WaitPolicy {
			return WaitPolicy{
				TimeoutEnabled: false,
				Grace:          100 * time.Millisecond,
				HasViewer:      func() bool { return false },
			}
		},
		OnExpired: func(r ApprovalRequest, decision, reason string) {
			select {
			case expired <- r.ID + ":" + decision + ":" + reason:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	defer l.Close()

	resp := dialAndSend(t, sockPath, ApprovalRequest{ID: "wv-2", SessionID: "S1", Cmd: "rm -rf /"})
	if resp.Decision != DecisionBlock {
		t.Errorf("Decision: got %q, want %q", resp.Decision, DecisionBlock)
	}
	if resp.Reason != "no viewer" {
		t.Errorf("Reason: got %q, want %q", resp.Reason, "no viewer")
	}

	// The browser must be told, or a modal opened by another tab hangs
	// around offering buttons that can only return 410.
	select {
	case got := <-expired:
		if got != "wv-2:"+DecisionBlock+":no viewer" {
			t.Errorf("OnExpired payload: got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnExpired never fired")
	}
}

// A request the wait loop gave up on must not still look pending, or a
// late click would be delivered into a channel nobody reads.
func TestListener_ExpiredRequestLeavesPending(t *testing.T) {
	sockPath := socketPathFor(t)
	l, err := NewListener(ListenerOptions{
		SocketPath: sockPath,
		Timeout:    100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	defer l.Close()

	dialAndSend(t, sockPath, ApprovalRequest{ID: "wv-3", SessionID: "S1", Cmd: "ls"})

	if _, ok := l.LookupPending("wv-3"); ok {
		t.Error("expired request is still in the pending set")
	}
	if l.Resolve("wv-3", DecisionApproveOnce, "late click") {
		t.Error("Resolve accepted a decision for an expired request")
	}
}

// A prompt that never had a viewer is not the same as one whose viewer
// left. Nobody was there to miss it, so waiting out the full grace window
// only delays a block that was inevitable — headless callers (Slack, cron,
// the API) pay that delay on every gated command.
func TestListener_NoViewerEverBlocksFast(t *testing.T) {
	sockPath := socketPathFor(t)
	l, err := NewListener(ListenerOptions{
		SocketPath: sockPath,
		Timeout:    time.Hour, // proves the block came from viewer absence
		Policy: func(ApprovalRequest) WaitPolicy {
			return WaitPolicy{
				TimeoutEnabled: false,
				Grace:          10 * time.Second, // long enough that waiting it out would fail this test
				HasViewer:      func() bool { return false },
			}
		},
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	defer l.Close()

	start := time.Now()
	resp := dialAndSend(t, sockPath, ApprovalRequest{ID: "nv-1", SessionID: "S1", Cmd: "ls"})
	elapsed := time.Since(start)

	if resp.Decision != DecisionBlock {
		t.Errorf("Decision: got %q, want %q", resp.Decision, DecisionBlock)
	}
	if resp.Reason != "no viewer" {
		t.Errorf("Reason: got %q, want %q", resp.Reason, "no viewer")
	}
	// Should resolve on the first poll, not after the grace window.
	if elapsed > 3*time.Second {
		t.Errorf("waited %v for a prompt nobody was watching — grace should not apply when no viewer was ever seen", elapsed)
	}
}

// The grace window still protects a viewer that goes away mid-wait, which
// is what makes a page reload survivable.
func TestListener_GraceAfterViewerLeaves(t *testing.T) {
	sockPath := socketPathFor(t)
	var watching atomic.Bool
	watching.Store(true)
	l, err := NewListener(ListenerOptions{
		SocketPath: sockPath,
		Timeout:    time.Hour,
		Policy: func(ApprovalRequest) WaitPolicy {
			return WaitPolicy{
				TimeoutEnabled: false,
				Grace:          1500 * time.Millisecond,
				HasViewer:      func() bool { return watching.Load() },
			}
		},
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	defer l.Close()

	done := make(chan ApprovalResponse, 1)
	go func() {
		done <- dialAndSend(t, sockPath, ApprovalRequest{ID: "nv-2", SessionID: "S1", Cmd: "sleep 999"})
	}()

	// Let the loop observe a viewer, then take it away (a reload).
	time.Sleep(700 * time.Millisecond)
	watching.Store(false)
	start := time.Now()

	select {
	case resp := <-done:
		if resp.Reason != "no viewer" {
			t.Errorf("Reason: got %q, want %q", resp.Reason, "no viewer")
		}
		// The grace window must actually be honoured, or a reload blocks.
		if time.Since(start) < time.Second {
			t.Errorf("blocked %v after the viewer left — grace window was skipped, a reload would lose the prompt", time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("never blocked after the viewer left")
	}
}

// A browser tab is not the only thing that can answer a prompt. Slack and
// Telegram render the same approval as buttons, so a session driven from a
// channel has a responder even with no tab open. Treating "no SSE
// subscriber" as "nobody can answer" would blow those buttons away before
// anyone could press them.
func TestListener_ChannelCountsAsViewer(t *testing.T) {
	sockPath := socketPathFor(t)
	l, err := NewListener(ListenerOptions{
		SocketPath: sockPath,
		Timeout:    time.Hour,
		Policy: func(ApprovalRequest) WaitPolicy {
			return WaitPolicy{
				TimeoutEnabled: false,
				Grace:          500 * time.Millisecond,
				// No browser, but a channel is holding the request.
				HasViewer: func() bool { return true },
			}
		},
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	defer l.Close()

	done := make(chan ApprovalResponse, 1)
	go func() {
		done <- dialAndSend(t, sockPath, ApprovalRequest{ID: "ch-1", SessionID: "S1", Cmd: "deploy"})
	}()

	select {
	case resp := <-done:
		t.Fatalf("blocked a channel-answerable prompt: decision=%q reason=%q", resp.Decision, resp.Reason)
	case <-time.After(1500 * time.Millisecond):
		// Still waiting well past the grace window — correct.
	}

	if !l.Resolve("ch-1", DecisionApproveOnce, "slack button") {
		t.Fatal("Resolve reported the request was gone while a channel was watching")
	}
	select {
	case resp := <-done:
		if resp.Decision != DecisionApproveOnce {
			t.Errorf("Decision: got %q, want %q", resp.Decision, DecisionApproveOnce)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel decision never reached the gate binary")
	}
}
