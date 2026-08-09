package gate

import (
	"context"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// setupSharedHome points os.UserHomeDir() at a fresh tempdir so the
// shared spec / socket paths land somewhere isolated. Returns the
// home dir for callers that want to assert on file presence.
func setupSharedHome(t *testing.T) string {
	t.Helper()
	// Short tempdir — long names overflow Windows' AF_UNIX bind() limit.
	dir, err := os.MkdirTemp("", "g")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

// newTestManager wires a manager with the shared spec/socket model.
// routeByCWD is supplied by the caller — most tests can use a static
// "always returns S1" stub since the gate-side test fixtures encode
// cwd themselves.
func newTestManager(t *testing.T, app string, routeByCWD func(string) (string, bool)) *ApprovalManager {
	t.Helper()
	mgr, err := NewApprovalManager(ApprovalManagerOptions{
		AppName:    app,
		Timeout:    200 * time.Millisecond,
		RouteByCWD: routeByCWD,
	})
	if err != nil {
		t.Fatalf("NewApprovalManager: %v", err)
	}
	return mgr
}

func TestManager_StartStop(t *testing.T) {
	setupSharedHome(t)
	mgr := newTestManager(t, "appA", func(string) (string, bool) { return "S1", true })
	sock, err := mgr.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close()

	mgr.Stop()
	if _, err := net.DialTimeout("unix", sock, 200*time.Millisecond); err == nil {
		t.Fatal("expected dial to fail after Stop")
	}
}

func TestManager_ResolveApproveSession_AutoApprovesNext(t *testing.T) {
	setupSharedHome(t)
	mgr := newTestManager(t, "appB", func(string) (string, bool) { return "S1", true })
	defer mgr.Stop()

	requested := make(chan ApprovalRequest, 4)
	mgr.onRequest = func(_ string, r ApprovalRequest) { requested <- r }

	sock, err := mgr.Start()
	if err != nil {
		t.Fatal(err)
	}

	first := ApprovalRequest{ID: "req-1", Cmd: "ls", MatchKey: "key-ls"}
	respCh1 := make(chan ApprovalResponse, 1)
	go func() { respCh1 <- dialAndSend(t, sock, first) }()

	select {
	case <-requested:
	case <-time.After(2 * time.Second):
		t.Fatal("first request never reached onRequest")
	}

	if ok, err := mgr.Resolve("S1", "req-1", DecisionApproveSession, "user clicked", "key-ls"); err != nil || !ok {
		t.Fatalf("Resolve: ok=%v err=%v", ok, err)
	}
	if !mgr.IsSessionApproved("S1", "key-ls") {
		t.Error("matchKey should be in session-approved set after approve_session")
	}

	resp1 := <-respCh1
	if resp1.Decision != DecisionApproveSession {
		t.Errorf("first decision: %q", resp1.Decision)
	}

	// Second request same matchKey: auto-approves without onRequest.
	second := ApprovalRequest{ID: "req-2", Cmd: "ls", MatchKey: "key-ls"}
	resp2 := dialAndSend(t, sock, second)
	if resp2.Decision != DecisionApproveSession {
		t.Errorf("second decision: got %q, want %q", resp2.Decision, DecisionApproveSession)
	}
	select {
	case r := <-requested:
		t.Errorf("session-approved request should not reach onRequest, got: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestManager_ResolveApproveAlways_PersistsToSharedSpec(t *testing.T) {
	setupSharedHome(t)
	app := "appC"
	if err := WriteSharedSpec(app, Spec{}); err != nil {
		t.Fatal(err)
	}
	mgr := newTestManager(t, app, func(string) (string, bool) { return "S1", true })
	defer mgr.Stop()

	requested := make(chan ApprovalRequest, 1)
	mgr.onRequest = func(_ string, r ApprovalRequest) { requested <- r }

	sock, err := mgr.Start()
	if err != nil {
		t.Fatal(err)
	}

	respCh := make(chan ApprovalResponse, 1)
	go func() {
		respCh <- dialAndSend(t, sock, ApprovalRequest{ID: "req-x", Cmd: "git status", MatchKey: "key-gs"})
	}()
	<-requested

	if ok, err := mgr.Resolve("S1", "req-x", DecisionApproveAlways, "always", "key-gs"); err != nil || !ok {
		t.Fatalf("Resolve: ok=%v err=%v", ok, err)
	}
	<-respCh

	got, err := LoadSpec(app)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.AutoApproved) != 1 || got.AutoApproved[0] != "key-gs" {
		t.Errorf("shared AutoApproved: %+v", got.AutoApproved)
	}
	if a := mgr.AutoApproved(); len(a) != 1 || a[0] != "key-gs" {
		t.Errorf("AutoApproved(): %+v", a)
	}
}

func TestManager_RevokeAlways(t *testing.T) {
	setupSharedHome(t)
	app := "appD"
	if err := WriteSharedSpec(app, Spec{
		AutoApproved: []string{"k1", "k2", "k3"},
	}); err != nil {
		t.Fatal(err)
	}
	mgr := newTestManager(t, app, func(string) (string, bool) { return "S1", true })
	defer mgr.Stop()

	if err := mgr.RevokeAlways("S1", "k2"); err != nil {
		t.Fatalf("RevokeAlways: %v", err)
	}
	got := mgr.AutoApproved()
	if len(got) != 2 || got[0] != "k1" || got[1] != "k3" {
		t.Errorf("AutoApproved after revoke: %+v", got)
	}
}

func TestManager_OnResolved_Fires(t *testing.T) {
	setupSharedHome(t)
	mgr := newTestManager(t, "appE", func(string) (string, bool) { return "S1", true })
	defer mgr.Stop()

	var (
		mu       sync.Mutex
		resolved []string
	)
	mgr.onResolved = func(_, requestID, decision string) {
		mu.Lock()
		resolved = append(resolved, requestID+"="+decision)
		mu.Unlock()
	}
	requested := make(chan ApprovalRequest, 1)
	mgr.onRequest = func(_ string, r ApprovalRequest) { requested <- r }

	sock, err := mgr.Start()
	if err != nil {
		t.Fatal(err)
	}

	respCh := make(chan ApprovalResponse, 1)
	go func() {
		respCh <- dialAndSend(t, sock, ApprovalRequest{ID: "r1", Cmd: "ls", MatchKey: "k"})
	}()
	<-requested

	if _, err := mgr.Resolve("S1", "r1", DecisionApproveOnce, "", "k"); err != nil {
		t.Fatal(err)
	}
	<-respCh

	mu.Lock()
	defer mu.Unlock()
	if len(resolved) != 1 || resolved[0] != "r1=approve_once" {
		t.Errorf("onResolved: %+v", resolved)
	}
}

// TestManager_RouteByCWD_NoMatch verifies an unroutable cwd is
// fail-open auto-approved (DecisionApproveOnce) so non-wick Claude
// sessions launched outside any wick workspace aren't blocked by the
// global PreToolUse hook. onRequest must NOT fire — there's no UI to
// route it to.
func TestManager_RouteByCWD_NoMatch(t *testing.T) {
	setupSharedHome(t)
	mgr := newTestManager(t, "appF", func(string) (string, bool) { return "", false })
	defer mgr.Stop()

	requested := make(chan struct{}, 1)
	mgr.onRequest = func(sid string, r ApprovalRequest) {
		requested <- struct{}{}
	}

	sock, err := mgr.Start()
	if err != nil {
		t.Fatal(err)
	}
	respCh := make(chan ApprovalResponse, 1)
	go func() {
		respCh <- dialAndSend(t, sock, ApprovalRequest{ID: "rZ", Cmd: "ls", WorkDir: "/orphan", MatchKey: "k"})
	}()

	select {
	case resp := <-respCh:
		if resp.Decision != DecisionApproveOnce {
			t.Errorf("unrouted cwd: got decision %q, want %q", resp.Decision, DecisionApproveOnce)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no response for unrouted cwd")
	}
	select {
	case <-requested:
		t.Error("onRequest fired for unrouted cwd; should be auto-approved")
	default:
	}
}

// ── RequestApproval (in-process, no socket) ─────────────────────────

func TestManager_RequestApproval_ResolvedByHTTPHandlerEquivalent(t *testing.T) {
	setupSharedHome(t)
	mgr := newTestManager(t, "appG", func(string) (string, bool) { return "S1", true })
	defer mgr.Stop()

	requested := make(chan ApprovalRequest, 1)
	mgr.onRequest = func(_ string, r ApprovalRequest) { requested <- r }

	respCh := make(chan ApprovalResponse, 1)
	go func() {
		respCh <- mgr.RequestApproval(context.Background(), ApprovalRequest{
			ID: "wick-req-1", SessionID: "S1", Tool: "write_file", Cmd: "notes.txt", MatchKey: "wick-key-1",
		})
	}()

	select {
	case r := <-requested:
		if r.Tool != "write_file" {
			t.Errorf("onRequest tool: got %q", r.Tool)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApproval never reached onRequest")
	}

	// Resolve exactly as the HTTP handler (approveCommand) would.
	if ok, err := mgr.Resolve("S1", "wick-req-1", DecisionApproveOnce, "user clicked", "wick-key-1"); err != nil || !ok {
		t.Fatalf("Resolve: ok=%v err=%v", ok, err)
	}

	select {
	case resp := <-respCh:
		if resp.Decision != DecisionApproveOnce {
			t.Errorf("decision: got %q, want %q", resp.Decision, DecisionApproveOnce)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApproval never returned after Resolve")
	}
}

func TestManager_RequestApproval_SessionApprovedFastPath(t *testing.T) {
	setupSharedHome(t)
	mgr := newTestManager(t, "appH", func(string) (string, bool) { return "S1", true })
	defer mgr.Stop()

	requested := make(chan struct{}, 1)
	mgr.onRequest = func(string, ApprovalRequest) { requested <- struct{}{} }

	mgr.markSessionApproved("S1", "wick-key-2")

	resp := mgr.RequestApproval(context.Background(), ApprovalRequest{
		ID: "wick-req-2", SessionID: "S1", Tool: "shell", Cmd: "sleep 1", MatchKey: "wick-key-2",
	})
	if resp.Decision != DecisionApproveSession {
		t.Errorf("decision: got %q, want %q", resp.Decision, DecisionApproveSession)
	}
	select {
	case <-requested:
		t.Error("onRequest fired for an already session-approved matchKey")
	default:
	}
}

func TestManager_RequestApproval_TimeoutBlocks(t *testing.T) {
	setupSharedHome(t)
	mgr, err := NewApprovalManager(ApprovalManagerOptions{
		AppName:    "appI",
		Timeout:    50 * time.Millisecond,
		RouteByCWD: func(string) (string, bool) { return "S1", true },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()

	resp := mgr.RequestApproval(context.Background(), ApprovalRequest{
		ID: "wick-req-3", SessionID: "S1", Tool: "shell", Cmd: "rm -rf /", MatchKey: "wick-key-3",
	})
	if resp.Decision != DecisionBlock {
		t.Errorf("decision: got %q, want %q (timeout)", resp.Decision, DecisionBlock)
	}
	if resp.Reason != "timeout" {
		t.Errorf("reason: got %q, want %q", resp.Reason, "timeout")
	}
}

func TestManager_RequestApproval_ContextCancelBlocks(t *testing.T) {
	setupSharedHome(t)
	mgr := newTestManager(t, "appJ", func(string) (string, bool) { return "S1", true })
	defer mgr.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	respCh := make(chan ApprovalResponse, 1)
	go func() {
		respCh <- mgr.RequestApproval(ctx, ApprovalRequest{
			ID: "wick-req-4", SessionID: "S1", Tool: "shell", Cmd: "sleep 999", MatchKey: "wick-key-4",
		})
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case resp := <-respCh:
		if resp.Decision != DecisionBlock || resp.Reason != "cancelled" {
			t.Errorf("got decision=%q reason=%q, want block/cancelled", resp.Decision, resp.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApproval did not return after ctx cancel")
	}
}

func TestManager_RequestApproval_AfterStopReturnsBlock(t *testing.T) {
	setupSharedHome(t)
	mgr := newTestManager(t, "appK", func(string) (string, bool) { return "S1", true })
	mgr.Stop()

	resp := mgr.RequestApproval(context.Background(), ApprovalRequest{
		ID: "wick-req-5", SessionID: "S1", Tool: "shell", Cmd: "ls", MatchKey: "wick-key-5",
	})
	if resp.Decision != DecisionBlock {
		t.Errorf("decision after Stop: got %q, want %q", resp.Decision, DecisionBlock)
	}
}

// ── Wait policy: deadline off, wait while a browser is watching ──────

// newWaitingManager wires a manager in the default shipping mode: the
// approval deadline is off, so prompts live as long as hasViewer says a
// tab is watching. grace is deliberately short so tests don't crawl.
func newWaitingManager(t *testing.T, app string, hasViewer func(string) bool, grace time.Duration) *ApprovalManager {
	t.Helper()
	mgr, err := NewApprovalManager(ApprovalManagerOptions{
		AppName:    app,
		Timeout:    50 * time.Millisecond, // must NOT be what decides these tests
		RouteByCWD: func(string) (string, bool) { return "S1", true },
		WaitPolicy: func() WaitPolicy {
			return WaitPolicy{TimeoutEnabled: false, Grace: grace}
		},
		HasViewer: hasViewer,
	})
	if err != nil {
		t.Fatalf("NewApprovalManager: %v", err)
	}
	return mgr
}

func TestManager_WaitsPastTimeoutWhileViewerPresent(t *testing.T) {
	setupSharedHome(t)
	mgr := newWaitingManager(t, "appW1", func(string) bool { return true }, 100*time.Millisecond)
	defer mgr.Stop()

	respCh := make(chan ApprovalResponse, 1)
	go func() {
		respCh <- mgr.RequestApproval(context.Background(), ApprovalRequest{
			ID: "wait-1", SessionID: "S1", Tool: "shell", Cmd: "sleep 999", MatchKey: "wait-key-1",
		})
	}()

	// Well past the 50ms fixed deadline this manager would use if the
	// policy were ignored — the whole point is that it is not.
	select {
	case resp := <-respCh:
		t.Fatalf("blocked while a viewer was watching: decision=%q reason=%q", resp.Decision, resp.Reason)
	case <-time.After(400 * time.Millisecond):
	}

	if ok, err := mgr.Resolve("S1", "wait-1", DecisionApproveOnce, "user clicked", "wait-key-1"); err != nil || !ok {
		t.Fatalf("Resolve: ok=%v err=%v", ok, err)
	}
	select {
	case resp := <-respCh:
		if resp.Decision != DecisionApproveOnce {
			t.Errorf("decision: got %q, want %q", resp.Decision, DecisionApproveOnce)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApproval did not return after Resolve")
	}
}

func TestManager_BlocksAfterGraceWhenViewerLeaves(t *testing.T) {
	setupSharedHome(t)
	var watching atomic.Bool
	watching.Store(true)
	mgr := newWaitingManager(t, "appW2", func(string) bool { return watching.Load() }, 100*time.Millisecond)
	defer mgr.Stop()

	respCh := make(chan ApprovalResponse, 1)
	go func() {
		respCh <- mgr.RequestApproval(context.Background(), ApprovalRequest{
			ID: "wait-2", SessionID: "S1", Tool: "shell", Cmd: "sleep 999", MatchKey: "wait-key-2",
		})
	}()

	time.Sleep(100 * time.Millisecond)
	watching.Store(false) // every tab closed

	select {
	case resp := <-respCh:
		if resp.Decision != DecisionBlock {
			t.Errorf("decision: got %q, want %q", resp.Decision, DecisionBlock)
		}
		if resp.Reason != "no viewer" {
			t.Errorf("reason: got %q, want %q", resp.Reason, "no viewer")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("did not block after the viewer left")
	}
}

// A prompt that dies on its own must still reach the browser, or the
// modal stays open with buttons that can only ever return 410.
func TestManager_NotifiesResolvedOnSelfExpiry(t *testing.T) {
	setupSharedHome(t)
	type resolved struct {
		sessionID, requestID, decision string
	}
	got := make(chan resolved, 1)
	mgr, err := NewApprovalManager(ApprovalManagerOptions{
		AppName:    "appW3",
		Timeout:    50 * time.Millisecond,
		RouteByCWD: func(string) (string, bool) { return "S1", true },
		OnResolved: func(sessionID, requestID, decision string) {
			select {
			case got <- resolved{sessionID, requestID, decision}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()

	mgr.RequestApproval(context.Background(), ApprovalRequest{
		ID: "wait-3", SessionID: "S1", Tool: "shell", Cmd: "sleep 999", MatchKey: "wait-key-3",
	})

	select {
	case r := <-got:
		if r.requestID != "wait-3" || r.decision != DecisionBlock {
			t.Errorf("got %+v, want request wait-3 blocked", r)
		}
		if r.sessionID != "S1" {
			t.Errorf("sessionID: got %q, want S1", r.sessionID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout did not broadcast approval_resolved")
	}
}

// The UI renders a countdown only when the daemon is actually enforcing
// one; advertising a deadline that nothing enforces is what made the old
// modal auto-block against a request the daemon still held open.
func TestManager_StampsDeadlineOnlyWhenTimeoutEnabled(t *testing.T) {
	setupSharedHome(t)

	seen := make(chan ApprovalRequest, 1)
	onReq := func(_ string, r ApprovalRequest) {
		select {
		case seen <- r:
		default:
		}
	}

	waiting, err := NewApprovalManager(ApprovalManagerOptions{
		AppName:    "appW4",
		RouteByCWD: func(string) (string, bool) { return "S1", true },
		OnRequest:  onReq,
		WaitPolicy: func() WaitPolicy { return WaitPolicy{TimeoutEnabled: false, Grace: 50 * time.Millisecond} },
		HasViewer:  func(string) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	waiting.RequestApproval(context.Background(), ApprovalRequest{
		ID: "stamp-1", SessionID: "S1", Tool: "shell", Cmd: "ls", MatchKey: "stamp-key-1",
	})
	waiting.Stop()
	select {
	case r := <-seen:
		if r.ExpiresInSec != 0 {
			t.Errorf("ExpiresInSec: got %d, want 0 (no deadline is being enforced)", r.ExpiresInSec)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnRequest never fired for the waiting manager")
	}

	timed, err := NewApprovalManager(ApprovalManagerOptions{
		AppName:    "appW5",
		RouteByCWD: func(string) (string, bool) { return "S1", true },
		OnRequest:  onReq,
		WaitPolicy: func() WaitPolicy { return WaitPolicy{TimeoutEnabled: true, Timeout: 30 * time.Second} },
	})
	if err != nil {
		t.Fatal(err)
	}
	go timed.RequestApproval(context.Background(), ApprovalRequest{
		ID: "stamp-2", SessionID: "S1", Tool: "shell", Cmd: "ls", MatchKey: "stamp-key-2",
	})
	defer timed.Stop()
	select {
	case r := <-seen:
		if r.ExpiresInSec != 30 {
			t.Errorf("ExpiresInSec: got %d, want 30", r.ExpiresInSec)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnRequest never fired for the timed manager")
	}
}

// Without a viewer probe there is nothing to wait on, so a manager must
// fall back to the fixed deadline rather than hang forever.
func TestManager_FallsBackToDeadlineWithoutViewerProbe(t *testing.T) {
	setupSharedHome(t)
	mgr, err := NewApprovalManager(ApprovalManagerOptions{
		AppName:    "appW6",
		Timeout:    50 * time.Millisecond,
		RouteByCWD: func(string) (string, bool) { return "S1", true },
		WaitPolicy: func() WaitPolicy { return WaitPolicy{TimeoutEnabled: false} },
		// HasViewer deliberately nil.
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()

	done := make(chan ApprovalResponse, 1)
	go func() {
		done <- mgr.RequestApproval(context.Background(), ApprovalRequest{
			ID: "wait-4", SessionID: "S1", Tool: "shell", Cmd: "sleep 999", MatchKey: "wait-key-4",
		})
	}()
	select {
	case resp := <-done:
		if resp.Decision != DecisionBlock || resp.Reason != "timeout" {
			t.Errorf("got decision=%q reason=%q, want block/timeout", resp.Decision, resp.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hung with no viewer probe available")
	}
}
