package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/provider/modelfilter"
)

// climodels_live.go backs the omp/opencode "live from CLI" model list
// (Instance.LiveModels): the picker offers what ListCLIModels returns,
// narrowed by LiveModelFilter, with LiveModelDefault (or the first match) as
// the default.
//
// No bun process is ever started just to read metadata. The list is
// refreshed on EVENTS, never on a timer:
//   - a read (render, picker, spawn default) only reads the cache; a cold
//     cache, or one whose account files changed since (login / logout /
//     a turn that refreshed omp's own cache), is filled from the CLI's own
//     files or an already-running server — a ModelHarvester, no spawn;
//   - after a turn ends on the instance, and after a model_not_found
//     refusal, HarvestCLIModels re-reads that source in the background;
//   - an API-key save / logout / binary change invalidates the entry;
//   - only the user's explicit Refresh (refresh=true) runs the CLI: one
//     process, guarded, serialized, killed after (HelperCommand).

// cliModelsMinRefresh is how soon after a fetch a forced refresh (the
// Refresh button) may exec the CLI again; sooner, the list just fetched is
// served.
const cliModelsMinRefresh = 30 * time.Second

// cliModelsFetchTimeout bounds one CLI list call.
const cliModelsFetchTimeout = 60 * time.Second

type cliModelsEntry struct {
	models []ModelSeed
	err    error
	at     time.Time
	// source: "cli" (Refresh), "files" / "server" (a harvest).
	source string
	// authStamp is the account files' state the entry was read under; a
	// different stamp makes a read re-harvest (login / logout / omp's own
	// cache refresh), still without a spawn.
	authStamp string
}

var (
	cliModelsMu       sync.Mutex
	cliModelsCache    = map[string]cliModelsEntry{}
	cliModelsInflight = map[string]chan struct{}{}
	cliModelsNow      = time.Now
)

// ModelHarvester reads an instance's model list without starting its CLI:
// the CLI's own files read-only (omp models.db) or a server that is already
// running (opencode serve). Registered per type from its package; returns
// the source label ("files" / "server") through the error-free path.
type ModelHarvester func(ctx context.Context, ins Instance) ([]ModelSeed, error)

var (
	modelHarvestersMu sync.RWMutex
	modelHarvesters   = map[Type]ModelHarvester{}
	// harvestSource names each type's harvest in "last updated (…)".
	harvestSource = map[Type]string{TypeOMP: "files", TypeOpencode: "server"}
)

// RegisterModelHarvester installs t's harvester (init); nil removes it.
func RegisterModelHarvester(t Type, h ModelHarvester) {
	modelHarvestersMu.Lock()
	defer modelHarvestersMu.Unlock()
	if h == nil {
		delete(modelHarvesters, t)
		return
	}
	modelHarvesters[t] = h
}

// AuthStamp is the state of ins's account store on disk (mtime + size of
// omp's agent.db / opencode's auth.json): it changes on login, logout and
// on omp's own writes. Set from the omp package (profile layout).
var AuthStamp = func(ins Instance) string {
	if ins.Type == TypeOpencode {
		if f, err := OpencodeAuthFile(ins); err == nil {
			return fileStamp(f)
		}
	}
	return ""
}

func fileStamp(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d|%d", fi.Size(), fi.ModTime().UnixNano())
}

// harvest runs ins's harvester (no spawn); nil when it has none or found
// nothing.
func harvest(ctx context.Context, ins Instance) ([]ModelSeed, string) {
	modelHarvestersMu.RLock()
	h := modelHarvesters[ins.Type]
	modelHarvestersMu.RUnlock()
	if h == nil {
		return nil, ""
	}
	models, err := h(ctx, ins)
	if err != nil || len(models) == 0 {
		return nil, ""
	}
	return models, harvestSource[ins.Type]
}

// cliModelsKey identifies one account store: the list depends on who is
// logged in, so the profile / data dir is part of the key, not just the name.
func cliModelsKey(ins Instance) string {
	k := string(ins.Type) + "/" + ins.Name + "|" + ins.Binary
	if ins.OMPConfig != nil {
		k += "|" + ins.OMPConfig.Profile
	}
	if ins.OpencodeConfig != nil {
		k += "|" + ins.OpencodeConfig.DataDir
	}
	return k
}

// LiveModelsEnabled reports whether ins offers its CLI's live model list.
func LiveModelsEnabled(ins Instance) bool {
	return ins.LiveModels && (ins.Type == TypeOMP || ins.Type == TypeOpencode)
}

