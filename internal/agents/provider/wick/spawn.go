package wick

import (
	"context"
	"io"
	"strings"
	"time"

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

	// Session overrides (/thinking etc.) deliberately OUTLIVE this goroutine:
	// they are persisted to the session's overrides.json sidecar and reloaded on
	// the next spawn. Clearing them here used to mean an idle-reaped session lost
	// the user's toggle with no visible cause ("refresh and thinking is off
	// again"), since the engine exits long before the user comes back. A reset is
	// now an explicit action (ClearSessionOverride) or session deletion.

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
		// Nothing resolved statically. The common cause is a live set whose
		// sticky default was never picked: it carries no Model of its own, so
		// there is nothing to run until the vendor list is fetched. Ask the
		// vendor now rather than failing — the set exists precisely to be
		// resolved at use time, and an operator who added one has already said
		// which models they want.
		m, ok = resolveLiveSetFallback(p.ctx, opt.Instance)
		if !ok {
			emit(initLine(newSessionID()))
			emit(errorLine("No runnable model for the wick provider. Open Providers → Wick and add a model, or pick a default model inside the live set."))
			emit(doneLine(""))
			return
		}
		log.Info().
			Str("model", m.Model).
			Str("entry", m.ID).
			Msg("wick.spawn: no model pinned; auto-picked the top of the live set")
	} else {
		m.APIKey = secretDecryptor(m.APIKey)
	}

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
	reasoning := buildReasoning(m, wc)
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
	// Skill catalog: wick runs in-process (no CLI --add-dir), so it must tell
	// the model which skills exist. Compact — names + descriptions + SKILL.md
	// paths; the agent reads a skill's body on demand via read_file.
	if cat := skillCatalog(); cat != "" {
		sysPrompt = strings.TrimSpace(sysPrompt + "\n\n" + cat)
	}

	eng := newEngine(llm, m.Model, sysPrompt, genCfg, tools, history, resolved.MaxTurns, emit)
	eng.setModelID(m.ID)
	eng.setReasoning(reasoning)
	eng.setWickSessionID(tc.SessionID)
	eng.setToolSpillDir(opt.SessionDir)
	eng.gate = gateCheckerFn
	eng.setContextBudget(maxContextTokens(wc))
	// Streaming is on by default (StreamDisabled stored inverted); the engine
	// falls back to one-shot JSON when off or for adapters without an SSE path.
	eng.setStream(wc == nil || !wc.StreamDisabled)
	// Record every model call to the wick session log (why the model
	// answered as it did) at <SessionDir>/wick-interactions.jsonl.
	eng.setInteractionSink(newInteractionSink(opt.SessionDir))
	// Let the engine drain mid-turn messages from the same channel the loop below
	// reads. Safe: while runTurn is executing, the loop is parked in its receive
	// (it only reads msgs to START a turn), so during a turn the engine is the
	// sole reader — no double-consume. Messages that arrive between turns are
	// still picked up by the loop as the next turn.
	eng.setSteer(p.msgs)
	if wc != nil {
		eng.setRetryPolicy(retryPolicyFromConfig(wc.MaxModelRetries, wc.ModelCallTimeoutSec))
		eng.setLoopGuards(wc.MaxConsecErrors, time.Duration(wc.MaxTurnMinutes)*time.Minute)
	}
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
// on the instance's own default. Returns false when none are configured,
// every one is disabled, or none resolves to a concrete vendor model id.
func pickModel(inst *provider.Instance, modelID string) (provider.WickModel, bool) {
	if inst == nil || len(inst.WickModels) == 0 {
		return provider.WickModel{}, false
	}
	if modelID != "" {
		// A live-set pick is "<entryID>@<vendorModelID>": resolve the entry for
		// its key/kind/base, then override the concrete model id with the
		// vendor model the user chose from the expanded list.
		entryID, vendorID := modelID, ""
		if at := strings.IndexByte(modelID, '@'); at >= 0 {
			entryID, vendorID = modelID[:at], modelID[at+1:]
		}
		for _, m := range inst.WickModels {
			if m.ID != entryID || m.Disabled {
				continue
			}
			if r, ok := runnableModel(m, vendorID); ok {
				return r, true
			}
			// The pin names an entry that cannot be called as-is. Fall
			// through to the default rather than spawning with no model id.
			break
		}
	}
	var firstEnabled provider.WickModel
	haveFirst := false
	for _, m := range inst.WickModels {
		if m.Disabled {
			continue
		}
		r, ok := runnableModel(m, "")
		if !ok {
			continue
		}
		if r.Default {
			return r, true
		}
		if !haveFirst {
			firstEnabled, haveFirst = r, true
		}
	}
	return firstEnabled, haveFirst
}

