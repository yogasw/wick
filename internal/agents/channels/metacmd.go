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
// before the pool. Commands are case-insensitive and may be prefixed with /
// or !. Returns IsMeta=false for regular user messages.
//
// Supported commands (agents-design.md §10):
//
//	/agent <name>   — switch active agent
//	/reset          — clear session context (next send starts fresh)
//	/status         — reply with current session/agent state
//	/dashboard      — reply with dashboard URL for this session
//	/link           — alias for /dashboard
//	/log            — reply with last N command-gate log lines
func ParseMeta(text string) MetaResult {
	t := strings.TrimSpace(text)
	if t == "" {
		return MetaResult{}
	}
	// Strip leading / or !
	raw := t
	if len(raw) > 0 && (raw[0] == '/' || raw[0] == '!') {
		raw = raw[1:]
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

	switch cmd {
	case "agent":
		return MetaResult{IsMeta: true, Cmd: "agent", Arg: arg}
	case "reset":
		return MetaResult{IsMeta: true, Cmd: "reset"}
	case "status":
		return MetaResult{IsMeta: true, Cmd: "status"}
	case "dashboard", "link":
		return MetaResult{IsMeta: true, Cmd: "dashboard"}
	case "log":
		return MetaResult{IsMeta: true, Cmd: "log", Arg: arg}
	}
	return MetaResult{}
}
