package omp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/cliserver"
)

func TestTranslateFrameMatchesPrintMode(t *testing.T) {
	out, ok := translateFrame([]byte(`{"type":"message_update","messageId":"msg-1","message":{"big":1},"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Hi","partial":{"x":1}}}`))
	if !ok || strings.Contains(string(out), "partial") || strings.Contains(string(out), "messageId") || strings.Contains(string(out), `"big"`) || !strings.Contains(string(out), `"delta":"Hi"`) {
		t.Fatalf("message_update = %s", out)
	}
	out, _ = translateFrame([]byte(`{"type":"message_update","assistantMessageEvent":{"type":"done","reason":"stop","message":{}}}`))
	if string(out) != `{"assistantMessageEvent":{"reason":"stop","type":"done"},"type":"message_update"}`+"\n" {
		t.Fatalf("done = %s", out)
	}
	for _, typ := range []string{"response", "prompt_result", "session_settled", "ready", "extension_ui_request", "subagent_event"} {
		if _, ok := translateFrame([]byte(`{"type":"` + typ + `"}`)); ok {
			t.Fatalf("%s forwarded", typ)
		}
	}
	if out, ok := translateFrame([]byte(`{"type":"agent_end","messages":[]}`)); !ok || !strings.Contains(string(out), "agent_end") {
		t.Fatal("agent_end dropped")
	}
}

func TestBuildRPCArgsKeepsIsolation(t *testing.T) {
	ins := provider.Instance{Type: provider.TypeOMP, Name: "work", OMPConfig: &provider.OMPConfig{Profile: "wick-work"}}
	opt := provider.SpawnOptions{Workspace: "/w", ResumeID: "sid-1", ModelID: "a/b", Instance: &ins}
	got := buildRPCArgs(ins, opt, "/s/soul.md", "/s/wick-settings.yml", nil)
	want := []string{"--profile", "wick-work", "--mode", "rpc", "--no-ui", "--no-title", "--auto-approve",
		"--config", "/s/wick-settings.yml", "--cwd", "/w", "--append-system-prompt", "/s/soul.md",
		"--model", "a/b", "--resume", "sid-1"}
	if !slices.Equal(got, want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
	k1 := rpcKey("work", "s", "u", "/omp", "/w", []string{"A=1", "WICK_MCP_TOKEN=t1"}, got)
	k2 := rpcKey("work", "s", "u", "/omp", "/w", []string{"WICK_MCP_TOKEN=t2", "A=1"}, append(got[:len(got)-1:len(got)-1], "sid-2"))
	if k1 != k2 {
		t.Fatal("per-turn token / resume id split the process")
	}
	if rpcKey("work", "s", "other", "/omp", "/w", []string{"A=1"}, got) == k1 {
		t.Fatal("another caller shared the process")
	}
}

// fakeOMP is an in-process RPC peer: it answers commands the way
// rpc-mode.ts does and streams a scripted turn.
type fakeOMP struct {
	mu      sync.Mutex
	cmds    []string
	steers  []string
	aborted chan struct{}
	// hang: the turn runs until abort.
	hang bool
	// failPrompt: prompt_result error before the agent runs.
	failPrompt string
	// compactErr: the RPC `compact` fails with this error.
	compactErr   string
	compactDelay time.Duration // holds the compact answer this long (a slow model)
	out          *io.PipeWriter
	done         chan struct{}
	// state is merged into get_state's data (isStreaming, …).
	state map[string]any
}

func (f *fakeOMP) write(v any) {
	b, _ := json.Marshal(v)
	_, _ = f.out.Write(append(b, '\n'))
}

func (f *fakeOMP) serve(in io.Reader) {
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		var c map[string]any
		_ = json.Unmarshal(sc.Bytes(), &c)
		typ, _ := c["type"].(string)
		id, _ := c["id"].(string)
		f.mu.Lock()
		f.cmds = append(f.cmds, typ)
		f.mu.Unlock()
		switch typ {
		case "get_state":
			// The model object omp 18.4.4 returns carries contextWindow.
			data := map[string]any{"sessionId": "omp-s1",
				"model": map[string]any{"id": "gpt-5.6-luna", "provider": "openai-codex", "contextWindow": 272000}, "autoCompactionEnabled": true,
				"contextUsage": map[string]any{"tokens": 25900, "contextWindow": 272000, "percent": 9.5}}
			f.mu.Lock()
			for k, v := range f.state {
				data[k] = v
			}
			f.mu.Unlock()
			f.write(map[string]any{"type": "response", "id": id, "command": typ, "success": true, "data": data})
		case "compact":
			time.Sleep(f.compactDelay)
			if f.compactErr != "" {
				f.write(map[string]any{"type": "response", "id": id, "command": typ, "success": false, "error": f.compactErr})
				continue
			}
			// CompactionResult of omp's RPC `compact` (counts of the real
			// session 93c7b1e2 compaction).
			f.write(map[string]any{"type": "response", "id": id, "command": typ, "success": true,
				"data": map[string]any{"summary": "Remote compaction", "tokensBefore": 26715, "tokensAfter": 25227, "method": "remote"}})
		case "steer":
			f.mu.Lock()
			f.steers = append(f.steers, c["message"].(string))
			f.mu.Unlock()
			f.write(map[string]any{"type": "response", "id": id, "command": typ, "success": true})
		case "abort":
			f.write(map[string]any{"type": "response", "id": id, "command": typ, "success": true})
			close(f.aborted)
		case "prompt":
			if msg, _ := c["message"].(string); strings.HasPrefix(msg, "/session pin ") {
				// omp runs the slash command; its text is a command_output frame.
				acct := strings.TrimPrefix(msg, "/session pin ")
				text := "Pinned acct" + acct + " to this session for openai-codex."
				if acct == "9" {
					text = `No openai-codex account matches "9".`
				}
				f.write(map[string]any{"type": "command_output", "text": text})
				f.write(map[string]any{"type": "response", "id": id, "command": typ, "success": true})
				continue
			}
			f.write(map[string]any{"type": "response", "id": id, "command": typ, "success": true})
			go f.turn(id, c["message"].(string))
		default:
			f.write(map[string]any{"type": "response", "id": id, "command": typ, "success": true})
		}
	}
}

