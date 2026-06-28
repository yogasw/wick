package plugin

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"golang.org/x/mod/semver"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// ── Catalog generation (writer side) ─────────────────────────────────────
//
// BuildCatalog turns the list of GitHub releases into the plugins.json the
// in-app marketplace reads. It is the Go, struct-typed replacement for the jq
// pipeline that used to live in release-plugins.yml: the catalog SHAPE is the
// Available struct (one source of truth, shared with the reader), so the
// generated JSON can't drift from what the app parses.

// GHRelease is the slice of a GitHub release this needs. Decode the
// `GET /repos/{owner}/{repo}/releases` response into []GHRelease.
type GHRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []GHAsset `json:"assets"`
}

// GHAsset is one downloadable file on a release.
type GHAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

// ManifestFetcher reads a plugin.json out of a release zip given its download
// URL, returning the parsed manifest. Returns an error the caller may ignore
// (Name/Description backfill is best-effort). Injected so BuildCatalog stays
// testable without network.
type ManifestFetcher func(zipURL string) (*wickplugin.Manifest, error)

// BuildCatalog folds releases into one Available entry per plugin key (the tag
// prefix before "/v"), keeping the highest semver version, and maps each zip
// asset to its os/arch download URL. When fetchManifest is non-nil it is used
// to lift Meta.Name / Meta.Description from the chosen release's first asset;
// failures there are non-fatal (the entry keeps key-derived defaults).
//
// Tag convention: plugin releases are tagged "<key>/v<version>"; core wick
// releases ("v<version>", no slash) are ignored.
func BuildCatalog(releases []GHRelease, fetchManifest ManifestFetcher) []Available {
	type picked struct {
		version string
		assets  []GHAsset
	}
	best := map[string]picked{}
	for _, r := range releases {
		key, ver, ok := splitPluginTag(r.TagName)
		if !ok {
			continue // core release or malformed tag
		}
		cur, seen := best[key]
		if !seen || semver.Compare("v"+ver, "v"+cur.version) > 0 {
			best[key] = picked{version: ver, assets: r.Assets}
		}
	}

	out := make([]Available, 0, len(best))
	for key, p := range best {
		a := Available{
			Key:     key,
			Name:    key, // refined below if a manifest is available
			Version: p.version,
			Assets:  map[string]string{},
		}
		for _, asset := range p.assets {
			if oa, ok := osArchFromZipName(asset.Name); ok {
				a.Assets[oa] = asset.DownloadURL
			}
		}
		// Backfill Name + Description + DefaultTags from the released manifest
		// (best effort — a fetch/parse failure leaves the key-derived defaults).
		// DefaultTags is the SAME []entity.DefaultTag a built-in connector
		// declares, so the app categorizes a plugin exactly like a built-in.
		if fetchManifest != nil {
			if url := firstAssetURL(a.Assets); url != "" {
				if mf, err := fetchManifest(url); err == nil {
					if mf.Module.Meta.Name != "" {
						a.Name = mf.Module.Meta.Name
					}
					a.Description = mf.Module.Meta.Description
					a.DefaultTags = mf.Module.Meta.DefaultTags
				}
			}
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// MarshalCatalog renders the catalog as the pretty JSON written to
// plugins/plugins.json (2-space indent, trailing newline) for a stable diff.
func MarshalCatalog(entries []Available) ([]byte, error) {
	if entries == nil {
		entries = []Available{}
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// splitPluginTag splits "<key>/v<version>" into (key, version, true). Core tags
// ("v1.2.3", no slash) and anything without a "/v" segment return ok=false.
func splitPluginTag(tag string) (key, version string, ok bool) {
	i := strings.Index(tag, "/v")
	if i <= 0 {
		return "", "", false
	}
	key = tag[:i]
	version = tag[i+2:]
	if key == "" || version == "" {
		return "", "", false
	}
	return key, version, true
}

// osArchFromZipName extracts "<goos>/<goarch>" from a release asset named
// "<name>-<version>-<goos>-<goarch>.zip". Non-zip names return ok=false.
func osArchFromZipName(name string) (osArch string, ok bool) {
	if !strings.HasSuffix(name, ".zip") {
		return "", false
	}
	base := strings.TrimSuffix(name, ".zip")
	parts := strings.Split(base, "-")
	if len(parts) < 2 {
		return "", false
	}
	goos := parts[len(parts)-2]
	goarch := parts[len(parts)-1]
	if goos == "" || goarch == "" {
		return "", false
	}
	return goos + "/" + goarch, true
}

func firstAssetURL(assets map[string]string) string {
	keys := make([]string, 0, len(assets))
	for k := range assets {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic pick
	for _, k := range keys {
		return assets[k]
	}
	return ""
}

// ManifestFromZipBytes reads and parses plugin.json from a plugin release zip's
// raw bytes. Shared by the live fetcher and by tests (which build zips in-mem).
func ManifestFromZipBytes(data []byte) (*wickplugin.Manifest, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != "plugin.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(rc, 1<<20))
		rc.Close()
		if err != nil {
			return nil, err
		}
		var mf wickplugin.Manifest
		if err := json.Unmarshal(raw, &mf); err != nil {
			return nil, fmt.Errorf("parse plugin.json: %w", err)
		}
		return &mf, nil
	}
	return nil, fmt.Errorf("plugin.json not found in zip")
}
