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
	// identity names the account behind an instance, so instances
	// sharing one login share one probe.
	identity(t provider.Type, env []string) string
	// usageSupported reports whether this type has a usage API at all.
	usageSupported(t provider.Type) bool
	account(t provider.Type, env []string) logintty.Account
	usage(ctx context.Context, t provider.Type, env []string) ([]logintty.UsageWindow, error)
	// credentialsChangedAt is when the account's stored credentials were
	// last rewritten, so a cached failure can be retried once after a
	// login was renewed. Zero when unknown.
	credentialsChangedAt(t provider.Type, env []string) time.Time
}

// liveProbe is the production connectionsProbe backed by logintty.
type liveProbe struct{}

func (liveProbe) configDir(t provider.Type, env []string) string {
	return logintty.ConfigDir(t, env)
}

func (liveProbe) identity(t provider.Type, env []string) string {
	return logintty.UsageIdentity(t, env)
}

func (liveProbe) credentialsChangedAt(t provider.Type, env []string) time.Time {
	return logintty.CredentialsChangedAt(t, env)
}

func (liveProbe) usageSupported(t provider.Type) bool {
	return logintty.SupportsUsage(t)
}

func (liveProbe) account(t provider.Type, env []string) logintty.Account {
	return logintty.ReadAccount(t, env)
}

func (liveProbe) usage(_ context.Context, t provider.Type, env []string) ([]logintty.UsageWindow, error) {
	return logintty.ReadUsage(t, env)
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
	UsageSupported bool `json:"usage_supported"`
	// UsagePending is true while the first reading for this account is
	// still being fetched — the probe is paced, so a cold page can
	// paint before the numbers arrive. Not an error: the badge fills in
	// on a later poll.
	UsagePending bool `json:"usage_pending,omitempty"`
	// UsageChecking is true while a probe for this account is in
	// flight, so the card can say it is working rather than looking
	// frozen on a number that is deliberately not refetched per paint.
	UsageChecking bool   `json:"usage_checking,omitempty"`
	UsageErr      string `json:"usage_err,omitempty"`
	// UsageFetchedAt is when this reading was actually taken upstream,
	// and UsageAgeS the same thing as seconds-ago so the card can say
	// "cached 42s ago" without trusting client clock skew. Readings are
	// shared per account and cached, so the page must show its age
	// rather than implying every paint is a fresh call.
	UsageFetchedAt string `json:"usage_fetched_at,omitempty"`
	UsageAgeS      int    `json:"usage_age_s,omitempty"`
	// UsageNextS is how many seconds until the next probe is allowed
	// (TTL when healthy, backoff after a failure). Negative never
	// happens — it clamps at 0, meaning "due on the next poll".
	UsageNextS int              `json:"usage_next_s,omitempty"`
	Windows    []usageWindowDTO `json:"windows,omitempty"`
}

// ProviderConnectionsResponse is GET /api/providers/connections.
type ProviderConnectionsResponse struct {
	Connections []providerConnectionDTO `json:"connections"`
}

// collectConnections reads account + usage for every instance that keeps
// credentials on disk.
//
// Account comes from local files and is cheap, so it is read per
// instance. Usage is a remote call against a rate-limited endpoint, so
// it is asked for ONCE PER ACCOUNT and served from usageCache: probes
// are deduped, single-flighted, paced apart and backed off on failure
// (see usage_probe.go). Types with no usage API never reach the cache.
func collectConnections(ctx context.Context, instances []provider.Instance, p connectionsProbe, cache *usageCache) []providerConnectionDTO {
	type slot struct {
		ins provider.Instance
		key string // account identity, "" when this type has no usage API
	}
	slots := make([]slot, 0, len(instances))
	// Keep the first instance seen per account as that account's probe;
	// its env is representative because the credentials are what the
	// probe reads, and instances sharing a key share those credentials.
	probeEnv := map[string][]string{}
	probeType := map[string]provider.Type{}
	for _, ins := range instances {
		if p.configDir(ins.Type, ins.Env) == "" {
			continue // no on-disk credentials (wick) — nothing to report
		}
		if !p.usageSupported(ins.Type) {
			slots = append(slots, slot{ins: ins})
			continue
		}
		key := p.identity(ins.Type, ins.Env)
		slots = append(slots, slot{ins: ins, key: key})
		if _, seen := probeEnv[key]; !seen {
			probeEnv[key] = ins.Env
			probeType[key] = ins.Type
		}
	}
	if len(slots) == 0 {
		return nil
	}

	var mu sync.Mutex
	results := make(map[string]usageView, len(probeEnv))

	now := time.Now()

	// The fan-out is over cache reads, not over network calls: the
	// cache serialises the actual probes behind the pace gate, so this
	// loop cannot burst the endpoint however many accounts exist.
	g, gctx := errgroup.WithContext(ctx)
	for key := range probeEnv {
		key, env, t := key, probeEnv[key], probeType[key]
		g.Go(func() error {
			v := cache.getWait(gctx, key, func() ([]logintty.UsageWindow, error) {
				return p.usage(gctx, t, env)
			}, p.credentialsChangedAt(t, env))
			mu.Lock()
			results[key] = v
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
		if s.key == "" {
			// Type has no usage API — not a failure, just nothing to show.
			out = append(out, dto)
			continue
		}
		dto.UsageSupported = true
		res := results[s.key]
		switch {
		case errors.Is(res.Err, logintty.ErrUsageUnsupported):
			dto.UsageSupported = false
		case res.Err != nil:
			dto.UsageErr = res.Err.Error()
		case !res.Known:
			dto.UsagePending = true
		default:
			dto.Windows = usageWindowDTOs(res.Windows)
		}
		dto.UsageChecking = res.Checking
		applyUsageProvenance(&dto, res, now)
		out = append(out, dto)
	}
	return out
}

// applyUsageProvenance stamps a row with when its reading was taken and
// when the next probe is due, so the UI can show the cache honestly.
func applyUsageProvenance(dto *providerConnectionDTO, v usageView, now time.Time) {
	if !v.FetchedAt.IsZero() {
		dto.UsageFetchedAt = v.FetchedAt.UTC().Format(time.RFC3339)
		if age := v.Age(now); age > 0 {
			dto.UsageAgeS = int(age.Round(time.Second) / time.Second)
		}
	}
	if !v.NextAt.IsZero() {
		if d := v.NextAt.Sub(now); d > 0 {
			dto.UsageNextS = int(d.Round(time.Second) / time.Second)
		}
	}
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
	if notReady(c) || !requireProviderMenu(c) {
		return
	}
	instances, err := provider.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Same filter as the list: a badge for an instance the caller does
	// not manage would leak both its existence and whose account runs it.
	instances = manageableProviders(c, instances, func(ins provider.Instance) (provider.Type, string) {
		return ins.Type, ins.Name
	})
	ctx, cancel := context.WithTimeout(c.Context(), connectionsUsageTimeout)
	defer cancel()

	conns := collectConnections(ctx, instances, liveProbe{}, usageProbes)
	if conns == nil {
		conns = []providerConnectionDTO{}
	}
	c.JSON(http.StatusOK, ProviderConnectionsResponse{Connections: conns})
}
