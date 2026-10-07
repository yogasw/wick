package opencode

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/procgroup"
	"github.com/yogasw/wick/internal/agents/skillsync"
	"github.com/yogasw/wick/internal/pkg/envscrub"
	"github.com/yogasw/wick/pkg/safeexec"
)

// Spawner spawns `opencode run --format json`, one process per turn.
type Spawner struct {
	Binary    string // empty → "opencode"
	ExtraArgs []string
	// MCPToken is the per-session credential for wick's MCP server.
	MCPToken string
}

// buildArgs is the argv minus the prompt (piped on stdin — run.ts reads a
// non-TTY stdin as the message).
func buildArgs(opt provider.SpawnOptions, extra []string, model string, modelInArgs bool) []string {
	args := []string{"run", "--format", "json", "--thinking", "--auto"}
	args = append(args, extra...)
	args = append(args, opt.ExtraArgs...)
	if model != "" && !modelInArgs {
		args = append(args, "--model", model)
	}
	if opt.ResumeID != "" {
		args = append(args, "--session", opt.ResumeID)
	}
	return args
}

// writeSoul writes the wick system prompt under the per-session dir.
func writeSoul(opt provider.SpawnOptions) string {
	soul := skillsync.AppendBuiltinCatalog(opt.Preset, opt.SkipSkills...)
	dir := opt.SessionDir
	if dir == "" {
		dir = opt.Workspace
	}
	if soul == "" || dir == "" {
		return ""
	}
	d := filepath.Join(dir, ".opencode-wick")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return ""
	}
	p := filepath.Join(d, "soul.md")
	if err := os.WriteFile(p, []byte(soul), 0o644); err != nil {
		log.Warn().Err(err).Str("path", p).Msg("agents.spawn: opencode write soul.md failed")
		return ""
	}
	return p
}

// spawnEnv is the env wick adds for one spawn: the instance's data dir,
// the inline config, and the MCP values its placeholders expand to.
func spawnEnv(ins provider.Instance, soulPath, endpoint, token string, disable []string) ([]string, error) {
	env, err := provider.OpencodeEnv(ins)
	if err != nil {
		return nil, err
	}
	parsed, err := provider.ParseExtraMCP(ins.ExtraMCPServers)
	if err != nil {
		return nil, err
	}
	extras := make(map[string]map[string]any, len(parsed))
	for name, s := range parsed {
		extras[name] = s.OpencodeEntry()
	}
	mcp := mcpEnv(endpoint, token)
	// Blank the host's extra config sources (config/config.ts): wick's
	// inline layer is the only addition allowed, and OPENCODE_AUTO_SHARE
	// must not publish anything.
	env = append(env, "OPENCODE_CONFIG=", "OPENCODE_CONFIG_DIR=", "OPENCODE_AUTO_SHARE=false")
	// Skip the external skill scans (~/.claude/skills, ~/.agents): the
	// host's skills are not the instance's, and the scan cost every spawn
	// ~70 files plus "duplicate skill name" noise.
	if !ins.LoadExternalSkills {
		env = append(env, "OPENCODE_DISABLE_EXTERNAL_SKILLS=1", "OPENCODE_DISABLE_CLAUDE_CODE_SKILLS=1")
	}
	env = append(env, configEnvVar+"="+configContent(mcp != nil, soulPath, extras, disable))
	return append(env, mcp...), nil
}

