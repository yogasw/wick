// Package opencode implements provider.Spawner for sst's opencode.
//
// One process per turn (SendRespawnQueue):
//
//	XDG_DATA_HOME=<instance dir> OPENCODE_CONFIG_CONTENT=<json> \
//	opencode run --format json --thinking --auto [-s <sid>] [-m <model>] <prompt on stdin>
//
// Flags are checked against packages/opencode/src/cli/cmd/run.ts (commit
// 7945de2): positional message / piped stdin, -s/--session, -m/--model,
// --format json, --thinking (without it reasoning parts are not emitted),
// --auto. OPENCODE_CONFIG_CONTENT is merged as the "local" layer
// (src/config/config.ts) and carries: permission allow-all, wick's MCP
// server (type remote, {env:...} substitution so the token stays in env)
// and the wick system prompt as an extra `instructions` file, which leaves
// the project's AGENTS.md untouched.
//
// Account isolation: opencode has no data-dir env of its own; it resolves
// $XDG_DATA_HOME/opencode (packages/core/src/global.ts) and keeps auth.json
// there, so each instance runs with its own XDG_DATA_HOME
// (provider.OpencodeEnv).
//
// Gate/hook capability is OFF (opencode plugins are JS, not a command hook).
package opencode
