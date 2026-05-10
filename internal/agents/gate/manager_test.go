package gate

import (
	"net"
	"os"
	"sync"
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

// TestManager_RouteByCWD_NoMatch shows that an unroutable cwd still
// fans out to onRequest with empty sessionID — useful for the UI to
// render an "unrouted" bucket instead of dropping silently.
func TestManager_RouteByCWD_NoMatch(t *testing.T) {
	setupSharedHome(t)
	mgr := newTestManager(t, "appF", func(string) (string, bool) { return "", false })
	defer mgr.Stop()

	requested := make(chan struct {
		sid string
		req ApprovalRequest
	}, 1)
	mgr.onRequest = func(sid string, r ApprovalRequest) {
		requested <- struct {
			sid string
			req ApprovalRequest
		}{sid, r}
	}

	sock, err := mgr.Start()
	if err != nil {
		t.Fatal(err)
	}
	go dialAndSend(t, sock, ApprovalRequest{ID: "rZ", Cmd: "ls", WorkDir: "/orphan", MatchKey: "k"})

	select {
	case got := <-requested:
		if got.sid != "" {
			t.Errorf("expected empty sessionID for unroutable cwd, got %q", got.sid)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onRequest never fired")
	}
}