func (f *fakeOMP) turn(id, msg string) {
	if f.failPrompt != "" {
		f.write(map[string]any{"type": "prompt_result", "id": id, "agentInvoked": false, "status": "error", "sessionSettled": true, "error": map[string]any{"message": f.failPrompt, "retryable": false}})
		return
	}
	f.write(map[string]any{"type": "agent_start"})
	f.write(map[string]any{"type": "message_update", "messageId": "msg-1", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "echo:" + msg, "partial": map[string]any{}}})
	if f.hang {
		<-f.aborted
		f.write(map[string]any{"type": "agent_end", "messages": []any{}})
		f.write(map[string]any{"type": "prompt_result", "id": id, "agentInvoked": true, "status": "aborted", "sessionSettled": true})
		return
	}
	f.write(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "stopReason": "stop"}})
	f.write(map[string]any{"type": "agent_end", "messages": []any{}})
	f.write(map[string]any{"type": "prompt_result", "id": id, "agentInvoked": true, "status": "completed", "sessionSettled": false})
	f.write(map[string]any{"type": "session_settled"})
}

// fakeConn wires a fakeOMP to an rpcConn through pipes.
func fakeConn(f *fakeOMP) *rpcConn {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	f.out, f.done, f.aborted = outW, make(chan struct{}), make(chan struct{})
	c := &rpcConn{pid: 4242, done: f.done, w: inW, pending: map[string]chan rpcFrame{}}
	var once sync.Once
	c.kill = func() { once.Do(func() { _ = inW.Close(); _ = outW.Close(); close(f.done) }) }
	go f.serve(inR)
	ready := make(chan struct{})
	go c.readLoop(outR, ready)
	f.write(map[string]any{"type": "ready", "protocolVersion": 1})
	<-ready
	return c
}

func runFakeTurn(t *testing.T, f *fakeOMP, prompt string) (*rpcProcess, *cliserver.Manager[*rpcConn]) {
	t.Helper()
	m := cliserver.New[*rpcConn]("omp-test", 1)
	m.Every = time.Hour
	l, err := m.Acquire(context.Background(), cliserver.Spec{Instance: "o", Key: "k"}, func(context.Context) (*rpcConn, error) { return fakeConn(f), nil })
	if err != nil {
		t.Fatal(err)
	}
	p := newRPCProcess(nil, "omp", nil)
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go func() { defer cancel(); p.run(ctx, l, rpcTurnSpec{prompt: prompt, cwd: "/w"}) }()
	return p, m
}

func readAll(t *testing.T, p *rpcProcess) string {
	t.Helper()
	b, _ := io.ReadAll(p.Stdout())
	return string(b)
}

func TestRPCTurnStreamsPrintModeLines(t *testing.T) {
	f := &fakeOMP{}
	p, m := runFakeTurn(t, f, "hello")
	defer m.Shutdown()
	out := readAll(t, p)
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.Contains(lines[0], `"type":"session"`) || !strings.Contains(lines[0], `"id":"omp-s1"`) {
		t.Fatalf("no session header: %q", lines[0])
	}
	if !strings.Contains(out, `"delta":"echo:hello"`) || !strings.Contains(out, "agent_end") || strings.Contains(out, "prompt_result") || strings.Contains(out, "messageId") {
		t.Fatalf("stream = %s", out)
	}
	if p.Inject("late") == nil {
		t.Fatal("inject accepted after the turn ended")
	}
	if m.Len() != 1 {
		t.Fatal("process not kept for the next turn")
	}
}

