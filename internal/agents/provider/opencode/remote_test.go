package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
)

func ev(typ string, props any) sseEvent {
	b, _ := json.Marshal(props)
	return sseEvent{Type: typ, Properties: b}
}

func part(p map[string]any) sseEvent { return ev("message.part.updated", map[string]any{"part": p}) }

// The translated lines must parse like `opencode run --format json`.
func TestTranslatorMatchesRunFormat(t *testing.T) {
	tr := newTranslator("ses_1")
	evs := []sseEvent{
		ev("message.updated", map[string]any{"info": map[string]any{"id": "msg_u", "role": "user", "sessionID": "ses_1"}}),
		part(map[string]any{"type": "text", "text": "the prompt", "sessionID": "ses_1", "messageID": "msg_u", "time": map[string]any{"end": 1}}),
		ev("session.status", map[string]any{"sessionID": "ses_1", "status": map[string]any{"type": "busy"}}),
		part(map[string]any{"type": "step-start", "sessionID": "ses_1", "messageID": "msg_a"}),
		part(map[string]any{"type": "text", "text": "", "sessionID": "ses_1", "messageID": "msg_a", "time": map[string]any{"start": 1}}),
		part(map[string]any{"type": "text", "text": "other session", "sessionID": "ses_2", "messageID": "msg_x", "time": map[string]any{"end": 2}}),
		part(map[string]any{"type": "tool", "tool": "bash", "callID": "c1", "sessionID": "ses_1", "messageID": "msg_a", "state": map[string]any{"status": "running"}}),
		part(map[string]any{"type": "tool", "tool": "bash", "callID": "c1", "sessionID": "ses_1", "messageID": "msg_a", "state": map[string]any{"status": "completed", "input": map[string]any{"command": "ls"}, "output": "a.txt"}}),
		part(map[string]any{"type": "step-finish", "reason": "tool-calls", "sessionID": "ses_1", "messageID": "msg_a", "tokens": map[string]any{"input": 1, "output": 1}}),
		part(map[string]any{"type": "text", "text": "hi there", "sessionID": "ses_1", "messageID": "msg_b", "time": map[string]any{"start": 1, "end": 2}}),
		part(map[string]any{"type": "step-finish", "reason": "stop", "sessionID": "ses_1", "messageID": "msg_b", "tokens": map[string]any{"input": 10, "output": 2}}),
		ev("session.idle", map[string]any{"sessionID": "ses_2"}),
	}
	var out []string
	for _, e := range evs {
		lines, done := tr.feed(e)
		if done {
			t.Fatalf("turn ended early at %s", e.Type)
		}
		for _, l := range lines {
			out = append(out, strings.TrimSpace(string(l)))
		}
	}
	if _, done := tr.feed(ev("session.status", map[string]any{"sessionID": "ses_1", "status": map[string]any{"type": "idle"}})); !done {
		t.Fatal("idle of our session did not end the turn")
	}
	var kinds []string
	for _, l := range out {
		var m struct{ Type, SessionID string }
		if err := json.Unmarshal([]byte(l), &m); err != nil || m.SessionID != "ses_1" {
			t.Fatalf("bad line %s", l)
		}
		kinds = append(kinds, m.Type)
	}
	if got := strings.Join(kinds, ","); got != "step_start,tool_running,tool_use,step_finish,text,step_finish" {
		t.Fatalf("kinds = %s", got)
	}

	p := event.NewOpencodeParser("oc")
	var types []event.EventType
	var text string
	for _, l := range out {
		es, _ := p.ParseAll(l)
		for _, e := range es {
			types = append(types, e.Type)
			if e.Type == event.TextDelta {
				text += e.Text
			}
		}
	}
	if text != "hi there" {
		t.Fatalf("text = %q", text)
	}
	// The started frame announces the tool; the finished one only closes
	// it, so the call is not reported twice.
	uses, results := 0, 0
	for _, ty := range types {
		switch ty {
		case event.ToolUse:
			uses++
		case event.ToolResult:
			results++
		}
	}
	if uses != 1 || results != 1 {
		t.Fatalf("tool events: %d use, %d result, want 1 and 1 (%v)", uses, results, types)
	}
	if types[0] != event.SessionStart || types[len(types)-1] != event.Done {
		t.Fatalf("event types = %v", types)
	}
}

