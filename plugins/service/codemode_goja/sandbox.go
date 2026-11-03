package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
)

const (
	maxLogLines = 200
	maxLogBytes = 20 << 10
	// maxAlloc caps what one builtin call may allocate in a single step
	// ("x".repeat(n), new Array(n), …): such a call is one Go allocation,
	// so the heap watchdog cannot stop it in time.
	maxAlloc = 16 << 20
)

// run is one script execution: its VM, the calls waiting on wick, and logs.
type run struct {
	id   string
	r    *runner
	send func(event) error

	vmMu sync.Mutex
	vm   *goja.Runtime

	mu      sync.Mutex
	pending map[int64]chan callResult
	logs    []string
	logSize int
	logCut  bool
}

func newRun(id string, r *runner, send func(event) error) *run {
	return &run{id: id, r: r, send: send, pending: map[int64]chan callResult{}}
}

func (rn *run) interrupt(reason string) {
	rn.vmMu.Lock()
	defer rn.vmMu.Unlock()
	if rn.vm != nil {
		rn.vm.Interrupt(reason)
	}
}

func (rn *run) deliver(res callResult) error {
	rn.mu.Lock()
	ch, ok := rn.pending[res.ID]
	delete(rn.pending, res.ID)
	rn.mu.Unlock()
	if !ok {
		return errUnknownCall
	}
	ch <- res
	return nil
}

func (rn *run) log(level string, args []goja.Value) {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = render(a)
	}
	line := strings.Join(parts, " ")
	if level != "" {
		line = "[" + level + "] " + line
	}
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if len(rn.logs) >= maxLogLines || rn.logSize+len(line) > maxLogBytes {
		rn.logCut = true
		return
	}
	rn.logs = append(rn.logs, line)
	rn.logSize += len(line)
}

func render(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) {
		return "undefined"
	}
	if goja.IsNull(v) {
		return "null"
	}
	if _, ok := v.Export().(string); ok {
		return v.String()
	}
	if o, ok := v.(*goja.Object); ok && o.ClassName() == "Error" {
		return v.String()
	}
	b, err := json.Marshal(v.Export())
	if err != nil {
		return v.String()
	}
	return string(b)
}

// call asks wick to run one tool and waits for the answer. The VM goroutine
// blocks here; the run's context ends the wait on timeout. timeoutMS is the
// script's own deadline for this call (0 = default).
func (rn *run) call(ctx context.Context, toolID string, params any, timeoutMS int64) callResult {
	raw, err := json.Marshal(params)
	if err != nil {
		return callResult{Error: "params are not JSON: " + err.Error()}
	}
	if string(raw) == "null" {
		raw = []byte("{}")
	}
	remaining := maxTimeout
	if dl, ok := ctx.Deadline(); ok {
		remaining = time.Until(dl)
	}
	d := callDeadline(timeoutMS, remaining)
	// Wick ends the call at d; past d+callGrace with no answer the runner
	// stops waiting itself.
	cctx, cancel := context.WithTimeoutCause(ctx, d+callGrace, fmt.Errorf("timed out after %s", seconds(d)))
	defer cancel()
	return rn.ask(cctx, event{Type: "call", ToolID: toolID, Params: raw, TimeoutMS: d.Milliseconds()}, toolID)
}

// callDeadline is one call's deadline: ms when the script asked for one,
// else callTimeout, never past what the run has left (a second is kept back
// on the default so the script can still handle the failure). Same rule as
// wick's codemode.callTimeout.
func callDeadline(ms int64, remaining time.Duration) time.Duration {
	t := min(callTimeout, remaining-time.Second)
	if ms > 0 {
		t = time.Duration(ms) * time.Millisecond
	}
	if t > remaining || t <= 0 {
		t = remaining
	}
	return max(t, minCallTimeout)
}

// seconds renders d as "20s" or "0.3s".
func seconds(d time.Duration) string {
	return strings.TrimSuffix(strconv.FormatFloat(d.Seconds(), 'f', 1, 64), ".0") + "s"
}

