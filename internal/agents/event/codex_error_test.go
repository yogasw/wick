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
