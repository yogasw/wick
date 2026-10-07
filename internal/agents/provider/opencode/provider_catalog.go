package opencode

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/pkg/envscrub"
)

// provider_catalog.go fetches an instance's provider catalog from its
// opencode server (GET /provider + GET /provider/auth) for
// provider.OpencodeCatalog: login choices, API-key providers and the live
// model list all come from opencode itself.
//
// Which server: the instance's running turn server for the same data
// folder when there is one (read-only GETs, no lease). Otherwise a
// throwaway `opencode serve` is booted for the listing and killed right
// after — NOT through the manager: its env differs from a turn server's
// (no workspace MCP switches), so acquiring it would be a new key in the
// same group and the stale sweep would kill the real server. One boot
// costs about what `opencode models` does, and yields all three lists.

func init() {
	provider.OpencodeCatalogFetcher = fetchInstanceCatalog
	provider.RegisterModelHarvester(provider.TypeOpencode, harvestModels)
}

// errNoLiveServe: no `opencode serve` is running for the instance and the
// caller may not start one.
var errNoLiveServe = errors.New("opencode: no running server for the catalog (Refresh to start one)")

// harvestModels is opencode's provider.ModelHarvester: the catalog of the
// instance's running `opencode serve` (GET /provider), the same list the
// CLI path builds (provider.ListCLIModels → catalogModels). Nothing is
// started when no server runs.
func harvestModels(ctx context.Context, ins provider.Instance) ([]provider.ModelSeed, error) {
	cat, err := fetchInstanceCatalog(ctx, ins)
	if err != nil || cat == nil {
		return nil, err
	}
	return cat.Models(), nil
}

// catalogServe boots the throwaway server; swapped in tests.
var catalogServe = startServe

const catalogRequestTimeout = 20 * time.Second

func fetchInstanceCatalog(ctx context.Context, ins provider.Instance) (*provider.OpencodeCatalog, error) {
	dataDir, err := provider.OpencodeDataDir(ins)
	if err != nil {
		return nil, err
	}
	spec := serverSpec{instance: ins.Name, dir: dataDir}
	if h, ok := servers.LiveInGroup(spec.group()); ok {
		if cat, err := fetchCatalog(ctx, catalogClient(h, dataDir)); err == nil {
			return cat, nil
		}
	}
	// No server running: a throwaway one is a full opencode process, so it
	// starts only for the user's explicit Refresh / login action
	// (provider.WithHelperSpawn); a render or harvest gets nothing.
	if !provider.HelperSpawnAllowed(ctx) {
		return nil, errNoLiveServe
	}
	bin, found := provider.ResolveBinary(ins)
	if !found {
		return nil, fmt.Errorf("opencode binary not found: %s", bin)
	}
	// The listing needs no MCP: the instance's extra servers stay off, so
	// the boot neither spawns nor dials them.
	lite := ins
	lite.ExtraMCPServers = ""
	added, err := spawnEnv(lite, "", "", "", nil)
	if err != nil {
		return nil, err
	}
	// AccountEnv carries the instance Env, so API keys count as connected.
	spec.bin = bin
	spec.env = append(append(envscrub.ScrubOSEnv(), provider.AccountEnv(ins)...), added...)
	// The throwaway server is a full opencode process: one model listing
	// at a time across instances, inside the memory guard
	// ("opencode-catalog" scope, the instance's own limit).
	free, err := provider.AcquireHelperSlot(ctx)
	defer free()
	if err != nil {
		return nil, err
	}
	wrap, release := provider.HelperWrap(&ins, provider.HelperLabel(provider.TypeOpencode, "catalog"))
	defer release()
	spec.wrap = wrap
	h, err := catalogServe(ctx, spec, randomPassword())
	if err != nil {
		return nil, err
	}
	defer h.Kill()
	return fetchCatalog(ctx, catalogClient(h, dataDir))
}

func catalogClient(h *serverHandle, dir string) *apiClient {
	return &apiClient{base: h.url, password: h.password, dir: dir, http: &http.Client{Timeout: catalogRequestTimeout}}
}

// providerList is GET /provider.
type providerList struct {
	All []struct {
		ID     string   `json:"id"`
		Name   string   `json:"name"`
		Env    []string `json:"env"`
		Models map[string]struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
			Limit  struct {
				Context int `json:"context"`
			} `json:"limit"`
		} `json:"models"`
	} `json:"all"`
	Default   map[string]string `json:"default"`
	Connected []string          `json:"connected"`
}

