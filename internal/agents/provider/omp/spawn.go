package omp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rs/zerolog/log"

	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/procgroup"
	"github.com/yogasw/wick/internal/agents/skillsync"
	"github.com/yogasw/wick/internal/pkg/envscrub"
	"github.com/yogasw/wick/pkg/safeexec"
)

// Spawner spawns `omp` in print/json mode, one process per turn.
type Spawner struct {
	Binary    string // empty → "omp"
	ExtraArgs []string
	// MCPToken is the per-session credential for wick's MCP server. Empty =
	// no wick tools (the mcp.json entry is left alone).
	MCPToken string
	// RevocableToken: MCPToken is a per-session credential omp revokes
	// when the process using it is gone (SetMCPTokenRevoker). False for
	// the shared per-boot token, which must never be revoked.
	RevocableToken bool
	// MCPOwner is who MCPToken speaks for (the caller). Part of the RPC
	// process key: another caller gets its own process and identity.
	MCPOwner string
}

// homeDir is swapped in tests so mcp.json lands in a temp dir.
var homeDir = os.UserHomeDir

// buildArgs is the argv minus the prompt (which goes on stdin).
// --profile leads: omp resolves the profile before any other flag.
func buildArgs(ins provider.Instance, opt provider.SpawnOptions, soulPath, overlayPath string, extra []string) []string {
	args := provider.OMPProfileArgs(ins)
	args = append(args, "-p", "--mode", "json", "--no-title", "--auto-approve")
	if overlayPath != "" {
		// Settings overlay that keeps host MCP out (see isolationOverlay).
		args = append(args, "--config", overlayPath)
	}
	if opt.Workspace != "" {
		args = append(args, "--cwd", opt.Workspace)
	}
	if soulPath != "" {
		// A value without a newline that names a readable file is read as
		// that file (system-prompt.ts resolvePromptInput).
		args = append(args, "--append-system-prompt", soulPath)
	}
	args = append(args, extra...)
	args = append(args, opt.ExtraArgs...)
	if m := provider.ModelArgs(opt, args); len(m) > 0 {
		args = append(args, m...)
	} else if d := provider.ProvenDefaultModel(ins); d != "" && !slices.Contains(args, "--model") && (opt.Instance == nil || !opt.Instance.UseAIRouter) {
		// No pin: omp would run its own default (the first listed model,
		// which may be one this account is refused). Only once wick has
		// seen a model work or fail does it pick instead.
		args = append(args, "--model", d)
	}
	if opt.ResumeID != "" {
		args = append(args, "--resume", opt.ResumeID)
	}
	return args
}

// writeSoul writes the wick system prompt (preset + shipped-skill catalog)
// under the per-session dir — never the shared workspace, see codex/spawn.go.
func writeSoul(opt provider.SpawnOptions) string {
	soul := skillsync.AppendBuiltinCatalog(opt.Preset, opt.SkipSkills...)
	dir := opt.SessionDir
	if dir == "" {
		dir = opt.Workspace
	}
	if soul == "" || dir == "" {
		return ""
	}
	d := filepath.Join(dir, ".omp")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return ""
	}
	p := filepath.Join(d, "soul.md")
	if err := os.WriteFile(p, []byte(soul), 0o644); err != nil {
		log.Warn().Err(err).Str("path", p).Msg("agents.spawn: omp write soul.md failed")
		return ""
	}
	return p
}

// sessionOMPDir is the wick session's own .omp dir ("" without one).
func sessionOMPDir(opt provider.SpawnOptions) string {
	dir := opt.SessionDir
	if dir == "" {
		dir = opt.Workspace
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, ".omp")
}

// withResume is opt with the --resume value swapped for resume (an id or
// a transcript path); opt.ResumeID itself stays the id everywhere else.
func withResume(opt provider.SpawnOptions, resume string) provider.SpawnOptions {
	opt.ResumeID = resume
	return opt
}

// writeOverlay writes the isolation overlay under the per-session dir and
// returns its path ("" when there is nowhere to put it).
func writeOverlay(opt provider.SpawnOptions) string {
	dir := opt.SessionDir
	if dir == "" {
		dir = opt.Workspace
	}
	if dir == "" {
		return ""
	}
	d := filepath.Join(dir, ".omp")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return ""
	}
	p := filepath.Join(d, "wick-settings.yml")
	if err := os.WriteFile(p, isolationOverlay(), 0o644); err != nil {
		log.Warn().Err(err).Str("path", p).Msg("agents.spawn: omp write settings overlay failed")
		return ""
	}
	return p
}

// extraMCPEntries converts the instance's extra MCP servers to omp's shape.
func extraMCPEntries(ins provider.Instance) (map[string]map[string]any, error) {
	parsed, err := provider.ParseExtraMCP(ins.ExtraMCPServers)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]any, len(parsed))
	for name, s := range parsed {
		out[name] = s.OMPEntry()
	}
	return out, nil
}

// envValue returns the last KEY=value for key in env, else "".
func envValue(env []string, key string) string {
	v := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			v = kv[len(key)+1:]
		}
	}
	return v
}

