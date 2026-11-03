package codemode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRunner speaks the runner wire: every line "CALL <tool_id>" of the code
// becomes a call event; done carries the results joined, in order.
type fakeRunner struct {
	busy    atomic.Int32 // answer 429 this many times first
	mu      sync.Mutex
	pending map[int64]chan wireResult
	next    atomic.Int64
	crash   bool
	// oldRunner leaves "file" out of done's features (a pre-0.2.0 runner).
	oldRunner bool
	// gotTimeout is the timeout_ms of the last run.
	gotTimeout atomic.Int64
}

func (f *fakeRunner) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/_wick/codemode/status":
		_ = json.NewEncoder(w).Encode(map[string]any{"langs": []string{"ts", "js"}})
	case "/_wick/codemode/result":
		var res wireResult
		_ = json.NewDecoder(r.Body).Decode(&res)
		f.mu.Lock()
		ch := f.pending[res.ID]
		f.mu.Unlock()
		if ch != nil {
			ch <- res
		}
	case "/_wick/codemode/run":
		if f.busy.Load() > 0 {
			f.busy.Add(-1)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		var in struct {
			Code      string
			TimeoutMS int64 `json:"timeout_ms"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.gotTimeout.Store(in.TimeoutMS)
		end := time.Now().Add(time.Duration(in.TimeoutMS) * time.Millisecond)
		fl := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		// timedOut plays the runner's deadline: HANG sends a call and never
		// waits for it, SPIN burns the time in script code.
		timedOut := func() {
			time.Sleep(time.Until(end))
			b, _ := json.Marshal(map[string]any{"type": "done", "ok": false, "error": "timeout", "timed_out": true})
			_, _ = w.Write(append(b, '\n'))
		}
		var got []string
		for _, line := range strings.Split(in.Code, "\n") {
			line = strings.TrimSpace(line)
			ev := map[string]any{"type": "call"}
			hang := false
			if line == "SPIN" {
				timedOut()
				return
			} else if tid, ok := strings.CutPrefix(line, "HANG "); ok {
				ev["tool_id"], hang = tid, true
			} else if rest, ok := strings.CutPrefix(line, "CALL "); ok {
				// CALL <tool_id> [timeout_ms]
				tid, ms, _ := strings.Cut(rest, " ")
				ev["tool_id"], ev["params"] = tid, map[string]any{"n": 1}
				if ms != "" {
					ev["timeout_ms"] = json.Number(ms)
				}
			} else if rest, ok := strings.CutPrefix(line, "wick.file "); ok {
				// wick.file <op> <path> [data]
				parts := strings.SplitN(rest+"  ", " ", 3)
				ev["type"], ev["op"], ev["path"], ev["data"] = "file", parts[0], parts[1], strings.TrimSpace(parts[2])
			} else {
				continue
			}
			id := f.next.Add(1)
			ev["id"] = id
			ch := make(chan wireResult, 1)
			f.mu.Lock()
			f.pending[id] = ch
			f.mu.Unlock()
			b, _ := json.Marshal(ev)
			_, _ = w.Write(append(b, '\n'))
			fl.Flush()
			if hang {
				timedOut()
				return
			}
			res := <-ch
			if res.OK {
				got = append(got, string(res.Result))
			} else {
				got = append(got, "ERR "+res.Error)
			}
		}
		if f.crash {
			return
		}
		out, _ := json.Marshal(strings.Join(got, ","))
		done := map[string]any{"type": "done", "ok": true, "result": json.RawMessage(out), "logs": []string{"hi"}, "features": []string{FeatureFile}}
		if f.oldRunner {
			done["ok"], done["error"] = false, "TypeError: Cannot read property 'read' of undefined"
			delete(done, "features")
		}
		b, _ := json.Marshal(done)
		_, _ = w.Write(append(b, '\n'))
	}
}

func hostFor(t *testing.T, f *fakeRunner) Host {
	f.pending = map[int64]chan wireResult{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	rt := rewrite{base: srv.URL}
	return Host{
		Runners: func() []Runner {
			return []Runner{{Key: "codemode_fake", Langs: []string{"ts", "js"}, Transport: func() (http.RoundTripper, error) { return rt, nil }}}
		},
		Exec: func(_ context.Context, toolID string, params map[string]any) (string, error) {
			if strings.HasSuffix(toolID, "/hang") || strings.HasSuffix(toolID, "/slow") {
				// A connector that ignores its context.
				time.Sleep(time.Hour)
			}
			if strings.HasSuffix(toolID, "/fail") {
				return "", errors.New("boom\nsecond line")
			}
			return fmt.Sprintf(`{"op":%q}`, toolID[strings.LastIndex(toolID, "/")+1:]), nil
		},
		Describe: func(toolID string) (string, bool) { return "Acme", strings.HasSuffix(toolID, "/send") },
	}
}

// rewrite sends http://plugin/... to the test server.
type rewrite struct{ base string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u := r.base + req.URL.Path
	out, _ := http.NewRequestWithContext(req.Context(), req.Method, u, req.Body)
	out.Header = req.Header
	return http.DefaultTransport.RoundTrip(out)
}

func TestRunExecutesCallsAsHost(t *testing.T) {
	h := hostFor(t, &fakeRunner{})
	res, err := h.Run(context.Background(), Request{Code: "CALL conn:a/list\nCALL conn:a/fail\nCALL conn:a/send"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || string(res.Result) != `"{\"op\":\"list\"},ERR boom\nsecond line,{\"op\":\"send\"}"` {
		t.Fatalf("result = %s (%s)", res.Result, res.Error)
	}
	if len(res.Calls) != 3 || res.Runner != "codemode_fake" || res.RunID == "" || len(res.Logs) != 1 {
		t.Fatalf("res = %+v", res)
	}
	byOp := map[string]Call{}
	for _, c := range res.Calls {
		byOp[c.ToolID] = c
	}
	if c := byOp["conn:a/fail"]; c.OK || c.Error != "boom" || c.Label != "Acme" {
		t.Fatalf("fail call = %+v", c)
	}
	if !byOp["conn:a/send"].Destructive {
		t.Fatal("send not marked destructive")
	}
}

func TestPreflightStopsBadToolIDs(t *testing.T) {
	h := hostFor(t, &fakeRunner{})
	ran := false
	h.Exec = func(context.Context, string, map[string]any) (string, error) { ran = true; return "{}", nil }
	h.Check = func(_ context.Context, id string) error {
		if strings.Contains(id, "nope") {
			return errors.New("not found or not accessible")
		}
		return nil
	}
	res, err := h.Run(context.Background(), Request{Code: `const a = "conn:ok-1/list"; // fine
const b = "conn:nope-2/send@acct-1";
CALL conn:ok-1/list`})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || ran || !strings.Contains(res.Error, "conn:nope-2/send@acct-1: not found") {
		t.Fatalf("res = %+v ran=%v", res, ran)
	}
}

func TestQueueWaitsForBusyRunner(t *testing.T) {
	f := &fakeRunner{}
	f.busy.Store(2)
	h := hostFor(t, f)
	res, err := h.Run(context.Background(), Request{Code: "CALL conn:a/list"})
	if err != nil || !res.OK {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if res.QueuedMS < 200 {
		t.Fatalf("queued_ms = %d", res.QueuedMS)
	}
}

func TestRunnerCrashIsReported(t *testing.T) {
	h := hostFor(t, &fakeRunner{crash: true})
	res, err := h.Run(context.Background(), Request{Code: "CALL conn:a/send"})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !strings.Contains(res.Error, "runner stopped") || len(res.Calls) != 1 {
		t.Fatalf("res = %+v", res)
	}
}

func TestNoRunnerForLang(t *testing.T) {
	h := hostFor(t, &fakeRunner{})
	_, err := h.Run(context.Background(), Request{Lang: "py", Code: "print(1)"})
	if !errors.Is(err, ErrNoRunner) || !strings.Contains(err.Error(), "available: ts, js") {
		t.Fatalf("err = %v", err)
	}
	h.Runners = nil
	if _, err := h.Run(context.Background(), Request{Code: "1"}); !errors.Is(err, ErrNoRunner) {
		t.Fatalf("err = %v", err)
	}
}

func TestToolIDsAndCap(t *testing.T) {
	ids := ToolIDs("a('conn:x-1/op_a'); b(\"conn:x-1/op_a\"); c(`conn:y_2/op-b@acc-9`) conn:bad")
	if strings.Join(ids, " ") != "conn:x-1/op_a conn:y_2/op-b@acc-9" {
		t.Fatalf("ids = %v", ids)
	}
	big := json.RawMessage(`"` + strings.Repeat("x", MaxResultBytes) + `"`)
	out, cut := capResult(big)
	if !cut || len(out) > MaxResultBytes || !strings.Contains(string(out), "filter or aggregate") {
		t.Fatalf("cap: cut=%v len=%d", cut, len(out))
	}
}

func TestContextCancelStopsQueue(t *testing.T) {
	f := &fakeRunner{}
	f.busy.Store(1000)
	h := hostFor(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := h.Run(ctx, Request{Code: "CALL conn:a/list"})
	if err == nil {
		t.Fatal("expected error")
	}
}

var _ = bufio.NewScanner

func TestDefaultTimeoutIs60s(t *testing.T) {
	f := &fakeRunner{}
	h := hostFor(t, f)
	if _, err := h.Run(context.Background(), Request{Code: "CALL conn:a/list"}); err != nil {
		t.Fatal(err)
	}
	if got := f.gotTimeout.Load(); got != 60000 {
		t.Fatalf("timeout_ms = %d, want 60000", got)
	}
}

func TestCallTimeout(t *testing.T) {
	for _, tc := range []struct {
		ms        int64
		remaining time.Duration
		want      time.Duration
	}{
		{0, 60 * time.Second, CallTimeout},
		{0, 10 * time.Second, 9 * time.Second},
		{0, 500 * time.Millisecond, 500 * time.Millisecond},
		{5000, 60 * time.Second, 5 * time.Second},
		{90000, 30 * time.Second, 30 * time.Second},
		{0, 0, minCallTimeout},
	} {
		if got := callTimeout(tc.ms, tc.remaining); got != tc.want {
			t.Errorf("callTimeout(%d, %s) = %s, want %s", tc.ms, tc.remaining, got, tc.want)
		}
	}
}

func TestHungCallFailsAtItsDeadline(t *testing.T) {
	h := hostFor(t, &fakeRunner{})
	start := time.Now()
	res, err := h.Run(context.Background(), Request{Code: "CALL conn:a/hang 300\nCALL conn:a/list", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v", d)
	}
	// The run goes on past the failed call.
	if !res.OK || string(res.Result) != `"ERR timed out after 0.3s,{\"op\":\"list\"}"` {
		t.Fatalf("result = %s (%s)", res.Result, res.Error)
	}
	if len(res.Calls) != 2 || res.Calls[0].OK || res.Calls[0].Error != "timed out after 0.3s" {
		t.Fatalf("calls = %+v", res.Calls)
	}
}

func TestHungCallDefaultLeavesTheScriptASecond(t *testing.T) {
	h := hostFor(t, &fakeRunner{})
	res, err := h.Run(context.Background(), Request{Code: "CALL conn:a/hang\nCALL conn:a/list", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || !strings.HasPrefix(string(res.Result), `"ERR timed out after 1`) {
		t.Fatalf("result = %s (%s)", res.Result, res.Error)
	}
}

func TestRunTimeoutNamesInFlightCall(t *testing.T) {
	h := hostFor(t, &fakeRunner{})
	res, err := h.Run(context.Background(), Request{Code: "CALL conn:a/list\nHANG conn:a/slow", Timeout: 400 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !strings.HasPrefix(res.Error, "script timeout after 0.4s while waiting for conn:a/slow (Acme, 0.") {
		t.Fatalf("error = %q", res.Error)
	}
	if len(res.Calls) != 2 || !res.Calls[0].OK || res.Calls[1].ToolID != "conn:a/slow" || res.Calls[1].OK ||
		res.Calls[1].Error != "script timeout after 0.4s while this call was running" {
		t.Fatalf("calls = %+v", res.Calls)
	}
}

func TestRunTimeoutInScriptCode(t *testing.T) {
	h := hostFor(t, &fakeRunner{})
	res, err := h.Run(context.Background(), Request{Code: "CALL conn:a/list\nSPIN", Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Error != "script timeout after 0.3s (busy in script code)" || len(res.Calls) != 1 {
		t.Fatalf("res = %+v", res)
	}
}
