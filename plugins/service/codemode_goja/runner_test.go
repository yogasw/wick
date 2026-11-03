package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yogasw/wick/pkg/service"
)

// fakeWick plays wick's side of a run: it posts the script, answers every
// call event with answer(), and returns the done event.
type fakeWick struct {
	t      *testing.T
	srv    *httptest.Server
	calls  atomic.Int64
	answer func(toolID string, params map[string]any) callResult
	// file answers file events; noFiles plays a wick without wick.file.
	file    func(op, path, data string) callResult
	noFiles bool
	// hang lists tool_ids wick never answers (a hung connector on an old
	// wick); timeouts records each call's timeout_ms.
	hang     map[string]bool
	timeouts sync.Map
}

func newFake(t *testing.T, budget int) *fakeWick {
	t.Helper()
	cfg := map[string]string{}
	if budget > 0 {
		cfg[cfgBudgetMB] = fmt.Sprint(budget)
	}
	f := &fakeWick{t: t, srv: httptest.NewServer(service.Handler(Module(), cfg))}
	f.answer = func(toolID string, _ map[string]any) callResult {
		return callResult{OK: true, Result: json.RawMessage(`{"tool":"` + toolID + `","rows":[1,2,3,4,5]}`)}
	}
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeWick) run(lang, code string, input any, timeout time.Duration) (event, int) {
	f.t.Helper()
	in, _ := json.Marshal(input)
	rr := runRequest{RunID: fmt.Sprintf("r%d", time.Now().UnixNano()), Lang: lang, Code: code, Input: in, TimeoutMS: timeout.Milliseconds()}
	if !f.noFiles {
		rr.Features = []string{featureFile}
	}
	body, _ := json.Marshal(rr)
	resp, err := http.Post(f.srv.URL+"/_wick/codemode/run", "application/json", bytes.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return event{}, resp.StatusCode
	}
	var runID string
	_ = json.Unmarshal(body, &struct {
		RunID *string `json:"run_id"`
	}{&runID})
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var ev event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			f.t.Fatalf("bad event %q: %v", sc.Text(), err)
		}
		switch ev.Type {
		case "call", "file":
			f.calls.Add(1)
			var res callResult
			if ev.Type == "file" {
				if f.file == nil {
					f.t.Fatalf("unexpected file event %+v", ev)
				}
				res = f.file(ev.Op, ev.Path, ev.Data)
			} else {
				f.timeouts.Store(ev.ToolID, ev.TimeoutMS)
				if f.hang[ev.ToolID] {
					continue
				}
				var p map[string]any
				_ = json.Unmarshal(ev.Params, &p)
				res = f.answer(ev.ToolID, p)
			}
			res.RunID, res.ID = runID, ev.ID
			go func() {
				b, _ := json.Marshal(res)
				r, err := http.Post(f.srv.URL+"/_wick/codemode/result", "application/json", bytes.NewReader(b))
				if err == nil {
					r.Body.Close()
				}
			}()
		case "done":
			return ev, http.StatusOK
		}
	}
	f.t.Fatal("stream ended without done")
	return event{}, 0
}

func TestRunTypeScriptWithCalls(t *testing.T) {
	f := newFake(t, 0)
	ev, _ := f.run("ts", `
		// Tools
		const listRows = "conn:00000000-0000-0000-0000-000000000001/list_rows";
		type R = { rows: number[] };
		const r: R = await wick.call(listRows, { limit: 5 });
		console.log("got", r.rows.length, "rows");
		console.warn("careful");
		return { big: r.rows.filter(n => n > 2).length, tool: r.tool, from: input.who };
	`, map[string]any{"who": "client A"}, 5*time.Second)
	if !ev.OK {
		t.Fatalf("run failed: %s", ev.Error)
	}
	if string(ev.Result) != `{"big":3,"from":"client A","tool":"conn:00000000-0000-0000-0000-000000000001/list_rows"}` {
		t.Fatalf("result = %s", ev.Result)
	}
	if strings.Join(ev.Logs, "|") != `got 5 rows|[WARN] careful` {
		t.Fatalf("logs = %q", ev.Logs)
	}
}

func TestCallManyAndToolHelper(t *testing.T) {
	f := newFake(t, 0)
	f.answer = func(toolID string, p map[string]any) callResult {
		if p["id"] == "bad" {
			return callResult{Error: "not found"}
		}
		return callResult{OK: true, Result: json.RawMessage(fmt.Sprintf(`{"id":%q}`, p["id"]))}
	}
	ev, _ := f.run("js", `
		const get = wick.tool("conn:x/get");
		const one = get({ id: "a" });
		const many = wick.callMany([
			{ toolId: "conn:x/get", params: { id: "b" } },
			{ toolId: "conn:x/get", params: { id: "bad" } },
			{ tool_id: "conn:x/get", params: { id: "c" } },
		]);
		return [one.id, many.map(m => m.ok ? m.result.id : "ERR:" + m.error)];
	`, nil, 5*time.Second)
	if !ev.OK || string(ev.Result) != `["a",["b","ERR:not found","c"]]` {
		t.Fatalf("got ok=%v result=%s err=%s", ev.OK, ev.Result, ev.Error)
	}
	if f.calls.Load() != 4 {
		t.Fatalf("calls = %d", f.calls.Load())
	}
}

