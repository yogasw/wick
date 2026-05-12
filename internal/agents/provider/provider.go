// Package runtime owns the per-AI-CLI runtime layer for agents:
//
//   - the supported-type list (claude / codex / gemini)
//   - per-instance config (named profile, binary override, extra args,
//     extra env) read from userconfig
//   - PATH detect + `--version` probes
//   - spawn-log files used by the Backends UI page
//
// "runtime" = how a single AI CLI invocation is configured + observed.
// A user can have multiple instances of the same type ("claude/work"
// + "claude/personal") so this package returns lists, not singletons.
//
// Why not in `agent/`: `agent/` drives one subprocess (stdin/stdout
// loop, idle timer). This package answers "which binary, with what
// flags + env, and is it healthy?" — orthogonal concerns kept apart
// so `agent/` stays CLI-agnostic.
package provider

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/userconfig"
)

// Type is the AI CLI kind. Adding a new type = add a constant here +
// teach Spawn how to wire its argv + auto-seed on bootstrap.
type Type string

const (
	TypeClaude Type = "claude"
	TypeCodex  Type = "codex"
	TypeGemini Type = "gemini"
)

// SupportedTypes returns all CLI types the agents module knows how to
// spawn. Order is the UI display order.
func SupportedTypes() []Type {
	return []Type{TypeClaude, TypeCodex, TypeGemini}
}

// Instance is the in-memory view of one configured runtime instance —
// merged from userconfig + supported-type defaults. The Backends UI
// page renders one card per Instance.
type Instance struct {
	Type      Type
	Name      string
	Binary    string   // override path; empty = use Type as PATH name
	ExtraArgs []string
	Env       []string
	Disabled  bool

	// Hooks holds the user's enable/disable intent per hook event
	// (PreToolUse, SessionStart, …). Spawners read this on every
	// Spawn to decide whether to install / remove the per-workspace
	// hook config.
	Hooks map[string]HookInstanceConfig

	// Storage configures credential-file syncing for this instance.
	// nil = sync disabled.
	Storage *StorageConfig
}

// StorageConfig mirrors userconfig.StorageConfig in-memory.
type StorageConfig struct {
	Mode            string // "folder" | "single"
	SyncPath        string
	IntervalSeconds int
}

// HookInstanceConfig mirrors userconfig.HookInstanceConfig in-memory.
// Per-event user intent; not capability state (that lives on Status).
type HookInstanceConfig struct {
	Enabled bool
}

// HookEnabled reports whether the user has opted this instance into
// the named hook event. Missing key = false (default off).
func (i Instance) HookEnabled(event string) bool {
	if i.Hooks == nil {
		return false
	}
	return i.Hooks[event].Enabled
}

// Bin returns the binary the spawner should execute: override path
// when set, else the canonical type name (resolved later via PATH).
func (i Instance) Bin() string {
	if i.Binary != "" {
		return i.Binary
	}
	return string(i.Type)
}

// Status is the live health of one instance, as shown in the UI.
//
// Hooks is per-hook-event capability info, keyed by event name
// ("PreToolUse" for the command gate; more events join the map
// later without struct churn). Empty map = never probed; UI shows
// a "Click Test" prompt.
type Status struct {
	Instance   Instance
	ResolvedAt time.Time
	Path       string // result of LookPath / override
	PathFound  bool
	Version    string // first line of `<bin> --version`
	VersionErr string // error message when version probe failed

	Hooks map[string]HookCapability

	// Probing is a render-time hint set by the HTTP layer when a
	// capability probe is currently in flight for this instance. UI
	// disables the Test / Enable buttons so the user can't double-fire.
	// Not persisted — pure in-memory state.
	Probing bool
}

// HookCapability is the in-memory mirror of userconfig.HookCapability
// — same fields, ProbedAt parsed as time.Time so handlers don't have
// to re-parse on every render.
type HookCapability struct {
	Supported bool
	Verified  bool
	ProbedAt  time.Time
	Error     string
	Scope     string
}

// HookEventPreToolUse is the well-known event key for the command gate.
// Hard-coded here so callers don't have to spell the string. New event
// keys are added as constants alongside this one as wick learns to
// intercept additional lifecycle hooks.
const HookEventPreToolUse = "PreToolUse"

// ── Config helpers ────────────────────────────────────────────────────

// AppName is the userconfig project name the agents module reads/writes
// under. Wired by the bootstrap: a server with APP_NAME=foo stores its
// agents config in ~/.foo/config.json.
var AppName = ""

// instanceCache holds the resolved per-instance config in memory so
// every spawn doesn't hit the userconfig file. Invalidated on Save /
// Delete / SetHookEnabled. Read by Find via FindCached.
var (
	instanceCacheMu sync.RWMutex
	instanceCache   map[string][]Instance // keyed by AppName; nil = uncached
)

