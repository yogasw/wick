package agents

import (
	"io/fs"
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

// spaAssetURL returns the absolute URL of the hashed entry .js bundle
// for `app`. Empty string when the bundle isn't present (e.g. dev
// machine without `npm run build:workflow`) — the caller renders a
// fallback message in that case.
//
// Bundle location: dist/<app>/assets/index-*.js (Vite default).
// Public URL:      /tools/agents/agents-v2/<app>/assets/<file>.
func spaAssetURL(app string) string {
	assetCacheMu.Lock()
	defer assetCacheMu.Unlock()
	if v, ok := assetCache[app]; ok {
		return v
	}
	entries, err := fs.ReadDir(SPAFS, "dist/"+app+"/assets")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "index-") && strings.HasSuffix(n, ".js") {
			url := "/tools/agents" + spaPrefix + app + "/assets/" + n
			assetCache[app] = url
			return url
		}
	}
	return ""
}

// resetAssetCache clears the cached bundle path. Tests use it after
// swapping in a fake embed; production code never needs it.
func resetAssetCache() {
	assetCacheMu.Lock()
	defer assetCacheMu.Unlock()
	assetCache = map[string]string{}
}
