package main

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/gate"
)

func TestReadHookInputHappyPath(t *testing.T) {
	in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"/tmp/x","session_id":"abc","tool_input":{"command":"ls -la"}}`)
	cmd, cwd, sid, err := readHookInput(in, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "ls -la" {
		t.Fatalf("cmd: %q", cmd)
	}
	if cwd != "/tmp/x" {
		t.Fatalf("cwd: %q", cwd)
	}
	if sid != "abc" {
		t.Fatalf("session_id: %q", sid)
	}
}

func TestReadHookInputEmpty(t *testing.T) {
	in := strings.NewReader("")
	if _, _, _, err := readHookInput(in, time.Second); err == nil {
		t.Fatal("empty stdin should error")
	}
}

func TestReadHookInputMalformed(t *testing.T) {
	in := strings.NewReader("not json")
	if _, _, _, err := readHookInput(in, time.Second); err == nil {
		t.Fatal("malformed json should error")
	}
}

func TestReadHookInputMissingCommandField(t *testing.T) {
	in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash"}`)
	if _, _, _, err := readHookInput(in, time.Second); err == nil {
		t.Fatal("missing command field should error")
	}
}

// blockingReader never returns — used to drive the timeout path.
type blockingReader struct{ ch chan struct{} }

func (b *blockingReader) Read(p []byte) (int, error) {
	<-b.ch
	return 0, nil
}

func TestReadHookInputTimeout(t *testing.T) {
	r := &blockingReader{ch: make(chan struct{})}
	defer close(r.ch)
	start := time.Now()
	_, _, _, err := readHookInput(r, 50*time.Millisecond)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout message, got %q", err.Error())
	}
}

// startFakeApprovalServer spins up a unix-socket listener that
// responds to one ApprovalRequest with the given decision.
func startFakeApprovalServer(t *testing.T, decision, reason string) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "g.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req gate.ApprovalRequest
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			return
		}
		_ = json.NewEncoder(conn).Encode(gate.ApprovalResponse{
			ID:       req.ID,
			Decision: decision,
			Reason:   reason,
		})
	}()
	return sockPath
}

func TestRequestApprovalApprove(t *testing.T) {
	sock := startFakeApprovalServer(t, gate.DecisionApproveOnce, "user clicked")
	dec, _, err := requestApprovalWithLog(sock, "git status", "/cwd", "claude-sid", gate.MatchKey("Bash", "git status"), "")
	if err != nil {
		t.Fatalf("requestApproval: %v", err)
	}
	if dec != gate.DecisionApproveOnce {
		t.Errorf("decision: got %q, want %q", dec, gate.DecisionApproveOnce)
	}
}

func TestRequestApprovalBlock(t *testing.T) {
	sock := startFakeApprovalServer(t, gate.DecisionBlock, "user said no")
	dec, reason, err := requestApprovalWithLog(sock, "rm -rf /", "/cwd", "", gate.MatchKey("Bash", "rm -rf /"), "")
	if err != nil {
		t.Fatalf("requestApproval: %v", err)
	}
	if dec != gate.DecisionBlock {
		t.Errorf("decision: got %q", dec)
	}
	if reason != "user said no" {
		t.Errorf("reason: got %q", reason)
	}
}

func TestRequestApprovalNoServer(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.sock")
	if _, _, err := requestApprovalWithLog(missing, "ls", "/cwd", "", gate.MatchKey("Bash", "ls"), ""); err == nil {
		t.Fatal("expected dial error when socket file missing")
	}
}

func TestNewRequestIDUnique(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 50; i++ {
		id := newRequestID()
		if len(id) != 32 {
			t.Errorf("expected 32-hex id, got %q", id)
		}
		if _, dup := seen[id]; dup {
			t.Errorf("duplicate id: %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestIsAutoApprovedShortCircuit(t *testing.T) {
	cmd := "git push origin main"
	key := gate.MatchKey("Bash", cmd)
	spec := gate.Spec{AutoApproved: []string{key, "other-key"}}
	if !gate.IsAutoApproved(spec, key) {
		t.Errorf("IsAutoApproved should return true for key in list")
	}
	if gate.IsAutoApproved(spec, gate.MatchKey("Bash", "different")) {
		t.Errorf("IsAutoApproved should return false for key not in list")
	}
}
