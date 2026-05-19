package config

// GateConfig holds command-gate knobs. Gate is the umbrella policy
// for every user-facing prompt the agent might issue:
//
//   - PermissionMode: gates per-tool permission prompts (PreToolUse
//     hook). "on" installs the hook; "bypass" skips it (use for
//     non-interactive channels — Slack/HTTP — where no human can
//     answer a prompt).
//   - AskUserMode: gates the MCP `ask_user` tool. "on" routes the
//     question to the web UI; "off" returns an error to the LLM so
//     it picks a default instead of hanging the run.
//
// The master GateEnabled flag is the global kill-switch — off means
// every sub-policy short-circuits to its unguarded default (permission
// bypass, ask_user off). When on, each sub-mode is honored.
//
// Per-channel override (planned phase 2) will let non-interactive
// channels (Slack/HTTP/cron) flip their own mode without changing the
// global config; until then the global mode applies everywhere.
type GateConfig struct {
	GateEnabled    bool   `wick:"checkbox;desc=Master gate switch. Off = every sub-policy short-circuits to its unguarded default (permission bypass, ask_user off). On = sub-modes below take effect."`
	PermissionMode string `wick:"dropdown=on|bypass;desc=Permission policy. on = install PreToolUse hook so the user approves each tool call. bypass = run unguarded (use for Slack/HTTP where no human can approve)."`
	AskUserMode    string `wick:"dropdown=on|off;desc=Ask-user policy. on = route ask_user MCP calls to the web UI. off = return an error so the agent picks a sensible default instead of blocking."`
	AllowedCmds    string `wick:"kvlist=pattern|scope;desc=Command whitelist for the permission gate. pattern supports a trailing * wildcard (e.g. 'git *'). scope (optional) restricts path args to a directory prefix."`
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
