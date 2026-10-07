package omp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"

	_ "github.com/glebarez/go-sqlite" // pure-Go sqlite, read-only use

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// modelsdb.go reads an omp profile's own model list from its files, with
// no omp process: `omp models --json` is registry.getAvailable("chat")
// (coding-agent/src/cli/models-cli.ts), i.e. the models omp cached per
// provider in <agent>/models.db (table model_cache, one row per provider,
// "models" = the JSON list; authoritative=1 = fetched from that account's
// API, materialization "…:auth") restricted to the providers this profile
// holds credentials for (<agent>/agent.db auth_credentials.provider) and
// to kind "chat" (model.kind ?? "chat"), sorted by provider then id.
// TestModelsDBMatchesCLI pins that equality on a scratch copy.
//
// Both databases are opened read-only (mode=ro, busy timeout) and only
// the columns named here are read — never a credential's data.

var errModelsDBIncomplete = errors.New("omp models.db: the profile's provider set is not knowable from files")

// errModelsDBUnknown: the file's layout is not one this reader was written
// against (omp changed its cache) — never guessed at; the caller shows the
// list empty with Refresh, which runs omp itself.
var errModelsDBUnknown = errors.New("omp models.db: unknown format")

// Layout this reader knows (omp 18.4.x): model_cache must have these
// columns, and each row's version must be a known one.
var (
	modelCacheColumns  = []string{"provider_id", "version", "models"}
	knownModelVersions = map[int]bool{13: true}
)

// unknownLogged logs an unknown models.db once per file.
var unknownLogged sync.Map

func logUnknownOnce(path, why string) {
	if _, dup := unknownLogged.LoadOrStore(path, true); !dup {
		log.Warn().Str("path", path).Str("why", why).Msg("agents.omp: models.db has an unknown format; model list left to Refresh")
	}
}

// checkModelCacheSchema verifies the columns this reader needs exist.
func checkModelCacheSchema(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(model_cache)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return err
		}
		have[name] = true
	}
	for _, c := range modelCacheColumns {
		if !have[c] {
			return fmt.Errorf("%w: model_cache has no %q column", errModelsDBUnknown, c)
		}
	}
	return rows.Err()
}

func init() {
	provider.RegisterModelHarvester(provider.TypeOMP, harvestModels)
	prev := provider.AuthStamp
	provider.AuthStamp = func(ins provider.Instance) string {
		if ins.Type != provider.TypeOMP {
			return prev(ins)
		}
		agent := instanceAgentDir(ins)
		if agent == "" {
			return ""
		}
		// Login / logout write agent.db; omp's own model refresh writes
		// models.db (both WAL).
		var b strings.Builder
		for _, f := range []string{"agent.db", "agent.db-wal", "models.db", "models.db-wal"} {
			if fi, err := os.Stat(filepath.Join(agent, f)); err == nil {
				fmt.Fprintf(&b, "%s:%d:%d;", f, fi.Size(), fi.ModTime().UnixNano())
			}
		}
		return b.String()
	}
}

// instanceAgentDir is the agent dir of the profile whose credentials ins
// uses (a sharer's owner).
func instanceAgentDir(ins provider.Instance) string {
	home, _ := homeDir()
	if home == "" {
		return ""
	}
	profile := envValue(provider.AccountEnv(ins), "OMP_PROFILE")
	if profile == "" {
		profile = provider.OMPProfile(ins)
	}
	cfg := envValue(ins.Env, "PI_CONFIG_DIR")
	if cfg == "" {
		cfg = os.Getenv("PI_CONFIG_DIR")
	}
	return profileAgentDir(home, cfg, profile)
}

// harvestModels is omp's provider.ModelHarvester: the profile's list from
// its files. errModelsDBIncomplete when the instance authenticates some
// provider through env API keys (omp maps those itself; the files cannot
// say which), so the caller falls back to "click Refresh".
func harvestModels(ctx context.Context, ins provider.Instance) ([]provider.ModelSeed, error) {
	for _, kv := range ins.Env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasSuffix(k, "_API_KEY") || strings.HasSuffix(k, "_TOKEN") {
			return nil, errModelsDBIncomplete
		}
	}
	agent := instanceAgentDir(ins)
	if agent == "" {
		return nil, errors.New("omp models.db: no home dir")
	}
	return readModelsDB(ctx, filepath.Join(agent, "models.db"), filepath.Join(agent, "agent.db"))
}

