package a2aremote

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yogasw/wick/internal/agents/a2aremote/a2aremotetest"
	"github.com/yogasw/wick/internal/agents/provider"
)

// line is the part of a stream-json line the tests read.
type line struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Message struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

type harness struct {
	t   *testing.T
	p   provider.Process
	sc  *bufio.Scanner
	dir string
}

func spawn(t *testing.T, srv *a2aremotetest.Server, auth PlainAuth, tweak func(*Config)) *harness {
	t.Helper()
	res, err := Resolve(context.Background(), local, srv.URL, auth)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{CardURL: res.CardURL, Card: SnapshotOf(res.Card), CardJSON: res.JSON}
	if tweak != nil {
		tweak(&cfg)
	}
	dir := t.TempDir()
	p, err := Spawner{Runtime: Runtime{Config: cfg, Auth: auth, Guard: local}}.Spawn(context.Background(), provider.SpawnOptions{SessionDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill() })
	h := &harness{t: t, p: p, sc: bufio.NewScanner(p.Stdout()), dir: dir}
	h.sc.Buffer(make([]byte, 1<<20), 1<<22)
	if l := h.next(); l.Type != "system" || l.Subtype != "init" {
		t.Fatalf("first line: %+v", l)
	}
	return h
}

func (h *harness) next() line {
	h.t.Helper()
	if !h.sc.Scan() {
		h.t.Fatalf("stream ended: %v", h.sc.Err())
	}
	var l line
	if err := json.Unmarshal(h.sc.Bytes(), &l); err != nil {
		h.t.Fatal(err)
	}
	return l
}

// say sends text and returns the streamed text and the closing result.
func (h *harness) say(text string) (string, line) {
	h.t.Helper()
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
	if _, err := h.p.Stdin().Write(append(b, '\n')); err != nil {
		h.t.Fatal(err)
	}
	var sb strings.Builder
	for {
		l := h.next()
		switch l.Type {
		case "assistant":
			for _, c := range l.Message.Content {
				sb.WriteString(c.Text)
			}
		case "result":
			return sb.String(), l
		}
	}
}

func TestTurnStreamingKeepsContext(t *testing.T) {
	for _, streaming := range []bool{true, false} {
		srv := a2aremotetest.New("Echo")
		srv.Streaming = streaming
		srv.Bearer = "k"
		h := spawn(t, srv, PlainAuth{Type: AuthBearer, Secret: "k"}, nil)
		text, res := h.say("hello")
		if res.IsError || text != "echo: hello" {
			t.Fatalf("streaming=%v: %q %+v", streaming, text, res)
		}
		if text, res = h.say("again"); res.IsError || text != "echo: again" {
			t.Fatalf("streaming=%v second: %q %+v", streaming, text, res)
		}
		got := srv.Received()
		if len(got) != 2 || got[0].ContextID == "" || got[1].ContextID != got[0].ContextID {
			t.Fatalf("streaming=%v context not kept: %+v", streaming, got)
		}
		if st := LoadState(h.dir); st.ContextID != got[0].ContextID {
			t.Fatalf("state not saved: %+v", st)
		}
		for _, a := range srv.Auths() {
			if a != "Bearer k" {
				t.Fatalf("request without the bearer: %q", a)
			}
		}
		srv.Close()
	}
}

func TestTurnInputRequiredContinuesTask(t *testing.T) {
	srv := a2aremotetest.New("Deployer")
	defer srv.Close()
	h := spawn(t, srv, PlainAuth{}, nil)
	text, res := h.say("ask deploy")
	if res.IsError || text != "Which environment?" {
		t.Fatalf("question: %q %+v", text, res)
	}
	if st := LoadState(h.dir); !st.InputRequired || st.TaskID == "" {
		t.Fatalf("state after question: %+v", st)
	}
	text, res = h.say("staging")
	if res.IsError || text != "deploying to staging" {
		t.Fatalf("answer: %q %+v", text, res)
	}
	got := srv.Received()
	if len(got) != 2 || !got[1].Continued || got[1].TaskID != got[0].TaskID {
		t.Fatalf("answer did not go to the same task: %+v", got)
	}
	if st := LoadState(h.dir); st.InputRequired || st.TaskID != "" {
		t.Fatalf("task kept after completion: %+v", st)
	}
}

func TestTurnLimits(t *testing.T) {
	srv := a2aremotetest.New("Limits")
	defer srv.Close()
	h := spawn(t, srv, PlainAuth{}, func(c *Config) { c.TimeoutSec = 1; c.MaxResponseBytes = 1024 })
	start := time.Now()
	if _, res := h.say("slow"); !res.IsError || !strings.Contains(res.Result, "No reply from the remote agent after") || time.Since(start) > 2500*time.Millisecond {
		t.Fatalf("timeout: %+v after %s", res, time.Since(start))
	}
	if _, res := h.say("big 5000"); !res.IsError || !strings.Contains(res.Result, "byte limit") {
		t.Fatalf("size cap: %+v", res)
	}
	if _, res := h.say("fail"); !res.IsError || !strings.Contains(res.Result, "boom") {
		t.Fatalf("failed task: %+v", res)
	}
	if text, res := h.say("still alive"); res.IsError || text != "echo: still alive" {
		t.Fatalf("after errors: %q %+v", text, res)
	}
}

