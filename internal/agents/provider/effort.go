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