// reloadInstanceCache pulls a fresh snapshot from userconfig under the
// caller-supplied lock contract. Called by FindCached on a miss and
// by mutating ops (Save/Delete/SetHookEnabled) after writing.
func reloadInstanceCacheLocked() {
	cfg, err := userconfig.Load(AppName)
	if err != nil {
		// Fail-soft: leave cache as-is. Callers will fall back to the
		// uncached Load path on miss.
		log.Warn().Err(err).Msg("agents.instance-cache: reload failed")
		return
	}
	if instanceCache == nil {
		instanceCache = map[string][]Instance{}
	}
	instanceCache[AppName] = mergeWithDefaults(cfg.Providers)
}

// invalidateInstanceCache forces the next FindCached / LoadCachedInstances
// to re-read userconfig. Triggered by mutating ops.
func invalidateInstanceCache() {
	instanceCacheMu.Lock()
	defer instanceCacheMu.Unlock()
	delete(instanceCache, AppName)
}

// Load returns every configured instance across all supported types,
// auto-seeding the per-type default entry when its list is empty so
// the UI always has at least one row per supported runtime.
func Load() ([]Instance, error) {
	cfg, err := userconfig.Load(AppName)
	if err != nil {
		return nil, err
	}
	return mergeWithDefaults(cfg.Providers), nil
}

// Find resolves an instance by {type, name}. Empty name resolves to
// the per-type default whose Name equals the type itself. Uses the
// in-memory cache so the hot Spawn path doesn't re-read userconfig.
func Find(t Type, name string) (Instance, error) {
	if name == "" {
		name = string(t)
	}
	instanceCacheMu.RLock()
	cached, ok := instanceCache[AppName]
	instanceCacheMu.RUnlock()
	if !ok {
		instanceCacheMu.Lock()
		if _, stillMiss := instanceCache[AppName]; stillMiss {
			reloadInstanceCacheLocked()
		}
		cached = instanceCache[AppName]
		instanceCacheMu.Unlock()
	}
	for _, ins := range cached {
		if ins.Type == t && ins.Name == name {
			return ins, nil
		}
	}
	// Cache miss after reload — fall through to the uncached path for
	// the error message. Should never happen in practice but keeps
	// behavior identical to the old API.
	all, err := Load()
	if err != nil {
		return Instance{}, err
	}
	for _, ins := range all {
		if ins.Type == t && ins.Name == name {
			return ins, nil
		}
	}
	return Instance{}, fmt.Errorf("runtime %s/%s not configured", t, name)
}

// Save persists a new or updated instance. Empty Name is rejected.
// Replaces any existing entry with the same {Type, Name}.
func Save(ins Instance) error {
	if ins.Name == "" {
		return errors.New("instance name required")
	}
	if !isSupported(ins.Type) {
		return fmt.Errorf("unsupported runtime type %q", ins.Type)
	}
	cfg, err := userconfig.Load(AppName)
	if err != nil {
		return err
	}
	list := pickList(&cfg.Providers, ins.Type)
	updated := false
	for i := range *list {
		if (*list)[i].Name == ins.Name {
			(*list)[i] = toUserInstance(ins)
			updated = true
			break
		}
	}
	if !updated {
		*list = append(*list, toUserInstance(ins))
	}
	if err := userconfig.Save(AppName, cfg); err != nil {
		return err
	}
	invalidateInstanceCache()
	InvalidateProbeCache(ins.Type, ins.Name)
	// Persist a fresh probe in the background — user just changed
	// Binary/ExtraArgs, the previous cached Status is now stale.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = RescanOne(ctx, ins.Type, ins.Name)
	}()
	return nil
}

// SetHookEnabled flips the user's enable/disable intent for one hook
// event on one instance, persisting through userconfig. Used by the
// per-card Enable/Disable button on the Providers page after a
// successful (or failed) capability probe.
func SetHookEnabled(t Type, name, event string, enabled bool) error {
	if name == "" || event == "" {
		return errors.New("instance name and event required")
	}
	cfg, err := userconfig.Load(AppName)
	if err != nil {
		return err
	}
	list := pickList(&cfg.Providers, t)
	if list == nil {
		return fmt.Errorf("unsupported runtime type %q", t)
	}
	for i := range *list {
		if (*list)[i].Name != name {
			continue
		}
		if (*list)[i].Hooks == nil {
			(*list)[i].Hooks = map[string]userconfig.HookInstanceConfig{}
		}
		(*list)[i].Hooks[event] = userconfig.HookInstanceConfig{Enabled: enabled}
		if err := userconfig.Save(AppName, cfg); err != nil {
			return err
		}
		invalidateInstanceCache()
		InvalidateProbeCache(t, name)
		return nil
	}

	// Instance not persisted yet — auto-seeded defaults (Name == type
	// string) only live in memory until the user explicitly edits or
	// (here) toggles a hook flag. Materialize the default row so the
	// intent has somewhere to land.
	if name == string(t) {
		*list = append(*list, userconfig.ProviderInstance{
			Name:  name,
			Hooks: map[string]userconfig.HookInstanceConfig{event: {Enabled: enabled}},
		})
		if err := userconfig.Save(AppName, cfg); err != nil {
			return err
		}
		invalidateInstanceCache()
		InvalidateProbeCache(t, name)
		return nil
	}
	return fmt.Errorf("instance %s/%s not found", t, name)
}

