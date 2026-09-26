// Package aimemory registers the ai-memory backend with agentmemory.
// ai-memory (github.com/akitaonrails/ai-memory) is a Rust binary that stores
// what agents observed across sessions and serves it over MCP on a loopback
// port, so a claude session and a codex session share one memory. init()
// registers it, so a blank-import of this package wires it into the registry.
package aimemory

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/agents/agentmemory"
	"github.com/yogasw/wick/internal/agents/provider"
)

func init() {
	agentmemory.Register(agentmemory.Descriptor{
		ID:          "ai-memory",
		DisplayName: "ai-memory",
		Blurb:       "Shared memory across sessions and across agent CLIs — recall over MCP, stored locally.",
		GitHubURL:   "https://github.com/akitaonrails/ai-memory",
		IconSVG:     iconSVG,
		InstallKind: agentmemory.InstallGitHubRelease,
		ReleaseRepo: "akitaonrails/ai-memory",
		BinName:     binName,
		PrefPort:    49374,
		// Verified 2026-09-25: /healthz answers 200; /, /health, /status and
		// /version all 404. Probing anything else reports a live daemon down.
		HealthPath: "/healthz",
		Launch:     launch,
		Adopt:      adoptBind,
		Hook:       hook{},
		Data:       source{run: execCLI},
	})
}

// binName is the command on PATH, and also the MCP server name agents see.
const binName = "ai-memory"

// iconSVG is the inner markup for the backend tile (rendered inside an <svg>)
// — a stack of stored layers.
const iconSVG = `<ellipse cx="8" cy="4" rx="5.5" ry="2.2"></ellipse><path d="M2.5 4v4c0 1.2 2.5 2.2 5.5 2.2s5.5-1 5.5-2.2V4" stroke-linecap="round"></path><path d="M2.5 8v4c0 1.2 2.5 2.2 5.5 2.2s5.5-1 5.5-2.2V8" stroke-linecap="round"></path>`

// launch builds the ai-memory daemon args. --data-dir is a global flag, so it
// goes BEFORE the `serve` subcommand; the bind is loopback-only.
//
// --enable-web mounts ai-memory's /api/v1 AND its own web UI, and wick turns
// it ON by default (Yoga, 2026-09-25): the panel's project list and search are
// reads against /api/v1, so without the flag those two are dead in the default
// configuration. The bind is loopback and wick is the only consumer, so the
// extra UI is not a surface worth the empty tab. Still a setting — an operator
// who wants the daemon bare can switch it off.
func launch(opt agentmemory.LaunchOptions) (args, env []string) {
	if opt.DataDir != "" {
		args = append(args, "--data-dir", opt.DataDir)
	}
	args = append(args, "serve", "--transport", "http", "--bind", "127.0.0.1:"+strconv.Itoa(opt.Port))
	if opt.EnableWeb {
		args = append(args, "--enable-web")
	}
	// --base-path is the one tuning field with a real flag, because it moves
	// every mounted route at once; the rest arrive as AI_MEMORY_* env so the
	// operator's own config.toml is never rewritten (see config.go).
	if bp := strings.TrimSpace(opt.Tuning.BasePath); bp != "" {
		args = append(args, "--base-path", bp)
	}
	// The bind above is always loopback, so --allow-insecure-no-auth is
	// never passed: wick has no way to produce the configuration that flag
	// exists to unlock, and a flag named "insecure" should not be reachable
	// from a settings form by accident.
	return args, tuningEnv(opt.Tuning, opt.AuthToken)
}

