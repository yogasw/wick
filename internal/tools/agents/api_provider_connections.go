package agents

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/logintty"
	"github.com/yogasw/wick/pkg/tool"
)

// connectionsUsageTimeout caps the whole fan-out. The per-probe client
// already carries its own 10s timeout; this bounds the page-level wait
// when several distinct accounts are slow at once.
const connectionsUsageTimeout = 12 * time.Second

// connectionsProbe is the seam over logintty so collectConnections can
// be tested without credential files or a live usage endpoint.
type connectionsProbe interface {
	configDir(t provider.Type, env []string) string
	account(t provider.Type, env []string) logintty.Account
	usage(ctx context.Context, t provider.Type, env []string) ([]logintty.UsageWindow, error)
}

// liveProbe is the production connectionsProbe backed by logintty.
type liveProbe struct{}

func (liveProbe) configDir(t provider.Type, env []string) string {
	return logintty.ConfigDir(t, env)
}

func (liveProbe) account(t provider.Type, env []string) logintty.Account {
	return logintty.ReadAccount(t, env)
}

func (liveProbe) usage(_ context.Context, t provider.Type, env []string) ([]logintty.UsageWindow, error) {
	return logintty.ReadUsage(t, env)
}

// usageCacheTTL is how long a usage probe's result is reused. The
// numbers are rolling-window utilization percentages that move over
// minutes, so a short cache keeps the badges honest while stopping a
// page refresh (or several open tabs) from hammering the endpoint.
const usageCacheTTL = 60 * time.Second

// usageCache memoizes usage probes per credential dir.
//
// A transient failure is NOT cached — the next page load retries. An
// ErrUsageUnsupported verdict IS cached: "this provider type has no
// usage API" is a property of the build, not a condition that clears.
type usageCache struct {
	ttl time.Duration
	mu  sync.Mutex
	m   map[string]usageCacheEntry
}

type usageCacheEntry struct {
	windows []logintty.UsageWindow
	err     error
	at      time.Time
}

func newUsageCache(ttl time.Duration) *usageCache {
	return &usageCache{ttl: ttl, m: map[string]usageCacheEntry{}}
}

// usageProbes is the process-wide cache backing the connections endpoint.
var usageProbes = newUsageCache(usageCacheTTL)

// get returns the cached windows for dir, calling fetch when the entry
// is absent or older than the TTL. now is passed in so tests can move
// time without sleeping.
func (c *usageCache) get(dir string, now time.Time, fetch func() ([]logintty.UsageWindow, error)) ([]logintty.UsageWindow, error) {
	c.mu.Lock()
	e, ok := c.m[dir]
	c.mu.Unlock()
	if ok && now.Sub(e.at) < c.ttl {
		return e.windows, e.err
	}

	windows, err := fetch()
	if err != nil && !errors.Is(err, logintty.ErrUsageUnsupported) {
		return nil, err // transient — leave any prior entry to expire on its own
	}
	c.mu.Lock()
	c.m[dir] = usageCacheEntry{windows: windows, err: err, at: now}
	c.mu.Unlock()
	return windows, err
}

// usageWindowDTO is one rate-limit window on the wire.
type usageWindowDTO struct {
	Key         string  `json:"key"`
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at,omitempty"`
}

// providerConnectionDTO is one instance's account + usage, keyed by the
// same {type, name} pair the providers list uses so the SPA can join it
// onto the cards it already rendered.
type providerConnectionDTO struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Connected  bool   `json:"connected"`
	Email      string `json:"email,omitempty"`
	Plan       string `json:"plan,omitempty"`
	Org        string `json:"org,omitempty"`
	AuthMethod string `json:"auth_method,omitempty"`
	// UsageSupported is false for provider types with no usage API
	// (codex/gemini today) — a different state from a failed fetch.
	UsageSupported bool             `json:"usage_supported"`
	UsageErr       string           `json:"usage_err,omitempty"`
	Windows        []usageWindowDTO `json:"windows,omitempty"`
}

// ProviderConnectionsResponse is GET /api/providers/connections.
type ProviderConnectionsResponse struct {
	Connections []providerConnectionDTO `json:"connections"`
}