// CachedCLIModels returns ins's model list.
//
// refresh=false (every read): the cached list; when there is none, or the
// account files changed since it was read, the harvester fills it — never
// the CLI. An empty result means "nothing known yet: Refresh".
//
// refresh=true (the user's Refresh only): one CLI run, guarded and
// serialized; concurrent callers share it, and one within
// cliModelsMinRefresh of the last fetch is served that fetch.
// fetchedAt is when the returned list was read.
func CachedCLIModels(ctx context.Context, ins Instance, refresh bool) (models []ModelSeed, fetchedAt time.Time, err error) {
	key := cliModelsKey(ins)
	if !refresh {
		stamp := AuthStamp(ins)
		cliModelsMu.Lock()
		e, ok := cliModelsLookup(ins, key)
		cliModelsMu.Unlock()
		if ok && e.authStamp == stamp && (len(e.models) > 0 || e.err != nil) {
			return e.models, e.at, e.err
		}
		// A known list whose account files moved since (omp writes its
		// own on every turn, a restart reads the file back): serve it at
		// once and re-read the harvester in the background — the picker
		// never waits on a list it already has.
		if ok && len(e.models) > 0 {
			HarvestCLIModels(ins)
			return e.models, e.at, e.err
		}
		if hm, src := harvest(ctx, ins); len(hm) > 0 {
			cliModelsMu.Lock()
			e = cliModelsEntry{models: hm, at: cliModelsNow(), source: src, authStamp: stamp}
			cliModelsStore(ins, key, e)
			cliModelsMu.Unlock()
			return e.models, e.at, nil
		}
		return e.models, e.at, e.err
	}
	for {
		cliModelsMu.Lock()
		e, ok := cliModelsLookup(ins, key)
		if ok && e.err == nil && e.source == "cli" && cliModelsNow().Sub(e.at) < cliModelsMinRefresh {
			cliModelsMu.Unlock()
			return e.models, e.at, e.err // just fetched: a refresh now would only burn a process
		}
		if wait, busy := cliModelsInflight[key]; busy {
			cliModelsMu.Unlock()
			select {
			case <-wait:
				// Share the fetch that just ended, failure included: a
				// waiter re-running a CLI that just failed only queues
				// more failing runs.
				cliModelsMu.Lock()
				e, _ := cliModelsLookup(ins, key)
				cliModelsMu.Unlock()
				return e.models, e.at, e.err
			case <-ctx.Done():
				return nil, time.Time{}, ctx.Err()
			}
		}
		done := make(chan struct{})
		cliModelsInflight[key] = done
		cliModelsMu.Unlock()

		models, err := ListCLIModels(WithHelperSpawn(ctx), ins)
		cliModelsMu.Lock()
		e = cliModelsEntry{models: models, err: err, at: cliModelsNow(), source: "cli", authStamp: AuthStamp(ins)}
		// A failed refresh keeps the last good list on screen/in the picker.
		if prev, had := cliModelsLookup(ins, key); err != nil && had && prev.err == nil {
			e.models = prev.models
		}
		cliModelsStore(ins, key, e)
		delete(cliModelsInflight, key)
		close(done)
		cliModelsMu.Unlock()
		return e.models, e.at, e.err
	}
}

// CLIModelsInfo is when and from where ins's cached list was read (zero
// time / "" when nothing is cached) — the "last updated" line.
func CLIModelsInfo(ins Instance) (time.Time, string) {
	cliModelsMu.Lock()
	defer cliModelsMu.Unlock()
	e, _ := cliModelsLookup(ins, cliModelsKey(ins))
	return e.at, e.source
}

// PeekCLIModels returns whatever list is cached for ins (possibly nil)
// without blocking and without starting anything that spawns. A cold cache
// is filled from the harvester in the background (files / running server).
// For render paths and the reader path.
func PeekCLIModels(ins Instance) []ModelSeed {
	key := cliModelsKey(ins)
	cliModelsMu.Lock()
	e, ok := cliModelsLookup(ins, key)
	cliModelsMu.Unlock()
	if !ok {
		// Debounced per instance: a harvester that finds nothing caches
		// nothing, so without the gate every render would start another.
		HarvestCLIModels(ins)
	}
	return e.models
}

// harvestGate debounces HarvestCLIModels per instance.
var (
	harvestMu   sync.Mutex
	harvestLast = map[string]time.Time{}
)

// harvestEvery is the shortest gap between two after-turn harvests of one
// instance.
const harvestEvery = 15 * time.Second

// HarvestCLIModels re-reads ins's list from its harvester in the
// background — after a turn ended on ins, or a model was refused — so the
// next read sees what the CLI itself now knows. No spawn; a no-op when
// live models are off or nothing was found (the cached list stays).
func HarvestCLIModels(ins Instance) {
	if !LiveModelsEnabled(ins) {
		return
	}
	key := cliModelsKey(ins)
	harvestMu.Lock()
	if cliModelsNow().Sub(harvestLast[key]) < harvestEvery {
		harvestMu.Unlock()
		return
	}
	harvestLast[key] = cliModelsNow()
	harvestMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), cliModelsFetchTimeout)
		defer cancel()
		hm, src := harvest(ctx, ins)
		if len(hm) == 0 {
			return
		}
		cliModelsMu.Lock()
		cliModelsStore(ins, key, cliModelsEntry{models: hm, at: cliModelsNow(), source: src, authStamp: AuthStamp(ins)})
		cliModelsMu.Unlock()
	}()
}

// helperSpawnKey marks a context whose caller is the user's explicit
// Refresh / login action: only then may a throwaway CLI server start.
type helperSpawnKey struct{}

