package main

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// memFiles is a fake wick side of wick.file: an in-memory codemode/ folder
// that refuses the same escapes wick does (absolute, ..).
type memFiles struct {
	mu    sync.Mutex
	files map[string]string
	ops   []string
}

func (m *memFiles) answer(op, path, data string) callResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops = append(m.ops, op+" "+path)
	if strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return callResult{Error: "must stay inside codemode/"}
	}
	ok := func(v any) callResult {
		b, _ := json.Marshal(v)
		return callResult{OK: true, Result: b}
	}
	switch op {
	case "read":
		s, found := m.files[path]
		if !found {
			return callResult{Error: "not found in codemode/"}
		}
		return ok(s)
	case "write":
		m.files[path] = data
		return ok(map[string]any{"path": path, "bytes": len(data)})
	case "exists":
		_, found := m.files[path]
		return ok(found)
	case "list":
		out := []map[string]any{}
		for p, s := range m.files {
			if path == "" || strings.HasPrefix(p, path+"/") {
				out = append(out, map[string]any{"path": p, "size": len(s), "modified": "2026-01-01T00:00:00Z"})
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i]["path"].(string) < out[j]["path"].(string) })
		return ok(out)
	}
	return callResult{Error: "unknown op " + op}
}

func newFileFake(t *testing.T) (*fakeWick, *memFiles) {
	f := newFake(t, 0)
	m := &memFiles{files: map[string]string{"items.json": `[{"id":"a"},{"id":"b"}]`, "note.txt": "hello"}}
	f.file = m.answer
	return f, m
}

func TestFileReadWriteJSON(t *testing.T) {
	f, m := newFileFake(t)
	ev, _ := f.run("ts", `
		const items: { id: string }[] = wick.file.readJSON("items.json");
		const note = wick.file.read("note.txt");
		wick.file.writeJSON("out/result.json", { n: items.length, note });
		wick.file.write("out/plain.txt", "x" + 1);
		return { exists: wick.file.exists("out/result.json"), missing: wick.file.exists("nope.json"), n: items.length };
	`, nil, 5*time.Second)
	if !ev.OK || string(ev.Result) != `{"exists":true,"missing":false,"n":2}` {
		t.Fatalf("got ok=%v result=%s err=%s", ev.OK, ev.Result, ev.Error)
	}
	if got := m.files["out/result.json"]; got != "{\n  \"n\": 2,\n  \"note\": \"hello\"\n}\n" {
		t.Fatalf("writeJSON wrote %q", got)
	}
	if m.files["out/plain.txt"] != "x1" {
		t.Fatalf("write wrote %q", m.files["out/plain.txt"])
	}
}

func TestFileList(t *testing.T) {
	f, _ := newFileFake(t)
	ev, _ := f.run("js", `
		wick.file.write("sub/a.txt", "1");
		return [wick.file.list().map(e => e.path), wick.file.list("sub").map(e => e.path + ":" + e.size)];
	`, nil, 5*time.Second)
	if !ev.OK || string(ev.Result) != `[["items.json","note.txt","sub/a.txt"],["sub/a.txt:1"]]` {
		t.Fatalf("got ok=%v result=%s err=%s", ev.OK, ev.Result, ev.Error)
	}
}

func TestFileErrorsThrow(t *testing.T) {
	f, _ := newFileFake(t)
	ev, _ := f.run("js", `
		const errs = [];
		for (const p of ["../escape.txt", "/etc/hosts", "missing.json"]) {
			try { wick.file.read(p); errs.push("read " + p); } catch (e) { errs.push(String(e.message || e)); }
		}
		try { wick.file.write("x.txt"); } catch (e) { errs.push(String(e.message || e)); }
		return errs;
	`, nil, 5*time.Second)
	var errs []string
	_ = json.Unmarshal(ev.Result, &errs)
	if len(errs) != 4 || !strings.Contains(errs[0], "inside codemode/") || !strings.Contains(errs[1], "inside codemode/") ||
		!strings.Contains(errs[2], "not found") || !strings.Contains(errs[3], "text is required") {
		t.Fatalf("errors = %q (err=%s)", errs, ev.Error)
	}
	if f.calls.Load() != 3 {
		t.Fatalf("file events = %d, want 3 (write without text never leaves the runner)", f.calls.Load())
	}
}

func TestFileJSONNotJSON(t *testing.T) {
	f, _ := newFileFake(t)
	ev, _ := f.run("js", `return wick.file.readJSON("note.txt");`, nil, 5*time.Second)
	if ev.OK || !strings.Contains(ev.Error, "not JSON") {
		t.Fatalf("got ok=%v err=%s", ev.OK, ev.Error)
	}
}

func TestFileOldWick(t *testing.T) {
	f, _ := newFileFake(t)
	f.noFiles = true
	ev, _ := f.run("js", `return wick.file.read("note.txt");`, nil, 5*time.Second)
	if ev.OK || !strings.Contains(ev.Error, "update wick") || f.calls.Load() != 0 {
		t.Fatalf("got ok=%v err=%s calls=%d", ev.OK, ev.Error, f.calls.Load())
	}
}

func TestDoneAnnouncesFileFeature(t *testing.T) {
	f := newFake(t, 0)
	ev, _ := f.run("js", `return 1;`, nil, 5*time.Second)
	if len(ev.Features) != 1 || ev.Features[0] != featureFile {
		t.Fatalf("features = %v", ev.Features)
	}
}