func TestRPCTurnSteerAndAbort(t *testing.T) {
	f := &fakeOMP{hang: true}
	p, m := runFakeTurn(t, f, "long")
	defer m.Shutdown()
	r := bufio.NewReader(p.Stdout())
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, "echo:long") {
			break
		}
	}
	if err := p.Inject("more"); err != nil {
		t.Fatalf("steer: %v", err)
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("wait after kill = %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.Equal(f.steers, []string{"more"}) || !slices.Contains(f.cmds, "abort") {
		t.Fatalf("steers=%v cmds=%v", f.steers, f.cmds)
	}
	select {
	case <-f.done:
		t.Fatal("abort killed the RPC process")
	default:
	}
}

func TestRPCTurnPromptErrorIsTurnError(t *testing.T) {
	f := &fakeOMP{failPrompt: "No API key for provider"}
	p, m := runFakeTurn(t, f, "x")
	defer m.Shutdown()
	out := readAll(t, p)
	if err := p.Wait(); err != nil {
		t.Fatalf("model error surfaced as a process failure: %v", err)
	}
	if !strings.Contains(out, `"stopReason":"error"`) || !strings.Contains(out, "No API key") || !strings.Contains(out, "agent_end") {
		t.Fatalf("stream = %s", out)
	}
}

func TestUseServerDefaultsOn(t *testing.T) {
	if !useServer(provider.Instance{}, []string{"--model", "x"}) {
		t.Fatal("server mode not the default")
	}
	if useServer(provider.Instance{RunPerTurn: true}) || useServer(provider.Instance{}, []string{"--thinking", "high"}) {
		t.Fatal("run-only cases went to the RPC process")
	}
	if !provider.SupportsServerMode(provider.TypeOMP) {
		t.Fatal("omp has no server mode toggle")
	}
}

func TestRPCPinAccount(t *testing.T) {
	f := &fakeOMP{}
	c := fakeConn(f)
	defer c.kill()
	pinAccount(context.Background(), c, "omp-s1", "2")
	if c.pinned != "omp-s1#2" {
		t.Fatalf("pin not recorded: %q", c.pinned)
	}
	// omp refused the account: nothing recorded, the turn runs on Auto.
	c.pinned = ""
	pinAccount(context.Background(), c, "omp-s1", "9")
	if c.pinned != "" {
		t.Fatalf("refused pin recorded: %q", c.pinned)
	}
}

func TestRPCTurnPinsAccountOncePerSession(t *testing.T) {
	f := &fakeOMP{}
	m := cliserver.New[*rpcConn]("omp-test", 1)
	m.Every = time.Hour
	defer m.Shutdown()
	var conn *rpcConn
	for turn := 0; turn < 2; turn++ {
		l, err := m.Acquire(context.Background(), cliserver.Spec{Instance: "o", Key: "k"}, func(context.Context) (*rpcConn, error) { conn = fakeConn(f); return conn, nil })
		if err != nil {
			t.Fatal(err)
		}
		p := newRPCProcess(nil, "omp", nil)
		ctx, cancel := context.WithCancel(context.Background())
		p.cancel = cancel
		go func() { defer cancel(); p.run(ctx, l, rpcTurnSpec{prompt: "hi", cwd: "/w", account: "2"}) }()
		readAll(t, p)
		if err := p.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	prompts := 0
	for _, c := range f.cmds {
		if c == "prompt" {
			prompts++
		}
	}
	f.mu.Unlock()
	// 1 pin + 2 real prompts: the pin is not repeated for the same session.
	if prompts != 3 {
		t.Fatalf("prompts = %d, want 3 (%v)", prompts, f.cmds)
	}
}

// A turn whose process failed to start never gets a cancel func; Kill on
// it (a Stop while the error lines are still being emitted) must not panic.
func TestRPCProcessKillWithoutTurnDoesNotPanic(t *testing.T) {
	p := newRPCProcess(nil, "omp", nil)
	go func() { p.emit(errorLines("boom")); p.finish(nil) }()
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
}

// A result arriving once the turn stopped reading must not block the
// shared stdout reader.
func TestTurnSinkDoesNotBlockAfterTurnEnds(t *testing.T) {
	frames := make(chan []byte, 1)
	results := make(chan rpcFrame, 1)
	done := make(chan struct{})
	sink := turnSink(frames, results, done)
	close(done)
	returned := make(chan struct{})
	go func() {
		for i := 0; i < 3; i++ {
			sink(nil, rpcFrame{Type: "session_settled"})
			sink([]byte("x"), rpcFrame{Type: "message_update"})
		}
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("sink blocked on a full results channel after the turn ended")
	}
}

// "/compact" runs omp's RPC `compact` (never a prompt: that compacts but
// ends no turn, and the UI spun forever) and closes the turn with the
// result; every turn opens with the model's context window.
func TestRPCCompactTurn(t *testing.T) {
	f := &fakeOMP{}
	p, m := runFakeTurn(t, f, "/compact")
	defer m.Shutdown()
	out := readAll(t, p)
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"context"`, `"window":272000`, `"autoCompact":true`, `"type":"compaction"`, `"tokensBefore":26715`, `"tokensAfter":25900`, `"type":"agent_end"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in\n%s", want, out)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if slices.Contains(f.cmds, "prompt") || !slices.Contains(f.cmds, "compact") {
		t.Fatalf("commands %v", f.cmds)
	}
}

