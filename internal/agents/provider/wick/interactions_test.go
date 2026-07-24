package wick

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/genai"
)

// TestInteractionSink_RecordsPerCall: each model call appends one record
// to wick-interactions.jsonl with the request, response, and tokens — the
// data behind "why did the model answer this".
func TestInteractionSink_RecordsPerCall(t *testing.T) {
	dir := t.TempDir()
	f := &fakeModel{responses: []*LLMResponse{
		toolCallResp("echo", map[string]any{"msg": "hi"}),
		textResp("all done"),
	}}
	echo := toolDef{
		decl:    &genai.FunctionDeclaration{Name: "echo"},
		handler: func(ctx context.Context, args map[string]any) (string, bool) { return "ok", false },
	}
	eng, _ := newTestEngine(f, []toolDef{echo})
	eng.setInteractionSink(newInteractionSink(dir))
	eng.start()
	eng.runTurn(context.Background(), "please echo hi")

	data, err := os.ReadFile(filepath.Join(dir, "wick-interactions.jsonl"))
	if err != nil {
		t.Fatalf("interactions file not written: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// Two model calls: the tool-call turn + the final text turn.
	if len(lines) != 2 {
		t.Fatalf("want 2 interaction records, got %d:\n%s", len(lines), data)
	}
	var first interactionRecord
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Seq != 1 || first.Model != "fake" || first.Kind != "generate" {
		t.Errorf("record[0] header wrong: %+v", first)
	}
	if len(first.Request) == 0 || !strings.Contains(first.Request[len(first.Request)-1].Text, "echo hi") {
		t.Errorf("record[0] should carry the sent user message: %+v", first.Request)
	}
	if len(first.Tools) == 0 || first.Tools[0] != "echo" {
		t.Errorf("record[0] should list offered tools: %+v", first.Tools)
	}
	if len(first.ToolCalls) == 0 || first.ToolCalls[0] != "echo" {
		t.Errorf("record[0] should record the tool call: %+v", first.ToolCalls)
	}

	var second interactionRecord
	_ = json.Unmarshal([]byte(lines[1]), &second)
	if second.Response != "all done" {
		t.Errorf("record[1] response wrong: %q", second.Response)
	}
}

// TestInteractionSink_RecordsError: a failed call records the error so the
// operator can see why the model didn't answer.
func TestInteractionSink_RecordsError(t *testing.T) {
	dir := t.TempDir()
	f := &fakeModel{responses: []*LLMResponse{{ErrorCode: "429", ErrorMessage: "rate limited"}}}
	eng, _ := newTestEngine(f, nil)
	eng.setInteractionSink(newInteractionSink(dir))
	eng.start()
	eng.runTurn(context.Background(), "hi")

	data, _ := os.ReadFile(filepath.Join(dir, "wick-interactions.jsonl"))
	if !strings.Contains(string(data), "rate limited") {
		t.Errorf("error should be recorded: %s", data)
	}
}

// TestNewInteractionSink_NilForEmptyDir: no session dir → no sink (no panic).
func TestNewInteractionSink_NilForEmptyDir(t *testing.T) {
	if newInteractionSink("") != nil {
		t.Error("empty sessionDir should yield a nil sink")
	}
}

// TestInteractionSink_SeqUniqueAcrossRespawns is a regression test for the
// FE's "each_key_duplicate" crash: a session that respawns (idle-kill, then
// a later message spins up a fresh engine) must NOT restart Seq at 1 in the
// same wick-interactions.jsonl file, or the FE's keyed list throws on the
// duplicate. Each new sink instance seeds its counter from the file's
// existing line count, so Seq keeps climbing across spawns.
func TestInteractionSink_SeqUniqueAcrossRespawns(t *testing.T) {
	dir := t.TempDir()

	// First "spawn": a fresh engine + sink writes two records.
	sink1 := newInteractionSink(dir)
	sink1(interactionRecord{Kind: "generate", Model: "fake"})
	sink1(interactionRecord{Kind: "generate", Model: "fake"})

	// Second "spawn" (respawn after idle-kill): a NEW engine + NEW sink
	// instance, same session dir. Must continue from 3, not restart at 1.
	sink2 := newInteractionSink(dir)
	sink2(interactionRecord{Kind: "generate", Model: "fake"})

	data, err := os.ReadFile(filepath.Join(dir, "wick-interactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 records, got %d:\n%s", len(lines), data)
	}
	seen := map[int]bool{}
	for i, ln := range lines {
		var rec interactionRecord
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			t.Fatal(err)
		}
		if rec.Seq != i+1 {
			t.Errorf("line %d: want Seq=%d, got %d", i, i+1, rec.Seq)
		}
		if seen[rec.Seq] {
			t.Fatalf("duplicate Seq=%d across respawns — this is exactly the bug that broke the FE's keyed list", rec.Seq)
		}
		seen[rec.Seq] = true
	}
}