// An idle seen before the session ever went busy is the previous turn's.
func TestTranslatorIgnoresIdleBeforeBusy(t *testing.T) {
	tr := newTranslator("ses_1")
	if _, done := tr.feed(ev("session.idle", map[string]any{"sessionID": "ses_1"})); done {
		t.Fatal("ended on a stale idle")
	}
	lines, _ := tr.feed(ev("session.error", map[string]any{"sessionID": "ses_1", "error": map[string]any{"name": "APIError", "data": map[string]any{"message": "nope"}}}))
	if len(lines) != 1 || !strings.Contains(string(lines[0]), `"type":"error"`) {
		t.Fatalf("error line = %q", lines)
	}
	if _, done := tr.feed(ev("session.idle", map[string]any{"sessionID": "ses_1"})); !done {
		t.Fatal("idle after an error did not end the turn")
	}
}

func TestReadSSE(t *testing.T) {
	in := "data: {\"type\":\"a\",\"properties\":{}}\n\n: comment\n\ndata: {\"type\":\"b\",\"properties\":{\"x\":1}}\n\n"
	var got []string
	if err := readSSE(strings.NewReader(in), func(e sseEvent) bool { got = append(got, e.Type); return true }); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("got %v", got)
	}
}

func TestPromptBodyToolOrder(t *testing.T) {
	b := string(promptBody(turnSpec{model: "openai/gpt-5.5", system: "soul", prompt: "hi", mcpName: "wick_abc"}))
	deny, allow := strings.Index(b, `"wick_*":false`), strings.Index(b, `"wick_abc_*":true`)
	if deny < 0 || allow < 0 || deny > allow {
		t.Fatalf("deny-all must precede allow-mine: %s", b)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(b), &m); err != nil {
		t.Fatalf("not JSON: %v %s", err, b)
	}
	if m["system"] != "soul" || m["model"].(map[string]any)["modelID"] != "gpt-5.5" {
		t.Fatalf("body = %s", b)
	}
	if n := sessionMCPName("s1"); n != sessionMCPName("s1") || n == sessionMCPName("s2") || !strings.HasPrefix(n, "wick_") {
		t.Fatalf("mcp name %q", n)
	}
}

// fakeOpencode is a minimal opencode server: sessions, prompt_async that
// streams a canned turn (or hangs until abort), abort, mcp.
type fakeOpencode struct {
	summarized []string // POST /session/{id}/summarize bodies
	noSummary  bool     // summarize writes no summary message
	mu         sync.Mutex
	subs       []chan string
	hang       bool
	aborted    []string
	mcp        []string
	created    int
	prompts    []string
	password   string

	// summarizeErr, when set, fails summarize with it (HTTP 400).
	summarizeErr string
	// summarizeDelay holds the summarize answer this long (a slow model).
	summarizeDelay time.Duration
	// promptStatus, when set, is how prompt_async answers instead of 204.
	promptStatus int
	// gate, when set, holds the first prompt's idle until closed; later
	// prompts join that run (as opencode does for a busy session).
	gate chan struct{}
	// storeDelay delays storing (publishing) the FIRST prompt's user
	// message, as a fresh server does while it loads its catalog.
	storeDelay time.Duration
	// stored is the order user messages were stored in (prompt texts).
	stored []string
	// silent: the run goes busy then idle with nothing in between.
	silent bool
	// silentNoEvents: the prompt is accepted and then NOTHING is published.
	silentNoEvents bool
	// config is GET /config's body ("" = "{}").
	config string
	// joined tracks prompts that joined the gated run: like opencode, the
	// run goes idle only once every joined message has been answered.
	joined sync.WaitGroup

	// midTurn, when set, plays the run after the prompt's user message and
	// busy frame instead of the canned reply.
	midTurn func(sid string)
	// onSubscribe, when set, runs as the n-th GET /event (1-based) opens.
	onSubscribe func(n int)
	subscribed  int
	// status is GET /session/status's type for every session ("" = none).
	status string
	// messages is GET /session/{id}/message's body ("" = the token fixture).
	messages string
}

