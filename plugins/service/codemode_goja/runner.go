package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"time"
)

// Wire types. wick's internal/codemode keeps an identical copy: the plugins
// module pins a released wick, so the two cannot share a package yet.
type runRequest struct {
	RunID     string          `json:"run_id"`
	Lang      string          `json:"lang"`
	Code      string          `json:"code"`
	Input     json.RawMessage `json:"input,omitempty"`
	TimeoutMS int64           `json:"timeout_ms,omitempty"`
	// Features wick speaks; "file" = it answers file events (wick.file).
	Features []string `json:"features,omitempty"`
}

// event is one NDJSON line of a /run response.
type event struct {
	Type string `json:"type"` // "call" | "file" | "done"

	// call (and file: ID)
	ID     int64           `json:"id,omitempty"`
	ToolID string          `json:"tool_id,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	// TimeoutMS is the call's own deadline, which wick enforces.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`

	// file: wick does the IO inside the project's codemode/ folder
	Op   string `json:"op,omitempty"` // "read" | "write" | "list" | "exists"
	Path string `json:"path,omitempty"`
	Data string `json:"data,omitempty"`

	// done
	OK     bool            `json:"ok,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Logs   []string        `json:"logs,omitempty"`
	// Features this runner speaks, so wick can name an outdated runner.
	Features []string `json:"features,omitempty"`
	// TimedOut: the run deadline ended the script.
	TimedOut bool `json:"timed_out,omitempty"`
}

type callResult struct {
	RunID  string          `json:"run_id"`
	ID     int64           `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type status struct {
	HeapMB   int      `json:"heap_mb"`
	BudgetMB int      `json:"budget_mb"`
	Running  int      `json:"running"`
	Busy     bool     `json:"busy"`
	Langs    []string `json:"langs"`
	Engine   string   `json:"engine"`
}

const (
	defaultTimeout = 120 * time.Second
	maxTimeout     = 300 * time.Second
	// callTimeout is one call's default deadline, as in wick. callGrace is
	// how much longer the runner waits for wick's own timeout answer before
	// giving up on the call itself (a wick that never answers).
	callTimeout    = 60 * time.Second
	minCallTimeout = 100 * time.Millisecond
	callGrace      = 500 * time.Millisecond
	// admitRatio: new runs are refused (429) while the heap is above this
	// share of the budget; the running ones keep going.
	admitRatio = 0.8
	watchEvery = 10 * time.Millisecond
)

var langs = []string{"ts", "js"}

// featureFile is the wick.file support both sides announce.
const featureFile = "file"

type runner struct {
	budgetMB func() int

	mu   sync.Mutex
	runs map[string]*run

	nextCall atomic.Int64
}

func newRunner(budgetMB func() int) *runner {
	r := &runner{budgetMB: budgetMB, runs: map[string]*run{}}
	go r.watch()
	return r
}

func (r *runner) mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /_wick/codemode/run", r.handleRun)
	mux.HandleFunc("POST /_wick/codemode/result", r.handleResult)
	mux.HandleFunc("GET /_wick/codemode/status", r.handleStatus)
	mux.HandleFunc("GET /status", r.handleStatus)
}

func heapMB() int {
	s := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return int(s[0].Value.Uint64() >> 20)
}

func (r *runner) status() status {
	r.mu.Lock()
	n := len(r.runs)
	r.mu.Unlock()
	b := r.budgetMB()
	h := heapMB()
	return status{HeapMB: h, BudgetMB: b, Running: n, Busy: float64(h) > float64(b)*admitRatio, Langs: langs, Engine: "goja"}
}

func (r *runner) handleStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(r.status())
}

// watch keeps the heap under the budget: the Go GC is told the budget, and
// past it every running script is interrupted with "out of memory" (one
// shared heap, so the one at fault cannot be singled out).
func (r *runner) watch() {
	t := time.NewTicker(watchEvery)
	defer t.Stop()
	lastBudget := 0
	for range t.C {
		b := r.budgetMB()
		if b != lastBudget {
			debug.SetMemoryLimit(int64(b) << 20)
			lastBudget = b
		}
		if heapMB() <= b {
			continue
		}
		r.mu.Lock()
		for _, rn := range r.runs {
			rn.interrupt(fmt.Sprintf("out of memory: the runner is over its %d MB budget", b))
		}
		r.mu.Unlock()
	}
}

func (r *runner) handleRun(w http.ResponseWriter, req *http.Request) {
	var in runRequest
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		http.Error(w, "bad run request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if in.RunID == "" {
		http.Error(w, "run_id is required", http.StatusBadRequest)
		return
	}
	if in.Lang == "" {
		in.Lang = "ts"
	}
	if in.Lang != "ts" && in.Lang != "js" {
		http.Error(w, fmt.Sprintf("lang %q is not run here (ts, js)", in.Lang), http.StatusBadRequest)
		return
	}
	if st := r.status(); st.Busy {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(st)
		return
	}
	timeout := defaultTimeout
	if in.TimeoutMS > 0 {
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}

	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	var wmu sync.Mutex
	send := func(ev event) error {
		b, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		wmu.Lock()
		defer wmu.Unlock()
		if _, err := w.Write(append(b, '\n')); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	defer cancel()
	rn := newRun(in.RunID, r, send)
	r.mu.Lock()
	if _, dup := r.runs[in.RunID]; dup {
		r.mu.Unlock()
		_ = send(event{Type: "done", Error: "run_id already running"})
		return
	}
	r.runs[in.RunID] = rn
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.runs, in.RunID)
		r.mu.Unlock()
	}()

	res := rn.exec(ctx, in)
	_ = send(res)
}

func (r *runner) handleResult(w http.ResponseWriter, req *http.Request) {
	var in callResult
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		http.Error(w, "bad result: "+err.Error(), http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	rn := r.runs[in.RunID]
	r.mu.Unlock()
	if rn == nil {
		http.Error(w, "run not found (finished or never started)", http.StatusNotFound)
		return
	}
	if err := rn.deliver(in); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var errUnknownCall = errors.New("call not found (already answered or never asked)")