func TestFailedCallThrows(t *testing.T) {
	f := newFake(t, 0)
	f.answer = func(string, map[string]any) callResult {
		return callResult{Error: "tool_id not found or not accessible"}
	}
	ev, _ := f.run("ts", `try { await wick.call("conn:x/y", {}); return "no"; } catch (e) { return String(e.message || e); }`, nil, 5*time.Second)
	if !ev.OK || !strings.Contains(string(ev.Result), "not accessible") {
		t.Fatalf("got ok=%v result=%s err=%s", ev.OK, ev.Result, ev.Error)
	}
	ev, _ = f.run("ts", `await wick.call("conn:x/y", {});`, nil, 5*time.Second)
	if ev.OK || !strings.Contains(ev.Error, "not accessible") {
		t.Fatalf("uncaught failure: ok=%v err=%s", ev.OK, ev.Error)
	}
}

func TestNoHostAccess(t *testing.T) {
	f := newFake(t, 0)
	ev, _ := f.run("js", `return [typeof fetch, typeof require, typeof process, typeof std, typeof os, typeof setTimeout, typeof XMLHttpRequest].join("/");`, nil, 5*time.Second)
	if !ev.OK || string(ev.Result) != `"undefined/undefined/undefined/undefined/undefined/undefined/undefined"` {
		t.Fatalf("globals leaked: %s %s", ev.Result, ev.Error)
	}
}

func TestSyntaxError(t *testing.T) {
	f := newFake(t, 0)
	ev, _ := f.run("ts", "const a = ;", nil, 5*time.Second)
	if ev.OK || !strings.Contains(ev.Error, "syntax error at line 1") {
		t.Fatalf("got %+v", ev)
	}
}

func TestTimeoutStopsLoop(t *testing.T) {
	f := newFake(t, 0)
	start := time.Now()
	ev, _ := f.run("js", `while (true) {}`, nil, 500*time.Millisecond)
	if ev.OK || ev.Error != "timeout" {
		t.Fatalf("got %+v", ev)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v", d)
	}
	// The runner is still usable.
	ev, _ = f.run("js", `return 1 + 1`, nil, 5*time.Second)
	if !ev.OK || string(ev.Result) != "2" {
		t.Fatalf("after timeout: %+v", ev)
	}
}

func TestOneShotAllocationsRefused(t *testing.T) {
	f := newFake(t, 0)
	for _, code := range []string{
		`return "x".repeat(3e9).length`,
		`return new Array(3e8).fill(1).length`,
		`return Array(3e8).length`,
		`return "".padStart(1e9, "x").length`,
	} {
		ev, _ := f.run("js", code, nil, 5*time.Second)
		if ev.OK || !strings.Contains(ev.Error, "allocation too large") {
			t.Fatalf("%s: got ok=%v err=%s", code, ev.OK, ev.Error)
		}
	}
	// Ordinary use of the same builtins still works.
	ev, _ := f.run("js", `return ["ab".repeat(3), Array.isArray(new Array(3)), [1,2,3].fill(0).join(""), Array.from([1,2]).length, "7".padStart(3, "0")]`, nil, 5*time.Second)
	if !ev.OK || string(ev.Result) != `["ababab",true,"000",2,"007"]` {
		t.Fatalf("got %s %s", ev.Result, ev.Error)
	}
}

func TestGradualAllocationStopsAtBudget(t *testing.T) {
	f := newFake(t, 64)
	ev, _ := f.run("js", `const a = []; for (let i = 0; ; i++) a.push("x".repeat(1e5) + i);`, nil, 20*time.Second)
	if ev.OK || !strings.Contains(ev.Error, "out of memory") {
		t.Fatalf("got ok=%v err=%s", ev.OK, ev.Error)
	}
}

func TestStatusAndBadLang(t *testing.T) {
	f := newFake(t, 0)
	resp, err := http.Get(f.srv.URL + "/_wick/codemode/status")
	if err != nil {
		t.Fatal(err)
	}
	var st status
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if st.BudgetMB != defaultBudgetMB || st.Engine != "goja" || strings.Join(st.Langs, ",") != "ts,js" {
		t.Fatalf("status = %+v", st)
	}
	if _, code := f.run("py", "print(1)", nil, time.Second); code != http.StatusBadRequest {
		t.Fatalf("py accepted: %d", code)
	}
}

func TestManyRunsAtOnce(t *testing.T) {
	f := newFake(t, 0)
	f.answer = func(string, map[string]any) callResult {
		time.Sleep(50 * time.Millisecond)
		return callResult{OK: true, Result: json.RawMessage(`{"n":1}`)}
	}
	const n = 50
	errs := make(chan string, n)
	start := time.Now()
	for i := 0; i < n; i++ {
		go func() {
			ev, _ := f.run("js", `const r = wick.call("conn:x/y", {}); return r.n;`, nil, 10*time.Second)
			if !ev.OK {
				errs <- ev.Error
				return
			}
			errs <- ""
		}()
	}
	for i := 0; i < n; i++ {
		if e := <-errs; e != "" {
			t.Fatal(e)
		}
	}
	// 50 runs each waiting 50 ms on "I/O" finish together, not one by one.
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("50 concurrent runs took %v", d)
	}
}
