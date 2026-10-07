package provider

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// opencode_catalog.go is what an opencode instance's own server says it
// offers: its providers (GET /provider) and their login methods (GET
// /provider/auth). The login picker, the API-key picker and the live model
// list are built from it, so a provider or model opencode adds shows up in
// wick without a code change. The static lists in logintty and the
// `opencode models` CLI stay as the fallback whenever no catalog is at hand.
//
// The fetch itself lives in the opencode package (it owns the servers);
// it registers OpencodeCatalogFetcher at init. This file only caches.

// OpencodeCatalog is one fetch of an instance's provider catalog.
type OpencodeCatalog struct {
	// Providers is `all[]`, sorted by id.
	Providers []OpencodeCatalogProvider
	// Connected are the provider ids opencode can use right now (a login,
	// an API key in env, or a keyless provider).
	Connected []string
	// Default is opencode's default model per provider: providerID → modelID.
	Default map[string]string
	// Auth is each provider's login methods, in opencode's order.
	Auth map[string][]OpencodeAuthMethod
}

// OpencodeCatalogProvider is one provider of `all[]`.
type OpencodeCatalogProvider struct {
	ID   string
	Name string
	// Env are the env vars opencode reads an API key from (models.dev).
	Env []string
	// Models are "<providerID>/<modelID>" seeds, sorted by id.
	Models []ModelSeed
}

// OpencodeAuthMethod is one login method of a provider.
type OpencodeAuthMethod struct {
	Type  string // "oauth" | "api"
	Label string
}

// Provider returns the catalog entry for id.
func (c *OpencodeCatalog) Provider(id string) (OpencodeCatalogProvider, bool) {
	if c == nil {
		return OpencodeCatalogProvider{}, false
	}
	for _, p := range c.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return OpencodeCatalogProvider{}, false
}

// Models is the model list of the connected providers, in connected[]
// order. The first connected provider's default model leads (the picker
// treats entry 0 as the default); every provider's default leads its own
// block.
func (c *OpencodeCatalog) Models() []ModelSeed {
	if c == nil {
		return nil
	}
	var out []ModelSeed
	seen := map[string]bool{}
	for _, id := range c.Connected {
		p, ok := c.Provider(id)
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		def := ""
		if m := c.Default[id]; m != "" {
			def = id + "/" + m
		}
		block := make([]ModelSeed, 0, len(p.Models))
		for _, m := range p.Models {
			if m.ID == def {
				block = append([]ModelSeed{m}, block...)
				continue
			}
			block = append(block, m)
		}
		out = append(out, block...)
	}
	return out
}

// OpencodeCatalogFetcher asks ins's opencode server for its catalog; set by
// the opencode package. nil (tests, a build without it) = no catalog.
var OpencodeCatalogFetcher func(ctx context.Context, ins Instance) (*OpencodeCatalog, error)

// errNoCatalogFetcher is returned when no fetcher is registered.
var errNoCatalogFetcher = errors.New("opencode catalog: no fetcher")

const (
	// opencodeCatalogRetry is how soon a failed fetch is tried again: a
	// short window, so one boot hiccup does not pin the static lists for
	// a whole cliModelsTTL.
	opencodeCatalogRetry = time.Minute
	// opencodeCatalogFetchTimeout bounds one background fetch (it may boot
	// a server).
	opencodeCatalogFetchTimeout = 90 * time.Second
)

type opencodeCatalogEntry struct {
	cat *OpencodeCatalog // last good catalog (kept across a failed refresh)
	err error
	at  time.Time
}

var (
	opencodeCatalogMu       sync.Mutex
	opencodeCatalogCache    = map[string]opencodeCatalogEntry{}
	opencodeCatalogInflight = map[string]chan struct{}{}
)

// FetchOpencodeCatalog fetches ins's catalog now (sharing a fetch already
// in flight) and caches it. A failure keeps the last good catalog cached
// and returns it next to the error.
func FetchOpencodeCatalog(ctx context.Context, ins Instance) (*OpencodeCatalog, error) {
	if OpencodeCatalogFetcher == nil {
		return nil, errNoCatalogFetcher
	}
	key := cliModelsKey(ins)
	opencodeCatalogMu.Lock()
	if wait, busy := opencodeCatalogInflight[key]; busy {
		opencodeCatalogMu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		opencodeCatalogMu.Lock()
		e := opencodeCatalogCache[key]
		opencodeCatalogMu.Unlock()
		return e.cat, e.err
	}
	done := make(chan struct{})
	opencodeCatalogInflight[key] = done
	opencodeCatalogMu.Unlock()

	cat, err := OpencodeCatalogFetcher(ctx, ins)
	if err == nil && cat != nil {
		sortCatalog(cat)
	}
	opencodeCatalogMu.Lock()
	e := opencodeCatalogEntry{cat: cat, err: err, at: cliModelsNow()}
	if err != nil || cat == nil {
		e.cat = opencodeCatalogCache[key].cat
		if err == nil {
			e.err = errors.New("opencode catalog: empty")
		}
	}
	opencodeCatalogCache[key] = e
	delete(opencodeCatalogInflight, key)
	close(done)
	opencodeCatalogMu.Unlock()
	return e.cat, e.err
}

// opencodeCatalogTTL is how often a render re-reads the catalog from the
// instance's RUNNING server. That read never starts one: without
// WithHelperSpawn the fetcher only asks a live `opencode serve`.
const opencodeCatalogTTL = 10 * time.Minute

// PeekOpencodeCatalog returns the cached catalog for ins (possibly stale,
// nil when none was ever fetched) without blocking, and starts a
// background fetch when it is cold or expired. For render paths: the fetch
// reads a running `opencode serve` only, never a throwaway one.
func PeekOpencodeCatalog(ins Instance) *OpencodeCatalog {
	if OpencodeCatalogFetcher == nil || ins.Type != TypeOpencode {
		return nil
	}
	key := cliModelsKey(ins)
	opencodeCatalogMu.Lock()
	e, ok := opencodeCatalogCache[key]
	_, busy := opencodeCatalogInflight[key]
	ttl := opencodeCatalogTTL
	if e.err != nil {
		ttl = opencodeCatalogRetry
	}
	stale := !ok || cliModelsNow().Sub(e.at) >= ttl
	opencodeCatalogMu.Unlock()
	if stale && !busy {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), opencodeCatalogFetchTimeout)
			defer cancel()
			_, _ = FetchOpencodeCatalog(ctx, ins)
		}()
	}
	return e.cat
}

func sortCatalog(c *OpencodeCatalog) {
	sort.Slice(c.Providers, func(i, j int) bool { return c.Providers[i].ID < c.Providers[j].ID })
	for i := range c.Providers {
		ms := c.Providers[i].Models
		sort.Slice(ms, func(a, b int) bool { return ms[a].ID < ms[b].ID })
	}
}
