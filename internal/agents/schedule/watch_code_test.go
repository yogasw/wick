package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/codemode"
	"github.com/yogasw/wick/internal/entity"
)

// fakeCode answers every code run with fn, recording what it was given.
type fakeCode struct {
	runs []CodeRun
	fn   func(in map[string]json.RawMessage) (*CodeOutcome, error)
}

func (f *fakeCode) exec(_ context.Context, _ entity.ScheduledMessage, run CodeRun) (*CodeOutcome, error) {
	f.runs = append(f.runs, run)
	var in map[string]json.RawMessage
	_ = json.Unmarshal(run.Input, &in)
	return f.fn(in)
}

// returns makes a fake whose script returns v.
func returns(v string) *fakeCode {
	return &fakeCode{fn: func(map[string]json.RawMessage) (*CodeOutcome, error) {
		return &CodeOutcome{OK: true, Result: json.RawMessage(v), Calls: 2}, nil
	}}
}

func runCode(t *testing.T, x watchExec, steps ...Step) (RunRecord, string) {
	t.Helper()
	if err := ValidateSteps(steps); err != nil {
		t.Fatalf("validate: %v", err)
	}
	m := entity.ScheduledMessage{ID: "sm_test", Message: "new rows?"}
	x.tmpDir = filepath.Join(t.TempDir(), "tick")
	return executeWatchWith(context.Background(), m, steps, x, nil)
}

func TestValidateSteps_Code(t *testing.T) {
	bad := []Step{
		{Kind: "code"},
		{Kind: "code", Code: "return 1", ScriptPath: "a.ts"},
		{Kind: "code", ScriptPath: "../outside.ts"},
		{Kind: "code", ScriptPath: "/etc/x.ts"},
		{Kind: "code", Code: "return 1", TimeoutSec: 500},
		{Kind: "code", Code: "return 1", ToolID: "conn:a/b"},
		{Kind: "code", Code: strings.Repeat("x", codeMaxBytes+1)},
	}
	for i, s := range bad {
		if err := ValidateSteps([]Step{s}); err == nil {
			t.Errorf("case %d must be rejected: %+v", i, s.ScriptPath)
		}
	}
	ok := []Step{{Kind: " Code ", ScriptPath: "watches/new_rows.ts"}}
	if err := ValidateSteps(ok); err != nil {
		t.Fatal(err)
	}
	if ok[0].Kind != StepKindCode || ok[0].Lang != "ts" || ok[0].Name != "code 1" {
		t.Fatalf("normalized: %+v", ok[0])
	}
	if err := ValidateSteps([]Step{{Kind: "connector", ToolID: "conn:a/b", Code: "x"}}); err == nil {
		t.Fatal("a connector step must not take code")
	}
}

func TestExecuteWatch_CodeEmptyChangedIsPending(t *testing.T) {
	f := returns(`{"changed":[],"snapshot":{"last_id":7}}`)
	rec, result := runCode(t, watchExec{code: f.exec, snapshots: map[string]json.RawMessage{"0": json.RawMessage(`{"last_id":5}`)}},
		Step{Kind: "code", Code: "return {changed: [], snapshot: {last_id: 7}}", Params: map[string]any{"table": "orders"}})
	if rec.Result != entity.WatchResultPending || result != "[]" {
		t.Fatalf("result=%s out=%q", rec.Result, result)
	}
	sr := rec.Steps[0]
	if sr.ExitCode != 1 || sr.Decision != DecisionWait || sr.Calls != 2 || len(sr.CodeHash) != 16 {
		t.Fatalf("step: %+v", sr)
	}
	if string(rec.snapshots["0"]) != `{"last_id":7}` {
		t.Fatalf("snapshot not carried: %s", rec.snapshots["0"])
	}
	if !strings.Contains(rec.Reason, "nothing changed") {
		t.Fatalf("reason: %q", rec.Reason)
	}
	// The script saw the previous snapshot and its params; ts by default.
	in := f.runs[0]
	if in.Lang != "ts" || !strings.Contains(string(in.Input), `"snapshot":{"last_id":5}`) || !strings.Contains(string(in.Input), `"table":"orders"`) {
		t.Fatalf("run: %+v input=%s", in, in.Input)
	}
}