// adoptBind reads the port out of a RUNNING ai-memory's own launch line.
//
// It is the inverse of launch() above and has to stay next to it: both know
// that the store is `--data-dir` before the subcommand and the address is
// `--bind host:port` after `serve`. Read off the live process on this host —
// `serve --transport http --bind 127.0.0.1:49375 --enable-web`.
//
// The store is part of the MATCH, not just the port: two ai-memory daemons can
// be running, and picking the wrong one is the whole bug (agentmemory/adopt.go).
// wick's own daemon is the one started with wick's data dir — and when wick
// configures none, the one started with none, since that is the line launch()
// would have produced.
func adoptBind(argv []string, opt agentmemory.LaunchOptions) (int, bool) {
	var (
		serve   bool
		dataDir string
		bind    string
	)
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "serve":
			serve = true
		case "--data-dir":
			if i+1 < len(argv) {
				dataDir = argv[i+1]
				i++
			}
		case "--bind":
			if i+1 < len(argv) {
				bind = argv[i+1]
				i++
			}
		}
	}
	if !serve || bind == "" {
		return 0, false
	}
	if !sameStore(dataDir, opt.DataDir) {
		return 0, false
	}
	return bindPort(bind)
}

// sameStore compares two --data-dir values the way the filesystem would, so a
// trailing slash or a relative spelling of the same directory is not read as a
// different store. Both empty = both took the backend's default, which is a
// match; one empty and one not is not.
func sameStore(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return a == b
	}
	if a == b {
		return true
	}
	ra, erra := filepath.EvalSymlinks(a)
	rb, errb := filepath.EvalSymlinks(b)
	if erra != nil || errb != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// bindPort takes the port half of a "host:port" bind address. A bind with no
// port, or one that is not a number, is not an answer.
func bindPort(bind string) (int, bool) {
	i := strings.LastIndex(bind, ":")
	if i < 0 {
		return 0, false
	}
	p, err := strconv.Atoi(strings.TrimSpace(bind[i+1:]))
	if err != nil || p <= 0 || p > 65535 {
		return 0, false
	}
	return p, true
}

// hook implements agentmemory.SpawnHook for ai-memory. It absorbs the
// per-agent-type wiring: claude takes an inline MCP config, codex takes `-c`
// TOML overrides. Both point at the same /mcp endpoint.
type hook struct{}

func (hook) Contribute(t provider.Type, ins provider.Instance, conn agentmemory.SpawnConn) (args, env []string, err error) {
	mcpURL := conn.ServerURL + "/mcp"
	switch t {
	case provider.TypeClaude:
		cfg, err := mcpConfigJSON(mcpURL)
		if err != nil {
			return nil, nil, err
		}
		// Deliberately NO --strict-mcp-config. wick stopped passing it on
		// purpose (see internal/entity/agent_profile.go): what a spawn may
		// reach is enforced server-side by tags and per-op access, so the only
		// thing the flag still does is drop the USER'S own MCP servers.
		// Turning memory on must not silently unplug someone's other tools.
		//
		// Nothing needs to terminate this list either: wick's claude spawn
		// streams the prompt over stdin (-p --input-format stream-json), so
		// argv carries no trailing operand for the variadic --mcp-config to
		// eat. Position still matters for a hand-run command, which is why
		// the spawner inserts this mid-list; TestSpawnArgvMemoryOrder locks it.
		args = []string{"--mcp-config", cfg}
		if ins.AgentMemoryCapture {
			if s := captureSettings(conn); s != "" {
				args = append([]string{"--settings", s}, args...)
			}
		}
		return args, memoryEnv(conn), nil
	case provider.TypeCodex:
		// Values are TOML strings, quoted — the shell-quoted form in the
		// verified command (`-c 'mcp_servers.ai-memory.url="…"'`) is one argv
		// entry with the value still in double quotes. approve = the tools
		// answer without a per-call prompt, which a headless spawn needs.
		args = []string{
			"-c", "mcp_servers." + binName + ".url=" + strconv.Quote(mcpURL),
			"-c", "mcp_servers." + binName + ".default_tools_approval_mode=" + strconv.Quote("approve"),
		}
		if ins.AgentMemoryCapture {
			args = append(codexCaptureArgs(conn), args...)
		}
		return args, memoryEnv(conn), nil
	default:
		return nil, nil, nil
	}
}

