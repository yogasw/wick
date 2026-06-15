package agents

import (
	"io/fs"
	"regexp"
	"strings"
	"sync"
)

// Resolve the hashed Vite bundle entry once per process — saves the
// directory scan on every editor page render. The cache key is the
// app slug (e.g. "workflow"); the value is the absolute URL path the
// browser uses to fetch the bundle.
var (
	assetCacheMu sync.Mutex
	assetCache   = map[string]string{}
)

// Match the Vite-injected `<script type="module" ... src="/path/to/index-xxx.js">`
// tag. The src attribute is the canonical bundle URL for the build that
// also wrote index.html in the same flush — using it dodges the "multiple
// hashed bundles linger on disk" pitfall that affects `vite build --watch`
// (emptyOutDir is off there, so old bundles accumulate; a plain readdir
// would return the lexicographically first match, which is the OLDEST
// build, not the newest).
var bundleSrcRe = regexp.MustCompile(`<script[^>]*\bsrc="([^"]*/index-[^"]+\.js)"`)

// spaAssetURL returns the absolute URL of the hashed entry .js bundle
// for `app`. Empty string when the bundle isn't present (e.g. dev
// machine without `npm run build` in fe/) — the caller renders a
// fallback message in that case.
//
// Read order:
//  1. Cached value (production fast path; bypassed in live-disk mode).
//  2. dist/<app>/index.html — extract the bundle src Vite just wrote.
//  3. dist/<app>/assets/index-*.js — fallback readdir for the rare case
//     where index.html has no script tag (manual edit / pre-Vite shell).
func spaAssetURL(app string) string {
	assetCacheMu.Lock()
	defer assetCacheMu.Unlock()
	// In live-disk dev mode the hashed bundle name changes on every
	// Vite rebuild; serving a cached URL would point browsers at a
	// 404'd hash. Re-scan every call when WICK_DEV_REPO_ROOT is set —
	// production stays on the cached fast path.
	if !spaLiveDisk {
		if v, ok := assetCache[app]; ok {
			return v
		}
	}

	// Prefer the index.html-declared src. Always authoritative.
	if data, err := fs.ReadFile(SPAFS, "dist/"+app+"/index.html"); err == nil {
		if m := bundleSrcRe.FindSubmatch(data); m != nil {
			url := string(m[1])
			if !spaLiveDisk {
				assetCache[app] = url
			}
			return url
		}
	}

	// Fallback: scan assets/. Returns the lexicographically first
	// match; only reliable when emptyOutDir is on (single bundle in
	// dir). Kept so a hand-rolled dist tree without index.html still
	// boots the SPA.
	entries, err := fs.ReadDir(SPAFS, "dist/"+app+"/assets")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "index-") && strings.HasSuffix(n, ".js") {
			url := "/tools/agents" + spaPrefix + app + "/assets/" + n
			if !spaLiveDisk {
				assetCache[app] = url
			}
			return url
		}
	}
	return ""
}