func TestExecuteWatch_CodeChangedMatches(t *testing.T) {
	f := returns(`{"changed":[{"id":8},{"id":9}],"snapshot":{"last_id":9}}`)
	rec, result := runCode(t, watchExec{code: f.exec}, Step{Kind: "code", Code: "…"})
	if rec.Result != entity.WatchResultMatched || rec.Outcome != OutcomeSuccess || rec.Done {
		t.Fatalf("rec: %+v", rec)
	}
	if result != `[{"id":8},{"id":9}]` {
		t.Fatalf("the session must get changed: %q", result)
	}
	// First fire: no snapshot yet.
	if !strings.Contains(string(f.runs[0].Input), `"snapshot":null`) {
		t.Fatalf("input: %s", f.runs[0].Input)
	}
}

func TestExecuteWatch_CodeChangedFeedsNextStep(t *testing.T) {
	f := returns(`{"changed":[{"id":1,"state":"OK"}]}`)
	rec, _ := runCode(t, watchExec{code: f.exec},
		Step{Kind: "code", Code: "…"},
		Step{Kind: "check", Rules: []Rule{{Path: "0.state", Op: OpEquals, Value: "OK"}}})
	if rec.Result != entity.WatchResultMatched || len(rec.Steps) != 2 || rec.Steps[0].Decision != DecisionNext {
		t.Fatalf("rec: %+v", rec)
	}
}

func TestExecuteWatch_CodeDoneEndsEvenWithoutChanges(t *testing.T) {
	f := returns(`{"changed":[],"done":true}`)
	rec, result := runCode(t, watchExec{code: f.exec}, Step{Kind: "code", Code: "…"}, Step{Kind: "bash", Script: "exit 3"})
	if rec.Result != entity.WatchResultMatched || !rec.Done || len(rec.Steps) != 1 || result != "[]" {
		t.Fatalf("done: rec=%+v result=%q", rec, result)
	}
}

func TestExecuteWatch_CodeFailures(t *testing.T) {
	cases := map[string]*fakeCode{
		"throw": {fn: func(map[string]json.RawMessage) (*CodeOutcome, error) {
			return &CodeOutcome{Error: "TypeError: x is undefined"}, nil
		}},
		"bad return": returns(`[1,2]`),
		"no return":  returns(``),
		"no runner":  {fn: func(map[string]json.RawMessage) (*CodeOutcome, error) { return nil, errors.New("no Code Mode runner") }},
	}
	for name, f := range cases {
		rec, _ := runCode(t, watchExec{code: f.exec}, Step{Kind: "code", Code: "…"})
		if rec.Result != entity.WatchResultError || rec.Steps[0].OK || rec.snapshots != nil {
			t.Errorf("%s: rec=%+v", name, rec)
		}
	}
	rec, _ := runCode(t, watchExec{}, Step{Kind: "code", Code: "…"})
	if rec.Result != entity.WatchResultError || !strings.Contains(rec.Error, "not available") {
		t.Fatalf("no executor: %+v", rec)
	}
}

func TestExecuteWatch_CodeScriptPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "watches"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "return {changed: [], snapshot: input.snapshot}"
	if err := os.WriteFile(filepath.Join(dir, "watches", "rows.ts"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	f := returns(`{"changed":[]}`)
	rec, _ := runCode(t, watchExec{code: f.exec, codeDir: dir}, Step{Kind: "code", ScriptPath: "watches/rows.ts"})
	if rec.Result != entity.WatchResultPending || f.runs[0].Code != src || rec.Steps[0].ScriptPath != "watches/rows.ts" {
		t.Fatalf("rec=%+v code=%q", rec, f.runs[0].Code)
	}
	if f.runs[0].Dir != dir {
		t.Fatalf("wick.file folder = %q, want %q", f.runs[0].Dir, dir)
	}
	if rec.Steps[0].CodeHash != hashText(src)[:16] {
		t.Fatalf("hash: %s", rec.Steps[0].CodeHash)
	}

	// Missing file: a visible failure, retried next fire.
	rec, _ = runCode(t, watchExec{code: f.exec, codeDir: dir}, Step{Kind: "code", ScriptPath: "watches/gone.ts"})
	if rec.Result != entity.WatchResultError || rec.Denied || !strings.Contains(rec.Error, "not found") {
		t.Fatalf("missing: %+v", rec)
	}

	// A symlink out of the folder: refused, and it ends the watch.
	outside := filepath.Join(t.TempDir(), "secret.ts")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "watches", "link.ts")); err != nil {
		t.Fatal(err)
	}
	n := len(f.runs)
	rec, _ = runCode(t, watchExec{code: f.exec, codeDir: dir}, Step{Kind: "code", ScriptPath: "watches/link.ts"})
	if rec.Result != entity.WatchResultError || !rec.Denied || len(f.runs) != n {
		t.Fatalf("escape: %+v", rec)
	}

	// No folder resolved: refused rather than read from anywhere else.
	rec, _ = runCode(t, watchExec{code: f.exec}, Step{Kind: "code", ScriptPath: "watches/rows.ts"})
	if rec.Result != entity.WatchResultError {
		t.Fatalf("no dir: %+v", rec)
	}
}

