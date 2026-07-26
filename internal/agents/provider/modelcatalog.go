package provider

// modelcatalog.go owns the per-provider-type model catalog: the list of
// models the CLI model picker offers for each type (claude/codex/gemini).
//
// The catalog is layered from three sources of the SAME schema (models.json):
//
//  1. EMBEDDED baseline — models.json compiled into the binary. Always
//     present, ships with sane defaults. Never fails.
//  2. REMOTE overlay — the same file fetched from GitHub raw. This is
//     ADDITIVE data, not a fallback: it adds models that shipped after this
//     binary, and can flip a model's enabled flag or refresh its desc. Rate
//     limited, so fetched lazily behind a TTL and persisted to disk.
//  3. DISK cache — the last successful remote fetch, saved under the app
//     data dir so a restart keeps the newest data without re-fetching.
//
// Merge is per-model by id: newest updated_at wins (embedded vs remote),
// with remote-only ids added. A model with enabled=false disappears from the
// picker entirely. Operators can edit the disk cache file by hand to force a
// change without redeploying the server — that file is treated exactly like a
// remote result (its updated_at competes with the embedded one).

import (
	_ "embed"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/userconfig"
)

//go:embed models.json
var embeddedModelsJSON []byte

// remoteModelsURL is the GitHub raw URL for the same models.json. Overriding
// it in tests avoids real network. master branch so operators can update the
// list without cutting a release.
var remoteModelsURL = "https://raw.githubusercontent.com/yogasw/wick/master/internal/agents/provider/models.json"

// modelCacheFile is the disk-cache filename under the app data dir. Holds the
// last successful remote fetch; hand-editable to force a change.
const modelCacheFile = "models.json"

// remoteModelsTTL is how stale the disk cache may be before a lazy fetch
// re-hits GitHub. Kept long: the list rarely changes and GitHub raw is rate
// limited per-IP.
const remoteModelsTTL = 6 * time.Hour

// ── on-disk / wire schema ──────────────────────────────────────────────────

// modelEntry is one model in models.json. Enabled=false hides it from the
// picker. UpdatedAt drives the per-model merge (newest wins).
type modelEntry struct {
	ID        string `json:"id"`
	Desc      string `json:"desc"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at"`
}

// modelCatalog is the whole models.json document: a top-level updated_at plus
// per-type model lists keyed by provider Type string.
type modelCatalog struct {
	UpdatedAt string                  `json:"updated_at"`
	Providers map[string][]modelEntry `json:"providers"`
}

// ── in-memory merged view + TTL guard ──────────────────────────────────────

var (
	modelCatMu       sync.Mutex
	modelCatMerged   map[Type][]ModelSeed // enabled-only, per type, merge result
	modelCatBuiltAt  time.Time            // when modelCatMerged was last rebuilt
	modelCatFetchGen int                  // bumped per fetch to avoid dup inflight
)

// clock is time.Now, overridable in tests.
var clock = time.Now

// ── public entry points ────────────────────────────────────────────────────

// SeedModels returns the enabled model seeds for a provider type, merged from
// embedded + disk-cached-remote data. Returns nil when the type has none
// (wick uses WickModels). Cheap after the first build; a stale disk cache
// triggers a background remote refresh but never blocks the caller.
func SeedModels(t Type) []ModelSeed {
	m := loadMergedCatalog()
	return append([]ModelSeed(nil), m[t]...)
}

// seedDescFor returns the description for a model id on a type, or "" when
// it's a user-added model the catalog doesn't know.
func seedDescFor(t Type, id string) string {
	for _, s := range SeedModels(t) {
		if s.ID == id {
			return s.Desc
		}
	}
	return ""
}

// RefreshRemoteModels forces a synchronous remote fetch + disk persist +
// in-memory rebuild, ignoring the TTL. Returns the fetch error (nil on
// success). Wired to a manual "refresh" control / instance rescan.
func RefreshRemoteModels(ctx context.Context) error {
	cat, err := fetchRemoteCatalog(ctx)
	if err != nil {
		return err
	}
	persistDiskCatalog(cat)
	modelCatMu.Lock()
	modelCatMerged = buildMerged()
	modelCatBuiltAt = clock()
	modelCatMu.Unlock()
	return nil
}

// ── merge pipeline ─────────────────────────────────────────────────────────

// loadMergedCatalog returns the merged, enabled-only catalog. It builds once,
// then serves the cached result; when the build is older than the TTL it
// kicks a background remote refresh (non-blocking) and serves the current
// merge in the meantime.
func loadMergedCatalog() map[Type][]ModelSeed {
	modelCatMu.Lock()
	if modelCatMerged == nil {
		modelCatMerged = buildMerged()
		modelCatBuiltAt = clock()
	}
	stale := clock().Sub(modelCatBuiltAt) > remoteModelsTTL
	m := modelCatMerged
	modelCatMu.Unlock()

	if stale {
		go backgroundRefreshModels()
	}
	return m
}