// WithHelperSpawn allows a throwaway helper process under ctx.
func WithHelperSpawn(ctx context.Context) context.Context {
	return context.WithValue(ctx, helperSpawnKey{}, true)
}

// HelperSpawnAllowed reports whether ctx came through WithHelperSpawn.
func HelperSpawnAllowed(ctx context.Context) bool {
	v, _ := ctx.Value(helperSpawnKey{}).(bool)
	return v
}

// opencodeHostedProviders are opencode's own hosted services (Zen and its
// Go plan). Mirrors opencode.hostedProviders — the spawn-side rule (6a).
var opencodeHostedProviders = []string{"opencode", "opencode-go"}

// IsOpencodeHostedModel reports whether an opencode model id runs on
// opencode's own servers (opencode/…, opencode-go/…).
func IsOpencodeHostedModel(id string) bool {
	p, _, _ := strings.Cut(id, "/")
	return slices.Contains(opencodeHostedProviders, p)
}

// OpencodeHostedAllowed reports whether hosted opencode models may run on
// ins: the operator opted in (opencode_allow_hosted), or the instance is
// itself logged into opencode's service (an auth.json entry for it, or
// OPENCODE_API_KEY in its env) — then the hosted service IS the account.
func OpencodeHostedAllowed(ins Instance) bool {
	if ins.OpencodeConfig != nil && ins.OpencodeConfig.AllowHosted {
		return true
	}
	for _, e := range ins.Env {
		if k, v, ok := strings.Cut(e, "="); ok && k == "OPENCODE_API_KEY" && strings.TrimSpace(v) != "" {
			return true
		}
	}
	p, err := OpencodeAuthFile(ins)
	if err != nil {
		return false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return false
	}
	for _, h := range opencodeHostedProviders {
		if _, ok := m[h]; ok {
			return true
		}
	}
	return false
}

// FilterLiveModels narrows a CLI model list to what ins offers: hosted
// opencode models only when OpencodeHostedAllowed, then LiveModelFilter.
func FilterLiveModels(ins Instance, models []ModelSeed) []ModelSeed {
	hosted := ins.Type != TypeOpencode || OpencodeHostedAllowed(ins)
	out := make([]ModelSeed, 0, len(models))
	for _, m := range models {
		if !hosted && IsOpencodeHostedModel(m.ID) {
			continue
		}
		if modelfilter.Match(m.ID+" "+m.Desc, ins.LiveModelFilter) {
			out = append(out, m)
		}
	}
	return out
}

// LiveDefaultFirst returns filtered with the effective default moved to the
// front: LiveModelDefault when still listed, else the first match. The
// picker treats entry 0 as the default, so the order IS the default rule.
func LiveDefaultFirst(filtered []ModelSeed, pin string) []ModelSeed {
	pin = strings.TrimSpace(pin)
	for i, m := range filtered {
		if pin != "" && m.ID == pin {
			out := make([]ModelSeed, 0, len(filtered))
			out = append(out, m)
			out = append(out, filtered[:i]...)
			return append(out, filtered[i+1:]...)
		}
	}
	return filtered
}

// LiveDefaultModel is the model a live-mode instance runs when the session
// pinned none: the pin if still offered, else the first filtered match. It
// never runs the CLI (CachedCLIModels reads only). "" when live mode is off
// or nothing matches; with no list known yet, the pin as typed.
func LiveDefaultModel(ctx context.Context, ins Instance) string {
	if !LiveModelsEnabled(ins) {
		return ""
	}
	models, _, err := CachedCLIModels(ctx, ins, false)
	if len(models) == 0 {
		// Nothing known yet (no harvest, no Refresh) or a failed fetch:
		// the chosen Default as typed — never a CLI run to check it.
		_ = err
		return strings.TrimSpace(ins.LiveModelDefault)
	}
	// Refused models are skipped and the last model that worked wins over
	// list order (modelwatch.go); with no evidence this is list[0] as before.
	return pickLiveDefault(ins, LiveDefaultFirst(FilterLiveModels(ins, models), ins.LiveModelDefault))
}

// SetCLIModelsForTest puts ids in ins's CLI model cache (fresh, so nothing
// execs); the returned func removes the entry. Tests only (other packages
// cannot reach the unexported cache).
func SetCLIModelsForTest(ins Instance, ids ...string) (restore func()) {
	seeds := make([]ModelSeed, 0, len(ids))
	for _, id := range ids {
		seeds = append(seeds, ModelSeed{ID: id})
	}
	key := cliModelsKey(ins)
	cliModelsMu.Lock()
	cliModelsCache[key] = cliModelsEntry{models: seeds, at: cliModelsNow()}
	cliModelsMu.Unlock()
	return func() {
		cliModelsMu.Lock()
		delete(cliModelsCache, key)
		cliModelsMu.Unlock()
	}
}

// InvalidateCLIModels drops ins's cached CLI model list, so the next read
// fetches it — for a change that makes the list wrong (a new API key or
// login), where the minimum refresh interval must not serve the old one.
func InvalidateCLIModels(ins Instance) {
	cliModelsMu.Lock()
	cliModelsDrop(ins, cliModelsKey(ins))
	cliModelsMu.Unlock()
}