// A compact slower than the per-command wait still completes: omp answers
// only once the summary is written (session 292e2e51 got "no response in
// 30s" for a compaction omp went on to finish).
func TestRPCCompactOutlivesCallWait(t *testing.T) {
	oldCall, oldPrompt := rpcCallWait, rpcPromptWait
	rpcCallWait, rpcPromptWait = 100*time.Millisecond, 150*time.Millisecond
	t.Cleanup(func() { rpcCallWait, rpcPromptWait = oldCall, oldPrompt })
	// Slower than both the per-call bound and the prompt watchdog: a
	// compact sends no prompt, so the watchdog must not end it.
	f := &fakeOMP{compactDelay: 400 * time.Millisecond}
	p, m := runFakeTurn(t, f, "/compact")
	defer m.Shutdown()
	out := readAll(t, p)
	if err := p.Wait(); err != nil {
		t.Fatalf("slow compact failed the turn: %v", err)
	}
	if !strings.Contains(out, `"type":"compaction"`) {
		t.Fatalf("no compaction after a slow compact:\n%s", out)
	}
}

// omp refusing a second /compact ("Already compacted") ends the turn with
// a neutral notice, never an error bubble.
func TestRPCCompactAlreadyCompacted(t *testing.T) {
	f := &fakeOMP{compactErr: "Already compacted"}
	p, m := runFakeTurn(t, f, "/compact")
	defer m.Shutdown()
	out := readAll(t, p)
	if err := p.Wait(); err != nil {
		t.Fatalf("no-op compact failed the turn: %v", err)
	}
	if !strings.Contains(out, "Nothing to compact — already compacted") || strings.Contains(out, `"type":"compaction"`) ||
		strings.Contains(out, `"stopReason":"error"`) || !strings.Contains(out, `"type":"agent_end"`) {
		t.Fatalf("out:\n%s", out)
	}
	parser := event.NewOMPParser("omp")
	var types []event.EventType
	for _, l := range strings.Split(out, "\n") {
		if l == "" {
			continue
		}
		ev, _ := parser.Parse(l)
		types = append(types, ev.Type)
	}
	if slices.Contains(types, event.Error) || !slices.Contains(types, event.TextDelta) || !slices.Contains(types, event.Done) {
		t.Fatalf("events %v", types)
	}
}

func TestCompactNoop(t *testing.T) {
	for _, msg := range []string{"Already compacted", "omp: ALREADY COMPACTED.", "Nothing to compact (session too small)"} {
		if _, ok := compactNoop(msg); !ok {
			t.Errorf("%q not a no-op", msg)
		}
	}
	if _, ok := compactNoop("Compaction already in progress"); ok {
		t.Error("in-progress compaction read as a no-op")
	}
}

func TestCompactPrompt(t *testing.T) {
	for in, want := range map[string]string{"/compact": "", " /COMPACT ": "", "/compact keep the API notes": "keep the API notes"} {
		if got, ok := compactPrompt(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"/compaction", "please /compact", "hello"} {
		if _, ok := compactPrompt(in); ok {
			t.Errorf("%q treated as /compact", in)
		}
	}
}

// A turn whose stream nobody reads (its agent's reader left on a
// cancelled ctx) blocks on its first write, before the prompt. It must
// end within rpcPromptWait and give the lease back, so the idle process
// can be yielded instead of counting as busy forever.
func TestRPCTurnNeverPromptedReleasesLease(t *testing.T) {
	old := rpcPromptWait
	rpcPromptWait = 200 * time.Millisecond
	defer func() { rpcPromptWait = old }()
	f := &fakeOMP{}
	p, m := runFakeTurn(t, f, "hello")
	defer m.Shutdown()
	done := make(chan struct{})
	go func() { _ = p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("unread turn still holds its lease")
	}
	f.mu.Lock()
	prompted := slices.Contains(f.cmds, "prompt")
	f.mu.Unlock()
	if prompted {
		t.Fatal("turn sent its prompt although its stream was never read")
	}
	waitUntil(t, func() bool { return m.RetireIdle(nil) == 1 })
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
