package event

import (
	"testing"
	"time"
)

// The meter used to move only when a turn ended — which is the moment it
// stops being the interesting number. Claude reports the level on every
// assistant frame, so every frame that carries one now says so.
func TestClaudeStampsTheLevelMidTurn(t *testing.T) {
	p := NewClaudeParser()
	line := `{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":1200,"cache_read_input_tokens":300000},` +
		`"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`
	ev, err := p.Parse(line)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != ToolUse {
		t.Fatalf("type = %v, want ToolUse", ev.Type)
	}
	if ev.ContextUsed != 301200 {
		t.Fatalf("context used = %d, want the frame's whole input", ev.ContextUsed)
	}
}

// The frame carrying the newest level is often one nothing renders: the
// trailing copy of text that already streamed. Dropping its reading
// would leave the meter a request behind for the rest of the turn.
func TestClaudeStampsTheLevelOnASuppressedFrame(t *testing.T) {
	p := NewClaudeParser()
	// A streamed delta first, so the trailing assistant text is suppressed.
	if _, err := p.Parse(`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}}`); err != nil {
		t.Fatal(err)
	}
	ev, err := p.Parse(`{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":50,"cache_read_input_tokens":400},"content":[{"type":"text","text":"hi"}]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != Unknown {
		t.Fatalf("type = %v, want the frame suppressed", ev.Type)
	}
	if ev.ContextUsed != 450 {
		t.Fatalf("context used = %d, want the level to survive suppression", ev.ContextUsed)
	}
}

// Zero means "this frame said nothing about the window", never "the
// window is empty" — a consumer that drew the zero would show a drop
// that never happened.
func TestClaudeLeavesTheLevelUnsetWhenAFrameCarriesNone(t *testing.T) {
	p := NewClaudeParser()
	ev, err := p.Parse(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"no usage here"}]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if ev.ContextUsed != 0 {
		t.Fatalf("context used = %d, want 0", ev.ContextUsed)
	}
}

// The live reading and the one the turn finally records are the same
// number from the same place — the last request's level. If they could
// drift, the meter would jump when the turn ended.
func TestClaudeLiveLevelMatchesTheEndOfTurnLevel(t *testing.T) {
	p := NewClaudeParser()
	last, err := p.Parse(`{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":2000,"cache_read_input_tokens":90000},"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`)
	if err != nil {
		t.Fatal(err)
	}
	done, err := p.Parse(`{"type":"result","subtype":"success","is_error":false,"result":"ok",` +
		`"usage":{"input_tokens":999999,"cache_read_input_tokens":999999},` +
		`"modelUsage":{"claude-opus-5":{"contextWindow":1000000,"inputTokens":2000,"outputTokens":40,"cacheReadInputTokens":90000}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if done.Usage == nil {
		t.Fatal("the result frame carried no usage")
	}
	if done.Usage.ContextUsed != last.ContextUsed {
		t.Fatalf("end-of-turn level %d != last live reading %d", done.Usage.ContextUsed, last.ContextUsed)
	}
}

// Codex's stream carries no usage until the turn ends, so its level is
// read beside the stream — throttled, because the journal is a file.
func TestCodexStampsTheLevelFromTheRollout(t *testing.T) {
	p := NewCodexParserIn("/nowhere")
	p.threadID = "thread-1"
	reads := 0
	p.newest = func(home, threadID string) (codexRolloutReading, bool) {
		reads++
		return codexRolloutReading{Level: 18874, Window: 258400}, true
	}
	at := time.Now()
	p.now = func() time.Time { return at }

	ev, err := p.Parse(`{"type":"item.started","item":{"id":"i1","item_type":"command_execution","command":"ls"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if ev.ContextUsed != 18874 {
		t.Fatalf("context used = %d, want the rollout's level", ev.ContextUsed)
	}

	// Straight away again: the throttle holds, and the frame carries no
	// reading rather than a stale one being re-read from disk.
	ev2, err := p.Parse(`{"type":"item.started","item":{"id":"i2","item_type":"command_execution","command":"ls"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if ev2.ContextUsed != 0 || reads != 1 {
		t.Fatalf("second frame read the journal again: used=%d reads=%d", ev2.ContextUsed, reads)
	}

	// Past the interval it reads again.
	at = at.Add(codexLiveInterval + time.Second)
	if _, err := p.Parse(`{"type":"item.started","item":{"id":"i3","item_type":"command_execution","command":"ls"}}`); err != nil {
		t.Fatal(err)
	}
	if reads != 2 {
		t.Fatalf("reads = %d, want the throttle to have elapsed", reads)
	}
}

// Before thread.started there is no journal to name, and asking for one
// would glob the filesystem on every line of every spawn.
func TestCodexReadsNothingBeforeTheThreadIsKnown(t *testing.T) {
	p := NewCodexParserIn("/nowhere")
	p.newest = func(home, threadID string) (codexRolloutReading, bool) {
		t.Fatal("the journal was read without a thread id")
		return codexRolloutReading{}, false
	}
	if _, err := p.Parse(`{"type":"turn.started"}`); err != nil {
		t.Fatal(err)
	}
}