// timeoutOpt reads timeout_ms (or timeoutMs) from a call's options.
func timeoutOpt(o map[string]any) int64 {
	v, ok := o["timeout_ms"]
	if !ok {
		v = o["timeoutMs"]
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

// file asks wick for one wick.file op; wick does the IO, never the runner.
func (rn *run) file(ctx context.Context, op, path, data string) callResult {
	return rn.ask(ctx, event{Type: "file", Op: op, Path: path, Data: data}, "wick.file."+op)
}

// ask sends ev with a fresh id and waits for wick's answer to it.
func (rn *run) ask(ctx context.Context, ev event, what string) callResult {
	id := rn.r.nextCall.Add(1)
	ev.ID = id
	ch := make(chan callResult, 1)
	rn.mu.Lock()
	rn.pending[id] = ch
	rn.mu.Unlock()
	if err := rn.send(ev); err != nil {
		rn.mu.Lock()
		delete(rn.pending, id)
		rn.mu.Unlock()
		return callResult{Error: "wick is gone: " + err.Error()}
	}
	select {
	case res := <-ch:
		return res
	case <-ctx.Done():
		rn.mu.Lock()
		delete(rn.pending, id)
		rn.mu.Unlock()
		if c := context.Cause(ctx); c != ctx.Err() {
			return callResult{Error: c.Error()}
		}
		return callResult{Error: "run stopped while waiting for " + what}
	}
}

func transform(lang, code string) (string, error) {
	loader := api.LoaderTS
	if lang == "js" {
		loader = api.LoaderJS
	}
	// Wrapped BEFORE the transform so top-level await and return are valid.
	src := "(async () => {\n" + code + "\n})()"
	tr := api.Transform(src, api.TransformOptions{Loader: loader, Target: api.ES2020, Sourcefile: "script." + lang})
	if len(tr.Errors) > 0 {
		e := tr.Errors[0]
		if e.Location != nil {
			return "", fmt.Errorf("syntax error at line %d:%d: %s", e.Location.Line-1, e.Location.Column, e.Text)
		}
		return "", fmt.Errorf("syntax error: %s", e.Text)
	}
	return string(tr.Code), nil
}

// guard caps the builtins that allocate a whole buffer in one call.
const guard = `(() => {
  const MAX = %d;
  const big = (n) => { if (n > MAX) throw new RangeError("allocation too large (" + n + " > " + MAX + ")"); };
  const rep = String.prototype.repeat;
  Object.defineProperty(String.prototype, "repeat", { value: function (n) { big(this.length * n); return rep.call(this, n); }, writable: true, configurable: true });
  for (const k of ["padStart", "padEnd"]) {
    const f = String.prototype[k];
    Object.defineProperty(String.prototype, k, { value: function (n, s) { big(n); return f.call(this, n, s); }, writable: true, configurable: true });
  }
  const fill = Array.prototype.fill;
  Object.defineProperty(Array.prototype, "fill", { value: function (...a) { big(this.length); return fill.apply(this, a); }, writable: true, configurable: true });
  const A = Array;
  globalThis.Array = new Proxy(A, {
    construct(t, args) { if (args.length === 1 && typeof args[0] === "number") big(args[0]); return Reflect.construct(t, args); },
    apply(t, self, args) { if (args.length === 1 && typeof args[0] === "number") big(args[0]); return Reflect.apply(t, self, args); },
  });
})();`

func (rn *run) exec(ctx context.Context, in runRequest) (out event) {
	out.Type = "done"
	out.Features = []string{featureFile}
	defer func() {
		if p := recover(); p != nil {
			out.OK, out.Result = false, nil
			out.Error = fmt.Sprintf("runner panic: %v", p)
		}
		out.TimedOut = !out.OK && ctx.Err() == context.DeadlineExceeded
		rn.mu.Lock()
		out.Logs = rn.logs
		if rn.logCut {
			out.Logs = append(out.Logs, "[wick] more log lines were dropped")
		}
		rn.mu.Unlock()
	}()

	js, err := transform(in.Lang, in.Code)
	if err != nil {
		out.Error = err.Error()
		return out
	}

	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	rn.vmMu.Lock()
	rn.vm = vm
	rn.vmMu.Unlock()
	stop := context.AfterFunc(ctx, func() {
		reason := "timeout"
		if ctx.Err() == context.Canceled {
			reason = "cancelled"
		}
		vm.Interrupt(reason)
	})
	defer stop()

	if _, err := vm.RunString(fmt.Sprintf(guard, maxAlloc)); err != nil {
		out.Error = "setup: " + err.Error()
		return out
	}
	if err := bindPure(vm); err != nil {
		out.Error = "setup: " + err.Error()
		return out
	}
	if err := rn.bind(ctx, vm, in.Input, slices.Contains(in.Features, featureFile)); err != nil {
		out.Error = "setup: " + err.Error()
		return out
	}

	v, err := vm.RunString(js)
	if err != nil {
		out.Error = scriptError(err)
		return out
	}
	p, ok := v.Export().(*goja.Promise)
	if !ok {
		out.Error = "script did not run as an async function"
		return out
	}
	switch p.State() {
	case goja.PromiseStateFulfilled:
		res := p.Result()
		if res == nil || goja.IsUndefined(res) {
			out.OK, out.Result = true, json.RawMessage("null")
			return out
		}
		b, err := json.Marshal(res.Export())
		if err != nil {
			out.Error = "result is not JSON-serialisable: " + err.Error()
			return out
		}
		out.OK, out.Result = true, b
	case goja.PromiseStateRejected:
		out.Error = rejection(p.Result())
	default:
		// Every wick binding is synchronous, so a pending promise means the
		// script awaited something that can never settle here.
		out.Error = "script awaited something that never resolved (no timers or network here; use wick.call)"
	}
	return out
}

func scriptError(err error) string {
	var ie *goja.InterruptedError
	if ok := asInterrupted(err, &ie); ok {
		return fmt.Sprint(ie.Value())
	}
	return err.Error()
}

func asInterrupted(err error, target **goja.InterruptedError) bool {
	if ie, ok := err.(*goja.InterruptedError); ok {
		*target = ie
		return true
	}
	return false
}

func rejection(v goja.Value) string {
	if o, ok := v.(*goja.Object); ok {
		if st := o.Get("stack"); st != nil && !goja.IsUndefined(st) {
			return st.String()
		}
	}
	return render(v)
}

// bind installs the only globals a script gets beyond the language itself
// (and the pure helpers of bindPure): wick (call, callMany, tool, file),
// console and input. files reports
// whether wick answers file events.
func (rn *run) bind(ctx context.Context, vm *goja.Runtime, input json.RawMessage, files bool) error {
	throw := func(msg string) { panic(vm.NewGoError(fmt.Errorf("%s", msg))) }

	call := func(toolID string, params any, opts map[string]any) any {
		toolID = strings.TrimSpace(toolID)
		if toolID == "" {
			throw("wick.call: tool_id is required")
		}
		res := rn.call(ctx, toolID, params, timeoutOpt(opts))
		if !res.OK {
			throw(toolID + ": " + res.Error)
		}
		return decode(res.Result)
	}

	w := vm.NewObject()
	_ = w.Set("call", call)
	_ = w.Set("tool", func(toolID string) any {
		return func(params any, opts map[string]any) any { return call(toolID, params, opts) }
	})
	_ = w.Set("callMany", func(list []map[string]any) []map[string]any {
		out := make([]map[string]any, len(list))
		var wg sync.WaitGroup
		for i, c := range list {
			tid, _ := c["toolId"].(string)
			if tid == "" {
				tid, _ = c["tool_id"].(string)
			}
			wg.Add(1)
			go func(i int, tid string, params any, ms int64) {
				defer wg.Done()
				if strings.TrimSpace(tid) == "" {
					out[i] = map[string]any{"ok": false, "error": "toolId is required"}
					return
				}
				res := rn.call(ctx, tid, params, ms)
				if !res.OK {
					out[i] = map[string]any{"ok": false, "toolId": tid, "error": res.Error}
					return
				}
				out[i] = map[string]any{"ok": true, "toolId": tid, "result": decode(res.Result)}
			}(i, tid, c["params"], timeoutOpt(c))
		}
		wg.Wait()
		return out
	})
	_ = w.Set("file", rn.fileAPI(ctx, vm, throw, files))
	if err := vm.Set("wick", w); err != nil {
		return err
	}

	con := vm.NewObject()
	for name, level := range map[string]string{"log": "", "info": "", "debug": "", "warn": "WARN", "error": "ERROR"} {
		level := level
		_ = con.Set(name, func(c goja.FunctionCall) goja.Value {
			rn.log(level, c.Arguments)
			return goja.Undefined()
		})
	}
	if err := vm.Set("console", con); err != nil {
		return err
	}

	var in any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return fmt.Errorf("input is not JSON: %w", err)
		}
	}
	if in == nil {
		in = map[string]any{}
	}
	return vm.Set("input", in)
}

