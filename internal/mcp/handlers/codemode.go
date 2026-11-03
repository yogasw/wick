package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/codemode"
	"github.com/yogasw/wick/internal/codemode/scriptpath"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/services/plugin"
)

// CodeModeRunners finds the installed Code Mode runner plugins. A var so
// tests can swap in a fake runner.
var CodeModeRunners = codemode.PluginRunners(plugin.Default)

// CodeModeInstall downloads and loads the default runner when none is
// installed (codemode.Installer.Ensure, set by the server). nil = no
// download, a run without a runner fails with codemode.ErrNoRunner.
var CodeModeInstall func(ctx context.Context) error

// WickRunCode runs a script in a Code Mode runner. Every wick.call it makes
// goes through the same path as wick_execute, as this caller.
func WickRunCode(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, args map[string]any, user *entity.User, tagIDs []string) {
	lang, _ := args["lang"].(string)
	var input json.RawMessage
	if v, ok := args["input"]; ok && v != nil {
		b, err := json.Marshal(v)
		if err != nil {
			rsp.ToolError(w, req.ID, "input must be JSON", "")
			return
		}
		input = b
	}
	timeout := time.Duration(intArg(args, "timeout_ms")) * time.Millisecond
	sessionID := CallSession(r, args)
	dir, noDir := sessionProjectDir(layout, sessionID)
	code, err := runCodeSource(dir, noDir, args)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), "")
		return
	}

	host := codemode.Host{
		Runners: CodeModeRunners,
		Install: CodeModeInstall,
		Check: func(ctx context.Context, toolID string) error {
			return checkToolAccess(ctx, svc, layout, toolID, sessionID, user, tagIDs)
		},
		Exec: func(ctx context.Context, toolID string, params map[string]any) (string, error) {
			return executeOneFrom(ctx, r, svc, layout, toolID, params, sessionID, user, tagIDs, entity.ConnectorRunSourceCodeMode)
		},
		Describe: func(toolID string) (string, bool) { return describeTool(r.Context(), svc, toolID) },
	}
	res, err := host.Run(r.Context(), codemode.Request{Lang: lang, Code: code, Input: input, Timeout: timeout, Dir: dir, NoDir: noDir})
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), "")
		return
	}
	out, err := json.Marshal(res)
	if err != nil {
		rsp.ToolError(w, req.ID, "failed to encode result: "+err.Error(), "")
		return
	}
	// A script that failed is a tool error so the model notices, but the
	// body still carries the calls already made and the logs.
	if !res.OK {
		rsp.WriteResult(w, req.ID, ToolCallResult{Content: []ToolContent{{Type: "text", Text: string(out)}}, IsError: true})
		return
	}
	if msg := OversizedResult(string(out)); msg != "" {
		rsp.ToolError(w, req.ID, msg, "")
		return
	}
	rsp.WriteResult(w, req.ID, ToolCallResult{Content: []ToolContent{{Type: "text", Text: string(out)}}})
}

// runCodeSource is the script wick_run_code runs: code inline, or
// script_path read fresh on every run from the calling session's project
// folder dir, with the watch code step's rules (scriptpath.Read). noDir
// says why dir is empty.
func runCodeSource(dir, noDir string, args map[string]any) (string, error) {
	code, _ := args["code"].(string)
	rel, _ := args["script_path"].(string)
	rel = strings.TrimSpace(rel)
	if (strings.TrimSpace(code) != "") == (rel != "") {
		return "", errors.New(`pass exactly one of code (inline) or script_path (relative to the session's project folder), e.g. "script_path":"scripts/report.ts"`)
	}
	if rel == "" {
		return code, nil
	}
	if dir == "" {
		return "", errors.New("script_path: " + noDir + "; pass code instead")
	}
	return scriptpath.Read(dir, rel)
}

