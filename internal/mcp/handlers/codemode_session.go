package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/codemode"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// SessionCodeRun is a wick_run_code request that arrives without MCP args —
// the CLI channel's `codemode run`. Exactly one of Code or ScriptPath is set.
type SessionCodeRun struct {
	Lang       string
	Code       string
	ScriptPath string
	Input      json.RawMessage
	Timeout    time.Duration
}

// ErrCodeSource marks a run refused before it started: no code, both code
// and script_path, or a script_path that does not resolve. The caller's
// mistake, not the runner's.
var ErrCodeSource = errors.New("code source")

// RunCodeInSession runs a script as one session's caller, with the same
// rules as wick_run_code: script_path and wick.file resolve in that
// session's project folder, session connectors resolve as that session,
// and every wick.call goes through RunCodeAs (pre-flight + ExecuteAs, audit
// source code_mode). The agent scope is whatever ctx carries.
func RunCodeInSession(ctx context.Context, svc *connectors.Service, layout agentconfig.Layout, run SessionCodeRun, sessionID string, user *entity.User, tagIDs []string) (*codemode.Result, error) {
	dir, noDir := sessionProjectDir(layout, sessionID)
	code, err := runCodeSource(dir, noDir, map[string]any{"code": run.Code, "script_path": run.ScriptPath})
	if err != nil {
		return nil, codeSourceError{err}
	}
	ctx = WithCodeRunOrigin(ctx, entity.CodeModeViaCLI, run.ScriptPath)
	return RunCodeAs(ctx, svc, layout, codemode.Request{Lang: run.Lang, Code: code, Input: run.Input, Timeout: run.Timeout, Dir: dir, NoDir: noDir}, sessionID, user, tagIDs)
}

// codeSourceError is a source error that matches ErrCodeSource but reads
// as the error itself.
type codeSourceError struct{ err error }

func (e codeSourceError) Error() string        { return e.err.Error() }
func (e codeSourceError) Is(target error) bool { return target == ErrCodeSource }