// Spawn starts one omp run with opt.InitialMessage as the prompt.
func (s Spawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	bin := s.Binary
	if bin == "" {
		bin = "omp"
	}
	resolved, err := safeexec.ResolveBin(bin)
	if err != nil {
		return nil, fmt.Errorf("omp binary not found: %w", err)
	}
	bin = resolved

	ins := provider.Instance{Type: provider.TypeOMP, Name: string(provider.TypeOMP)}
	if opt.Instance != nil {
		ins = *opt.Instance
	}
	profile := provider.OMPProfile(ins)
	if !provider.ValidOMPProfile(profile) {
		return nil, fmt.Errorf("omp instance %s: profile %q is not a valid omp profile name", ins.Name, profile)
	}

	// The profile's mcp.json holds wick's per-session entry (placeholders,
	// values in env) plus the instance's extra servers — and nothing else
	// wick put there before.
	extras, err := extraMCPEntries(ins)
	if err != nil {
		return nil, fmt.Errorf("omp instance %s: %w", ins.Name, err)
	}
	var mcpVars []string
	endpoint := mcpEndpointFromEnv()
	withWick := endpoint != "" && s.MCPToken != ""
	home, _ := homeDir()
	cfgDir := envValue(opt.ExtraEnv, "PI_CONFIG_DIR")
	if cfgDir == "" {
		cfgDir = os.Getenv("PI_CONFIG_DIR")
	}
	if home != "" {
		if err := ensureMCPConfig(profileAgentDir(home, cfgDir, profile), withWick, extras); err != nil {
			log.Warn().Err(err).Str("profile", profile).Msg("agents.spawn: omp mcp.json not updated — wick tools unavailable")
		} else if withWick {
			mcpVars = mcpEnv(endpoint, s.MCPToken)
		}
	}

	// A sharer (AuthFrom) reads its owner's login through omp's auth
	// broker; see broker.go. Released once the turn is over.
	brokerVars, releaseBroker, err := brokerEnv(ctx, ins, opt, bin)
	if err != nil {
		return nil, fmt.Errorf("omp instance %s: %w", ins.Name, err)
	}

	// The transcript may live in another instance's profile (the session
	// was moved here): resume it by path so the conversation carries on.
	resume := opt.ResumeID
	if home != "" {
		resume = resumeArg(ompRoot(home, cfgDir), profile, opt.ResumeID, sessionOMPDir(opt))
	}
	// Another instance's RPC process holding this session's transcript
	// open would serve a stale copy of it on a switch back: it goes.
	if opt.SessionID != "" {
		rpcServers.RetireGroups(func(instance, group string) bool {
			return instance != ins.Name && group == instance+"/"+opt.SessionID
		})
	}

	// No pin: the chosen live Default model, or — resuming a transcript
	// another profile wrote last — this instance's own model.
	extra := s.ExtraArgs
	if m := defaultModelArgs(ctx, ins, opt, profile, resume, sessionOMPDir(opt), s.ExtraArgs); m != nil {
		log.Info().Str("profile", profile).Str("resume", opt.ResumeID).Str("model", m[1]).
			Msg("agents.spawn: omp model from the instance (no session pin)")
		extra = append(append([]string{}, s.ExtraArgs...), m...)
	}
	noteWriter(sessionOMPDir(opt), profile, opt.ResumeID)
	s.ExtraArgs = extra

	soul, overlay := writeSoul(opt), writeOverlay(opt)
	if useServer(ins, s.ExtraArgs, opt.ExtraArgs) {
		return s.spawnRPC(ctx, opt, ins, bin, resume, soul, overlay, mcpVars, brokerVars, releaseBroker)
	}
	// Server mode off: a plain -p run, and an RPC process the instance no
	// longer uses stops once it has no turn.
	rpcServers.Retire(ins.Name)
	args := buildArgs(ins, withResume(opt, resume), soul, overlay, s.ExtraArgs)

	execBin, execArgs, scopeUnit := opt.MemGuard.Wrap(bin, args, "omp", opt.SpawnSeq)
	cmd := safeexec.CommandContext(ctx, execBin, execArgs...)
	cmd.Dir = opt.Workspace
	cmd.Env = append(envscrub.ScrubOSEnv(), opt.ExtraEnv...)
	cmd.Env = append(cmd.Env, mcpVars...)
	// omp turns ~/.claude on as an MCP/config source whenever
	// CLAUDE_CONFIG_DIR is set (capability/index.ts isUserSourceEnabled),
	// and PI_CONFIG_FILES adds config overlays — neither may leak in from
	// wick's own environment.
	cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR=", "PI_CONFIG_FILES=")
	cmd.Env = append(cmd.Env, brokerVars...)
	// Like --cwd: an inherited PWD must not name another directory.
	if opt.Workspace != "" {
		cmd.Env = append(cmd.Env, "PWD="+opt.Workspace)
	}
	hideConsole(cmd)
	procgroup.Apply(cmd)

	addedEnv := provider.MaskSpawnEnv(append(append([]string{}, opt.ExtraEnv...), mcpVars...))

	stdin, err := cmd.StdinPipe()
	if err != nil {
		releaseBroker()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		releaseBroker()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	log.Info().Str("bin", bin).Strs("argv", args).Str("cwd", opt.Workspace).
		Str("resume", opt.ResumeID).Str("profile", profile).Msg("agents.spawn: starting (omp)")
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		releaseBroker()
		return nil, fmt.Errorf("start omp: %w", err)
	}
	opt.MemGuard.BiasChild(cmd.Process.Pid)
	// omp reads a non-TTY stdin to EOF as the prompt (main.ts readPipedInput).
	go writePrompt(stdin, opt.InitialMessage)

	log.Info().Int("pid", cmd.Process.Pid).Str("scope", scopeUnit).Msg("agents.spawn: started (omp)")
	// No RPC to ask get_state: the window comes from models.db (window.go).
	stdoutR := withContextLine(stdout, spawnWindow(ctx, ins, args))
	proc := &process{cmd: cmd, stdout: stdoutR, env: addedEnv, scopeUnit: scopeUnit, realBin: bin, realArgv: args}
	revoke := s.RevocableToken
	proc.onExit = func() {
		releaseBroker()
		if revoke {
			revokeLater(s.MCPToken)
		}
	}
	return proc, nil
}

func writePrompt(w io.WriteCloser, prompt string) {
	_, _ = io.WriteString(w, prompt)
	_ = w.Close()
}
