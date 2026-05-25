package agents

import "embed"

// SPAFS embeds the Vite-built Svelte SPA under fe/agents/. The build
// pipeline (`npm run build:workflow` from the repo root) writes assets
// here; this file is the only Go-side glue. Served by spa_handler.go
// at /tools/agents-v2/.
//
//go:embed all:dist
var SPAFS embed.FS