func TestRunner_CodeStepKeepsSnapshotAndEndsOnDone(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	fire := 0
	var seen []string
	f := &fakeCode{fn: func(in map[string]json.RawMessage) (*CodeOutcome, error) {
		fire++
		seen = append(seen, string(in["snapshot"]))
		switch fire {
		case 1:
			return &CodeOutcome{OK: true, Result: json.RawMessage(`{"changed":[],"snapshot":{"n":1}}`)}, nil
		case 2:
			return &CodeOutcome{OK: true, Result: json.RawMessage(`{"changed":[{"id":"a"}],"snapshot":{"n":2}}`)}, nil
		case 3:
			return &CodeOutcome{Error: "boom"}, nil
		default:
			return &CodeOutcome{OK: true, Result: json.RawMessage(`{"changed":[{"id":"b"}],"snapshot":{"n":3},"done":true}`)}, nil
		}
	}}
	r := NewRunner(s, sender, layout).WithCodeExecutor(f.exec)
	steps, err := EncodeWatch([]Step{{Name: "rows", Kind: StepKindCode, Code: "…"}}, OnMatchContinue)
	if err != nil {
		t.Fatal(err)
	}
	m := newWatchRow(t, s, sid, "", func(e *entity.ScheduledMessage) { e.Steps = steps })

	for i := 0; i < 4; i++ {
		makeDue(r)
		fireDueWatches(t, r)
	}
	// Snapshot moves on with pending and matched runs, not with the error.
	want := []string{"null", `{"n":1}`, `{"n":2}`, `{"n":2}`}
	if strings.Join(seen, " ") != strings.Join(want, " ") {
		t.Fatalf("snapshots seen: %v", seen)
	}
	got, _ := s.Get(context.Background(), m.ID)
	if got.Status != entity.ScheduledStatusDone {
		t.Fatalf("done:true must end a continue watch: status=%s", got.Status)
	}
	// Fire 2 (new rows, watch keeps running) and fire 4 (done) told the session.
	if len(sender.calls) != 2 {
		t.Fatalf("notices: %d", len(sender.calls))
	}
	st := loadWatchState(watchStatePath(layout, *m))
	if string(st.Snapshots["0"]) != `{"n":3}` {
		t.Fatalf("stored snapshot: %s", st.Snapshots["0"])
	}
}

// The code step follows the Code Mode limits in the agents settings: its
// timeout_sec range and its default.
func TestCodeStepFollowsCodemodeLimits(t *testing.T) {
	codemode.SetLimitsSource(func() codemode.Limits {
		return codemode.Limits{Default: 30 * time.Second, Max: 600 * time.Second}
	})
	t.Cleanup(func() { codemode.SetLimitsSource(nil) })
	if err := ValidateSteps([]Step{{Kind: "code", Code: "return 1", TimeoutSec: 500}}); err != nil {
		t.Fatalf("500s is within a raised max: %v", err)
	}
	if err := ValidateSteps([]Step{{Kind: "code", Code: "return 1", TimeoutSec: 601}}); err == nil {
		t.Fatal("601s is past the max")
	}
	f := returns(`{"changed":[]}`)
	runCode(t, watchExec{code: f.exec}, Step{Kind: "code", Code: "…"})
	if f.runs[0].Timeout != 30*time.Second {
		t.Fatalf("default timeout = %s, want 30s", f.runs[0].Timeout)
	}
}