// backgroundRefreshModels fetches the remote catalog off the request path and
// rebuilds the merge on success. A generation counter collapses concurrent
// triggers into one inflight fetch.
func backgroundRefreshModels() {
	modelCatMu.Lock()
	// Bump the build time up-front so repeated stale reads don't each spawn
	// a fetch while this one is in flight.
	modelCatBuiltAt = clock()
	gen := modelCatFetchGen + 1
	modelCatFetchGen = gen
	modelCatMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cat, err := fetchRemoteCatalog(ctx)
	if err != nil {
		log.Debug().Err(err).Msg("agents.models: remote refresh failed, keeping cached")
		return
	}
	persistDiskCatalog(cat)

	modelCatMu.Lock()
	defer modelCatMu.Unlock()
	if gen != modelCatFetchGen {
		return // a newer fetch superseded this one
	}
	modelCatMerged = buildMerged()
	modelCatBuiltAt = clock()
	log.Info().Str("remote_updated_at", cat.UpdatedAt).Msg("agents.models: catalog refreshed from remote")
}

// buildMerged reads the embedded baseline + disk-cached remote and merges
// them per-model (newest updated_at wins, remote-only ids added), dropping
// disabled models. Never returns nil for a known type unless truly empty.
func buildMerged() map[Type][]ModelSeed {
	base := parseCatalog(embeddedModelsJSON)
	overlay := loadDiskCatalog()

	out := map[Type][]ModelSeed{}
	for _, t := range []Type{TypeClaude, TypeCodex, TypeGemini} {
		key := string(t)
		merged := mergeEntries(base.Providers[key], overlay.Providers[key])
		seeds := make([]ModelSeed, 0, len(merged))
		for _, e := range merged {
			if !e.Enabled {
				continue
			}
			seeds = append(seeds, ModelSeed{ID: e.ID, Desc: e.Desc})
		}
		if len(seeds) > 0 {
			out[t] = seeds
		}
	}
	return out
}

// mergeEntries overlays remote onto base by id: newest updated_at wins;
// remote-only ids are appended after the base order. Base order is preserved
// so the picker stays stable; new remote models trail the built-ins.
func mergeEntries(base, remote []modelEntry) []modelEntry {
	idx := make(map[string]int, len(base))
	out := make([]modelEntry, len(base))
	for i, e := range base {
		out[i] = e
		idx[e.ID] = i
	}
	for _, r := range remote {
		i, ok := idx[r.ID]
		if !ok {
			idx[r.ID] = len(out)
			out = append(out, r)
			continue
		}
		if newerThan(r.UpdatedAt, out[i].UpdatedAt) {
			out[i] = r
		}
	}
	return out
}

// newerThan reports whether a's timestamp is strictly newer than b's.
// Unparseable timestamps sort oldest so a well-formed side wins.
func newerThan(a, b string) bool {
	ta := parseTime(a)
	tb := parseTime(b)
	return ta.After(tb)
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ── disk cache ─────────────────────────────────────────────────────────────

func modelCachePath() (string, error) {
	dir, err := userconfig.Dir(AppName())
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, modelCacheFile), nil
}

// loadDiskCatalog reads the persisted remote catalog, or an empty catalog
// when absent/unreadable (a cold start with no fetch yet).
func loadDiskCatalog() modelCatalog {
	path, err := modelCachePath()
	if err != nil {
		return modelCatalog{}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return modelCatalog{}
	}
	return parseCatalog(b)
}

// persistDiskCatalog writes the fetched catalog to disk, creating the data
// dir if needed. Best-effort: a write error is logged, not fatal.
func persistDiskCatalog(cat modelCatalog) {
	path, err := modelCachePath()
	if err != nil {
		log.Warn().Err(err).Msg("agents.models: cache path failed")
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Warn().Err(err).Msg("agents.models: mkdir cache dir failed")
		return
	}
	b, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		log.Warn().Err(err).Msg("agents.models: marshal cache failed")
		return
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		log.Warn().Err(err).Msg("agents.models: write cache failed")
	}
}

// ── remote fetch ───────────────────────────────────────────────────────────

// fetchRemoteCatalog GETs the models.json from GitHub raw and parses it. A
// non-200 or parse failure is an error (caller keeps the cached merge).
func fetchRemoteCatalog(ctx context.Context) (modelCatalog, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteModelsURL, nil)
	if err != nil {
		return modelCatalog{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return modelCatalog{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return modelCatalog{}, &remoteStatusError{code: resp.StatusCode}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return modelCatalog{}, err
	}
	var cat modelCatalog
	if err := json.Unmarshal(b, &cat); err != nil {
		return modelCatalog{}, err
	}
	return cat, nil
}

type remoteStatusError struct{ code int }

func (e *remoteStatusError) Error() string {
	return "remote models fetch: unexpected status " + http.StatusText(e.code)
}

// parseCatalog unmarshals models.json bytes into a catalog, tolerating an
// empty/invalid payload by returning a zero catalog.
func parseCatalog(b []byte) modelCatalog {
	var cat modelCatalog
	if len(b) == 0 {
		return cat
	}
	if err := json.Unmarshal(b, &cat); err != nil {
		log.Warn().Err(err).Msg("agents.models: parse catalog failed")
		return modelCatalog{}
	}
	return cat
}
