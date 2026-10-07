package event

import (
	"strings"
	"testing"
)

// TestCodexTurnFailedIsError: {"type":"turn.failed","error":{"message":...}}
// carries its detail nested under error.message and must surface as a fatal
// Error (not fall through to Unknown, which silently drops it).
func TestCodexTurnFailedIsError(t *testing.T) {
	p := NewCodexParser()
	ev, err := p.Parse(`{"type":"turn.failed","error":{"message":"unexpected status 403 Forbidden"}}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Type != Error {
		t.Fatalf("type = %v, want Error", ev.Type)
	}
	if !strings.Contains(ev.ErrorMsg, "403") {
		t.Fatalf("ErrorMsg = %q, want the nested message", ev.ErrorMsg)
	}
}

// TestCodexTopLevelError: {"type":"error","message":...} stays a fatal Error.
func TestCodexTopLevelError(t *testing.T) {
	p := NewCodexParser()
	ev, _ := p.Parse(`{"type":"error","message":"boom"}`)
	if ev.Type != Error || ev.ErrorMsg != "boom" {
		t.Fatalf("got %v / %q", ev.Type, ev.ErrorMsg)
	}
}

// TestCodexItemErrorIsWarning: an error wrapped in item.completed is
// non-fatal — a Warning, so the turn keeps going but it still surfaces.
func TestCodexItemErrorIsWarning(t *testing.T) {
	p := NewCodexParser()
	ev, err := p.Parse(`{"type":"item.completed","item":{"id":"item_0","type":"error","message":"agent role subagent must define a description"}}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Type != Warning {
		t.Fatalf("type = %v, want Warning", ev.Type)
	}
	if !strings.Contains(ev.ErrorMsg, "subagent") {
		t.Fatalf("ErrorMsg = %q", ev.ErrorMsg)
	}
}

// TestCodexUnknownTypeIsTrace: an unrecognized, non-control frame is routed
// to Trace (kept in the turn trace) rather than dropped as Unknown.
func TestCodexUnknownTypeIsTrace(t *testing.T) {
	p := NewCodexParser()
	ev, _ := p.Parse(`{"type":"some.new.frame","foo":1}`)
	if ev.Type != Trace {
		t.Fatalf("type = %v, want Trace", ev.Type)
	}
	if ev.Raw == "" {
		t.Fatal("Trace event should carry Raw")
	}
}

// TestCodexControlFramesSkipped: known housekeeping frames stay Unknown
// (skipped from the trace, kept only in raw.jsonl).
func TestCodexControlFramesSkipped(t *testing.T) {
	p := NewCodexParser()
	for _, line := range []string{
		`{"type":"turn.started"}`,
		`{"type":"ping"}`,
	} {
		ev, _ := p.Parse(line)
		if ev.Type != Unknown {
			t.Errorf("%s → %v, want Unknown", line, ev.Type)
		}
	}
}

// TestCodexTurnUsage pins the shape captured from a live codex 0.129 run.
// The trap it guards: codex's input_tokens ALREADY includes the cached
// part, unlike Anthropic's disjoint trio. Adding them would count the
// cache twice and inflate every codex bill in the ledger.
func TestCodexTurnUsage(t *testing.T) {
	line := `{"type":"turn.completed","usage":{"input_tokens":20250,` +
		`"cached_input_tokens":11264,"cache_write_input_tokens":0,` +
		`"output_tokens":5,"reasoning_output_tokens":0}}`
	ev, err := NewCodexParser().Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Type != Done {
		t.Fatalf("want Done, got %v", ev.Type)
	}
	if ev.Usage == nil {
		t.Fatal("want usage, got nil")
	}
	// NOT the context level: input_tokens is the turn's SUM over every
	// request it made (see codex_rollout.go), so with no rollout to read
	// the level stays unknown rather than being inflated by the tool
	// calls. Proven on 0.149.1: 92,842 reported, 18,874 actually held.
	if ev.Usage.ContextUsed != 0 {
		t.Fatalf("context level: want 0 (unknown without a rollout), got %d", ev.Usage.ContextUsed)
	}
	if ev.Usage.Input != 20250-11264 {
		t.Fatalf("fresh input: want %d, got %d", 20250-11264, ev.Usage.Input)
	}
	if ev.Usage.CacheRead != 11264 || ev.Usage.Output != 5 {
		t.Fatalf("usage: %+v", ev.Usage)
	}
	// Codex's STREAM reports no model id and no window — better empty
	// than guessed. (The window does exist in its rollout; a parser
	// without one, like this bare NewCodexParser, never sees it.)
	if ev.Usage.Window != 0 || ev.Usage.Model != "" {
		t.Fatalf("want no window/model, got %+v", ev.Usage)
	}
}

// TestCodexTurnUsageAbsent: older codex builds send `"usage":{}` or omit
// it; that must yield no reading rather than a zero-cost turn.
func TestCodexTurnUsageAbsent(t *testing.T) {
	for _, line := range []string{
		`{"type":"turn.completed"}`,
		`{"type":"turn.completed","usage":{}}`,
	} {
		ev, err := NewCodexParser().Parse(line)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if ev.Usage != nil {
			t.Fatalf("want nil usage for %s, got %+v", line, ev.Usage)
		}
	}
}