// A remote task that ends failed, rejected, canceled or auth_required is
// an error in the chat, and the session state keeps the state and the
// remote's own message, so a team task ends the same way.
func TestTurnEndedStatesKeepReason(t *testing.T) {
	for _, streaming := range []bool{true, false} {
		srv := a2aremotetest.New("Moody")
		srv.Streaming = streaming
		h := spawn(t, srv, PlainAuth{}, nil)
		for _, c := range []struct {
			say    string
			state  a2a.TaskState
			reason string
		}{
			{"fail", a2a.TaskStateFailed, "boom"},
			{"reject", a2a.TaskStateRejected, "not my job"},
			{"cancel", a2a.TaskStateCanceled, "stopped by its owner"},
			{"auth", a2a.TaskStateAuthRequired, "sign in first"},
		} {
			_, res := h.say(c.say)
			if !res.IsError || res.Result != "Remote agent: "+c.reason {
				t.Fatalf("streaming=%v %s: %+v", streaming, c.say, res)
			}
			st := LoadState(h.dir)
			if got, ok := st.EndedState(); !ok || got != c.state || st.Reason != c.reason || st.InputRequired || st.TaskID != "" {
				t.Fatalf("streaming=%v %s: state %+v", streaming, c.say, st)
			}
		}
		if text, res := h.say("hello"); res.IsError || text != "echo: hello" {
			t.Fatalf("streaming=%v after: %q %+v", streaming, text, res)
		}
		if st := LoadState(h.dir); st.Reason != "" {
			t.Fatalf("streaming=%v reason kept after completion: %+v", streaming, st)
		}
		if _, ok := LoadState(h.dir).EndedState(); ok {
			t.Fatalf("streaming=%v completed reads as ended", streaming)
		}
		srv.Close()
	}
}

// auth_required and input_required end the turn even when the remote
// holds the stream open after them, as an a2a-go server does for a state
// that is not final: the turn must not wait for the call's timeout.
func TestTurnEndsOnHeldStream(t *testing.T) {
	srv := a2aremotetest.New("Holder")
	defer srv.Close()
	h := spawn(t, srv, PlainAuth{}, func(c *Config) { c.TimeoutSec = 5 })
	began := time.Now()
	if _, res := h.say("hold auth"); !res.IsError || res.Result != "Remote agent: sign in first" {
		t.Fatalf("auth: %+v", res)
	}
	if got, ok := LoadState(h.dir).EndedState(); !ok || got != a2a.TaskStateAuthRequired {
		t.Fatalf("auth state: %+v", LoadState(h.dir))
	}
	if text, res := h.say("hold ask"); res.IsError || text != "Which environment?" {
		t.Fatalf("ask: %q %+v", text, res)
	}
	if st := LoadState(h.dir); !st.InputRequired || st.TaskID == "" {
		t.Fatalf("ask state: %+v", st)
	}
	if took := time.Since(began); took > 3*time.Second {
		t.Fatalf("turns waited for the held stream: %v", took)
	}
}

// A remote's reason is cut and stripped of control characters before
// the chat, the session state or a team task sees it.
func TestTurnReasonBounded(t *testing.T) {
	srv := a2aremotetest.New("Loud")
	defer srv.Close()
	h := spawn(t, srv, PlainAuth{}, nil)
	_, res := h.say("loud 8000")
	st := LoadState(h.dir)
	if !res.IsError || res.Result != "Remote agent: "+st.Reason {
		t.Fatalf("result %q vs reason %q", res.Result, st.Reason)
	}
	if n := len([]rune(st.Reason)); n != maxReason+1 || !strings.HasSuffix(st.Reason, "…") {
		t.Fatalf("reason has %d runes", n)
	}
	if strings.ContainsAny(st.Reason, "\x1b\a") {
		t.Fatal("control characters kept")
	}
}

func TestCleanReason(t *testing.T) {
	for in, want := range map[string]string{
		"  plain  ":          "plain",
		"a\x1b[0mb\r\x00c":   "a[0mbc",
		"line one\n\tline 2": "line one\n\tline 2",
		"":                   "",
	} {
		if got := CleanReason(in); got != want {
			t.Fatalf("CleanReason(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("é", maxReason+5)
	if got := CleanReason(long); got != strings.Repeat("é", maxReason)+"…" {
		t.Fatalf("long: %d runes", len([]rune(got)))
	}
}
