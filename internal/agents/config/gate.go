package config

// GateConfig holds command-gate knobs.
//
//   - PermissionMode: gates per-tool permission prompts (PreToolUse
//     hook). "on" installs the hook; "bypass" skips it (use for
//     non-interactive channels — Slack/HTTP — where no human can
//     answer a prompt).
//   - AskUserMode: gates the MCP `ask_user` tool. "on" routes the
//     question to the web UI; "off" returns an error to the LLM so
//     it picks a default instead of hanging the run.
//
// GateEnabled is the master switch for the COMMAND gate only — the
// PreToolUse hook path (PermissionMode + AllowedCmds + the gate
// binary). Off = the hook is not installed and commands run
// unguarded.
//
// AskUserMode is INDEPENDENT of GateEnabled. ask_user does not ride
// the hook — it uses wick's own socket/SSE channel — so turning the
// command gate off must not disable it. Only AskUserMode governs
// ask_user (and wick_session_workspace configure/add modals).
//
// Per-channel override (planned phase 2) will let non-interactive
// channels (Slack/HTTP/cron) flip their own mode without changing the
// global config; until then the global mode applies everywhere.
type GateConfig struct {
	GateEnabled    bool   `wick:"bool;group=Permission Gate|Command gate and ask-user policy. Tune these for non-interactive channels (Slack/HTTP) where no human can approve.;desc=Master switch for the COMMAND gate (PreToolUse hook). Off = commands run unguarded (permission bypass). Does NOT affect ask_user — that has its own switch below."`
	PermissionMode string `wick:"dropdown=on|bypass;group=Permission Gate;desc=Permission policy. on = install PreToolUse hook so the user approves each tool call. bypass = run unguarded (use for Slack/HTTP where no human can approve)."`
	AskUserMode    string `wick:"dropdown=on|off;group=Permission Gate;desc=Ask-user policy (independent of the command gate). on = route ask_user MCP calls to the web UI. off = return an error so the agent picks a sensible default instead of blocking."`
	AllowedCmds    string `wick:"kvlist=pattern|scope;group=Permission Gate;desc=Command whitelist for the permission gate. pattern supports a trailing * wildcard (e.g. 'git *'). scope (optional) restricts path args to a directory prefix."`
}

// DefaultGateConfig returns the seed values for the gate config.
// Defaults are safe-for-interactive: gate on, permission prompts on,
// ask_user routed to the UI. Operators using non-interactive channels
// (Slack/HTTP) flip PermissionMode=bypass and AskUserMode=off here,
// or — once per-channel override lands — set the override on the
// individual channel instead.
func DefaultGateConfig() GateConfig {
	return GateConfig{
		GateEnabled:    true,
		PermissionMode: "on",
		AskUserMode:    "on",
		AllowedCmds:    "",
	}
}
