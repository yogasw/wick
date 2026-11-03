// Package codemode runs wick_run_code scripts. The script itself runs in a
// runner service plugin (codemode_<engine>, e.g. codemode_goja for JS/TS),
// never in wick: a runaway script can only take its runner down. Every
// connector call the script makes comes back to wick as a "call" event and is
// executed here, inside the caller's request, with the caller's session,
// user, tags and agent scope — the runner never sees a credential.
//
// Wire (all under /_wick/codemode/, unreachable from outside wick):
//
//	POST /run     {run_id, lang, code, input, timeout_ms}
//	              → 429 + status while the runner is over its memory share
//	              → 200 NDJSON: {"type":"call",id,tool_id,params}… {"type":"done",ok,result,error,logs}
//	POST /result  {run_id, id, ok, result|error}   (one per call event)
//	GET  /status  {heap_mb, budget_mb, running, busy, langs, engine}
package codemode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// RunnerPrefix marks a service plugin as a Code Mode runner.
const RunnerPrefix = "codemode_"

const (
	DefaultTimeout = 30 * time.Second
	MaxTimeout     = 120 * time.Second
	// MaxQueue is how long a run may wait for a busy runner before it gives up.
	MaxQueue = 2 * time.Minute
	// MaxResultBytes caps the result handed back to the model; the point of
	// Code Mode is to return a summary.
	MaxResultBytes = 20 << 10
	// MaxCalls bounds the connector calls of one run (a loop gone wrong).
	MaxCalls = 1000
)

// Runner is one Code Mode runner plugin.
type Runner struct {
	Key   string
	Langs []string
	// Transport reaches the plugin's socket (waking it when it sleeps).
	Transport func() (http.RoundTripper, error)
}

// Runners lists the installed runners.
type Runners func() []Runner

// Request is one script run.
type Request struct {
	Lang    string
	Code    string
	Input   json.RawMessage
	Timeout time.Duration
}

// Call is one connector call recorded during a run.
type Call struct {
	ToolID      string `json:"tool_id"`
	Label       string `json:"label,omitempty"`
	OK          bool   `json:"ok"`
	Destructive bool   `json:"destructive,omitempty"`
	MS          int64  `json:"ms"`
	Error       string `json:"error,omitempty"`
}