// providerAuth is GET /provider/auth: providerID → methods.
type providerAuth map[string][]struct {
	Type  string `json:"type"`
	Label string `json:"label"`
}

// fetchCatalog reads both routes; /provider/auth failing still yields the
// providers (the login picker then falls back on its own).
func fetchCatalog(ctx context.Context, c *apiClient) (*provider.OpencodeCatalog, error) {
	var list providerList
	if err := c.do(ctx, http.MethodGet, "/provider", nil, &list); err != nil {
		return nil, err
	}
	cat := &provider.OpencodeCatalog{Connected: list.Connected, Default: list.Default}
	for _, p := range list.All {
		if p.ID == "" {
			continue
		}
		cp := provider.OpencodeCatalogProvider{ID: p.ID, Name: p.Name, Env: p.Env}
		for key, m := range p.Models {
			if m.Status == "deprecated" {
				continue
			}
			id := m.ID
			if id == "" {
				id = key
			}
			cp.Models = append(cp.Models, provider.ModelSeed{ID: p.ID + "/" + id, Desc: m.Name})
		}
		cat.Providers = append(cat.Providers, cp)
	}
	var auth providerAuth
	if err := c.do(ctx, http.MethodGet, "/provider/auth", nil, &auth); err == nil {
		cat.Auth = make(map[string][]provider.OpencodeAuthMethod, len(auth))
		for id, ms := range auth {
			for _, m := range ms {
				cat.Auth[id] = append(cat.Auth[id], provider.OpencodeAuthMethod{Type: m.Type, Label: m.Label})
			}
		}
	}
	return cat, nil
}

// contextState is the meter's scale and the auto-compaction flag for
// model on this running server, read once per server and model and kept
// for contextStateTTL: both sit in front of every prompt, and neither
// changes between turns. Each read is bounded by contextStateWait, so a
// slow server delays the prompt by seconds, not minutes.
func contextState(ctx context.Context, c *apiClient, model string) (int, *bool) {
	key := c.base + "\x00" + c.password + "\x00" + model
	contextStateMu.Lock()
	e, ok := contextStateCache[key]
	contextStateMu.Unlock()
	if ok && time.Since(e.at) < contextStateTTL {
		return e.window, e.auto
	}
	rctx, cancel := context.WithTimeout(ctx, contextStateWait)
	defer cancel()
	w := modelWindow(rctx, c, model)
	if w <= 0 {
		return 0, nil // not cached: the next turn asks again
	}
	auto := autoCompaction(rctx, c)
	contextStateMu.Lock()
	contextStateCache[key] = contextStateEntry{window: w, auto: auto, at: time.Now()}
	contextStateMu.Unlock()
	return w, auto
}

type contextStateEntry struct {
	window int
	auto   *bool
	at     time.Time
}

var (
	contextStateMu    sync.Mutex
	contextStateCache = map[string]contextStateEntry{}
)

const (
	contextStateTTL  = 10 * time.Minute
	contextStateWait = 5 * time.Second
)

// modelWindow is model's ("provider/model") context limit from the running
// server's GET /provider (limit.context), 0 when not listed.
func modelWindow(ctx context.Context, c *apiClient, model string) int {
	prov, id, ok := strings.Cut(model, "/")
	if !ok {
		return 0
	}
	var list providerList
	if err := c.do(ctx, http.MethodGet, "/provider", nil, &list); err != nil {
		return 0
	}
	for _, p := range list.All {
		if p.ID != prov {
			continue
		}
		for key, m := range p.Models {
			if key == id || m.ID == id {
				return m.Limit.Context
			}
		}
	}
	return 0
}

// autoCompaction is whether this server compacts a session by itself
// once the window fills: its resolved config's compaction.auto (GET
// /config), true when unset — opencode compacts unless that is false.
// nil when the config cannot be read.
func autoCompaction(ctx context.Context, c *apiClient) *bool {
	var cfg struct {
		Compaction *struct {
			Auto *bool `json:"auto"`
		} `json:"compaction"`
	}
	if err := c.do(ctx, http.MethodGet, "/config", nil, &cfg); err != nil {
		return nil
	}
	on := true
	if cfg.Compaction != nil && cfg.Compaction.Auto != nil {
		on = *cfg.Compaction.Auto
	}
	return &on
}
