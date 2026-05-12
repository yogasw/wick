package provider

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/userconfig"
)

// Why a separate file: provider.go owns the registry CRUD; this file
// owns the *cached probe result* for those instances. Keeping them
// apart lets cache rules (TTLs, persistence, rescan triggers) evolve
// without churning the registry path.
//
// Storage: userconfig (file JSON) instead of the configs table —
// status keys are dynamic per-instance and would need explicit
// registration in configs.Service.meta. Single-file write per save
// is fine because saves are rare (boot prime, manual rescan, edit).

// VersionRefreshInterval is how stale a persisted version probe must
// be before a page render kicks off a background re-probe (when
// auto-rescan is on). Path scan is cheap and re-runs on every Rescan;
// version probe is the expensive bit and rarely changes outside CLI
// upgrades.
const VersionRefreshInterval = 24 * time.Hour

// AutoRescanConfig is the small slice of agents general config the
// provider package needs to honour the user's auto-rescan toggle.
// Defining the lookup as a function keeps the package free of a
// configs.Service dependency.
var autoRescanLookup func() bool

// SetAutoRescanLookup wires the boot-time accessor for the toggle.
// Until called, AutoRescanEnabled defaults to true.
func SetAutoRescanLookup(fn func() bool) { autoRescanLookup = fn }

// AutoRescanEnabled returns the current toggle value, defaulting to
// true when no lookup is wired.
func AutoRescanEnabled() bool {
	if autoRescanLookup == nil {
		return true
	}
	return autoRescanLookup()
}

func cacheKey(t Type, name string) string { return string(t) + "/" + name }

// loadAll reads every persisted status as the raw map. Keeps the
// userconfig file load to one read per call site.
func loadAll() map[string]userconfig.ProviderStatus {
	cfg, err := userconfig.Load(AppName)
	if err != nil {
		log.Warn().Err(err).Msg("agents.cache: load userconfig failed")
		return nil
	}
	return cfg.ProviderStatuses
}

// saveOne upserts one entry. Concurrent saves serialize on cacheMu so
// we never race on the read-modify-write of the userconfig file.
var cacheMu sync.Mutex

func saveOne(t Type, name string, ps userconfig.ProviderStatus) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cfg, err := userconfig.Load(AppName)
	if err != nil {
		log.Warn().Err(err).Msg("agents.cache: load for save failed")
		return
	}
	if cfg.ProviderStatuses == nil {
		cfg.ProviderStatuses = map[string]userconfig.ProviderStatus{}
	}
	cfg.ProviderStatuses[cacheKey(t, name)] = ps
	if err := userconfig.Save(AppName, cfg); err != nil {
		log.Warn().Err(err).Str("type", string(t)).Str("name", name).Msg("agents.cache: persist failed")
	}
}

func statusFromPersisted(ins Instance, ps userconfig.ProviderStatus) Status {
	scannedAt, _ := time.Parse(time.RFC3339Nano, ps.ScannedAt)
	var hooks map[string]HookCapability
	if len(ps.Hooks) > 0 {
		hooks = make(map[string]HookCapability, len(ps.Hooks))
		for k, hc := range ps.Hooks {
			probedAt, _ := time.Parse(time.RFC3339Nano, hc.ProbedAt)
			hooks[k] = HookCapability{
				Supported: hc.Supported,
				Verified:  hc.Verified,
				ProbedAt:  probedAt,
				Error:     hc.Error,
				Scope:     hc.Scope,
			}
		}
	}
	return Status{
		Instance:   ins,
		ResolvedAt: scannedAt,
		Path:       ps.Path,
		PathFound:  ps.PathFound,
		Version:    ps.Version,
		VersionErr: ps.VersionErr,
		Hooks:      hooks,
	}
}

// hooksToPersisted converts the in-memory hook map to the userconfig
// equivalent, formatting times as RFC3339Nano strings. Empty input
// returns nil so the JSON omits the key (omitempty respected).
func hooksToPersisted(hooks map[string]HookCapability) map[string]userconfig.HookCapability {
	if len(hooks) == 0 {
		return nil
	}
	out := make(map[string]userconfig.HookCapability, len(hooks))
	for k, hc := range hooks {
		probedAt := ""
		if !hc.ProbedAt.IsZero() {
			probedAt = hc.ProbedAt.UTC().Format(time.RFC3339Nano)
		}
		out[k] = userconfig.HookCapability{
			Supported: hc.Supported,
			Verified:  hc.Verified,
			ProbedAt:  probedAt,
			Error:     hc.Error,
			Scope:     hc.Scope,
		}
	}
	return out
}