// Spawn starts one opencode run with opt.InitialMessage as the prompt.
func (s Spawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	bin := s.Binary
	if bin == "" {
		bin = "opencode"
	}
	resolved, err := safeexec.ResolveBin(bin)
	if err != nil {
		return nil, fmt.Errorf("opencode binary not found: %w", err)
	}
	bin = resolved

	ins := provider.Instance{Type: provider.TypeOpencode, Name: string(provider.TypeOpencode)}
	if opt.Instance != nil {
		ins = *opt.Instance
	}
	// A second account of a provider lives in its own data folder: a pin
	// naming it (or Auto rotation away from a quota-hit one) runs there —
	// its auth.json, its sessions, its own server (the key hashes the env).
	prov, acct := provider.OpencodeSpawnAccount(ins, opt.ModelID)
	// A sharer's folder for that account holds a link to its owner's
	// auth.json (see provider/authshare.go); made here for extra accounts.
	if err := provider.EnsureOpencodeAuthLink(ins, acct); err != nil {
		return nil, err
	}
	if acct != "" {
		if acc, aerr := provider.WithOpencodeAccount(ins, acct); aerr == nil {
			log.Info().Str("provider", prov).Str("account", acct).Msg("agents.spawn: opencode account folder")
			ins = acc
		}
	}
	// An opencode session lives in the data folder that ran it: after a
	// switch to another instance, or a rotation to another account
	// folder, it is copied here first so the conversation carries on.
	if dir, derr := provider.OpencodeDataDir(ins); derr == nil {
		carryHistory(ctx, bin, opt.Workspace, stateDir(opt), opt.ResumeID, dir)
	}
	model, inArgs, err := resolveModel(ctx, ins, opt, append(append([]string{}, s.ExtraArgs...), opt.ExtraArgs...))
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	if useServer(ins, s.ExtraArgs, opt.ExtraArgs) {
		return s.spawnServe(ctx, opt, ins, bin, model, foreignMCPNames(opt.Workspace, home))
	}
	// Server mode off (or a run-only flag): this turn is a plain run, and
	// a server the instance no longer uses stops once it has no turns.
	servers.retire(ins.Name)
	added, err := spawnEnv(ins, writeSoul(opt), mcpEndpointFromEnv(), s.MCPToken, foreignMCPNames(opt.Workspace, home))
	if err != nil {
		return nil, fmt.Errorf("opencode instance %s: %w", ins.Name, err)
	}

	args := buildArgs(opt, s.ExtraArgs, model, inArgs)
	execBin, execArgs, scopeUnit := opt.MemGuard.Wrap(bin, args, "opencode", opt.SpawnSeq)
	cmd := safeexec.CommandContext(ctx, execBin, execArgs...)
	cmd.Dir = opt.Workspace
	cmd.Env = append(envscrub.ScrubOSEnv(), opt.ExtraEnv...)
	// After the instance env: the account dir must not be overridable by a
	// stray XDG_DATA_HOME in Env, or login and spawn would part ways.
	cmd.Env = append(cmd.Env, added...)
	cmd.Env = pinPWD(cmd.Env, opt.Workspace)
	hideConsole(cmd)
	procgroup.Apply(cmd)

	addedEnv := provider.MaskSpawnEnv(append(append([]string{}, opt.ExtraEnv...), added...))

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	log.Info().Str("bin", bin).Strs("argv", args).Str("cwd", opt.Workspace).
		Str("resume", opt.ResumeID).Msg("agents.spawn: starting (opencode)")
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start opencode: %w", err)
	}
	opt.MemGuard.BiasChild(cmd.Process.Pid)
	go func() {
		_, _ = io.WriteString(stdin, opt.InitialMessage)
		_ = stdin.Close()
	}()

	log.Info().Int("pid", cmd.Process.Pid).Str("scope", scopeUnit).Msg("agents.spawn: started (opencode)")
	// No serve to ask GET /provider: the window comes from opencode's
	// models.dev cache (window.go).
	stdoutR := withContextLine(stdout, cachedWindow(modelsCachePath(cmd.Env), model))
	return &process{cmd: cmd, stdout: stdoutR, env: addedEnv, scopeUnit: scopeUnit, realBin: bin, realArgv: args}, nil
}

