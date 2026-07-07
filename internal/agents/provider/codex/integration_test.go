package codex

// Integration test — requires a real `codex` binary on PATH.
// Skip automatically when codex is not installed.
//
// Run:
//   go test ./internal/agents/provider/codex/... -run TestIntegration -v -timeout 60s

import (
	"bufio"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/pkg/safeexec"
)

func skipIfNoCodex(t *testing.T) string {
	t.Helper()
	bin, err := safeexec.LookPath("codex")
	if err != nil {
		t.Skip("codex binary not found on PATH — skipping integration test")
	}
	return bin
}

// TestIntegration_SpawnAndReceiveJSON verifies:
//  1. codex spawns with --json + prompt as positional arg
//  2. stdout emits JSONL that CodexParser decodes correctly
//  3. thread.started → SessionStart, item.completed(agent_message) → TextDelta, turn.completed → Done
func TestIntegration_SpawnAndReceiveJSON(t *testing.T) {
	bin := skipIfNoCodex(t)

	s := Spawner{Binary: bin}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	proc, err := s.Spawn(ctx, provider.SpawnOptions{
		Workspace:      t.TempDir(),
		InitialMessage: "reply only the word: OK",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer func() { _ = proc.Kill() }()

	parser := event.NewCodexParser()
	scanner := bufio.NewScanner(proc.Stdout())

	var events []event.AgentEvent
	for scanner.Scan() {
		line := scanner.Text()
		t.Logf("raw: %s", line)
		ev, err := parser.Parse(line)
		if err != nil {
			t.Logf("parse error: %v", err)
			continue
		}
		if ev.Type != event.Unknown {
			events = append(events, ev)
			t.Logf("event: type=%s text=%q session_id=%q", ev.Type, ev.Text, ev.SessionID)
		}
	}
	_ = proc.Wait()

	if len(events) == 0 {
		t.Fatal("no events received — check codex --json output format")
	}

	hasSession := false
	hasText := false
	hasDone := false
	var sessionID string
	for _, ev := range events {
		switch ev.Type {
		case event.SessionStart:
			hasSession = true
			sessionID = ev.SessionID
		case event.TextDelta:
			if strings.TrimSpace(ev.Text) != "" {
				hasText = true
			}
		case event.Done:
			hasDone = true
		}
	}

	if !hasSession {
		t.Error("no SessionStart event (thread.started not parsed?)")
	}
	if !hasText {
		t.Error("no TextDelta with content (item.completed agent_message not parsed?)")
	}
	if !hasDone {
		t.Error("no Done event (turn.completed not parsed?)")
	}
	t.Logf("session_id: %s", sessionID)
}

// TestIntegration_ResumeSession verifies session_id captured from thread.started
// is forwarded as `codex exec resume <id> <prompt>` on next turn.
func TestIntegration_ResumeSession(t *testing.T) {
	bin := skipIfNoCodex(t)
	s := Spawner{Binary: bin}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Turn 1
	proc1, err := s.Spawn(ctx, provider.SpawnOptions{
		Workspace:      t.TempDir(),
		InitialMessage: "reply only: TURN1",
	})
	if err != nil {
		t.Fatalf("Spawn turn1: %v", err)
	}

	parser := event.NewCodexParser()
	scanner := bufio.NewScanner(proc1.Stdout())
	var sessionID string
	for scanner.Scan() {
		ev, _ := parser.Parse(scanner.Text())
		if ev.Type == event.SessionStart {
			sessionID = ev.SessionID
			t.Logf("turn1 session_id: %s", sessionID)
		}
	}
	_ = proc1.Wait()

	if sessionID == "" {
		t.Fatal("no session_id from turn 1 — thread.started not emitted?")
	}

	// Turn 2 with resume
	proc2, err := s.Spawn(ctx, provider.SpawnOptions{
		Workspace:      t.TempDir(),
		ResumeID:       sessionID,
		InitialMessage: "reply only: TURN2",
	})
	if err != nil {
		t.Fatalf("Spawn turn2: %v", err)
	}
	defer func() { _ = proc2.Kill() }()

	t.Logf("turn2 argv: %v", proc2.Argv())

	parser2 := event.NewCodexParser()
	scanner2 := bufio.NewScanner(proc2.Stdout())
	var turn2Text string
	for scanner2.Scan() {
		line := scanner2.Text()
		t.Logf("turn2 raw: %s", line)
		ev, _ := parser2.Parse(line)
		if ev.Type == event.TextDelta {
			turn2Text += ev.Text
		}
	}
	_ = proc2.Wait()

	if turn2Text == "" {
		t.Error("no text from resumed session")
	}
	t.Logf("turn2 response: %q", turn2Text)
}
