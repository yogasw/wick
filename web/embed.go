package web

import "embed"

// PublicFiles embeds the public directory (CSS, JS, images).
// Run `make setup` once to download third-party JS files before building.
//
//go:embed public
var PublicFiles embed.FS