func decode(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return v
}

// fileAPI is wick.file: read/write text or JSON, list and exists, all
// relative to the project's codemode/ folder. Every op is a file event that
// wick checks and performs; nothing here opens a file.
func (rn *run) fileAPI(ctx context.Context, vm *goja.Runtime, throw func(string), files bool) *goja.Object {
	do := func(op, path, data string) any {
		if !files {
			throw("wick.file." + op + ": this wick does not support wick.file yet (update wick)")
		}
		res := rn.file(ctx, op, path, data)
		if !res.OK {
			throw("wick.file." + op + " " + path + ": " + res.Error)
		}
		return decode(res.Result)
	}
	text := func(op string, v goja.Value) string {
		if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
			throw("wick.file." + op + ": text is required")
		}
		return v.String()
	}
	f := vm.NewObject()
	_ = f.Set("read", func(path string) any { return do("read", path, "") })
	_ = f.Set("write", func(path string, data goja.Value) any { return do("write", path, text("write", data)) })
	_ = f.Set("readJSON", func(path string) any {
		s, _ := do("read", path, "").(string)
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			throw("wick.file.readJSON " + path + ": not JSON: " + err.Error())
		}
		return v
	})
	_ = f.Set("writeJSON", func(path string, v goja.Value) any {
		if v == nil || goja.IsUndefined(v) {
			throw("wick.file.writeJSON: value is required")
		}
		b, err := json.MarshalIndent(v.Export(), "", "  ")
		if err != nil {
			throw("wick.file.writeJSON " + path + ": value is not JSON-serialisable: " + err.Error())
		}
		return do("write", path, string(b)+"\n")
	})
	_ = f.Set("list", func(dir goja.Value) any {
		d := ""
		if dir != nil && !goja.IsUndefined(dir) && !goja.IsNull(dir) {
			d = dir.String()
		}
		return do("list", d, "")
	})
	_ = f.Set("exists", func(path string) any { return do("exists", path, "") })
	return f
}