// collectConnections reads account + usage for every instance that keeps
// credentials on disk.
//
// Account comes from local files and is cheap. Usage is a remote HTTP
// call, so it is probed once per distinct config dir and fanned out:
// several instances commonly point at one credential dir (same account,
// different flags), and the providers list would otherwise fire one
// request per card on every page load. Probes also go through cache, so
// repeated loads inside the TTL cost nothing.
func collectConnections(ctx context.Context, instances []provider.Instance, p connectionsProbe, cache *usageCache) []providerConnectionDTO {
	type slot struct {
		ins provider.Instance
		dir string
	}
	slots := make([]slot, 0, len(instances))
	// Keep the first instance seen per dir as that dir's usage probe;
	// its env is representative because the dir is what the probe reads.
	probeEnv := map[string][]string{}
	probeType := map[string]provider.Type{}
	for _, ins := range instances {
		dir := p.configDir(ins.Type, ins.Env)
		if dir == "" {
			continue // no on-disk credentials (wick) — nothing to report
		}
		slots = append(slots, slot{ins: ins, dir: dir})
		if _, seen := probeEnv[dir]; !seen {
			probeEnv[dir] = ins.Env
			probeType[dir] = ins.Type
		}
	}
	if len(slots) == 0 {
		return nil
	}

	type usageResult struct {
		windows []logintty.UsageWindow
		err     error
	}
	var mu sync.Mutex
	results := make(map[string]usageResult, len(probeEnv))

	now := time.Now()
	g, gctx := errgroup.WithContext(ctx)
	for dir := range probeEnv {
		dir, env, t := dir, probeEnv[dir], probeType[dir]
		g.Go(func() error {
			w, err := cache.get(dir, now, func() ([]logintty.UsageWindow, error) {
				return p.usage(gctx, t, env)
			})
			mu.Lock()
			results[dir] = usageResult{windows: w, err: err}
			mu.Unlock()
			return nil // a failed probe is reported per-row, never fatal
		})
	}
	_ = g.Wait()

	out := make([]providerConnectionDTO, 0, len(slots))
	for _, s := range slots {
		acc := p.account(s.ins.Type, s.ins.Env)
		dto := providerConnectionDTO{
			Type:       string(s.ins.Type),
			Name:       s.ins.Name,
			Connected:  acc.Connected,
			Email:      acc.Email,
			Plan:       acc.Plan,
			Org:        acc.Org,
			AuthMethod: acc.AuthMethod,
		}
		res := results[s.dir]
		switch {
		case errors.Is(res.err, logintty.ErrUsageUnsupported):
			// Type has no usage API — not a failure, just nothing to show.
		case res.err != nil:
			dto.UsageSupported = true
			dto.UsageErr = res.err.Error()
		default:
			dto.UsageSupported = true
			dto.Windows = usageWindowDTOs(res.windows)
		}
		out = append(out, dto)
	}
	return out
}

func usageWindowDTOs(windows []logintty.UsageWindow) []usageWindowDTO {
	if len(windows) == 0 {
		return nil
	}
	out := make([]usageWindowDTO, 0, len(windows))
	for _, w := range windows {
		dto := usageWindowDTO{Key: w.Key, Utilization: w.Utilization}
		if !w.ResetsAt.IsZero() {
			dto.ResetsAt = w.ResetsAt.UTC().Format(time.RFC3339)
		}
		out = append(out, dto)
	}
	return out
}

// apiProviderConnections handles GET /api/providers/connections: the
// account and usage behind every provider instance in one request, so
// the providers list can badge each card without the SPA firing two
// requests per card.
//
// Deliberately separate from GET /api/providers: that payload is local
// and fast, while this one waits on a remote usage endpoint. Keeping
// them apart lets the list paint immediately and fill the badges in.
func apiProviderConnections(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	instances, err := provider.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), connectionsUsageTimeout)
	defer cancel()

	conns := collectConnections(ctx, instances, liveProbe{}, usageProbes)
	if conns == nil {
		conns = []providerConnectionDTO{}
	}
	c.JSON(http.StatusOK, ProviderConnectionsResponse{Connections: conns})
}