// sessionProjectDir is the calling session's project folder, used by
// script_path and wick.file; the second value says why there is none.
func sessionProjectDir(layout agentconfig.Layout, sessionID string) (string, string) {
	if sessionID == "" {
		return "", "no session, and the project folder comes from the session"
	}
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		return "", fmt.Sprintf("session %s: %v", sessionID, err)
	}
	if sess.Meta.ProjectID == "" || !project.Exists(layout, sess.Meta.ProjectID) {
		return "", "this session has no project folder"
	}
	dir, err := project.ResolvePath(layout, sess.Meta.ProjectID)
	if err != nil {
		return "", fmt.Sprintf("project folder: %v", err)
	}
	return dir, ""
}

// RunCodeAs runs a script for a caller that has no HTTP request — a watch
// schedule's code step, the Code Mode connector. Every call it makes takes
// the ExecuteAs route as user with tagIDs (narrowed by the agent scope ctx
// carries), and the tool_ids it names are pre-flighted the same way as
// wick_run_code's. Nothing here widens access.
func RunCodeAs(ctx context.Context, svc *connectors.Service, layout agentconfig.Layout, req codemode.Request, sessionID string, user *entity.User, tagIDs []string) (*codemode.Result, error) {
	if svc == nil || user == nil {
		return nil, errors.New("code mode unavailable: no run-as user")
	}
	ctx = login.WithUser(ctx, user, tagIDs)
	host := codemode.Host{
		Runners: CodeModeRunners,
		Install: CodeModeInstall,
		Check: func(ctx context.Context, toolID string) error {
			return checkToolAccess(ctx, svc, layout, toolID, sessionID, user, tagIDs)
		},
		Exec: func(ctx context.Context, toolID string, params map[string]any) (string, error) {
			return ExecuteAs(ctx, svc, layout, toolID, params, sessionID, user, tagIDs, entity.ConnectorRunSourceCodeMode)
		},
		Describe: func(toolID string) (string, bool) { return describeTool(ctx, svc, toolID) },
	}
	return host.Run(ctx, req)
}

// checkToolAccess is the pre-flight of one tool_id: it parses, names an
// operation the connector has, and is reachable by this caller (instance
// visibility and the agent's checklist). The call itself is checked again
// in full when it runs.
func checkToolAccess(ctx context.Context, svc *connectors.Service, layout agentconfig.Layout, toolID, sessionID string, user *entity.User, tagIDs []string) error {
	connectorID, opKey, accountID, err := ParseToolIDFull(toolID)
	if err != nil {
		return err
	}
	_, isSession, err := SessionInstanceForID(layout, sessionID, connectorID)
	if err != nil {
		return err
	}
	if isSession {
		return nil
	}
	allowed, verr := svc.IsVisibleTo(ctx, connectorID, callerIDFromCtx(ctx), tagIDs, user.IsAdmin())
	if verr != nil || !allowed {
		return errors.New("not found or not accessible")
	}
	row, err := svc.Get(ctx, connectorID)
	if err != nil || row == nil {
		return errors.New("not found or not accessible")
	}
	destructive, found := opOf(svc, row.Key, opKey)
	if !found {
		return errors.New("connector has no operation " + opKey)
	}
	if scope := connectors.AgentScopeFrom(ctx); scope != nil {
		if !scope.AllowConnector(connectorID) || !scope.AllowAccount(connectorID, accountID) || !scope.AllowOp(connectorID, opKey, destructive) {
			return errors.New("not allowed for this agent")
		}
	}
	return nil
}

func opOf(svc *connectors.Service, key, opKey string) (destructive, found bool) {
	mod, ok := svc.Module(key)
	if !ok {
		return false, false
	}
	for _, o := range mod.AllOps() {
		if o.Key == opKey && !o.ConfigOnly {
			return o.Destructive, true
		}
	}
	return false, false
}

// describeTool labels a tool_id for the run's call list.
func describeTool(ctx context.Context, svc *connectors.Service, toolID string) (string, bool) {
	connectorID, opKey, _, err := ParseToolIDFull(toolID)
	if err != nil {
		return "", false
	}
	row, err := svc.Get(ctx, connectorID)
	if err != nil || row == nil {
		return "", false
	}
	destructive, _ := opOf(svc, row.Key, opKey)
	return strings.TrimSpace(row.Label), destructive
}