// dropStreams ends every open GET /event, as a server or proxy dropping
// the connection does.
func (f *fakeOpencode) dropStreams() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.subs {
		close(c)
	}
	f.subs = nil
}

func (f *fakeOpencode) setStatus(s string) {
	f.mu.Lock()
	f.status = s
	f.mu.Unlock()
}

func (f *fakeOpencode) publish(typ string, props any) {
	b, _ := json.Marshal(map[string]any{"type": typ, "properties": props})
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.subs {
		c <- "data: " + string(b) + "\n\n"
	}
}

func (f *fakeOpencode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if u, p, ok := r.BasicAuth(); !ok || u != serverUser || p != f.password {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/event":
		c := make(chan string, 64)
		f.mu.Lock()
		f.subs = append(f.subs, c)
		f.subscribed++
		n, hook := f.subscribed, f.onSubscribe
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		if hook != nil {
			go hook(n)
		}
		for {
			select {
			case s, ok := <-c:
				if !ok {
					return
				}
				_, _ = io.WriteString(w, s)
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	case r.URL.Path == "/session/status" && r.Method == http.MethodGet:
		f.mu.Lock()
		st := f.status
		f.mu.Unlock()
		out := map[string]any{}
		if st != "" {
			for i := 1; i <= 3; i++ {
				out[fmt.Sprintf("ses_new%d", i)] = map[string]string{"type": st}
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.URL.Path == "/session" && r.Method == http.MethodPost:
		f.mu.Lock()
		f.created++
		id := fmt.Sprintf("ses_new%d", f.created)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
	case r.URL.Path == "/mcp":
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.mcp = append(f.mcp, string(b))
		f.mu.Unlock()
		_, _ = io.WriteString(w, "true")
	case strings.HasPrefix(r.URL.Path, "/mcp/"):
		_, _ = io.WriteString(w, "true")
	case strings.HasSuffix(r.URL.Path, "/prompt_async"):
		sid := strings.Split(r.URL.Path, "/")[2]
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.prompts = append(f.prompts, string(b))
		hang, status, gate, first := f.hang, f.promptStatus, f.gate, len(f.prompts) == 1
		f.mu.Unlock()
		if status != 0 {
			http.Error(w, `{"name":"UnknownError"}`, status)
			return
		}
		// Registered before the 204, so a caller that returns from the
		// prompt call can never race the run's idle past this answer.
		joins := gate != nil && !first
		if joins {
			f.joined.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
		var body struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		}
		_ = json.Unmarshal(b, &body)
		text := ""
		if len(body.Parts) > 0 {
			text = body.Parts[0].Text
		}
		f.mu.Lock()
		n, delay, silent := len(f.prompts), f.storeDelay, f.silent
		quiet := f.silentNoEvents
		f.mu.Unlock()
		go func() {
			if quiet {
				return
			}
			if joins {
				defer f.joined.Done() // every path, incl. hang/silent
			}
			if first && delay > 0 {
				time.Sleep(delay)
			}
			f.mu.Lock()
			f.stored = append(f.stored, text)
			f.mu.Unlock()
			f.publish("message.updated", map[string]any{"info": map[string]any{"id": fmt.Sprintf("msg_user%d", n), "role": "user", "sessionID": sid}})
			f.publish("session.status", map[string]any{"sessionID": sid, "status": map[string]any{"type": "busy"}})
			if silent {
				f.publish("session.status", map[string]any{"sessionID": sid, "status": map[string]any{"type": "idle"}})
				return
			}
			if f.midTurn != nil {
				f.midTurn(sid)
				return
			}
			if hang {
				return
			}
			f.publish("message.part.updated", map[string]any{"part": map[string]any{"type": "text", "text": "hello", "sessionID": sid, "messageID": "m", "time": map[string]any{"end": 1}}})
			f.publish("message.part.updated", map[string]any{"part": map[string]any{"type": "step-finish", "reason": "stop", "sessionID": sid, "messageID": "m"}})
			if gate != nil {
				if joins {
					return
				}
				<-gate
				f.joined.Wait()
			}
			f.publish("session.idle", map[string]any{"sessionID": sid})
		}()
	case strings.HasSuffix(r.URL.Path, "/abort"):
		sid := strings.Split(r.URL.Path, "/")[2]
		f.mu.Lock()
		f.aborted = append(f.aborted, sid)
		f.mu.Unlock()
		_, _ = io.WriteString(w, "true")
		go f.publish("session.idle", map[string]any{"sessionID": sid})
	case strings.HasSuffix(r.URL.Path, "/summarize") && r.Method == http.MethodPost:
		time.Sleep(f.summarizeDelay)
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.summarized = append(f.summarized, r.URL.Path+" "+string(b))
		serr := f.summarizeErr
		f.mu.Unlock()
		if serr != "" {
			http.Error(w, `{"name":"UnknownError","data":{"message":"`+serr+`"}}`, http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, "true")
	case strings.HasSuffix(r.URL.Path, "/message") && r.Method == http.MethodGet:
		f.mu.Lock()
		done := len(f.summarized) > 0 && !f.noSummary
		stored := f.messages
		f.mu.Unlock()
		if stored != "" {
			_, _ = io.WriteString(w, stored)
			return
		}
		// m2 = the first turn (system prompt + tools + one prompt: the
		// overhead floor), m4 = the last turn before /compact.
		msgs := `[{"info":{"id":"m1","role":"user","summary":{"diffs":[]}}},{"info":{"id":"m2","role":"assistant","tokens":{"input":1051,"output":300,"cache":{"read":40000,"write":0}}}},` +
			`{"info":{"id":"m4","role":"assistant","tokens":{"input":9000,"output":300,"cache":{"read":81000,"write":0}}}}`
		if done {
			msgs += `,{"info":{"id":"m3","role":"assistant","summary":true,"tokens":{"input":41051,"output":1800,"cache":{"read":0,"write":0}}}}`
		}
		_, _ = io.WriteString(w, msgs+"]")
	case r.URL.Path == "/config" && r.Method == http.MethodGet:
		f.mu.Lock()
		cfg := f.config
		f.mu.Unlock()
		if cfg == "" {
			cfg = "{}"
		}
		_, _ = io.WriteString(w, cfg)
	case r.URL.Path == "/provider" && r.Method == http.MethodGet:
		_, _ = io.WriteString(w, `{"all":[{"id":"openai","name":"OpenAI","models":{"gpt-5.5":{"id":"gpt-5.5","name":"GPT-5.5","limit":{"context":400000,"output":128000}}}}],"connected":["openai"],"default":{}}`)
	case strings.HasPrefix(r.URL.Path, "/session/") && r.Method == http.MethodGet:
		sid := strings.Split(r.URL.Path, "/")[2]
		if sid == "ses_known" {
			_ = json.NewEncoder(w).Encode(map[string]string{"id": sid})
			return
		}
		http.Error(w, `{"name":"NotFoundError"}`, http.StatusNotFound)
	default:
		http.NotFound(w, r)
	}
}

// startFake runs one turn against a fake server through a real lease.
func startFake(t *testing.T, f *fakeOpencode, ts turnSpec) (*remoteProcess, *fakeStarter) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	fs := &fakeStarter{}
	m := newManager(func(ctx context.Context, spec serverSpec, pw string) (*serverHandle, error) {
		h, err := fs.start(ctx, spec, pw)
		if err == nil {
			h.url = srv.URL
			f.mu.Lock()
			f.password = pw
			f.mu.Unlock()
		}
		return h, err
	})
	m.Every = time.Hour
	t.Cleanup(m.shutdown)
	l, err := m.acquire(context.Background(), spec(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	p := newRemoteProcess(nil, "opencode", nil, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go func() { defer cancel(); p.run(ctx, l, ts) }()
	return p, fs
}

func readAll(t *testing.T, p *remoteProcess) []string {
	t.Helper()
	var lines []string
	sc := bufio.NewScanner(p.Stdout())
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

func TestRemoteTurnNewSessionWithMCP(t *testing.T) {
	f := &fakeOpencode{}
	p, _ := startFake(t, f, turnSpec{title: "wick s1", model: "openai/gpt-5.5", prompt: "hi", resumeID: "ses_gone",
		mcpName: "wick_abc", mcpURL: "http://127.0.0.1:1/mcp", mcpToken: "tok"})
	lines := readAll(t, p)
	if err := p.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	// The unknown resume id is said out loud before the reply.
	// Then the model's window from the running server, then the reply.
	if len(lines) != 4 || !strings.Contains(lines[0], "could not find the session") || !strings.Contains(lines[0], `"sessionID":"ses_new1"`) ||
		!strings.Contains(lines[1], `"type":"context"`) || !strings.Contains(lines[2], `"text":"hello"`) {
		t.Fatalf("lines = %v", lines)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.created != 1 {
		t.Fatalf("unknown resume id should create a session, created=%d", f.created)
	}
	if len(f.mcp) != 1 || !strings.Contains(f.mcp[0], `"Bearer tok"`) || !strings.Contains(f.mcp[0], `"wick_abc"`) {
		t.Fatalf("mcp registration = %v", f.mcp)
	}
}

func TestRemoteTurnResumes(t *testing.T) {
	f := &fakeOpencode{}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "a/b", prompt: "hi", resumeID: "ses_known"})
	lines := readAll(t, p)
	_ = p.Wait()
	if f.created != 0 || len(lines) == 0 || !strings.Contains(lines[0], `"sessionID":"ses_known"`) || strings.Contains(strings.Join(lines, ""), "could not find") {
		t.Fatalf("resume did not reuse the session: created=%d lines=%v", f.created, lines)
	}
}

// Kill aborts the session; the server itself stays up.
func TestRemoteKillAborts(t *testing.T) {
	f := &fakeOpencode{hang: true}
	p, fs := startFake(t, f, turnSpec{title: "t", model: "a/b", prompt: "long job"})
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.mu.Lock()
		n := len(f.prompts)
		f.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("prompt never sent")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // prompted flag set after the POST returns
	if p.Pid() != 0 {
		t.Fatal("Pid must be 0: the agent signals Pid's process group, which is the server")
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { readAll(t, p); _ = p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(killGrace + 2*time.Second):
		t.Fatal("turn did not end after Kill")
	}
	f.mu.Lock()
	aborted := append([]string(nil), f.aborted...)
	f.mu.Unlock()
	if len(aborted) != 1 || aborted[0] != "ses_new1" {
		t.Fatalf("aborted = %v", aborted)
	}
	if _, k := fs.counts(); k != 0 {
		t.Fatal("Kill killed the shared server")
	}
	if p.Wait() == nil {
		t.Fatal("an aborted turn must not report success")
	}
}

func waitPrompted(t *testing.T, f *fakeOpencode) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.mu.Lock()
		n := len(f.prompts)
		f.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("prompt never sent")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // prompted flag set after the POST returns
}

// Kill returns only once the abort went out — the next turn prompts the
// same session and must not be hit by an abort still in flight — and the
// killed turn reads as a cancel, not as a crash. Nobody drains a killed
// turn's stdout, and that must not wedge it.
func TestRemoteKillIsSynchronousAndACancel(t *testing.T) {
	f := &fakeOpencode{hang: true}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "a/b", prompt: "long job"})
	waitPrompted(t, f)
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	aborted := len(f.aborted)
	f.mu.Unlock()
	if aborted != 1 {
		t.Fatalf("Kill returned before the abort went out (aborted=%d)", aborted)
	}
	waited := make(chan error, 1)
	go func() { waited <- p.Wait() }()
	select {
	case err := <-waited:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Wait = %v, want a context.Canceled (a stop, not a crash)", err)
		}
	case <-time.After(killGrace + 2*time.Second):
		t.Fatal("killed turn never ended with nobody reading its stdout")
	}
}

// A turn that already went idle has nothing to abort; aborting anyway
// would land on the next turn of the same session.
func TestRemoteKillAfterIdleDoesNotAbort(t *testing.T) {
	f := &fakeOpencode{}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "a/b", prompt: "hi"})
	readAll(t, p)
	_ = p.Wait()
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.aborted) != 0 {
		t.Fatalf("aborted a finished turn: %v", f.aborted)
	}
}

// A failure wick hits mid-turn (here the server refusing the prompt) is
// the turn's error frame, and the process ends normally: a Wait error
// would make the pool treat it as an unexplained crash and restart.
func TestRemoteTurnErrorEndsTurnNotProcess(t *testing.T) {
	f := &fakeOpencode{promptStatus: http.StatusInternalServerError}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "a/b", prompt: "hi"})
	lines := readAll(t, p)
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait = %v, want nil — the error belongs to the turn", err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], `"type":"error"`) {
		t.Fatalf("lines = %v, want one error frame", lines)
	}
}

