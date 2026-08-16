package wick

import (
	"context"

	"google.golang.org/genai"
)

// toolDef is one function tool the engine exposes to the model: the
// genai declaration sent in the request plus the Go handler invoked
// when the model calls it. Handlers return the result text and whether
// it represents an error (surfaced as tool_result is_error=true).
//
// This is wick's own minimal tool abstraction rather than adk-go's
// tool.Tool / functiontool, because the engine drives model.LLM
// directly (see engine.go) and needs the raw handler + declaration, not
// the runner-oriented wrapper.
type toolDef struct {
	decl    *genai.FunctionDeclaration
	handler func(ctx context.Context, args map[string]any) (result string, isError bool)
}

// toolContext carries the per-spawn dependencies tool handlers need
// (workspace dir for shell, session id for session tools, …). Passed to
// the builders so each spawn gets tools bound to its own session.
type toolContext struct {
	Workspace  string
	SessionDir string
	SessionID  string
	Config     *WickConfigResolved
	// Bg holds this spawn's background shells (run_in_background). One
	// registry per spawn, reaped on teardown. nil when shell is disabled
	// or the tool set is built without background support (tests).
	Bg *bgRegistry
	// Jobs holds this spawn's async jobs (job_start + completion inject).
	// One manager per spawn, cancelled on teardown. nil = job tools report
	// "not available" (tests / unwired).
	Jobs *jobManager
	// ToolMemoryMaxMB caps a shell command this agent runs, counting its
	// whole process tree. 0 (the default) = no limit, exactly as before
	// the memory guard existed. Exceeding it fails that one command and
	// returns an error the model can act on; the agent keeps running.
	ToolMemoryMaxMB int
}

// WickConfigResolved is the effective instance config after defaults,
// used by the engine + tool builders. Kept separate from the persisted
// provider.WickConfig so the engine has a non-pointer, always-populated
// view.
//
// "todo" moved from a wick-only native tool to the shared MCP tool
// surface (internal/mcp/handlers/todo.go) so claude/codex/gemini get
// the same task-list tool wick always had — one definition, same UI
// rendering (fe/agents/conversation's merged checklist widget groups
// tool_use events named "todo" regardless of which provider produced
// them). It arrives via externalToolDefs below like ask_user/
// wick_set_title/etc, so there's no separate enable/disable knob for it
// here.
type WickConfigResolved struct {
	ShellTool bool
	FsTools   bool
	MaxTurns  int
}

// buildTools assembles the tool set for one spawn. shell + native fs
// tools have no external deps and ship directly; everything else
// (including todo) comes from the shared MCP tool surface via
// externalToolDefs so wick and the CLI providers share one definition.
func buildTools(tc toolContext) []toolDef {
	var tools []toolDef
	if tc.Config == nil || tc.Config.ShellTool {
		// shell_output / shell_kill share tc.Bg (the per-spawn background
		// registry, set by the spawner). When Bg is nil (tests / disabled)
		// the bg handlers report "not available" rather than panic, so it's
		// safe to register them unconditionally alongside shell.
		tools = append(tools, shellTool(tc), shellOutputTool(tc), shellKillTool(tc))
		// Async jobs run off-turn and inject a completion turn. The only
		// runner today is shell, so gate the job tools on shell too. tc.Jobs
		// nil (tests / unwired) → handlers report "not available".
		tools = append(tools, jobStartTool(tc), jobStatusTool(tc), jobLogTool(tc), jobCancelTool(tc))
	}
	if tc.Config == nil || tc.Config.FsTools {
		tools = append(tools, readFileTool(tc), writeFileTool(tc), editFileTool(tc))
	}
	// The full wick MCP tool surface (todo, ask_user, wick_set_title,
	// wick_session_info, connectors, providers, skills, wickmanager, …)
	// contributed by the host via SetToolProvider. nil provider (tests /
	// unwired) → just the built-ins above.
	tools = append(tools, externalToolDefs(ToolScope{
		SessionID: tc.SessionID,
		Workspace: tc.Workspace,
	})...)
	return tools
}

// declarations converts the tool set into the genai Tool payload placed
// on GenerateContentConfig.Tools. Empty set → nil (no tools advertised).
func declarations(tools []toolDef) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, t.decl)
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}
