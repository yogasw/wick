package channels

import (
	"strings"
)

// MetaResult is what ParseMeta returns.
type MetaResult struct {
	// IsMeta is true when the text is a wick meta-command and should NOT
	// be forwarded to the agent subprocess.
	IsMeta bool
	// Cmd is the canonical command name (e.g. "dashboard", "reset").
	Cmd string
	// Arg is the optional argument following the command (e.g. agent name
	// after "agent <name>").
	Arg string
}

// ParseMeta checks whether text is one of the wick meta-commands intercepted
// before the pool. Commands are case-insensitive and must be prefixed with /
// or !; a bare word with no prefix is never a command, and reset, status and
// dashboard/link must be the whole message (only agent and log take an argument). Returns IsMeta=false
// for regular user messages.
//
// Supported commands (agents-design.md §10):
//
//	/agent <name>   — switch active agent
//	/reset          — clear session context (next send starts fresh)
//	/status         — reply with current session/agent state
//	/dashboard      — reply with dashboard URL for this session
//	/link           — alias for /dashboard
//	/log            — reply with last N command-gate log lines
//	/help           — list these commands
func ParseMeta(text string) MetaResult {
	t := strings.TrimSpace(text)
	if t == "" {
		return MetaResult{}
	}
	// Strip leading / or !
	raw := t
	prefixed := false
	if len(raw) > 0 && (raw[0] == '/' || raw[0] == '!') {
		raw = raw[1:]
		prefixed = true
	}

	parts := strings.Fields(strings.ToLower(raw))
	if len(parts) == 0 {
		return MetaResult{}
	}

	cmd := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = strings.Join(parts[1:], " ")
	}
	// Only an explicit prefix makes a command. A message that merely STARTS
	// with one of these words ("link href nya dong", "status deploy tadi",
	// "log error kemarin") is for the agent: taken as a command, the person's
	// request was swallowed and never reached the agent, the conversation or
	// the logs.
	if !prefixed {
		return MetaResult{}
	}

	// The commands that take no argument must be the WHOLE message:
	// "!link href nya dong" is a sentence for the agent, not a command.
	// Only agent and log take a trailing argument.
	switch cmd {
	case "agent":
		return MetaResult{IsMeta: true, Cmd: "agent", Arg: arg}
	case "log":
		return MetaResult{IsMeta: true, Cmd: "log", Arg: arg}
	case "reset", "status", "dashboard", "link", "help":
		if arg != "" {
			return MetaResult{}
		}
		if cmd == "link" {
			cmd = "dashboard"
		}
		return MetaResult{IsMeta: true, Cmd: cmd}
	}
	return MetaResult{}
}