// A message injected mid-turn goes to the same session as another
// prompt, and its reply comes out of the same stream: one process, one
// run, two answers, then the turn ends normally.
func TestRemoteInjectJoinsRunningTurn(t *testing.T) {
	f := &fakeOpencode{gate: make(chan struct{})}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "a/b", prompt: "first"})
	waitPrompted(t, f)
	if err := p.Inject("second"); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	close(f.gate)
	lines := readAll(t, p)
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	f.mu.Lock()
	prompts := append([]string(nil), f.prompts...)
	f.mu.Unlock()
	if len(prompts) != 2 || !strings.Contains(prompts[1], `"text":"second"`) || !strings.Contains(prompts[1], `"modelID":"b"`) {
		t.Fatalf("prompts = %v", prompts)
	}
	finishes := 0
	for _, l := range lines {
		if strings.Contains(l, `"type":"step_finish"`) {
			finishes++
		}
	}
	if finishes != 2 {
		t.Fatalf("want both replies in the one stream, got %d step_finish in %v", finishes, lines)
	}
	if err := p.Inject("late"); err == nil {
		t.Fatal("Inject after the turn ended must fail so the agent queues it")
	}
}

// Prompts injected while the server has not stored the first one yet
// wait for it: prompt_async answers before storing, and a fresh server
// stored the injected ones first, so the model answered only the last.
func TestRemoteInjectWaitsForPreviousPrompt(t *testing.T) {
	f := &fakeOpencode{gate: make(chan struct{}), storeDelay: 300 * time.Millisecond}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "a/b", prompt: "alpha"})
	// The agent reads stdout all along; so does this test, or the event
	// loop blocks on the pipe and never sees the prompt stored.
	read := make(chan []string, 1)
	go func() { read <- readAll(t, p) }()
	waitPrompted(t, f)
	start := time.Now()
	errs := make(chan error, 2)
	go func() { errs <- p.Inject("bravo") }()
	time.Sleep(20 * time.Millisecond)
	go func() { errs <- p.Inject("charlie") }()
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("Inject: %v", err)
		}
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("injections waited %s (the order wait timed out instead of seeing the prompt stored)", d)
	}
	close(f.gate)
	<-read
	_ = p.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.stored) != 3 || f.stored[0] != "alpha" {
		t.Fatalf("stored order = %q, want alpha first", f.stored)
	}
}

