package provider

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"
)

// EffortDefault delegates to the selected provider/model's default. An empty
// stored selection instead inherits the next enclosing configuration.
const EffortDefault = "default"

// EffortOption is a native effort ID, not a universal token budget.
type EffortOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// EffortCapabilities describes a model on a specific provider instance.
// Default is empty when unknown. The UI supplies the implicit Default option.
type EffortCapabilities struct {
	Options []EffortOption `json:"options"`
	Default string         `json:"default,omitempty"`
	Source  string         `json:"source,omitempty"`
}

// EffortResolver receives the full pin, including account/live-set path.
// Implementations should use cached metadata or bounded context-aware probes.
type EffortResolver func(ctx context.Context, ins Instance, modelID string) (EffortCapabilities, error)

var (
	effortMu        sync.RWMutex
	effortResolvers = map[Type]EffortResolver{}
)

// RegisterEffortResolver registers the provider's capability callback.
// A nil resolver removes the registration, following RegisterModelSets.
func RegisterEffortResolver(t Type, resolve EffortResolver) {
	effortMu.Lock()
	defer effortMu.Unlock()
	if resolve == nil {
		delete(effortResolvers, t)
		return
	}
	effortResolvers[t] = resolve
	dropEffortCache(func(k effortKey) bool { return k.typ == t })
}

// EffortCapabilitiesFor delegates to the provider. registered=false means
// unknown; a registered resolver with zero options means not configurable.
func EffortCapabilitiesFor(ctx context.Context, ins Instance, modelID string) (caps EffortCapabilities, registered bool, err error) {
	effortMu.RLock()
	resolve := effortResolvers[ins.Type]
	effortMu.RUnlock()
	if resolve == nil {
		return EffortCapabilities{}, false, nil
	}
	caps, err = cachedEffort(ctx, ins, modelID, resolve)
	if err != nil {
		return EffortCapabilities{}, true, err
	}
	caps.Options = append([]EffortOption{}, caps.Options...)
	return caps, true, nil
}

// Capabilities are cached per instance + model pin, so a composer re-render,
// a model list with many rows, or a failover check never re-runs a provider
// probe. Concurrent misses for one key share a single resolver call.
var (
	effortCacheTTL      = 6 * time.Hour
	effortErrorCacheTTL = time.Minute
	effortNow           = time.Now

	effortCacheMu sync.Mutex
	effortCache   = map[effortKey]*effortEntry{}
)

// effortKey also hashes binary, args and env: a CLI upgrade or a new key
// can change what a model offers, so it must not reuse the old answer.
type effortKey struct {
	typ    Type
	name   string
	model  string
	config uint64
}

type effortEntry struct {
	done    chan struct{}
	caps    EffortCapabilities
	err     error
	expires time.Time
}

