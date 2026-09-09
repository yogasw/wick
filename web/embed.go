package web

import "embed"

// PublicFiles embeds the public directory (CSS, JS, images).
// Run `make setup` once to download third-party JS files before building.
//
// The extra patterns below are not redundant. `//go:embed public` is a
// DIRECTORY embed: it takes whatever happens to be on disk and says nothing
// when a file is absent. Two assets under public/ are BUILD OUTPUTS and are
// gitignored — css/app.css (Tailwind) and lib/wick-markdown.js (the shared FE
// bundle) — so a build from a fresh checkout, or from a `replace` pointing at
// a source tree that never ran those steps, embedded the directory minus
// those files and produced a binary that 404s them. Nothing fails at build
// time, the UI just renders unstyled, and a PWA service worker keeps serving
// the previous copy so it only surfaces when someone clears their cache.
//
// Naming each build output explicitly turns that into a compile failure
// ("pattern public/css/app.css: no matching files found"), which is where a
// missing build artifact belongs.
//
// A glob would not give the same guarantee: go:embed only errors when a
// pattern matches NOTHING, so `public/css/*.css` stops guarding app.css the
// moment any other .css lands in that directory. (Also, go:embed patterns use
// path.Match — `*` never crosses a `/` and `**` has no special meaning — so
// there is no recursive-glob form to write here anyway.) One line per
// generated file, and only generated files need a line.
//
//go:embed public
//go:embed public/css/app.css
//go:embed public/lib/wick-markdown.js
var PublicFiles embed.FS