// LoadCached returns Status per configured instance, served from the
// persistent cache. Misses return a zero Status (with the instance
// metadata filled in) and trigger a background RescanOne so the next
// render sees the result — page render must NEVER block on a cold
// `--version` spawn or the page hangs while npm shims warm up.
//
// Background refresh: when AutoRescanEnabled() and a cached entry's
// version_at is older than VersionRefreshInterval, also spawn a
// detached re-probe.
func LoadCached(ctx context.Context) ([]Status, error) {
	all, err := Load()
	if err != nil {
		return nil, err
	}
	cache := loadAll()
	out := make([]Status, len(all))
	auto := AutoRescanEnabled()
	now := time.Now()
	for i := range all {
		ins := all[i]
		ps, ok := cache[cacheKey(ins.Type, ins.Name)]
		if !ok {
			// Cache miss: render an empty card now, fill in the
			// background. The boot prime usually wins this race; if
			// not, the next reload picks up the result.
			out[i] = Status{Instance: ins}
			go backgroundRescan(ins.Type, ins.Name)
			continue
		}
		out[i] = statusFromPersisted(ins, ps)
		versionAt, _ := time.Parse(time.RFC3339Nano, ps.VersionAt)
		if auto && now.Sub(versionAt) > VersionRefreshInterval {
			go backgroundRescan(ins.Type, ins.Name)
		}
	}
	return out, nil
}

// backgroundRescan runs RescanOne with its own context + a guard so
// concurrent triggers for the same instance collapse into one. Without
// the guard, a page reload during a slow probe would queue another
// probe behind the first, multiplying the saveOne mutex pressure.
var rescanInflight sync.Map // key=string -> struct{}

func backgroundRescan(t Type, name string) {
	key := cacheKey(t, name)
	if _, loaded := rescanInflight.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	defer rescanInflight.Delete(key)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = RescanOne(ctx, t, name)
}

func persistFromStatus(t Type, name string, st Status) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	saveOne(t, name, userconfig.ProviderStatus{
		Path:       st.Path,
		PathFound:  st.PathFound,
		Version:    st.Version,
		VersionErr: st.VersionErr,
		ScannedAt:  now,
		VersionAt:  now,
		Hooks:      hooksToPersisted(st.Hooks),
	})
}

// RescanOne forces a fresh Probe + persist for one instance.
func RescanOne(ctx context.Context, t Type, name string) Status {
	ins, err := Find(t, name)
	if err != nil {
		return Status{}
	}
	st := Probe(ctx, ins)
	persistFromStatus(t, name, st)
	log.Info().
		Str("type", string(t)).
		Str("name", name).
		Str("path", st.Path).
		Str("version", st.Version).
		Bool("found", st.PathFound).
		Msg("agents.rescan: one")
	return st
}

// MergeHookCapability writes the capability-probe result for one hook
// event into the persisted status, leaving other hooks + version/path
// fields untouched. Called by the HTTP handler that runs
// HookCapabilityCheck so the next page render reflects the new
// Verified state straight from disk — same TTL semantics as the
// version probe (only re-runs on Rescan / Version change / explicit
// Test).
//
// event: well-known hook event key (HookEventPreToolUse, …). Callers
// supplying a free-form string is fine — the map accommodates any key
// future versions decide to probe.
func MergeHookCapability(t Type, name, event string, hc HookCapability) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cfg, err := userconfig.Load(AppName)
	if err != nil {
		log.Warn().Err(err).Msg("agents.cache: load for merge capability failed")
		return
	}
	if cfg.ProviderStatuses == nil {
		cfg.ProviderStatuses = map[string]userconfig.ProviderStatus{}
	}
	key := cacheKey(t, name)
	ps := cfg.ProviderStatuses[key]
	if ps.Hooks == nil {
		ps.Hooks = map[string]userconfig.HookCapability{}
	}
	probedAt := ""
	if !hc.ProbedAt.IsZero() {
		probedAt = hc.ProbedAt.UTC().Format(time.RFC3339Nano)
	}
	ps.Hooks[event] = userconfig.HookCapability{
		Supported: hc.Supported,
		Verified:  hc.Verified,
		ProbedAt:  probedAt,
		Error:     hc.Error,
		Scope:     hc.Scope,
	}
	cfg.ProviderStatuses[key] = ps
	if err := userconfig.Save(AppName, cfg); err != nil {
		log.Warn().Err(err).Str("type", string(t)).Str("name", name).Msg("agents.cache: persist capability failed")
	}
}

// RescanAll re-probes every configured instance in parallel and
// persists each result.
func RescanAll(ctx context.Context) []Status {
	all, err := Load()
	if err != nil {
		return nil
	}
	out := make([]Status, len(all))
	var wg sync.WaitGroup
	for i := range all {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := Probe(ctx, all[i])
			persistFromStatus(all[i].Type, all[i].Name, st)
			probeCacheMu.Lock()
			probeCache[probeCacheKey(all[i].Type, all[i].Name)] = probeCacheEntry{status: st, at: time.Now()}
			probeCacheMu.Unlock()
			out[i] = st
		}()
	}
	wg.Wait()
	log.Info().Int("count", len(out)).Msg("agents.rescan: all")
	return out
}