func newEffortKey(ins Instance, modelID string) effortKey {
	h := fnv.New64a()
	for _, part := range append(append([]string{ins.Binary}, ins.ExtraArgs...), ins.Env...) {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return effortKey{typ: ins.Type, name: ins.Name, model: modelID, config: h.Sum64()}
}

func cachedEffort(ctx context.Context, ins Instance, modelID string, resolve EffortResolver) (EffortCapabilities, error) {
	key := newEffortKey(ins, modelID)
	effortCacheMu.Lock()
	entry, ok := effortCache[key]
	if ok {
		select {
		case <-entry.done:
			if effortNow().Before(entry.expires) {
				effortCacheMu.Unlock()
				return entry.caps, entry.err
			}
			ok = false // expired: fall through and refresh
		default: // another caller is resolving this key: wait for it
		}
	}
	if !ok {
		entry = &effortEntry{done: make(chan struct{})}
		effortCache[key] = entry
		effortCacheMu.Unlock()
		caps, err := resolve(ctx, ins, modelID)
		if err == nil {
			err = validateEffortCapabilities(caps)
		}
		ttl := effortCacheTTL
		if err != nil {
			caps, ttl = EffortCapabilities{}, effortErrorCacheTTL
		}
		effortCacheMu.Lock()
		entry.caps, entry.err, entry.expires = caps, err, effortNow().Add(ttl)
		if ctx.Err() != nil && err != nil {
			// a cancelled caller's failure says nothing about the model
			delete(effortCache, key)
		}
		close(entry.done)
		effortCacheMu.Unlock()
		return caps, err
	}
	effortCacheMu.Unlock()
	select {
	case <-entry.done:
		return entry.caps, entry.err
	case <-ctx.Done():
		return EffortCapabilities{}, ctx.Err()
	}
}

func dropEffortCache(match func(effortKey) bool) {
	effortCacheMu.Lock()
	defer effortCacheMu.Unlock()
	for k := range effortCache {
		if match(k) {
			delete(effortCache, k)
		}
	}
}

// InvalidateEffortCache drops every cached model answer for one instance —
// call it when its model list is refreshed or its config changes. An empty
// type drops everything.
func InvalidateEffortCache(t Type, name string) {
	dropEffortCache(func(k effortKey) bool { return t == "" || (k.typ == t && k.name == name) })
}

func validateEffortCapabilities(caps EffortCapabilities) error {
	seen := map[string]bool{}
	for _, option := range caps.Options {
		if option.ID == "" || strings.TrimSpace(option.ID) != option.ID || option.ID == EffortDefault || seen[option.ID] {
			return fmt.Errorf("invalid or duplicate effort option %q", option.ID)
		}
		seen[option.ID] = true
	}
	if caps.Default != "" && !seen[caps.Default] {
		return fmt.Errorf("default effort %q is not an offered option", caps.Default)
	}
	return nil
}

// EffortSelection distinguishes saved choice from known effective effort.
// Native is empty when no explicit override should be emitted.
type EffortSelection struct {
	Requested string `json:"requested"`
	Effective string `json:"effective,omitempty"`
	Native    string `json:"-"`
}

// ResolveEffortSelection checks choices in caller-supplied precedence order.
// Explicit Default stops inheritance. Unsupported choices are never silently
// translated to a different effort.
func ResolveEffortSelection(caps EffortCapabilities, choices ...string) (EffortSelection, error) {
	for _, choice := range choices {
		if choice == "" {
			continue
		}
		if choice == EffortDefault {
			break
		}
		for _, option := range caps.Options {
			if option.ID == choice {
				return EffortSelection{Requested: choice, Effective: choice, Native: choice}, nil
			}
		}
		return EffortSelection{}, fmt.Errorf("effort %q is not supported by the selected model", choice)
	}
	return EffortSelection{Requested: EffortDefault, Effective: caps.Default}, nil
}

// effortLabels names the native ids the integrations use. An id missing
// here is shown as itself, so a new vendor level still renders.
var effortLabels = map[string]string{
	"none": "None", "off": "Off", "minimal": "Minimal", "low": "Low",
	"medium": "Medium", "high": "High", "xhigh": "Extra High", "max": "Max",
	"auto": "Auto",
}

// EffortLabel is the display label of a native effort id.
func EffortLabel(id string) string {
	if l, ok := effortLabels[id]; ok {
		return l
	}
	return id
}

// StaticEffortResolver offers the same native ids for every model of a
// provider, as its verified catalog lists them. def is the known default
// ("" when the vendor does not say), source names where the list comes from.
func StaticEffortResolver(source, def string, ids ...string) EffortResolver {
	opts := make([]EffortOption, 0, len(ids))
	for _, id := range ids {
		opts = append(opts, EffortOption{ID: id, Label: EffortLabel(id)})
	}
	return func(context.Context, Instance, string) (EffortCapabilities, error) {
		return EffortCapabilities{Options: append([]EffortOption{}, opts...), Default: def, Source: source}, nil
	}
}

// ErrEffortNotConfigurable is returned when an explicit effort is asked of
// a provider or model that offers none.
var ErrEffortNotConfigurable = fmt.Errorf("the selected model has no configurable reasoning effort")

// ResolveSpawnEffort resolves the requested choices (precedence order,
// "" = inherit, "default" = stop and use the model default) for one
// instance + model. A provider without a resolver, or a model with no
// options, resolves to Default; an explicit choice there is an error so
// the caller can say why it was not applied.
func ResolveSpawnEffort(ctx context.Context, ins Instance, modelID string, choices ...string) (EffortSelection, error) {
	explicit := ""
	for _, c := range choices {
		if c != "" {
			explicit = c
			break
		}
	}
	caps, registered, err := EffortCapabilitiesFor(ctx, ins, modelID)
	if err != nil {
		return EffortSelection{Requested: EffortDefault}, err
	}
	if !registered || len(caps.Options) == 0 {
		if explicit != "" && explicit != EffortDefault {
			return EffortSelection{Requested: EffortDefault, Effective: caps.Default}, ErrEffortNotConfigurable
		}
		return EffortSelection{Requested: EffortDefault, Effective: caps.Default}, nil
	}
	return ResolveEffortSelection(caps, choices...)
}

// effortProbeTimeout bounds a capability lookup on the spawn path; the
// cache makes every later spawn of the same model free.
var effortProbeTimeout = 5 * time.Second

// spawnEffortLocked is the native effort the next spawn applies: the
// agent's requested effort checked against the model this spawn runs. A
// choice the model does not offer is dropped with a notice instead of
// failing the spawn — the turn still runs, on the model's default.
// Caller holds a.mu.
func (a *Agent) spawnEffortLocked(ctx context.Context) string {
	if a.cfg.Effort == "" || a.cfg.Effort == EffortDefault || a.cfg.Instance == nil {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pctx, cancel := context.WithTimeout(ctx, effortProbeTimeout)
	defer cancel()
	model := a.spawnModelIDLocked()
	sel, err := ResolveSpawnEffort(pctx, *a.cfg.Instance, model, a.cfg.Effort)
	if err != nil {
		if a.store != nil {
			_ = a.store.AppendNoticeTurn(fmt.Sprintf("Reasoning effort %q was not applied: %v. This turn runs on the model's default effort.", EffortLabel(a.cfg.Effort), err))
		}
		return ""
	}
	return sel.Native
}

// EffortInherited, when set, names the reasoning effort a session's agent
// runs with when the session picked none: its Team agent's default, else
// its project's. ok false = there is none (the model default applies).
// The agents tool wires it, as it does SessionDefault.
var EffortInherited func(ctx context.Context, sessionID string) (effort string, ok bool)

// EffortInfo is what a picker needs about one agent's effort: the stops the
// selected model offers, its known default, what was asked for and what a
// spawn would run with.
type EffortInfo struct {
	// Configurable is false when the provider or model offers no effort:
	// the picker hides itself instead of showing invented levels.
	Configurable bool           `json:"configurable"`
	Options      []EffortOption `json:"options"`
	// Default is the model's own default when known, else "".
	Default string `json:"default,omitempty"`
	Source  string `json:"source,omitempty"`
	// Requested is the session's own pick ("" = none, "default", or an id).
	Requested string `json:"requested"`
	// Inherited is the agent/project default used when Requested is "".
	Inherited string `json:"inherited,omitempty"`
	// Effective is the native id a spawn applies ("" = the model default).
	Effective string `json:"effective,omitempty"`
	// Stale is set when the pick is not offered by the current model (it
	// was made on another model): a spawn ignores it until it is changed.
	Stale bool   `json:"stale,omitempty"`
	Error string `json:"error,omitempty"`
}

// EffortInfoFor describes the effort of an agent on providerKey
// ("type/name") + modelID, with its own pick and the inherited default.
func EffortInfoFor(ctx context.Context, providerKey, modelID, requested, inherited string) EffortInfo {
	info := EffortInfo{Requested: requested, Inherited: inherited, Options: []EffortOption{}}
	key := normalizeProviderKey(providerKey)
	typ, name, _ := strings.Cut(key, "/")
	ins, err := Find(Type(typ), name)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	caps, registered, err := EffortCapabilitiesFor(ctx, ins, modelID)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	info.Default, info.Source = caps.Default, caps.Source
	if !registered || len(caps.Options) == 0 {
		return info
	}
	info.Configurable = true
	info.Options = caps.Options
	sel, err := ResolveEffortSelection(caps, requested, inherited)
	if err != nil {
		info.Stale = true
		info.Effective = caps.Default
		return info
	}
	info.Effective = sel.Effective
	return info
}

// ValidEffortChoice reports whether v can be saved as a pick for caps: ""
// (clear) and "default" always can, anything else must be offered.
func ValidEffortChoice(info EffortInfo, v string) bool {
	if v == "" || v == EffortDefault {
		return true
	}
	for _, o := range info.Options {
		if o.ID == v {
			return true
		}
	}
	return false
}