// Delete removes an instance. Removing the last instance for a type
// is allowed — Load will auto-seed the default again on the next read.
func Delete(t Type, name string) error {
	if name == "" {
		return errors.New("instance name required")
	}
	cfg, err := userconfig.Load(AppName)
	if err != nil {
		return err
	}
	list := pickList(&cfg.Providers, t)
	for i := range *list {
		if (*list)[i].Name == name {
			*list = append((*list)[:i], (*list)[i+1:]...)
			if err := userconfig.Save(AppName, cfg); err != nil {
				return err
			}
			invalidateInstanceCache()
		InvalidateProbeCache(t, name)
			return nil
		}
	}
	return nil
}

// ── Detect / version probing ──────────────────────────────────────────

// Probe resolves the binary path and runs `--version` for one
// instance. Disabled instances skip the spawn but still report the
// resolved path so the UI can show what wick would have run.
//
// ctx bounds the version probe; HTTP handlers should pass a 3s timeout.
func Probe(ctx context.Context, ins Instance) Status {
	st := Status{Instance: ins, ResolvedAt: time.Now()}
	source := ""
	if ins.Binary != "" {
		st.Path = ins.Binary
		source = "registry"
		if _, err := exec.LookPath(ins.Binary); err == nil {
			st.PathFound = true
		}
	} else {
		path, err := exec.LookPath(string(ins.Type))
		if err == nil {
			st.Path = path
			st.PathFound = true
			source = "path"
		} else if p, ok := scanKnownLocations(ins.Type); ok {
			// PATH miss is normal when CLI is installed via npm/curl
			// installer that drops binary outside PATH (e.g. claude in
			// ~/.local/bin on Windows). Fall back to per-OS install
			// locations so users don't need to edit PATH manually.
			st.Path = p
			st.PathFound = true
			source = "scan"
		} else {
			source = "miss"
		}
	}
	log.Debug().
		Str("type", string(ins.Type)).
		Str("name", ins.Name).
		Str("path", st.Path).
		Str("source", source).
		Bool("found", st.PathFound).
		Msg("agents.probe: resolve")
	if !st.PathFound {
		return st
	}
	if ins.Disabled {
		return st
	}
	cmd := exec.CommandContext(ctx, st.Path, "--version")
	hideConsole(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		st.VersionErr = err.Error()
		log.Warn().
			Str("type", string(ins.Type)).
			Str("name", ins.Name).
			Str("path", st.Path).
			Err(err).
			Msg("agents.probe: --version failed")
		return st
	}
	st.Version = firstLine(strings.TrimSpace(string(out)))
	log.Debug().
		Str("type", string(ins.Type)).
		Str("name", ins.Name).
		Str("version", st.Version).
		Msg("agents.probe: ok")
	return st
}

// ProbeAll runs Probe on every configured instance in parallel,
// honouring ctx as the total timeout (per-probe is bounded by ctx).
func ProbeAll(ctx context.Context) ([]Status, error) {
	all, err := Load()
	if err != nil {
		return nil, err
	}
	out := make([]Status, len(all))
	var wg sync.WaitGroup
	for i := range all {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = Probe(ctx, all[i])
		}()
	}
	wg.Wait()
	return out, nil
}

// ── Cached probes ─────────────────────────────────────────────────────
//
// Why cache: `<bin> --version` on Windows .cmd shims (npm-installed
// codex/gemini) cold-starts Node and can take 1–3s each. Without a
// cache, every Providers page reload re-spawns 3 probes serially in
// the user's perception, blocking render. The Status payload only
// changes when the user edits a binary or installs a new CLI, so a
// short TTL (30s) gives instant reloads while still picking up
// install/edit changes within the next interval.
//
// Mutating ops (Save, Delete) call InvalidateProbeCache to drop the
// stale entry so the next render re-probes immediately.

const probeCacheTTL = 30 * time.Second

type probeCacheEntry struct {
	status Status
	at     time.Time
}

var (
	probeCacheMu sync.RWMutex
	probeCache   = map[string]probeCacheEntry{}
)