// Result is what wick_run_code returns.
type Result struct {
	OK         bool            `json:"ok"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	Logs       []string        `json:"logs,omitempty"`
	Calls      []Call          `json:"calls"`
	Truncated  bool            `json:"truncated,omitempty"`
	DurationMS int64           `json:"duration_ms"`
	QueuedMS   int64           `json:"queued_ms,omitempty"`
	Runner     string          `json:"runner,omitempty"`
	RunID      string          `json:"run_id"`
}

// Host is what a run needs from wick.
type Host struct {
	Runners Runners
	// Check vets one tool_id before the run starts (exists, visible to the
	// caller). A nil Check skips the pre-flight.
	Check func(ctx context.Context, toolID string) error
	// Exec runs one connector call as the caller.
	Exec func(ctx context.Context, toolID string, params map[string]any) (string, error)
	// Describe labels a tool_id for the call list (connector label, whether
	// the op is destructive). Optional.
	Describe func(toolID string) (label string, destructive bool)
	// Install, when set, runs when no runner is installed at all (see
	// Installer.Ensure); the run then picks again from Runners. Optional.
	Install func(ctx context.Context) error
}

var toolIDLiteral = regexp.MustCompile("conn:[A-Za-z0-9_.:-]+/[A-Za-z0-9_.-]+(?:@[A-Za-z0-9_.:-]+)?")

// ToolIDs returns the distinct tool_id literals written in code, sorted.
func ToolIDs(code string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range toolIDLiteral.FindAllString(code, -1) {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

// ErrNoRunner means no installed runner takes the language.
var ErrNoRunner = errors.New("no Code Mode runner is installed for this language")

func (h Host) pick(ctx context.Context, lang string) (Runner, error) {
	r, n, avail := h.find(lang)
	if n == 0 && h.Install != nil {
		// No runner at all: install the default one, then look again on
		// this same run.
		if err := h.Install(ctx); err != nil {
			return Runner{}, fmt.Errorf("%w: %v", ErrNoRunner, err)
		}
		r, _, avail = h.find(lang)
	}
	if r.Key != "" {
		return r, nil
	}
	if len(avail) == 0 {
		return Runner{}, fmt.Errorf("%w (install the codemode_goja service plugin)", ErrNoRunner)
	}
	return Runner{}, fmt.Errorf("%w %q; available: %s", ErrNoRunner, lang, strings.Join(avail, ", "))
}

// find returns the runner taking lang (zero Runner when none), how many
// runners are installed, and the languages they take.
func (h Host) find(lang string) (Runner, int, []string) {
	if h.Runners == nil {
		return Runner{}, 0, nil
	}
	var avail []string
	rs := h.Runners()
	for _, r := range rs {
		for _, l := range r.Langs {
			if l == lang {
				return r, len(rs), nil
			}
			avail = append(avail, l)
		}
	}
	return Runner{}, len(rs), avail
}

// Run executes req. Failures of the script itself come back as a Result with
// OK=false; an error is returned only when the run could not happen at all.
func (h Host) Run(ctx context.Context, req Request) (*Result, error) {
	start := time.Now()
	if strings.TrimSpace(req.Code) == "" {
		return nil, errors.New("code is required")
	}
	if req.Lang == "" {
		req.Lang = "ts"
	}
	req.Lang = strings.ToLower(req.Lang)
	if req.Timeout <= 0 {
		req.Timeout = DefaultTimeout
	}
	if req.Timeout > MaxTimeout {
		req.Timeout = MaxTimeout
	}
	if len(req.Input) > 0 && !json.Valid(req.Input) {
		return nil, errors.New("input must be JSON")
	}

	// Pre-flight: a wrong or forbidden tool_id stops the run before any call
	// (a write half-done by a script that dies on its 3rd call is worse).
	if h.Check != nil {
		var bad []string
		for _, id := range ToolIDs(req.Code) {
			if err := h.Check(ctx, id); err != nil {
				bad = append(bad, id+": "+err.Error())
			}
		}
		if len(bad) > 0 {
			return &Result{OK: false, Calls: []Call{}, RunID: "",
				Error: "not run, these tool_ids cannot be used: " + strings.Join(bad, "; ") +
					". Get exact tool_ids from wick_search/wick_get."}, nil
		}
	}

	rn, err := h.pick(ctx, req.Lang)
	if err != nil {
		return nil, err
	}
	runID := uuid.NewString()
	body, _ := json.Marshal(map[string]any{
		"run_id": runID, "lang": req.Lang, "code": req.Code,
		"input": rawOrNil(req.Input), "timeout_ms": req.Timeout.Milliseconds(),
	})

	// Queue: the runner answers 429 while its heap is over its share; wait
	// for room instead of failing, up to MaxQueue.
	qctx, qcancel := context.WithTimeout(ctx, MaxQueue)
	defer qcancel()
	var resp *http.Response
	var rt http.RoundTripper
	wait := 100 * time.Millisecond
	queuedFrom := time.Now()
	for {
		rt, err = rn.Transport()
		if err != nil {
			return nil, fmt.Errorf("runner %s is not available: %w", rn.Key, err)
		}
		hreq, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://plugin/_wick/codemode/run", bytes.NewReader(body))
		hreq.Header.Set("Content-Type", "application/json")
		resp, err = rt.RoundTrip(hreq)
		if err != nil {
			return nil, fmt.Errorf("runner %s did not answer: %w", rn.Key, err)
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			break
		}
		resp.Body.Close()
		select {
		case <-qctx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("runner %s stayed busy (memory) for %s; try again later", rn.Key, MaxQueue)
		case <-time.After(wait):
		}
		if wait < 2*time.Second {
			wait *= 2
		}
	}
	queued := time.Since(queuedFrom)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("runner %s refused the run (%d): %s", rn.Key, resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	res := &Result{RunID: runID, Runner: rn.Key, Calls: []Call{}}
	if queued > 200*time.Millisecond {
		res.QueuedMS = queued.Milliseconds()
	}
	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		count int
	)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	done := false
	for sc.Scan() {
		var ev wireEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "call":
			mu.Lock()
			count++
			over := count > MaxCalls
			mu.Unlock()
			wg.Add(1)
			go func(ev wireEvent) {
				defer wg.Done()
				c := Call{ToolID: ev.ToolID}
				if h.Describe != nil {
					c.Label, c.Destructive = h.Describe(ev.ToolID)
				}
				t0 := time.Now()
				var out wireResult
				out.RunID, out.ID = runID, ev.ID
				if over {
					out.Error = fmt.Sprintf("too many calls in one run (max %d)", MaxCalls)
				} else {
					var params map[string]any
					if len(ev.Params) > 0 {
						_ = json.Unmarshal(ev.Params, &params)
					}
					txt, err := h.Exec(ctx, ev.ToolID, params)
					if err != nil {
						out.Error = err.Error()
					} else {
						out.OK = true
						out.Result = asJSON(txt)
					}
				}
				c.OK, c.Error, c.MS = out.OK, firstLine(out.Error), time.Since(t0).Milliseconds()
				mu.Lock()
				res.Calls = append(res.Calls, c)
				mu.Unlock()
				post(ctx, rt, out)
			}(ev)
		case "done":
			done = true
			res.OK, res.Error, res.Logs = ev.OK, ev.Error, ev.Logs
			res.Result, res.Truncated = capResult(ev.Result)
		}
		if done {
			break
		}
	}
	wg.Wait()
	res.DurationMS = time.Since(start).Milliseconds()
	if !done {
		res.OK = false
		if res.Error == "" {
			res.Error = "the runner stopped before the script finished (crashed or restarted; calls already made are listed)"
		}
	}
	return res, nil
}

type wireEvent struct {
	Type   string          `json:"type"`
	ID     int64           `json:"id"`
	ToolID string          `json:"tool_id"`
	Params json.RawMessage `json:"params"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
	Logs   []string        `json:"logs"`
}

type wireResult struct {
	RunID  string          `json:"run_id"`
	ID     int64           `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func post(ctx context.Context, rt http.RoundTripper, out wireResult) {
	b, _ := json.Marshal(out)
	req, err := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodPost, "http://plugin/_wick/codemode/result", bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := rt.RoundTrip(req)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

func rawOrNil(r json.RawMessage) any {
	if len(r) == 0 {
		return nil
	}
	return r
}

func asJSON(s string) json.RawMessage {
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	b, _ := json.Marshal(s)
	return b
}

func capResult(r json.RawMessage) (json.RawMessage, bool) {
	if len(r) <= MaxResultBytes {
		return r, false
	}
	b, _ := json.Marshal(map[string]any{
		"truncated": true,
		"bytes":     len(r),
		"preview":   string(r[:MaxResultBytes/2]),
		"note":      "result too large: filter or aggregate inside the script and return only what is needed",
	})
	return b, true
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