// resolveLiveSetFallback asks the vendor which models a live set actually
// contains, and returns the one that set would default to.
//
// This is the last resort when pickModel found nothing runnable. A live
// set stores no concrete model id — its whole purpose is to be resolved
// against the vendor's current list — so an operator who added one but
// never opened the picker to choose a sticky default left every spawn with
// nothing to run. Failing there is technically correct and practically
// useless: the set names the models they want, and the picker's own rule
// for "no sticky pin" is already "the top of the list".
//
// That rule is mirrored here rather than shared, because the picker lives
// in the tools layer (HTTP handlers, config decryption) and the spawn path
// must not depend on it. markLiveSetDefault is the tools-side twin.
//
// Only ENABLED live sets are considered — a disabled entry is parked, not
// a fallback — and the first one that resolves wins, matching pickModel's
// own top-down scan. Returns ok=false when the instance has no live set,
// discovery fails, or every set comes back empty; the caller then reports
// the original error.
func resolveLiveSetFallback(ctx context.Context, inst *provider.Instance) (provider.WickModel, bool) {
	if inst == nil {
		return provider.WickModel{}, false
	}
	for _, m := range inst.WickModels {
		if m.Disabled || !m.LiveSet {
			continue
		}
		// Decrypted before the call, not after: discovery authenticates with
		// this key, and the returned model carries it into the engine.
		entry := m
		entry.APIKey = secretDecryptor(entry.APIKey)
		if strings.TrimSpace(entry.APIKey) == "" && strings.ToLower(strings.TrimSpace(entry.Kind)) != "other" {
			continue
		}
		live, err := DiscoverModels(ctx, entry.Kind, entry.APIKey, strings.TrimSpace(entry.BaseURL))
		if err != nil {
			log.Debug().Err(err).Str("entry", entry.ID).
				Msg("wick.spawn: live-set discovery failed while looking for a fallback model")
			continue
		}
		pick := liveSetPick(FilterModels(live, entry.DiscoveryFilter), entry.DefaultVendorModel)
		if pick == "" {
			continue
		}
		entry.Model = pick
		return entry, true
	}
	return provider.WickModel{}, false
}

// liveSetPick chooses which model in a resolved live set to run: the sticky
// pin when it is still in the list, else the top of it.
//
// Pure, and separate from the network call, so the rule is testable — the
// tools layer's markLiveSetDefault makes the same decision for the picker
// UI, and the two must agree or wick would run a different model than the
// UI shows as default. Empty when the list is empty.
func liveSetPick(models []DiscoveredModel, stickyPin string) string {
	if len(models) == 0 {
		return ""
	}
	if want := strings.TrimSpace(stickyPin); want != "" {
		for _, d := range models {
			if d.ID == want {
				return d.ID
			}
		}
	}
	return models[0].ID
}

// runnableModel resolves one catalogue entry to something the vendor SDK
// can actually be called with, and reports whether it got there.
//
// A LIVE set carries no Model of its own — its list is fetched from the
// vendor — so an entry taken without an explicit "@vendor" override has to
// fall back to its sticky default. Every spawn that supplies no pin goes
// through here, which is every sub-agent whose role names no model: those
// used to reach the vendor with an empty model id and fail inside the SDK
// ("model is empty"), which reads as a wick bug rather than as "this live
// set has no model picked yet".
func runnableModel(m provider.WickModel, vendorID string) (provider.WickModel, bool) {
	switch {
	case vendorID != "":
		m.Model = vendorID // explicit live-set override
	case m.Model == "" && m.LiveSet:
		// A stale pin is corrected at picker time (expandLiveWickSet
		// re-marks the top of the fresh list as default); this is the
		// last-resort resolution when only the entry id arrives.
		m.Model = m.DefaultVendorModel
	}
	return m, strings.TrimSpace(m.Model) != ""
}

// resolveWickConfig applies defaults to the instance config so the
// engine + tools always see a populated view (shell + fs on by default;
// MaxTurns 0 → unlimited; the engine's consec-error + wall-clock guards
// are the safety net). todo has no toggle here anymore — see
// WickConfigResolved's doc comment.
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
