package manager

import (
	"io/fs"
	"regexp"
	"sync"
)

// spaAssetBase is the URL prefix the Vite bundle is served under. The
// Vite build bakes this as the asset `base`, so the hashed bundle + chunk
// URLs in dist/manager/index.html resolve back to spaAssetHandler. Lives
// under /manager so the whole module shares one clean path namespace.
const spaAssetBase = "/manager/_app/"

// spaBase is injected into the #app div's data-base attribute. The SPA
// router reads it as the client-side route prefix (e.g. /manager/connectors
// resolves to the client route /connectors). API calls use server-absolute
// /manager paths and ignore it.
const spaBase = "/manager"

// bundleSrcRe matches the Vite-injected module script src in index.html.
// Using the src Vite wrote in the same flush dodges the "stale hashed
// bundle lingers on disk" pitfall a plain readdir would hit.
var bundleSrcRe = regexp.MustCompile(`<script[^>]*\bsrc="([^"]*/index-[^"]+\.js)"`)

var (
	assetURLOnce sync.Once
	assetURL     string
)

// spaAssetURL returns the absolute URL of the hashed entry .js bundle for
// the manager SPA, read from dist/manager/index.html. In live-disk dev mode
// re-reads on every call so Vite watch rebuilds are reflected immediately.
// In production resolves once per process. Empty string when not built yet.
func spaAssetURL() string {
	if spaLiveDisk {
		return readAssetURL()
	}
	assetURLOnce.Do(func() { assetURL = readAssetURL() })
	return assetURL
}

func readAssetURL() string {
	data, err := fs.ReadFile(spaFS, "dist/manager/index.html")
	if err != nil {
		return ""
	}
	if m := bundleSrcRe.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return ""
}
