package api

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	agentgate "github.com/yogasw/wick/internal/agents/gate"
	agentpool "github.com/yogasw/wick/internal/agents/pool"
)

// wick_gate_test.go covers the wick provider's gate wiring: extending
// coverage from "shell only" to every tool, routed through the same
// synchronous ApprovalManager.RequestApproval the CLI providers' gate
// binary drives (see wickRequestApproval in server.go). These tests
// exercise the extracted top-level helpers directly rather than the
// full server, since SetGateChecker's closure needs the entire boot
// wiring (configsSvc, agentsLayout, etc.) to construct.

func TestSummarizeToolArgs_RendersJSON(t *testing.T) {
	out := summarizeToolArgs(map[string]any{"path": "notes.txt", "content": "hi"})
	var back map[string]any
	if err := json.Unmarshal([]byte(out), &back); err != nil {
		t.Fatalf("summarizeToolArgs output isn't valid JSON: %v (%q)", err, out)
	}
	if back["path"] != "notes.txt" {
		t.Errorf("path: got %v", back["path"])
	}
}

func TestSummarizeToolArgs_StableForIdenticalArgs(t *testing.T) {
	// MatchKey hashes this output — two calls with identical args must
	// render identically or "approve_always" would never re-match.
	args := map[string]any{"op": "list_channels", "workspace": "abc"}
	a := summarizeToolArgs(args)
	b := summarizeToolArgs(args)
	if a != b {
		t.Errorf("non-deterministic rendering: %q vs %q", a, b)
	}
}

func newTestApprovalManager(t *testing.T, onRequest func(sessionID string, r agentgate.ApprovalRequest)) *agentgate.ApprovalManager {
	t.Helper()
	dir, err := os.MkdirTemp("", "gatetest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	mgr, err := agentgate.NewApprovalManager(agentgate.ApprovalManagerOptions{
		AppName:    "testapp",
		Timeout:    100 * time.Millisecond,
		RouteByCWD: func(string) (string, bool) { return "S1", true },
		OnRequest:  onRequest,
	})
	if err != nil {
		t.Fatalf("NewApprovalManager: %v", err)
	}
	return mgr
}

func TestWickRequestApproval_NilManagerFailsOpen(t *testing.T) {
	deny, reason := wickRequestApproval(context.Background(), nil, agentpool.New(agentpool.PoolConfig{}), "S1", "wick_execute", "{}")
	if deny {
		t.Errorf("nil manager should fail open (allow), got deny with reason %q", reason)
	}
}

func TestWickRequestApproval_TimeoutDenies(t *testing.T) {
	mgr := newTestApprovalManager(t, nil)
	defer mgr.Stop()
	pool := agentpool.New(agentpool.PoolConfig{})

	deny, reason := wickRequestApproval(context.Background(), mgr, pool, "S1", "wick_execute", `{"op":"send"}`)
	if !deny {
		t.Fatal("expected deny on approval timeout (no one resolved the request)")
	}
	if reason == "" {
		t.Error("expected a non-empty reason")
	}
}

func TestWickRequestApproval_ApprovedByResolve(t *testing.T) {
	var seenReq agentgate.ApprovalRequest
	requested := make(chan struct{}, 1)
	mgr := newTestApprovalManager(t, func(sessionID string, r agentgate.ApprovalRequest) {
		seenReq = r
		requested <- struct{}{}
	})
	defer mgr.Stop()
	pool := agentpool.New(agentpool.PoolConfig{})

	respCh := make(chan struct {
		deny   bool
		reason string
	}, 1)
	go func() {
		deny, reason := wickRequestApproval(context.Background(), mgr, pool, "S1", "wick_execute", `{"op":"send"}`)
		respCh <- struct {
			deny   bool
			reason string
		}{deny, reason}
	}()

	select {
	case <-requested:
	case <-time.After(2 * time.Second):
		t.Fatal("onRequest never fired")
	}
	if seenReq.Tool != "wick_execute" {
		t.Errorf("request tool: got %q", seenReq.Tool)
	}
	if seenReq.SessionID != "S1" {
		t.Errorf("request sessionID: got %q", seenReq.SessionID)
	}

	if ok, err := mgr.Resolve("S1", seenReq.ID, agentgate.DecisionApproveOnce, "approved in test", seenReq.MatchKey); err != nil || !ok {
		t.Fatalf("Resolve: ok=%v err=%v", ok, err)
	}

	select {
	case r := <-respCh:
		if r.deny {
			t.Errorf("expected allow after approve_once, got deny with reason %q", r.reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wickRequestApproval never returned after Resolve")
	}
}

func TestWickRequestApproval_BlockedByResolve(t *testing.T) {
	requested := make(chan agentgate.ApprovalRequest, 1)
	mgr := newTestApprovalManager(t, func(sessionID string, r agentgate.ApprovalRequest) { requested <- r })
	defer mgr.Stop()
	pool := agentpool.New(agentpool.PoolConfig{})

	respCh := make(chan struct {
		deny   bool
		reason string
	}, 1)
	go func() {
		deny, reason := wickRequestApproval(context.Background(), mgr, pool, "S1", "shell", "rm -rf /")
		respCh <- struct {
			deny   bool
			reason string
		}{deny, reason}
	}()

	req := <-requested
	if ok, err := mgr.Resolve("S1", req.ID, agentgate.DecisionBlock, "user blocked", req.MatchKey); err != nil || !ok {
		t.Fatalf("Resolve: ok=%v err=%v", ok, err)
	}

	r := <-respCh
	if !r.deny {
		t.Fatal("expected deny after block decision")
	}
	if r.reason == "" {
		t.Error("expected a non-empty deny reason")
	}
}