// A run that goes idle with nothing at all is the turn's error, not an
// empty answer.
func TestRemoteSilentIdleIsTurnError(t *testing.T) {
	f := &fakeOpencode{silent: true}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "a/b", prompt: "hi"})
	lines := readAll(t, p)
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(lines) == 0 || !strings.Contains(lines[len(lines)-1], "without a reply") {
		t.Fatalf("lines = %v", lines)
	}
}

// A workspace that does not exist fails the turn with that reason.
func TestRemoteMissingWorkspaceIsTurnError(t *testing.T) {
	f := &fakeOpencode{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	m := newManager(func(ctx context.Context, spec serverSpec, pw string) (*serverHandle, error) {
		f.mu.Lock()
		f.password = pw
		f.mu.Unlock()
		return &serverHandle{url: srv.URL, password: pw, pid: 1, kill: func() {}, done: make(chan struct{})}, nil
	})
	m.Every = time.Hour
	t.Cleanup(m.shutdown)
	l, err := m.acquire(context.Background(), spec(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	p := newRemoteProcess(nil, "opencode", nil, t.TempDir()+"/missing")
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go func() { defer cancel(); p.run(ctx, l, turnSpec{prompt: "hi"}) }()
	lines := readAll(t, p)
	if err := p.Wait(); err != nil || len(lines) != 1 || !strings.Contains(lines[0], "is not a directory") {
		t.Fatalf("err=%v lines=%v", err, lines)
	}
}

// "/compact" on the serve path runs opencode's own summarize (not a prompt
// to the model) and ends the turn with the compaction notice; every turn
// opens with the model's context limit from the running server.
func TestRemoteCompactTurnAndWindow(t *testing.T) {
	f := &fakeOpencode{}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "openai/gpt-5.5", prompt: "/compact", resumeID: "ses_known"})
	lines := readAll(t, p)
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, `"autoCompact":true`) {
		t.Fatalf("compaction.auto unset means opencode compacts on its own: %s", joined)
	}
	if !strings.Contains(joined, `"type":"context"`) || !strings.Contains(joined, `"window":400000`) {
		t.Fatalf("no context window line: %v", lines)
	}
	// before = the last assistant context (9000+81000), after = the
	// overhead floor (1051+40000) + the summary (1800) — not the bare
	// summary, which no next request is ever that small.
	if !strings.Contains(joined, `"type":"compaction"`) || !strings.Contains(joined, `"tokensBefore":90000`) ||
		!strings.Contains(joined, `"tokensAfter":42851`) || !strings.Contains(joined, `"type":"step_finish"`) {
		t.Fatalf("compact turn: %v", lines)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.summarized) != 1 || !strings.Contains(f.summarized[0], "/session/ses_known/summarize") || !strings.Contains(f.summarized[0], `"modelID":"gpt-5.5"`) {
		t.Fatalf("summarize calls %v", f.summarized)
	}
	if len(f.prompts) != 0 {
		t.Fatalf("/compact was sent to the model as a prompt: %v", f.prompts)
	}
}