func probeCacheKey(t Type, name string) string { return string(t) + "/" + name }

// ProbeAllCached returns Status per configured instance, serving from
// an in-memory cache when the entry is younger than probeCacheTTL.
// Stale or missing entries are re-probed in parallel under ctx.
func ProbeAllCached(ctx context.Context) ([]Status, error) {
	all, err := Load()
	if err != nil {
		return nil, err
	}
	out := make([]Status, len(all))
	var wg sync.WaitGroup
	now := time.Now()
	for i := range all {
		i := i
		key := probeCacheKey(all[i].Type, all[i].Name)
		probeCacheMu.RLock()
		entry, ok := probeCache[key]
		probeCacheMu.RUnlock()
		if ok && now.Sub(entry.at) < probeCacheTTL {
			out[i] = entry.status
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := Probe(ctx, all[i])
			probeCacheMu.Lock()
			probeCache[key] = probeCacheEntry{status: st, at: time.Now()}
			probeCacheMu.Unlock()
			out[i] = st
		}()
	}
	wg.Wait()
	return out, nil
}

// InvalidateProbeCache drops the cached Status for one instance.
// Empty type or name drops the whole cache (useful on bulk ops).
func InvalidateProbeCache(t Type, name string) {
	probeCacheMu.Lock()
	defer probeCacheMu.Unlock()
	if t == "" || name == "" {
		probeCache = map[string]probeCacheEntry{}
		return
	}
	delete(probeCache, probeCacheKey(t, name))
}

// ── internal ──────────────────────────────────────────────────────────

func mergeWithDefaults(c userconfig.ProvidersConfig) []Instance {
	out := make([]Instance, 0, 3)
	for _, t := range SupportedTypes() {
		list := readList(c, t)
		if len(list) == 0 {
			out = append(out, Instance{Type: t, Name: string(t)})
			continue
		}
		for _, raw := range list {
			out = append(out, Instance{
				Type:      t,
				Name:      raw.Name,
				Binary:    raw.BinaryPath,
				ExtraArgs: raw.ExtraArgs,
				Env:       raw.Env,
				Disabled:  raw.Disabled,
				Hooks:     hooksFromUser(raw.Hooks),
				Storage:   storageFromUser(raw.Storage),
			})
		}
	}
	return out
}

func readList(c userconfig.ProvidersConfig, t Type) []userconfig.ProviderInstance {
	switch t {
	case TypeClaude:
		return c.Claude
	case TypeCodex:
		return c.Codex
	case TypeGemini:
		return c.Gemini
	}
	return nil
}

func pickList(c *userconfig.ProvidersConfig, t Type) *[]userconfig.ProviderInstance {
	switch t {
	case TypeClaude:
		return &c.Claude
	case TypeCodex:
		return &c.Codex
	case TypeGemini:
		return &c.Gemini
	}
	return nil
}

func toUserInstance(ins Instance) userconfig.ProviderInstance {
	return userconfig.ProviderInstance{
		Name:       ins.Name,
		BinaryPath: ins.Binary,
		Disabled:   ins.Disabled,
		ExtraArgs:  ins.ExtraArgs,
		Env:        ins.Env,
		Hooks:      hooksToUser(ins.Hooks),
		Storage:    storageToUser(ins.Storage),
	}
}

// hooksFromUser converts the persisted shape into the in-memory map.
// Nil-in → nil-out so callers see "no hook intent recorded" via the
// same zero value pattern as a fresh Instance.
func hooksFromUser(in map[string]userconfig.HookInstanceConfig) map[string]HookInstanceConfig {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]HookInstanceConfig, len(in))
	for k, v := range in {
		out[k] = HookInstanceConfig{Enabled: v.Enabled}
	}
	return out
}

func storageFromUser(in *userconfig.StorageConfig) *StorageConfig {
	if in == nil {
		return nil
	}
	return &StorageConfig{
		Mode:            in.Mode,
		SyncPath:        in.SyncPath,
		IntervalSeconds: in.IntervalSeconds,
	}
}

func storageToUser(in *StorageConfig) *userconfig.StorageConfig {
	if in == nil {
		return nil
	}
	return &userconfig.StorageConfig{
		Mode:            in.Mode,
		SyncPath:        in.SyncPath,
		IntervalSeconds: in.IntervalSeconds,
	}
}

func hooksToUser(in map[string]HookInstanceConfig) map[string]userconfig.HookInstanceConfig {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]userconfig.HookInstanceConfig, len(in))
	for k, v := range in {
		out[k] = userconfig.HookInstanceConfig{Enabled: v.Enabled}
	}
	return out
}

func isSupported(t Type) bool {
	for _, s := range SupportedTypes() {
		if s == t {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