// openRO opens a sqlite file strictly read-only.
func openRO(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(2000)&_pragma=query_only(1)"}
	return sql.Open("sqlite", u.String())
}

// authedProviders is the set of providers the profile holds an enabled
// credential for (only the provider column is read).
func authedProviders(ctx context.Context, agentDB string) (map[string]bool, error) {
	db, err := openRO(agentDB)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT provider FROM auth_credentials WHERE disabled_cause IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil && p != "" {
			out[p] = true
		}
	}
	return out, rows.Err()
}

type cachedModel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	// ContextWindow is the model's context limit (packages/ai Model), the
	// same number RPC's get_state.model.contextWindow reports.
	ContextWindow int `json:"contextWindow"`
}

// modelsDBWindow is the context window models.db records for sel
// ("provider/model"), 0 when the file, the provider row or the model is
// not there. A `-p` turn has no RPC to ask, so this is its meter's scale.
func modelsDBWindow(ctx context.Context, modelsDB, sel string) int {
	prov, id, ok := strings.Cut(sel, "/")
	if !ok || prov == "" || id == "" {
		return 0
	}
	db, err := openRO(modelsDB)
	if err != nil {
		return 0
	}
	defer db.Close()
	if checkModelCacheSchema(ctx, db) != nil {
		return 0
	}
	rows, err := db.QueryContext(ctx, `SELECT version, models FROM model_cache WHERE provider_id = ? OR provider_id LIKE ?`, prov, prov+":%")
	if err != nil {
		return 0
	}
	defer rows.Close()
	for rows.Next() {
		var version int
		var raw string
		if rows.Scan(&version, &raw) != nil || !knownModelVersions[version] {
			continue
		}
		var models []cachedModel
		if json.Unmarshal([]byte(raw), &models) != nil {
			var wrapped struct {
				Models []cachedModel `json:"models"`
			}
			if json.Unmarshal([]byte(raw), &wrapped) != nil {
				continue
			}
			models = wrapped.Models
		}
		for _, m := range models {
			if m.Provider == prov && m.ID == id && m.ContextWindow > 0 {
				return m.ContextWindow
			}
		}
	}
	return 0
}

// readModelsDB returns `omp models --json`'s chat list from the files.
func readModelsDB(ctx context.Context, modelsDB, agentDB string) ([]provider.ModelSeed, error) {
	authed, err := authedProviders(ctx, agentDB)
	if err != nil {
		return nil, err
	}
	db, err := openRO(modelsDB)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := checkModelCacheSchema(ctx, db); err != nil {
		logUnknownOnce(modelsDB, err.Error())
		return nil, errModelsDBUnknown
	}
	rows, err := db.QueryContext(ctx, `SELECT provider_id, version, models FROM model_cache`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type row struct{ prov, id, name string }
	seen := map[string]bool{}
	var list []row
	for rows.Next() {
		var pid, raw string
		var version int
		if err := rows.Scan(&pid, &version, &raw); err != nil {
			return nil, err
		}
		// provider_id is "<provider>" or "<provider>:<variant>"; rows of
		// providers without a credential are skipped before decoding
		// (some are megabytes of JSON).
		if base, _, _ := strings.Cut(pid, ":"); !authed[base] {
			continue
		}
		if !knownModelVersions[version] {
			logUnknownOnce(modelsDB, fmt.Sprintf("model_cache row %s has version %d", pid, version))
			return nil, errModelsDBUnknown
		}
		var models []cachedModel
		if err := json.Unmarshal([]byte(raw), &models); err != nil {
			var wrapped struct {
				Models []cachedModel `json:"models"`
			}
			if json.Unmarshal([]byte(raw), &wrapped) != nil {
				logUnknownOnce(modelsDB, "model_cache.models is neither a list nor {models}")
				return nil, errModelsDBUnknown
			}
			models = wrapped.Models
		}
		for _, m := range models {
			kind := m.Kind
			if kind == "" {
				kind = "chat"
			}
			if kind != "chat" || m.ID == "" || !authed[m.Provider] {
				continue
			}
			sel := m.Provider + "/" + m.ID
			if seen[sel] {
				continue
			}
			seen[sel] = true
			list = append(list, row{m.Provider, m.ID, m.Name})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].prov != list[j].prov {
			return list[i].prov < list[j].prov
		}
		return list[i].id < list[j].id
	})
	out := make([]provider.ModelSeed, 0, len(list))
	for _, r := range list {
		out = append(out, provider.ModelSeed{ID: r.prov + "/" + r.id, Desc: r.name})
	}
	return out, nil
}