// mcpConfigJSON renders the inline value for claude's --mcp-config: one
// streamable-HTTP MCP server pointing at the daemon. claude accepts a file
// path OR a JSON string here, and inline keeps the spawn free of temp files.
func mcpConfigJSON(url string) (string, error) {
	type server struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	b, err := json.Marshal(struct {
		MCPServers map[string]server `json:"mcpServers"`
	}{MCPServers: map[string]server{binName: {Type: "http", URL: url}}})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// captureEvents are claude's lifecycle events and the --event slug ai-memory
// expects for each. Taken verbatim from what `ai-memory --data-dir <dir>
// install-hooks --agent claude-code` writes (ai-memory 2.4.0, captured
// 2026-09-25) — not derived from the event names, because the mapping is a
// contract with the backend, not a naming convention wick may extend.
//
// Kept as an ordered slice rather than a map so the generated block is stable
// across spawns: a settings string that reshuffles every run would churn the
// spawn log and make two commands look different when they are not.
var captureEvents = []struct{ event, slug string }{
	{"SessionStart", "session-start"},
	{promptEvent, "user-prompt-submit"},
	{"PreToolUse", "pre-tool-use"},
	{"PostToolUse", "post-tool-use"},
	{"PreCompact", "pre-compact"},
	{"Stop", "stop"},
	{"SessionEnd", "session-end"},
	{"SubagentStart", "subagent-start"},
	{"SubagentStop", "subagent-stop"},
}

// promptEvent is the one lifecycle event that carries the user's own words,
// named so the no-capture-prompts setting can drop exactly it.
const promptEvent = "UserPromptSubmit"

// claudeHooks is the shape of claude's `hooks` settings key: event name ->
// matcher groups -> the commands that run. The empty matcher means "every
// tool", which is what install-hooks emits for all nine events.
type claudeHooks struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

type hookMatcher struct {
	Matcher string        `json:"matcher"`
	Hooks   []hookCommand `json:"hooks"`
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// captureSettings builds the claude --settings payload that installs
// ai-memory's lifecycle hooks, so the session is RECORDED and not just read.
// MCP and hooks are both wired when capture is on: MCP is how the agent
// remembers, hooks are how anything gets written down (Yoga, 2026-09-25).
//
// Every hook is a native `ai-memory hook` invocation — no shell script, no
// file on disk — so the whole block is generated per spawn and handed to
// claude inline.
//
// SAFE BY MEASUREMENT, not by hope: `--settings` MERGES with the user's own
// settings, it does not replace them. Verified 2026-09-25 against claude
// 2.1.274 by running a prompt with a project .claude/settings.json that
// declared its own SessionStart and UserPromptSubmit hooks, plus an inline
// --settings declaring SessionStart and Stop: all four fired, including BOTH
// SessionStart commands. So turning capture on cannot silently unplug a
// user's own hooks or permissions — same reasoning as the deliberate absence
// of --strict-mcp-config above.
//
// Returns "" when the binary did not resolve: a hook line that cannot name an
// executable would make every tool call print a spawn failure, which is worse
// than not recording. The MCP side stays wired either way.
func captureSettings(conn agentmemory.SpawnConn) string {
	if conn.BinPath == "" {
		return ""
	}
	hooks := make(map[string][]hookMatcher, len(captureEvents))
	for _, e := range captureEvents {
		// "Do not capture prompts" is implemented by not installing the
		// hook at all, rather than by asking the daemon to discard what it
		// receives: prompt text then never enters the local spool or the
		// wire in the first place, which is the only version of the setting
		// that is worth anything on a host holding client sessions.
		if conn.Tuning.NoCapturePrompts && e.event == promptEvent {
			continue
		}
		hooks[e.event] = []hookMatcher{{
			Matcher: "",
			Hooks:   []hookCommand{{Type: "command", Command: hookLine(conn, e.slug)}},
		}}
	}
	b, err := json.Marshal(claudeHooks{Hooks: hooks})
	if err != nil {
		return ""
	}
	return string(b)
}

// ── codex capture ────────────────────────────────────────────────────

// codexCaptureEvents maps codex's lifecycle events onto the --event slug
// ai-memory expects. Both halves are measured, not assumed:
//
//   - the codex side is codex 0.149.1's own hook schema, read off the binary
//     (SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, PreCompact,
//     PostCompact, SessionEnd, PermissionRequest, Subagent{Start,Stop});
//   - the ai-memory side is the codex hook bundle shipped with 2.4.0
//     (session-start, user-prompt-submit, pre-tool-use, post-tool-use,
//     pre-compact, stop, session-end).
//
// The bundle's `stop` is deliberately absent here: codex has no bare Stop
// event, so a hook hung on that name would never fire and would sit in the
// spawn log looking like capture that works. PostCompact, PermissionRequest
// and the subagent pair are codex events ai-memory has no slug for, and are
// left out for the same reason — a hook that posts an event the server does
// not know is noise, not coverage.
//
// Ordered, like claude's, so two spawns of the same session produce the same
// argv rather than a reshuffled one.
var codexCaptureEvents = []struct{ event, slug string }{
	{"SessionStart", "session-start"},
	{codexPromptEvent, "user-prompt-submit"},
	{"PreToolUse", "pre-tool-use"},
	{"PostToolUse", "post-tool-use"},
	{"PreCompact", "pre-compact"},
	{"SessionEnd", "session-end"},
}

// codexPromptEvent is codex's name for the event carrying the user's words,
// so "do not capture prompts" can drop exactly it.
const codexPromptEvent = "UserPromptSubmit"

// codexTrustFlag is what makes a launch-passed hook actually RUN.
//
// Measured against codex 0.149.1 on 2026-09-25: the same `-c hooks.…`
// override loads cleanly without it and the hook never executes — silently,
// with nothing in the output to say a hook was skipped. With it, the hook
// runs. codex's own help calls the flag "intended only for automation that
// already vets hook sources", which is this case exactly: the command it
// bypasses trust for is one wick generated this second, naming a binary wick
// installed.
//
// What it costs, stated because it is not free: the flag is per-invocation
// and not per-hook, so any OTHER hook codex would have prompted about in this
// session — one from the user's own config or a project file — also runs
// unprompted. It is only ever passed when capture is explicitly on for the
// instance, never as a side effect of switching memory on.
//
// The alternative is persisting trust in codex's own state, which is a config
// file wick is not allowed to write (Yoga, 2026-09-25) — so this is the only
// shape available at launch.
const codexTrustFlag = "--dangerously-bypass-hook-trust"

// codexCaptureArgs renders the hook wiring as codex `-c` overrides:
//
//	--dangerously-bypass-hook-trust
//	-c hooks.SessionStart=[{hooks=[{type="command",command="…"}]}]
//	-c hooks.UserPromptSubmit=…
//	…
//
// The shape is codex's own, learned from its parser: an event maps to a list
// of matcher groups, each carrying a list of hook entries tagged by `type`
// (command | mcp_tool | prompt | agent). A malformed entry is refused at
// config load, so a wrong shape here fails the spawn loudly rather than
// quietly capturing nothing.
//
// Returns nil when the binary did not resolve, for the same reason claude's
// settings block does: a hook line that cannot name an executable turns every
// tool call into a spawn failure. Recall stays wired either way.
func codexCaptureArgs(conn agentmemory.SpawnConn) []string {
	if conn.BinPath == "" {
		return nil
	}
	out := []string{codexTrustFlag}
	for _, e := range codexCaptureEvents {
		// Not installing the prompt hook is how "do not capture prompts"
		// is implemented — the words never reach the spool or the wire,
		// rather than being discarded at the far end.
		if conn.Tuning.NoCapturePrompts && e.event == codexPromptEvent {
			continue
		}
		out = append(out, "-c", "hooks."+e.event+"="+codexHookValue(hookLineFor(conn, e.slug, "codex")))
	}
	return out
}

// codexHookValue renders one event's TOML value. strconv.Quote is the right
// escaper here: TOML basic strings are JSON-ish, and the command line can
// contain quotes of its own once a path is shell-quoted.
func codexHookValue(command string) string {
	return `[{hooks=[{type="command",command=` + strconv.Quote(command) + `}]}]`
}

// hookLine renders one hook line:
//
//	<bin> --data-dir <dir> hook --event <slug> --agent claude-code --server-url <url>
//
// --data-dir is a GLOBAL flag, so it goes before the `hook` subcommand, the
// same way the daemon's launch line places it. It is dropped when the daemon
// runs on the backend's own default store, so the hook inherits that default
// instead of being pinned to a path wick never chose.
//
// The agent name is "claude-code" — ai-memory's own spelling for the CLI, not
// wick's provider type.
//
// claude runs a hook command through a shell, so each path is quoted only when
// it contains something a shell would act on. A store under a path with a
// space is otherwise a hook that silently fails on every call.
func hookLine(conn agentmemory.SpawnConn, slug string) string {
	return hookLineFor(conn, slug, "claude-code")
}

// hookLineFor is hookLine with the agent name chosen by the caller.
//
// The name is ai-memory's own spelling for the CLI, not wick's provider type
// — "claude-code" and "codex", both taken from `install-hooks --agent`'s own
// value list. It is what every captured session is attributed to, so a wrong
// one does not fail, it mislabels: the Health tab's capture-coverage check
// compares harnesses by exactly this string.
func hookLineFor(conn agentmemory.SpawnConn, slug, agentName string) string {
	parts := []string{shellArg(conn.BinPath)}
	if d := strings.TrimSpace(conn.DataDir); d != "" {
		parts = append(parts, "--data-dir", shellArg(d))
	}
	parts = append(parts, "hook", "--event", slug, "--agent", agentName)
	if u := strings.TrimSpace(conn.ServerURL); u != "" {
		parts = append(parts, "--server-url", shellArg(u))
	}
	// capture-mode and project-strategy are NOT config-file keys: upstream
	// persists them through `install-hooks --apply`, which writes the
	// AGENT's own settings files. wick deliberately never does that
	// (PLAN §3.3), and `hook` accepts both flags directly — so they are
	// baked onto the line wick generates, which scopes them to sessions wick
	// starts and leaves every other harness on the host alone.
	if m := strings.TrimSpace(conn.Tuning.CaptureMode); m != "" {
		parts = append(parts, "--capture-mode", shellArg(m))
	}
	if ps := strings.TrimSpace(conn.Tuning.ProjectStrategy); ps != "" {
		parts = append(parts, "--project-strategy", shellArg(ps))
	}
	// The hook half of the assistant-capture double opt-in. The server half
	// is AI_MEMORY_CAPTURE_ASSISTANT on the daemon; both are driven by the
	// one toggle, but the daemon still refuses to store the text without its
	// own half, so an external daemon does not inherit wick's decision.
	if conn.Tuning.CaptureAssistant && slug == "stop" {
		parts = append(parts, "--capture-assistant")
	}
	return strings.Join(parts, " ")
}

// shellArg returns s ready to paste into a shell command line: unchanged when
// every character is inert, single-quoted otherwise. Leaving ordinary paths
// untouched keeps the generated block byte-identical to what install-hooks
// writes, which is what makes it checkable against the captured original.
func shellArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\"'\\$`&|;<>()*?[]{}!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// memoryEnv is the documented ai-memory environment, passed to the child so
// any hook script it runs talks to the same server as the MCP wiring above.
// The token goes to env, never argv (argv is logged).
//
// AI_MEMORY_DATA_DIR is deliberately NOT set here: the store belongs to the
// daemon, not to the agent talking to it (PLAN §19). One daemon already serves
// every workspace/project, and the client-facing separation happens on the
// workspace/project axis via the .ai-memory.toml marker — telling the child a
// different store than the server it is pointed at could only confuse a hook
// script.
func memoryEnv(conn agentmemory.SpawnConn) []string {
	env := []string{"AI_MEMORY_SERVER_URL=" + conn.ServerURL}
	if conn.AuthKey != "" {
		env = append(env, "AI_MEMORY_AUTH_TOKEN="+conn.AuthKey)
	}
	return env
}
