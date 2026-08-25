package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"github.com/yogasw/wick/pkg/entity"
)

// Catalog discovers connector plugins that are AVAILABLE to install from a
// single curated JSON file checked into the wick repo's default branch
// (raw.githubusercontent.com/yogasw/wick/master/plugins/plugins.json).
//
// This is deliberately NOT the GitHub Releases API: listing is a plain raw-file
// fetch, so it never hits the API rate limit and needs no token. The JSON
// carries, per plugin, the direct release download URL for each os/arch — the
// binary is pulled from the GitHub Release only when the user clicks Download.
type Catalog struct {
	url string
	ttl time.Duration
	hc  *http.Client

	mu        sync.Mutex
	etag      string
	cached    []Available
	fetchedAt time.Time
}

// Available is one installable connector surfaced by the catalog.
//
// Key vs Name mirrors connector.Meta: Key is the slug (= source folder = zip
// name = install dir = registry key — the one identity used for matching and
// install); Name is the free display string shown in the UI. Older catalog
// entries that only have "name" are tolerated: parseCatalog backfills Key from
// Name when Key is absent.
type Available struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	// DefaultTags are the plugin's Meta.DefaultTags, carried verbatim from the
	// released manifest — the SAME []entity.DefaultTag a built-in connector
	// declares. The app derives the category from them with connectorCategory,
	// exactly as for built-ins, so a plugin groups under its real category.
	DefaultTags []entity.DefaultTag `json:"default_tags,omitempty"`
	// Assets maps "<goos>/<goarch>" → direct download URL of the release zip.
	Assets map[string]string `json:"assets"`
}

// AssetFor returns the download URL matching host (e.g. "linux/arm64"), or "".
func (a Available) AssetFor(host string) string { return a.Assets[host] }

// VersionNewer reports whether catalog version a is strictly newer than the
// installed version b, by semver. Versions may be bare ("1.4.2") or "v"-prefixed;
// both are normalized. Unparseable versions fall back to a plain string !=
// comparison so a malformed tag still surfaces *some* update hint rather than
// silently hiding one.
func VersionNewer(a, b string) bool {
	na, nb := "v"+strings.TrimPrefix(a, "v"), "v"+strings.TrimPrefix(b, "v")
	if semver.IsValid(na) && semver.IsValid(nb) {
		return semver.Compare(na, nb) > 0
	}
	return a != b && a != ""
}

const (
	defaultCatalogURL     = "https://raw.githubusercontent.com/yogasw/wick/master/plugins/plugins.json"
	defaultCatalogTTL     = 15 * time.Minute
	catalogRequestTimeout = 20 * time.Second
)

// DefaultRegistry builds a Catalog from env overrides:
//
//	WICK_PLUGIN_CATALOG  full URL to plugins.json (default: wick repo master, plugins/plugins.json)
//
// Named DefaultRegistry for call-site compatibility with the earlier API.
func DefaultRegistry() *Catalog {
	url := os.Getenv("WICK_PLUGIN_CATALOG")
	if url == "" {
		url = defaultCatalogURL
	}
	return &Catalog{
		url: url,
		ttl: defaultCatalogTTL,
		hc:  &http.Client{Timeout: catalogRequestTimeout},
	}
}

// List returns every installable connector from the catalog JSON. Served from
// cache while the TTL is fresh; otherwise a conditional GET (ETag) reuses the
// cache on a 304.
func (c *Catalog) List(ctx context.Context) ([]Available, error) {
	c.mu.Lock()
	if c.cached != nil && time.Since(c.fetchedAt) < c.ttl {
		out := append([]Available(nil), c.cached...)
		c.mu.Unlock()
		return out, nil
	}
	etag := c.etag
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("catalog fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		c.mu.Lock()
		c.fetchedAt = time.Now()
		out := append([]Available(nil), c.cached...)
		c.mu.Unlock()
		return out, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("catalog %s: status %d: %s", c.url, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	avail, err := parseCatalog(raw)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cached = avail
	c.etag = resp.Header.Get("ETag")
	c.fetchedAt = time.Now()
	out := append([]Available(nil), avail...)
	c.mu.Unlock()
	return out, nil
}

// Resolve returns the arch-matching download URL for the named connector. host
// defaults to the current runtime when empty.
func (c *Catalog) Resolve(ctx context.Context, name, host string) (Available, string, error) {
	if host == "" {
		host = runtime.GOOS + "/" + runtime.GOARCH
	}
	list, err := c.List(ctx)
	if err != nil {
		return Available{}, "", err
	}
	for _, a := range list {
		if a.Key != name {
			continue
		}
		url := a.AssetFor(host)
		if url == "" {
			return a, "", fmt.Errorf("%q v%s has no build for %s (available: %s)", name, a.Version, host, archList(a))
		}
		return a, url, nil
	}
	return Available{}, "", fmt.Errorf("connector %q not found in catalog", name)
}

// parseCatalog parses the plugins.json array, skipping malformed entries and
// sorting by name for a stable render order.
func parseCatalog(raw []byte) ([]Available, error) {
	var entries []Available
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse plugins.json: %w", err)
	}
	out := make([]Available, 0, len(entries))
	for _, a := range entries {
		// Key is the identity. Backfill from Name for older entries that only
		// carried "name"; backfill Name from Key so the UI always has a label.
		if a.Key == "" {
			a.Key = a.Name
		}
		if a.Name == "" {
			a.Name = a.Key
		}
		if a.Key == "" {
			continue // neither → unusable
		}
		if a.Assets == nil {
			a.Assets = map[string]string{}
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func archList(a Available) string {
	if len(a.Assets) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(a.Assets))
	for oa := range a.Assets {
		parts = append(parts, oa)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
