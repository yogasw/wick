package event

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// feed runs every line of a fixture through p and returns the non-Unknown
// events in order.
func feed(t *testing.T, p Parser, path string) []AgentEvent {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []AgentEvent
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		evs, err := ParseLine(p, sc.Text())
		if err != nil {
			t.Fatalf("parse %q: %v", sc.Text(), err)
		}
		for _, ev := range evs {
			if ev.Type != Unknown {
				out = append(out, ev)
			}
		}
	}
	return out
}

func types(evs []AgentEvent) string {
	var s []string
	for _, e := range evs {
		s = append(s, e.Type.String())
	}
	return strings.Join(s, ",")
}

func TestOMPParserFixture(t *testing.T) {
	evs := feed(t, NewOMPParser("omp-2"), "testdata/omp_turn.jsonl")
	want := "session_start,thinking,tool_use,tool_result,text_delta,text_delta,trace,trace,done"
	if got := types(evs); got != want {
		t.Fatalf("types\n got %s\nwant %s", got, want)
	}
	if evs[0].SessionID != "0197aa-omp-sid" {
		t.Errorf("session id = %q", evs[0].SessionID)
	}
	if evs[2].ToolName != "bash" || evs[2].ToolUseID != "call_1" || evs[2].ToolInput != `{"command":"ls"}` {
		t.Errorf("tool use = %+v", evs[2])
	}
	if evs[3].Text != "a.go" || evs[3].ToolUseID != "call_1" || evs[3].IsError {
		t.Errorf("tool result = %+v", evs[3])
	}
	if evs[4].Text+evs[5].Text != "Hello" {
		t.Errorf("text = %q", evs[4].Text+evs[5].Text)
	}
	done := evs[len(evs)-1]
	u := done.Usage
	if u == nil || u.Input != 120 || u.Output != 15 || u.CacheRead != 210 || u.CacheWrite != 5 || u.ContextUsed != 180 || u.Model != "gpt-5.2" {
		t.Fatalf("usage = %+v", u)
	}
}

func TestOMPParserLimitErrorNamesInstance(t *testing.T) {
	p := NewOMPParser("omp-work")
	lines := []string{
		`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"429 You have hit your usage limit"}}`,
		`{"type":"agent_end","messages":[]}`,
	}
	var last AgentEvent
	for _, l := range lines {
		ev, _ := p.Parse(l)
		last = ev
	}
	if last.Type != Error || !strings.Contains(last.ErrorMsg, "omp/omp-work") || !strings.Contains(last.ErrorMsg, "limit") {
		t.Fatalf("got %+v", last)
	}
	// Parser state reset: next clean run ends in Done.
	ev, _ := p.Parse(`{"type":"agent_end","messages":[]}`)
	if ev.Type != Done {
		t.Fatalf("after reset got %v", ev.Type)
	}
}

func TestOMPParserNonLimitErrorUnchanged(t *testing.T) {
	p := NewOMPParser("x")
	p.Parse(`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"boom"}}`)
	ev, _ := p.Parse(`{"type":"agent_end","messages":[]}`)
	if ev.Type != Error || ev.ErrorMsg != "boom" {
		t.Fatalf("got %+v", ev)
	}
}

func TestOpencodeParserFixture(t *testing.T) {
	evs := feed(t, NewOpencodeParser("oc"), "testdata/opencode_turn.jsonl")
	want := "session_start,thinking,tool_use,tool_result,tool_use,tool_result,text_delta,done"
	if got := types(evs); got != want {
		t.Fatalf("types\n got %s\nwant %s", got, want)
	}
	if evs[0].SessionID != "ses_abc" {
		t.Errorf("sid = %q", evs[0].SessionID)
	}
	if evs[2].ToolUseID != "c1" || evs[3].ToolUseID != "c1" || evs[3].Text != "a.go" || evs[2].ToolInput != `{"command":"ls"}` {
		t.Errorf("tool pair = %+v / %+v", evs[2], evs[3])
	}
	if !evs[5].IsError || evs[5].Text != "ENOENT" {
		t.Errorf("tool error = %+v", evs[5])
	}
	u := evs[len(evs)-1].Usage
	if u == nil || u.Input != 120 || u.Output != 17 || u.CacheRead != 210 || u.CacheWrite != 5 || u.ContextUsed != 180 || u.CostUSD < 0.029 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestOpencodeParserErrorEndsTurnOnce(t *testing.T) {
	p := NewOpencodeParser("oc-2")
	evs, _ := p.ParseAll(`{"type":"error","sessionID":"s","error":{"name":"APIError","data":{"message":"Rate limit exceeded"}}}`)
	if len(evs) != 2 || evs[0].Type != SessionStart || evs[1].Type != Error || !strings.Contains(evs[1].ErrorMsg, "opencode/oc-2") {
		t.Fatalf("got %+v", evs)
	}
	evs, _ = p.ParseAll(`{"type":"step_finish","sessionID":"s","part":{"type":"step-finish","reason":"error"}}`)
	for _, e := range evs {
		if e.Type == Done {
			t.Fatalf("Done after Error: %+v", evs)
		}
	}
}

func TestParseLinePlainParser(t *testing.T) {
	evs, err := ParseLine(NewClaudeParser(), "")
	if err != nil || len(evs) != 1 {
		t.Fatalf("got %v %v", evs, err)
	}
}
