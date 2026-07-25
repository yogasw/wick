package wick

import (
	"context"
	"io"
	"strings"

	"github.com/rs/zerolog/log"
	provider "github.com/yogasw/wick/internal/agents/provider"
)

// Spawner implements provider.Spawner for the built-in in-process
// runtime. "Spawn" starts a goroutine-backed engine (no OS process) and
// returns a Process whose Stdout carries claude-shaped stream-json —
// see process.go / engine.go.
type Spawner struct{}

// Spawn wires an in-process engine for one session and returns
// immediately; the engine runs in a goroutine, reading user messages
// from Stdin and emitting stream-json to Stdout. Model-resolution
// failures surface as an error turn in the conversation rather than a
// hard Spawn error, so the user sees WHY in the UI.
func (s Spawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	pr, pw := io.Pipe()
	runCtx, cancel := context.WithCancel(ctx)
	p := &wickProcess{
		r:          pr,
		w:          pw,
		env:        provider.MaskSpawnEnv(opt.ExtraEnv),
		msgs:       make(chan string, 16),
		ctx:        runCtx,
		cancel:     cancel,
		engineDone: make(chan struct{}),
		label:      "wick (built-in)",
		// Created here (not in the engine goroutine) so Kill can read p.bg /
		// p.jobs without racing the goroutine that would otherwise set them.
		bg:   newBgRegistry(opt.Workspace),
		jobs: newJobManager(),
	}
	go p.runEngine(opt)
	return p, nil
}

// runEngine is the process's engine goroutine: resolve the model, build
// tools + history, then loop over incoming user messages running one
// turn each. Closing engineDone lets Wait close the pipe (reader EOF).
func (p *wickProcess) runEngine(opt provider.SpawnOptions) {
	defer close(p.engineDone)

	emit := func(b []byte) {
		if _, err := p.w.Write(append(b, '\n')); err != nil {
			log.Debug().Err(err).Msg("wick.engine: pipe write failed (reader gone)")
		}
	}

	// Wire the job manager's engine seams: on completion a job injects a
	// [job-done] turn (injectTurn), and while any job runs off-turn the
	// keepAlive heartbeat stops the pool idle-kill from reaping the session
	// before the job can wake it.
	p.jobs.setSeams(p.injectTurn, func() { emit(heartbeatLine()) })

	m, ok := pickModel(opt.Instance, opt.ModelID)
	if !ok {
		emit(initLine(newSessionID()))
		emit(errorLine("No model configured for the wick provider. Open Providers → Wick and add a model."))
		emit(doneLine(""))
		return
	}
	m.APIKey = secretDecryptor(m.APIKey)

	llm, err := resolveModel(p.ctx, m)
	if err != nil {
		emit(initLine(newSessionID()))
		emit(errorLine(vendorErrorMessage(err)))
		emit(doneLine(""))
		return
	}

	var wc *provider.WickConfig
	if opt.Instance != nil {
		wc = opt.Instance.WickConfig
	}
	genCfg := buildGenConfig(m, wc)
	resolved := resolveWickConfig(wc)

	tc := toolContext{
		Workspace:  opt.Workspace,
		SessionDir: opt.SessionDir,
		SessionID:  sessionIDFromDir(opt.SessionDir),
		Config:     resolved,
		// Per-spawn background-shell registry (created in Spawn). Shared by
		// shell(run_in_background)/shell_output/shell_kill; reaped on Kill.
		Bg: p.bg,
		// Per-spawn async-job manager (created in Spawn). Shared by
		// job_start/status/log/cancel; cancelled on Kill.
		Jobs: p.jobs,
	}
	tools := buildTools(tc)
	history := loadHistory(opt.SessionDir, maxContextTokens(wc))

	// System prompt = the factory-assembled preset (immutable rules +
	// preset body + connector catalog + session identity) plus the
	// project's ambient context files (AGENT.md / memory.md / skills.md),
	// matching what the CLI providers read from the workspace.
	sysPrompt := opt.Preset
	if ctxFiles := loadContextFiles(opt.Workspace); ctxFiles != "" {
		sysPrompt = strings.TrimSpace(sysPrompt + "\n\n" + ctxFiles)
	}

	eng := newEngine(llm, m.Model, sysPrompt, genCfg, tools, history, resolved.MaxTurns, emit)
	eng.setModelID(m.ID)
	eng.setWickSessionID(tc.SessionID)
	eng.setToolSpillDir(opt.SessionDir)
	eng.gate = gateCheckerFn
	eng.setContextBudget(maxContextTokens(wc))
	// Record every model call to the wick session log (why the model
	// answered as it did) at <SessionDir>/wick-interactions.jsonl.
	eng.setInteractionSink(newInteractionSink(opt.SessionDir))
	eng.start()

	// If the agent supplied an initial message positionally (respawn
	// providers), run it first. wick uses SendAppend, so this is normally
	// empty and messages arrive via Stdin.
	if opt.InitialMessage != "" {
		eng.runTurn(p.ctx, opt.InitialMessage)
	}

	for {
		select {
		case <-p.ctx.Done():
			return
		case msg, ok := <-p.msgs:
			if !ok {
				return
			}
			eng.runTurn(p.ctx, msg)
		}
	}
}

// pickModel selects the model to run: the pinned modelID if it exists and
// is enabled, else the Default model, else the first enabled one. Disabled
// models are never auto-picked — parked, not gone. A pin that no longer
// resolves (model deleted/disabled since the pin was set) silently falls
// back rather than erroring, since the session otherwise still works fine
// on the instance's own default. Returns false when none are configured or
// every one is disabled.
func pickModel(inst *provider.Instance, modelID string) (provider.WickModel, bool) {
	if inst == nil || len(inst.WickModels) == 0 {
		return provider.WickModel{}, false
	}
	if modelID != "" {
		for _, m := range inst.WickModels {
			if m.ID == modelID && !m.Disabled {
				return m, true
			}
		}
	}
	var firstEnabled *provider.WickModel
	for i, m := range inst.WickModels {
		if m.Disabled {
			continue
		}
		if m.Default {
			return m, true
		}
		if firstEnabled == nil {
			firstEnabled = &inst.WickModels[i]
		}
	}
	if firstEnabled != nil {
		return *firstEnabled, true
	}
	return provider.WickModel{}, false
}

// resolveWickConfig applies defaults to the instance config so the
// engine + tools always see a populated view (shell + fs on by default;
// MaxTurns 0 → engine's own safety cap). todo has no toggle here
// anymore — see WickConfigResolved's doc comment.
func resolveWickConfig(wc *provider.WickConfig) *WickConfigResolved {
	r := &WickConfigResolved{ShellTool: true, FsTools: true, MaxTurns: 0}
	if wc != nil {
		r.ShellTool = !wc.ShellToolDisabled
		r.MaxTurns = wc.MaxTurns
	}
	return r
}

func maxContextTokens(wc *provider.WickConfig) int {
	if wc == nil {
		return 0
	}
	return wc.MaxContextTokens
}