// useServer reports whether a turn goes to the shared `opencode serve`
// (server.go) instead of its own `opencode run`. The run path stays for an
// instance that opts out, and for extra args other than --model: those are
// `run` flags the server API has no equivalent for.
func useServer(ins provider.Instance, extra ...[]string) bool {
	if ins.RunPerTurn {
		return false
	}
	for _, args := range extra {
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case a == "--model" || a == "-m":
				i++
			case strings.HasPrefix(a, "--model="):
			default:
				return false
			}
		}
	}
	return true
}

// serverIdle is the instance's idle window, else the pool's idle timeout
// (the same window claude/codex processes get), else DefaultServerIdle;
// never zero (see manager.reap).
func serverIdle(ins provider.Instance, opt provider.SpawnOptions) time.Duration {
	if ins.ServerIdleMinutes > 0 {
		return time.Duration(ins.ServerIdleMinutes) * time.Minute
	}
	if opt.IdleTimeout > 0 {
		return opt.IdleTimeout
	}
	return DefaultServerIdle
}

// spawnServe runs one turn on the instance's shared server, starting it
// when needed. The server env is the run env minus the per-session parts:
// the wick MCP token (registered per turn, see remote.go) and the soul
// (sent as the prompt's system text).
func (s Spawner) spawnServe(ctx context.Context, opt provider.SpawnOptions, ins provider.Instance, bin, model string, disable []string) (provider.Process, error) {
	added, err := spawnEnv(ins, "", "", "", disable)
	if err != nil {
		return nil, fmt.Errorf("opencode instance %s: %w", ins.Name, err)
	}
	dataDir, _ := provider.OpencodeDataDir(ins)
	spec := serverSpec{
		instance: ins.Name,
		bin:      bin,
		env:      append(append(envscrub.ScrubOSEnv(), opt.ExtraEnv...), added...),
		dir:      dataDir,
		idle:     serverIdle(ins, opt),
		turns:    DefaultServerTurns,
		wrap: func(b string, a []string) (string, []string, string) {
			return opt.MemGuard.Wrap(b, a, "opencode-serve", opt.SpawnSeq)
		},
	}
	l, err := servers.acquire(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("opencode instance %s: server: %w", ins.Name, err)
	}

	t := turnSpec{
		resumeID: opt.ResumeID,
		title:    "wick " + opt.SessionID,
		model:    model,
		system:   skillsync.AppendBuiltinCatalog(opt.Preset, opt.SkipSkills...),
		prompt:   opt.InitialMessage,
	}
	if endpoint := mcpEndpointFromEnv(); endpoint != "" && s.MCPToken != "" {
		t.mcpName, t.mcpURL, t.mcpToken = sessionMCPName(opt.SessionID), endpoint, s.MCPToken
	}
	argv := []string{"serve-turn", "--server", l.s.h.url, "--model", model}
	if opt.ResumeID != "" {
		argv = append(argv, "--session", opt.ResumeID)
	}
	addedEnv := provider.MaskSpawnEnv(append(append([]string{}, opt.ExtraEnv...), added...))
	p := newRemoteProcess(addedEnv, bin, argv, opt.Workspace)
	// The turn is bounded by Kill (abort), not by ctx: ctx is the spawn
	// call's, and a turn outlives it the way a run subprocess does.
	rctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	log.Info().Str("instance", ins.Name).Int("server_pid", l.s.h.pid).Str("resume", opt.ResumeID).
		Str("cwd", opt.Workspace).Msg("agents.spawn: starting (opencode serve turn)")
	go func() {
		defer cancel()
		p.run(rctx, l, t)
	}()
	return p, nil
}

// pinPWD sets PWD to dir (last entry wins). opencode takes its project
// directory from PWD before the process cwd, so a PWD inherited from
// whoever started wick (a shell, go test) would put the session's files
// and AGENTS.md lookups in that directory instead of the workspace.
func pinPWD(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	return append(env, "PWD="+dir)
}

// stateDir is the wick session's own .opencode-wick dir ("" without one).
func stateDir(opt provider.SpawnOptions) string {
	dir := opt.SessionDir
	if dir == "" {
		dir = opt.Workspace
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, ".opencode-wick")
}
