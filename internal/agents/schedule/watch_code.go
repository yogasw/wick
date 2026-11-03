package schedule

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// A code step runs a script through Code Mode (a codemode_* runner plugin):
// the script's wick.call goes back through the wick_execute path as the
// schedule's run-as identity, so it can do what a connector step does, but
// compare across calls and remember what it saw.
//
// Contract. The script reads input.snapshot (what it returned as snapshot on
// the previous fire, null on the first), input.prev (the previous step's
// output, JSON when it parses) and input.params (the step's params), and
// returns {changed: [...], snapshot, done?}:
//
//	changed empty   → pending: nothing delivered, no LLM
//	changed ≠ empty → ok: the session is woken with changed as the result
//	done: true      → ok, and the watch ends here (on_match continue too)
//
// The snapshot is kept in watch-state.json per step, and only after a run
// that did not error — a later step failing must not swallow the changes the
// next fire would report again.

// StepKindCode is a Code Mode step.
const StepKindCode = "code"

const (
	codeMaxBytes     = 64 << 10 // inline code
	codeFileMaxBytes = 256 << 10
	codeMaxTimeout   = 120
	codeDefaultLang  = "ts"
	// codeQueueSlack is how long a code step may wait for a busy runner on
	// top of its own timeout.
	codeQueueSlack = 2 * time.Minute
)

// CodeRun is one script run asked of the Code Mode host.
type CodeRun struct {
	Lang    string
	Code    string
	Input   json.RawMessage
	Timeout time.Duration
}

// CodeOutcome is what a code run returned (a subset of codemode.Result).
type CodeOutcome struct {
	OK     bool
	Result json.RawMessage
	Error  string
	Logs   []string
	Calls  int
}

// CodeExecutor runs a script as the schedule's run-as identity; every call
// it makes goes through the same gated, audited path as a connector step.
// An error means the run could not happen at all (no runner, no identity).
type CodeExecutor func(ctx context.Context, m entity.ScheduledMessage, run CodeRun) (*CodeOutcome, error)

// CodeCall runs one script as user (with tagIDs) for sessionID.
type CodeCall func(ctx context.Context, run CodeRun, sessionID string, user *entity.User, tagIDs []string) (*CodeOutcome, error)

// NewCodeExecutor builds the watch code executor, with the same identity
// rules as NewConnectorExecutor.
func NewCodeExecutor(lookup UserLookup, call CodeCall) CodeExecutor {
	return func(ctx context.Context, m entity.ScheduledMessage, run CodeRun) (*CodeOutcome, error) {
		u, tags, sessionID, err := resolveRunAs(ctx, lookup, m)
		if err != nil {
			return nil, err
		}
		return call(ctx, run, sessionID, u, tags)
	}
}

// HasCode reports whether any step is a code step.
func HasCode(steps []Step) bool {
	for _, s := range steps {
		if s.Kind == StepKindCode {
			return true
		}
	}
	return false
}

func validateCode(s *Step, at string) error {
	hasCode, hasPath := strings.TrimSpace(s.Code) != "", strings.TrimSpace(s.ScriptPath) != ""
	switch {
	case hasCode == hasPath:
		return fmt.Errorf(`%s: kind=code takes exactly one of code (inline) or script_path (relative to the project folder), e.g. "script_path":"watches/new_rows.ts"`, at)
	case len(s.Code) > codeMaxBytes:
		return fmt.Errorf("%s.code: too long (%d bytes, max %d) — put it in a file and use script_path", at, len(s.Code), codeMaxBytes)
	}
	if hasPath {
		if err := validScriptPath(s.ScriptPath); err != nil {
			return fmt.Errorf("%s.script_path: %q %v", at, s.ScriptPath, err)
		}
	}
	s.Lang = strings.ToLower(strings.TrimSpace(s.Lang))
	if s.Lang == "" {
		s.Lang = codeDefaultLang
	}
	if s.TimeoutSec < 0 || s.TimeoutSec > codeMaxTimeout {
		return fmt.Errorf("%s.timeout_sec: %d out of range; use 1-%d (default 30)", at, s.TimeoutSec, codeMaxTimeout)
	}
	if s.ToolID != "" || s.Script != "" || len(s.Rules)+len(s.FailRules) > 0 {
		return fmt.Errorf("%s: a code step takes code or script_path (+ lang, params, timeout_sec) only", at)
	}
	return nil
}

// validScriptPath is the shape check done when the step is stored; where the
// path really leads is checked again on every run (readScript).
func validScriptPath(p string) error {
	if filepath.IsAbs(p) || strings.HasPrefix(p, "~") {
		return errors.New("must be relative to the project folder")
	}
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == ".." {
			return errors.New("must stay inside the project folder (no ..)")
		}
	}
	return nil
}