// A summarize slower than the per-request cap still completes: opencode
// answers only once the summary is written, and on a big session that
// takes minutes — with the cap wick reported a timeout for a compaction
// that had in fact happened.
func TestRemoteCompactOutlivesRequestCap(t *testing.T) {
	old := apiRequestTimeout
	apiRequestTimeout = 100 * time.Millisecond
	t.Cleanup(func() { apiRequestTimeout = old })
	f := &fakeOpencode{summarizeDelay: 400 * time.Millisecond}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "openai/gpt-5.5", prompt: "/compact", resumeID: "ses_known"})
	joined := strings.Join(readAll(t, p), "\n")
	if err := p.Wait(); err != nil {
		t.Fatalf("slow summarize failed the turn: %v", err)
	}
	if !strings.Contains(joined, `"type":"compaction"`) {
		t.Fatalf("no compaction line after a slow summarize: %s", joined)
	}
}

// opencode wrote no summary: the notice says nothing was compacted (no
// "compacted" claim), and the turn still ends.
func TestRemoteCompactNothingToSummarize(t *testing.T) {
	f := &fakeOpencode{noSummary: true}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "openai/gpt-5.5", prompt: "/compact", resumeID: "ses_known"})
	joined := strings.Join(readAll(t, p), "\n")
	_ = p.Wait()
	if strings.Contains(joined, `"type":"compaction"`) || !strings.Contains(joined, "Nothing was compacted") || !strings.Contains(joined, `"type":"step_finish"`) {
		t.Fatalf("lines: %s", joined)
	}
}

