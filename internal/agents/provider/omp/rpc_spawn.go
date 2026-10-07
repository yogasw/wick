package omp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/cliserver"
	"github.com/yogasw/wick/internal/pkg/envscrub"
)

// useServer reports whether a turn goes to the session's RPC process
// instead of its own `omp -p`. The -p path stays for an instance that opts
// out, and for extra args other than --model (print-only flags the RPC
// process could not honour per turn).
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
// never zero (cliserver reaper).
func serverIdle(ins provider.Instance, opt provider.SpawnOptions) time.Duration {
	if ins.ServerIdleMinutes > 0 {
		return time.Duration(ins.ServerIdleMinutes) * time.Minute
	}
	if opt.IdleTimeout > 0 {
		return opt.IdleTimeout
	}
	return DefaultServerIdle
}

// buildRPCArgs is buildArgs with the print flags (-p --mode json) swapped
// for RPC mode, and --resume only when the process is started for a
// session omp already has. Isolation (--profile first, --config overlay,
// --cwd, soul) is the same argv as -p.
func buildRPCArgs(ins provider.Instance, opt provider.SpawnOptions, soul, overlay string, extra []string) []string {
	args := buildArgs(ins, opt, soul, overlay, extra)
	out := make([]string, 0, len(args)+1)
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-p":
		case args[i] == "--mode" && i+1 < len(args) && args[i+1] == "json":
			out = append(out, "--mode", "rpc", "--no-ui")
			i++
		default:
			out = append(out, args[i])
		}
	}
	return out
}

// rpcKey is the process key: instance, binary, env minus the per-turn
// token, argv minus --resume, and the wick session + caller. Anything that
// changes it (account, config, model, caller) gets a fresh process and the
// old one stops after its turn.
func rpcKey(instance, sessionID, owner, bin, dir string, env, args []string) string {
	h := sha256.New()
	e := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, mcpTokenEnvVar+"=") {
			e = append(e, kv)
		}
	}
	sort.Strings(e)
	_, _ = io.WriteString(h, bin+"\x00"+dir+"\x00"+owner+"\x00")
	for _, kv := range e {
		_, _ = io.WriteString(h, kv+"\x00")
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "--resume" {
			i++
			continue
		}
		_, _ = io.WriteString(h, args[i]+"\x01")
	}
	return instance + "/" + sessionID + "/" + hex.EncodeToString(h.Sum(nil))[:16]
}

var startRPCFn startFn = startRPC

// spawnRPC runs one turn on the wick session's omp RPC process, starting
// it (with --resume when the session has one) when there is none.
// resume is the --resume value (the id, or the transcript's path when
// another profile holds it); opt.ResumeID stays the id the live process
// is compared against.
func (s Spawner) spawnRPC(ctx context.Context, opt provider.SpawnOptions, ins provider.Instance, bin, resume, soul, overlay string, mcpVars, brokerVars []string, releaseBroker func()) (provider.Process, error) {
	args := buildRPCArgs(ins, withResume(opt, resume), soul, overlay, s.ExtraArgs)
	env := append(envscrub.ScrubOSEnv(), opt.ExtraEnv...)
	env = append(env, mcpVars...)
	// Same as -p: no ~/.claude MCP/config source, no config overlays from
	// wick's own environment.
	env = append(env, "CLAUDE_CONFIG_DIR=", "PI_CONFIG_FILES=")
	// A sharer's broker URL + token: part of the key, so a broker that
	// came back on another port gets a fresh process.
	env = append(env, brokerVars...)
	if opt.Workspace != "" {
		env = append(env, "PWD="+opt.Workspace) // like --cwd; see Spawn
	}
	key := rpcKey(ins.Name, opt.SessionID, s.MCPOwner, bin, opt.Workspace, env, args)

	// A live process serving another omp session than this turn resumes
	// (history reset, session picked elsewhere) is replaced by one started
	// with --resume; a turn with no resume id on a used process starts a
	// new omp session in it.
	fresh := false
	if c, ok := rpcServers.Live(key); ok {
		c.mu.Lock()
		sid, turns := c.sessionID, c.turns
		c.mu.Unlock()
		switch {
		case opt.ResumeID != "" && sid != "" && sid != opt.ResumeID:
			rpcServers.RetireKey(key)
		case opt.ResumeID == "" && turns > 0:
			fresh = true
		}
	}
	token := ""
	if s.RevocableToken {
		token = s.MCPToken
	}
	spec := rpcSpec{bin: bin, args: args, env: env, dir: opt.Workspace, token: token,
		wrap: func(b string, a []string) (string, []string, string) {
			return opt.MemGuard.Wrap(b, a, "omp-rpc", opt.SpawnSeq)
		}}
	l, err := rpcServers.Acquire(ctx, cliserver.Spec{Instance: ins.Name, Group: ins.Name + "/" + opt.SessionID, Key: key, Idle: serverIdle(ins, opt), Turns: 1, MaxAge: rpcMaxAge},
		func(ctx context.Context) (*rpcConn, error) { return startRPCFn(ctx, spec) })
	addedEnv := provider.MaskSpawnEnv(append(append([]string{}, opt.ExtraEnv...), mcpVars...))
	if err != nil {
		if errors.Is(err, cliserver.ErrShuttingDown) || ctx.Err() != nil {
			releaseBroker()
			return nil, err
		}
		// A process that would not start (no model, not logged in, bad
		// config) fails the TURN with omp's own words, like a -p run that
		// exits on the same error — not the spawn, which the pool would
		// read as a crash and retry.
		revokeLater(token)
		releaseBroker()
		p := newRPCProcess(addedEnv, bin, args)
		go func() { p.emit(headerLine("", opt.Workspace)); p.emit(errorLines(err.Error())); p.finish(nil) }()
		return p, nil
	}
	if !l.Fresh && token != "" && token != l.H.token {
		// The process speaks with the token it was started with; this
		// turn's own is never used.
		revokeLater(token)
	}
	if l.Fresh {
		fresh = false
	}
	p := newRPCProcess(addedEnv, bin, args)
	rctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	log.Info().Str("instance", ins.Name).Int("rpc_pid", l.H.Pid()).Bool("started", l.Fresh).
		Str("resume", opt.ResumeID).Str("cwd", opt.Workspace).Msg("agents.spawn: starting (omp rpc turn)")
	go func() {
		defer releaseBroker()
		defer cancel()
		p.run(rctx, l, rpcTurnSpec{prompt: opt.InitialMessage, cwd: opt.Workspace, fresh: fresh, account: pinnedAccount(opt)})
	}()
	return p, nil
}

// pinnedAccount is the account of a picker pin ("<provider>/<n>@<model>"),
// "" for Auto / a flat model. RPC mode only: `omp -p` has no session to pin,
// so a -p turn always runs on Auto.
func pinnedAccount(opt provider.SpawnOptions) string {
	if p, ok := provider.ResolvePin(opt.Instance, opt.ModelID); ok {
		return p.Account
	}
	return ""
}
