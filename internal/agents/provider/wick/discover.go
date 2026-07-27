package wick

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// DiscoveredModel is one model id offered by a vendor's list API,
// surfaced in the Add-Model picker.
type DiscoveredModel struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// MatchFilter reports whether a model matches a discovery-filter query. The
// grammar mirrors the UI's: space-separated terms; a bare term must be
// contained (case-insensitive) in the id or label, a `-`/`!` prefix excludes.
// An empty/blank query matches everything. Shared by the FE filter box and the
// server-side expansion of a live model set, so both agree.
func MatchFilter(m DiscoveredModel, query string) bool {
	// "*" (or empty) is the match-all wildcard: a live set with no filter
	// stands in for the vendor's ENTIRE model list. Lets the user define a
	// live set without being forced to type a narrowing query.
	if q := strings.TrimSpace(query); q == "" || q == "*" {
		return true
	}
	hay := strings.ToLower(m.ID + " " + m.Label)
	for _, raw := range strings.Fields(query) {
		t := strings.ToLower(raw)
		if t == "-" || t == "!" {
			continue
		}
		exclude := strings.HasPrefix(t, "-") || strings.HasPrefix(t, "!")
		needle := t
		if exclude {
			needle = t[1:]
		}
		hit := strings.Contains(hay, needle)
		if exclude == hit {
			return false
		}
	}
	return true
}

// FilterModels returns the subset of models matching query (see MatchFilter).
func FilterModels(models []DiscoveredModel, query string) []DiscoveredModel {
	if q := strings.TrimSpace(query); q == "" || q == "*" {
		return models
	}
	out := make([]DiscoveredModel, 0, len(models))
	for _, m := range models {
		if MatchFilter(m, query) {
			out = append(out, m)
		}
	}
	return out
}

// discoverCacheEntry memoises a vendor list result so typing in the
// search box doesn't re-hit the vendor on every keystroke.
type discoverCacheEntry struct {
	models []DiscoveredModel
	at     time.Time
}

var (
	discoverMu    sync.Mutex
	discoverCache = map[string]discoverCacheEntry{}
)

const discoverTTL = 5 * time.Minute

// DiscoverModels lists the models available for a vendor given a
// plaintext API key (caller decrypts first). kind is google | openai |
// anthropic | openrouter; "other" and any unknown kind returns an empty
// list (manual model id). baseURL overrides the default endpoint.
//
// Results are cached in-memory for discoverTTL keyed by (kind, baseURL,
// key fingerprint) so the picker's live filter is free after the first
// fetch.
func DiscoverModels(ctx context.Context, kind, apiKey, baseURL string) ([]DiscoveredModel, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	cacheKey := kind + "|" + baseURL + "|" + fingerprint(apiKey)

	discoverMu.Lock()
	if e, ok := discoverCache[cacheKey]; ok && time.Since(e.at) < discoverTTL {
		discoverMu.Unlock()
		return e.models, nil
	}
	discoverMu.Unlock()

	models, err := fetchModels(ctx, kind, apiKey, baseURL)
	if err != nil {
		return nil, err
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })

	discoverMu.Lock()
	discoverCache[cacheKey] = discoverCacheEntry{models: models, at: time.Now()}
	discoverMu.Unlock()
	return models, nil
}

func fetchModels(ctx context.Context, kind, apiKey, baseURL string) ([]DiscoveredModel, error) {
	switch kind {
	case "google", "gemini":
		return fetchGoogleModels(ctx, apiKey, baseURL)
	case "openai":
		return fetchOpenAIModels(ctx, apiKey, orDefault(baseURL, "https://api.openai.com/v1"))
	case "openrouter":
		return fetchOpenAIModels(ctx, apiKey, orDefault(baseURL, "https://openrouter.ai/api/v1"))
	case "anthropic", "claude":
		return fetchAnthropicModels(ctx, apiKey, orDefault(baseURL, "https://api.anthropic.com/v1"))
	default:
		// "other" and unknown kinds have no discovery — manual entry.
		return []DiscoveredModel{}, nil
	}
}

func fetchGoogleModels(ctx context.Context, apiKey, baseURL string) ([]DiscoveredModel, error) {
	base := orDefault(strings.TrimRight(baseURL, "/"), "https://generativelanguage.googleapis.com")
	var resp struct {
		Models []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"models"`
	}
	url := base + "/v1beta/models?key=" + apiKey + "&pageSize=1000"
	if err := getJSON(ctx, url, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]DiscoveredModel, 0, len(resp.Models))
	for _, m := range resp.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		out = append(out, DiscoveredModel{ID: id, Label: m.DisplayName})
	}
	return out, nil
}

func fetchOpenAIModels(ctx context.Context, apiKey, baseURL string) ([]DiscoveredModel, error) {
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	headers := map[string]string{"Authorization": "Bearer " + apiKey}
	if err := getJSON(ctx, strings.TrimRight(baseURL, "/")+"/models", headers, &resp); err != nil {
		return nil, err
	}
	out := make([]DiscoveredModel, 0, len(resp.Data))
	for _, m := range resp.Data {
		out = append(out, DiscoveredModel{ID: m.ID})
	}
	return out, nil
}

func fetchAnthropicModels(ctx context.Context, apiKey, baseURL string) ([]DiscoveredModel, error) {
	var resp struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	headers := map[string]string{"x-api-key": apiKey, "anthropic-version": anthropicVersion}
	if err := getJSON(ctx, strings.TrimRight(baseURL, "/")+"/models?limit=1000", headers, &resp); err != nil {
		return nil, err
	}
	out := make([]DiscoveredModel, 0, len(resp.Data))
	for _, m := range resp.Data {
		out = append(out, DiscoveredModel{ID: m.ID, Label: m.DisplayName})
	}
	return out, nil
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// fingerprint returns a short, non-reversible tag of a secret for cache
// keying — never the key itself.
func fingerprint(s string) string {
	if s == "" {
		return "nokey"
	}
	if len(s) <= 6 {
		return "k" + itoa(len(s))
	}
	return s[:3] + itoa(len(s)) + s[len(s)-3:]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