// readScript loads script_path from dir. The real path (symlinks resolved)
// must sit inside dir's real path; anything else is refused as denied, since
// it cannot heal by itself.
func readScript(dir, rel string) (string, error) {
	if dir == "" {
		return "", errors.New("no project folder to read script_path from")
	}
	if err := validScriptPath(rel); err != nil {
		return "", watchDenied("script_path " + rel + ": " + err.Error())
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("project folder: %w", err)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(root, rel))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("script_path %s: file not found in the project folder", rel)
		}
		return "", fmt.Errorf("script_path %s: %w", rel, err)
	}
	if !strings.HasPrefix(real, root+string(os.PathSeparator)) {
		return "", watchDenied("script_path " + rel + " resolves outside the project folder")
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("script_path %s: %w", rel, err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("script_path %s: not a regular file", rel)
	}
	if fi.Size() > codeFileMaxBytes {
		return "", fmt.Errorf("script_path %s: too large (%d bytes, max %d)", rel, fi.Size(), codeFileMaxBytes)
	}
	b, err := os.ReadFile(real)
	if err != nil {
		return "", fmt.Errorf("script_path %s: %w", rel, err)
	}
	return string(b), nil
}

// codeReturn is the shape a code step's script returns.
type codeReturn struct {
	Changed  []json.RawMessage `json:"changed"`
	Snapshot json.RawMessage   `json:"snapshot"`
	Done     bool              `json:"done"`
}

// codeStepResult is what runCodeStep hands back to executeWatch.
type codeStepResult struct {
	out      []byte // changed, as a JSON array
	snapshot json.RawMessage
	pending  bool
	done     bool
	hash     string
	logs     string
	calls    int
}

// runCodeStep runs step s (index i). snapshot is what the step returned last
// time. An error is a failed step; pending means changed came back empty.
func runCodeStep(ctx context.Context, m entity.ScheduledMessage, s Step, i int, x watchExec, prev []byte) (r codeStepResult, err error) {
	src := s.Code
	if strings.TrimSpace(s.ScriptPath) != "" {
		if src, err = readScript(x.codeDir, s.ScriptPath); err != nil {
			return r, err
		}
	}
	r.hash = hashText(src)[:16]
	if x.code == nil {
		return r, errors.New("code steps are not available in this process")
	}
	input := map[string]any{"snapshot": nil, "params": s.Params, "schedule_id": m.ID}
	if snap, ok := x.snapshots[snapshotKey(i)]; ok && len(snap) > 0 {
		input["snapshot"] = snap
	}
	if len(prev) > 0 {
		if json.Valid(prev) {
			input["prev"] = json.RawMessage(prev)
		} else {
			input["prev"] = string(prev)
		}
	}
	in, err := json.Marshal(input)
	if err != nil {
		return r, fmt.Errorf("input: %w", err)
	}
	timeout := bashDefaultTimeout
	if s.TimeoutSec > 0 {
		timeout = time.Duration(s.TimeoutSec) * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout+codeQueueSlack)
	defer cancel()
	res, err := x.code(cctx, m, CodeRun{Lang: s.Lang, Code: src, Input: in, Timeout: timeout})
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return r, fmt.Errorf("timed out after %s", timeout+codeQueueSlack)
		}
		return r, err
	}
	r.calls = res.Calls
	r.logs = strings.Join(res.Logs, "\n")
	if !res.OK {
		msg := strings.TrimSpace(res.Error)
		if msg == "" {
			msg = "script failed"
		}
		return r, errors.New(msg)
	}
	var ret codeReturn
	if len(bytes.TrimSpace(res.Result)) == 0 || json.Unmarshal(res.Result, &ret) != nil {
		return r, fmt.Errorf(`the script must return {changed: [...], snapshot, done?}; got %s`, clip(string(res.Result)))
	}
	r.snapshot, r.done = ret.Snapshot, ret.Done
	if ret.Changed == nil {
		ret.Changed = []json.RawMessage{}
	}
	r.out, _ = json.Marshal(ret.Changed)
	r.pending = len(ret.Changed) == 0 && !ret.Done
	return r, nil
}

func snapshotKey(i int) string { return strconv.Itoa(i) }

// watchCodeDir is where script_path is read from: the same confined folder
// bash steps run in.
func (r *Runner) watchCodeDir(m entity.ScheduledMessage, steps []Step) string {
	for _, s := range steps {
		if s.Kind == StepKindCode && strings.TrimSpace(s.ScriptPath) != "" {
			if dir, err := watchExecDir(r.layout, m); err == nil {
				return dir
			}
			return ""
		}
	}
	return ""
}
