package event

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// feedWith parses pre (wick's own lines) then the fixture.
func feedWith(t *testing.T, p Parser, pre []string, path string) []AgentEvent {
	t.Helper()
	var out []AgentEvent
	for _, l := range pre {
		evs, err := ParseLine(p, l)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, evs...)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		evs, err := ParseLine(p, sc.Text())
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, evs...)
	}
	return out
}

// The context line wick emits from the running server (omp get_state
// model.contextWindow / opencode /provider limit.context) puts a scale on
// the turn's usage, so the meter shows a percentage.
func TestContextLineSetsWindow(t *testing.T) {
	ctxLine := strings.TrimSpace(string(ContextLine(272000)))
	for name, c := range map[string]struct {
		p       Parser
		fixture string
	}{
		"omp":      {NewOMPParser("omp-2"), "testdata/omp_turn.jsonl"},
		"opencode": {NewOpencodeParser("oc"), "testdata/opencode_turn.jsonl"},
	} {
		evs := feedWith(t, c.p, []string{ctxLine}, c.fixture)
		u := evs[len(evs)-1].Usage
		if u == nil || u.Window != 272000 || u.ContextUsed != 180 {
			t.Errorf("%s: usage %+v", name, u)
		}
	}
}

// omp's own auto-compaction, with the result shape of omp 18.4.4
// (auto_compaction_end{action,result{tokensBefore,tokensAfter},aborted})
// and the counts of the real session 93c7b1e2's compaction entry.
func TestOMPAutoCompactionCarriesCounts(t *testing.T) {
	p := NewOMPParser("waba")
	ev, err := p.Parse(`{"type":"auto_compaction_end","action":"threshold","result":{"summary":"Remote compaction preserved provider-native history","tokensBefore":26715,"tokensAfter":25227,"method":"remote"},"aborted":false,"willRetry":false}`)
	if err != nil || ev.Type != Compaction || ev.Compaction == nil {
		t.Fatalf("ev %+v err %v", ev, err)
	}
	c := ev.Compaction
	if c.Trigger != "auto" || c.PreTokens != 26715 || c.PostTokens != 25227 || c.DroppedTokens != 1488 {
		t.Fatalf("compaction %+v", c)
	}
	if got := c.Summary(); got != "Context compacted (auto) — 26.7k → 25.2k tokens" {
		t.Fatalf("summary %q", got)
	}
	// An aborted one is not a compaction.
	if ev, _ := p.Parse(`{"type":"auto_compaction_end","action":"context-full","aborted":true,"willRetry":false}`); ev.Type == Compaction {
		t.Fatal("aborted compaction reported")
	}
}

// The /compact turn wick runs through the CLI's official compaction ends
// with its compaction line and then the turn's end: a notice, then Done —
// the UI stops "Compacting…".
func TestManualCompactionTurn(t *testing.T) {
	line := strings.TrimSpace(string(CompactionLine("manual", 26715, 25227)))
	p := NewOMPParser("waba")
	ev, _ := p.Parse(line)
	if ev.Type != Compaction || ev.Compaction.Trigger != "manual" || ev.Compaction.PreTokens != 26715 {
		t.Fatalf("omp: %+v", ev)
	}
	if ev, _ := p.Parse(`{"type":"agent_end","messages":[]}`); ev.Type != Done {
		t.Fatalf("omp end: %+v", ev)
	}
	op := NewOpencodeParser("oc")
	evs, _ := op.ParseAll(strings.TrimSpace(string(CompactionLine("manual", 0, 0))))
	if len(evs) != 1 || evs[0].Type != Compaction || evs[0].Compaction.Summary() != "Context compacted (manual)" {
		t.Fatalf("opencode: %+v", evs)
	}
	evs, _ = op.ParseAll(`{"type":"step_finish","sessionID":"s","part":{"type":"step-finish","reason":"stop","sessionID":"s"}}`)
	if len(evs) == 0 || evs[len(evs)-1].Type != Done {
		t.Fatalf("opencode end: %+v", evs)
	}
}

// omp's RPC compact can answer with tokensBefore alone (remote compaction on
// the provider side). The notice must not claim the context went to zero.
func TestCompactionSummaryUnknownAfter(t *testing.T) {
	c := &CompactionInfo{Trigger: "manual", PreTokens: 20943}
	if got := c.Summary(); got != "Context compacted (manual) — was 20.9k tokens" {
		t.Fatalf("summary = %q", got)
	}
}

// A compaction carries its "after" as the context level, so the live meter
// drops with the notice instead of holding the pre-compaction reading until
// the next turn ends; an unknown after says nothing (0).
func TestCompactionEventCarriesContextLevel(t *testing.T) {
	line := strings.TrimSpace(string(CompactionLine("manual", 68800, 25900)))
	if ev, _ := NewOMPParser("omp").Parse(line); ev.ContextUsed != 25900 {
		t.Fatalf("omp manual: %+v", ev)
	}
	if ev, _ := NewOMPParser("omp").Parse(`{"type":"auto_compaction_end","result":{"tokensBefore":26715,"tokensAfter":25227},"aborted":false}`); ev.ContextUsed != 25227 {
		t.Fatalf("omp auto: %+v", ev)
	}
	evs, _ := NewOpencodeParser("oc").ParseAll(strings.TrimSpace(string(CompactionLine("manual", 66653, 41677))))
	if len(evs) != 1 || evs[0].ContextUsed != 41677 {
		t.Fatalf("opencode: %+v", evs)
	}
	if ev, _ := NewOMPParser("omp").Parse(strings.TrimSpace(string(CompactionLine("manual", 68800, 0)))); ev.ContextUsed != 0 {
		t.Fatalf("unknown after: %+v", ev)
	}
	ci := &CompactionInfo{Trigger: "manual", PreTokens: 68800, PostTokens: 25900}
	if got := ci.Summary(); got != "Context compacted (manual) — 68.8k → 25.9k tokens" {
		t.Fatalf("summary %q", got)
	}
}

// The provider's auto-compact state rides the same line onto the turn's
// usage; a line without it leaves AutoCompact nil (unknown, not "off").
func TestContextLineCarriesAutoCompact(t *testing.T) {
	off := false
	for name, c := range map[string]struct {
		p       Parser
		fixture string
	}{
		"omp":      {NewOMPParser("omp-2"), "testdata/omp_turn.jsonl"},
		"opencode": {NewOpencodeParser("oc"), "testdata/opencode_turn.jsonl"},
	} {
		line := strings.TrimSpace(string(ContextStateLine(0, &off)))
		evs := feedWith(t, c.p, []string{line}, c.fixture)
		u := evs[len(evs)-1].Usage
		if u == nil || u.AutoCompact == nil || *u.AutoCompact {
			t.Errorf("%s: usage %+v, want AutoCompact=false", name, u)
		}
	}
	if s := string(ContextLine(5)); strings.Contains(s, "autoCompact") {
		t.Fatalf("unknown state must be left out: %s", s)
	}
}