// A summarize opencode refuses for having nothing to compact is a notice,
// not a failed turn.
func TestRemoteCompactRefusedNoop(t *testing.T) {
	f := &fakeOpencode{summarizeErr: "Already compacted"}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "openai/gpt-5.5", prompt: "/compact", resumeID: "ses_known"})
	joined := strings.Join(readAll(t, p), "\n")
	if err := p.Wait(); err != nil {
		t.Fatalf("no-op summarize failed the turn: %v", err)
	}
	if strings.Contains(joined, `"type":"compaction"`) || !strings.Contains(joined, "Nothing was compacted") || !strings.Contains(joined, `"type":"step_finish"`) {
		t.Fatalf("lines: %s", joined)
	}
}

// A model that streams nothing at all is stopped with a visible error
// within the silence bound, never a silent spinner.
func TestRemoteSilentModelIsStopped(t *testing.T) {
	ps, pc := silentTurnTimeout, silentTurnCheck
	silentTurnTimeout, silentTurnCheck = 300*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { silentTurnTimeout, silentTurnCheck = ps, pc })
	f := &fakeOpencode{silentNoEvents: true}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "opencode/kimi-k3", prompt: "hai"})
	done := make(chan string, 1)
	go func() { done <- strings.Join(readAll(t, p), "\n") }()
	select {
	case joined := <-done:
		if !strings.Contains(joined, `"type":"error"`) || !strings.Contains(joined, "kimi-k3 sent nothing") {
			t.Fatalf("lines: %s", joined)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("silent model left the turn hanging")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.aborted) == 0 {
		t.Fatal("silent session not aborted")
	}
}

