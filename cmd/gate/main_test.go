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

func TestReadHookCommandHappyPath(t *testing.T) {
	in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls -la"}}`)
	got, err := readHookCommand(in, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ls -la" {
		t.Fatalf("got %q", got)
	}
}

func TestReadHookCommandEmpty(t *testing.T) {
	in := strings.NewReader("")
	if _, err := readHookCommand(in, time.Second); err == nil {
		t.Fatal("empty stdin should error")
	}
}

func TestReadHookCommandMalformed(t *testing.T) {
	in := strings.NewReader("not json")
	if _, err := readHookCommand(in, time.Second); err == nil {
		t.Fatal("malformed json should error")
	}
}

func TestReadHookCommandMissingCommandField(t *testing.T) {
	in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash"}`)
	if _, err := readHookCommand(in, time.Second); err == nil {
		t.Fatal("missing command field should error")
	}
}

// blockingReader never returns — used to drive the timeout path.
type blockingReader struct{ ch chan struct{} }

func (b *blockingReader) Read(p []byte) (int, error) {
	<-b.ch
	return 0, nil
}

func TestReadHookCommandTimeout(t *testing.T) {
	r := &blockingReader{ch: make(chan struct{})}
	defer close(r.ch)
	start := time.Now()
	_, err := readHookCommand(r, 50*time.Millisecond)
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
// responds to one ApprovalRequest with the given decision. Closes
// itself after the single roundtrip — like the real daemon would,
// but without any pending-state machinery.
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
	spec := gate.Spec{
		SessionID:  "S1",
		AgentName:  "main",
		SocketPath: sock,
	}
	dec, _, err := requestApproval(spec, "git status", gate.MatchKey("Bash", "git status"))
	if err != nil {
		t.Fatalf("requestApproval: %v", err)
	}
	if dec != gate.DecisionApproveOnce {
		t.Errorf("decision: got %q, want %q", dec, gate.DecisionApproveOnce)
	}
}

func TestRequestApprovalBlock(t *testing.T) {
	sock := startFakeApprovalServer(t, gate.DecisionBlock, "user said no")
	spec := gate.Spec{SessionID: "S1", SocketPath: sock}
	dec, reason, err := requestApproval(spec, "rm -rf /", gate.MatchKey("Bash", "rm -rf /"))
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
	// Socket file doesn't exist → fail-safe error path. requestApproval
	// must surface the dial failure so run() can convert it to a block.
	spec := gate.Spec{
		SocketPath: filepath.Join(t.TempDir(), "missing.sock"),
	}
	if _, _, err := requestApproval(spec, "ls", gate.MatchKey("Bash", "ls")); err == nil {
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
	// Confirm that gate.IsAutoApproved + gate.MatchKey agree — a
	// command in AutoApproved must be detected by IsAutoApproved
	// using a freshly-computed key.
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