// compaction.auto false in the server's config is shown as off.
func TestRemoteAutoCompactOff(t *testing.T) {
	f := &fakeOpencode{config: `{"compaction":{"auto":false,"prune":true}}`}
	p, _ := startFake(t, f, turnSpec{title: "t", model: "openai/gpt-5.5", prompt: "/compact", resumeID: "ses_known"})
	lines := readAll(t, p)
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(lines, "\n"); !strings.Contains(joined, `"autoCompact":false`) {
		t.Fatalf("want autoCompact false: %s", joined)
	}
}

// A tool the server keeps updating while it runs is announced once, and
// only while it is running: an update with no call id is not a start.
func TestTranslatorAnnouncesRunningToolOnce(t *testing.T) {
	tr := newTranslator("ses_1")
	run := func(id string) []string {
		lines, _ := tr.feed(part(map[string]any{"type": "tool", "tool": "task", "callID": id, "sessionID": "ses_1", "messageID": "msg_a", "state": map[string]any{"status": "running"}}))
		var out []string
		for _, l := range lines {
			out = append(out, strings.TrimSpace(string(l)))
		}
		return out
	}
	if got := run("c1"); len(got) != 1 || !strings.Contains(got[0], `"type":"tool_running"`) {
		t.Fatalf("first running update = %v", got)
	}
	if got := run("c1"); len(got) != 0 {
		t.Fatalf("repeat running update emitted %v", got)
	}
	if got := run(""); len(got) != 0 {
		t.Fatalf("running update without call id emitted %v", got)
	}
	if got := run("c2"); len(got) != 1 {
		t.Fatalf("second tool not announced: %v", got)
	}
}
