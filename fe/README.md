# wick frontend

npm workspaces monorepo for every Svelte SPA that ships inside the wick
binary. The Go side embeds the build output at compile time and serves it
through the matching tool's HTTP router.

## Layout

```
fe/
├── package.json                  # workspaces glob + shared devDeps
├── package-lock.json             # tracked — pins shared transitive deps
└── agents/
    └── workflow/                 # @wick-fe/agents-workflow — first SPA
        ├── package.json
        ├── vite.config.ts        # base URL + outDir (see "Conventions")
        ├── tsconfig.json
        ├── svelte.config.js
        ├── index.html            # entry shell
        └── src/
            ├── main.ts
            └── lib/
```

Workspaces are declared in [`package.json`](package.json):

```json
"workspaces": ["agents/*"]
```

Adding `common/*` later is a single edit — see _Adding a shared library_.

## Conventions

A workspace under `agents/*` is an **app**. It owns:

| Concern         | Lives at                                                          |
| --------------- | ----------------------------------------------------------------- |
| Package name    | `@wick-fe/agents-<app>` — namespace mirrors folder.               |
| Vite base URL   | `/tools/<tool>/agents-v2/<app>/` — Go mount + app slug.           |
| Vite outDir     | `../../../internal/tools/<tool>/dist/<app>/`                      |
| Dev port        | Unique per app (default 5173 for workflow; bump for new apps).    |
| Build script    | `vite build` — required so root `npm run build` picks it up.      |

The `dist/<app>/` convention is what release tagging and Go embed both
rely on. Don't drift from it.

## Adding a new app workspace

Most new features are routes inside an existing app — see _Adding a
route_ below. Spin up a fresh workspace only when the new surface
needs:

- A different Go tool (different auth boundary, different mount).
- Truly orthogonal deps the existing app shouldn't carry.
- A separate bundle for lazy loading at the tool boundary.

Steps:

1. Create the folder:

   ```
   fe/agents/<new>/
     package.json
     vite.config.ts
     tsconfig.json
     svelte.config.js
     index.html
     src/main.ts
     src/App.svelte
   ```

   Copy from `fe/agents/workflow/` and rename fields. Change:

   - `package.json` → `"name": "@wick-fe/agents-<new>"`.
   - `vite.config.ts` → `base`, `outDir`, and `server.port` for the new
     app.

2. Run `npm install` at `fe/` once — npm picks up the new workspace from
   the `agents/*` glob automatically.

3. Wire the Go side. For an SPA under the existing `agents` tool the
   handler in [internal/tools/agents/spa_handler.go](../internal/tools/agents/spa_handler.go)
   already dispatches by the first URL segment — drop the workspace
   under `agents/` and the URL `/tools/agents/agents-v2/<new>/`
   resolves to `dist/<new>/` with zero Go changes.

   For a brand-new Go tool, follow the standard tool registration in
   `internal/tools/<tool>/` and embed `dist/` the same way `agents`
   does.

The release workflow does not need changes — see _Release pipeline_.

## Adding a route (preferred for most features)

Open the SPA's router and add a branch:

```svelte
{#if match("/edit/:id", $route)}
  <EditorShell .../>
{:else if match("/realtime/:id", $route)}
  {#await import("$lib/components/RealtimeGraph.svelte") then mod}
    <svelte:component this={mod.default} .../>
  {/await}
{:else}
  <WorkflowList .../>
{/if}
```

Use `import()` for heavy features (charts, editors) so Vite splits them
into separate chunks loaded on demand.

## Adding a shared library

Libraries are workspaces that other apps import as source. They do not
build — they have no `build` script and no `vite.config.ts`.

1. Extend the workspaces glob in `fe/package.json`:

   ```json
   "workspaces": ["agents/*", "common/*"]
   ```

2. Create `fe/common/<lib>/package.json`:

   ```json
   {
     "name": "@wick-fe/common-<lib>",
     "private": true,
     "type": "module",
     "main": "src/index.ts",
     "svelte": "src/index.ts"
   }
   ```

3. Export from `fe/common/<lib>/src/index.ts`.

4. Consume from an app workspace:

   ```json
   // fe/agents/workflow/package.json
   "dependencies": {
     "@wick-fe/common-<lib>": "*"
   }
   ```

   Then `import { X } from '@wick-fe/common-<lib>'` in any Svelte/TS
   file. npm symlinks the workspace; Vite tree-shakes what isn't
   imported.

5. Run `npm install` at `fe/` to materialise the symlink.

Libraries with their own runtime deps (e.g. `d3`, `chart.js`) declare
them under `dependencies` in their own `package.json`. Devs of consumer
apps don't have to add those — they ride along through the workspace
graph.

## Dev

Dev servers are per-app because each Vite server binds a port:

```bash
cd fe
npm run dev:workflow
```

The Vite proxy in each app's `vite.config.ts` forwards `/tools/*` +
`/public/*` to a running wick server (default `http://localhost:9425` —
the wicklab port; override via `WICK_PROXY=http://host:port` before
the dev command).

For a new app, add a script in `fe/package.json`:

```json
"dev:<new>": "npm --workspace=@wick-fe/agents-<new> run dev"
```

## Build

```bash
cd fe
npm run build       # builds every workspace that has a build script
npm run check       # svelte-check across workspaces with a check script
npm run test        # vitest across workspaces with a test:unit script
```

These use `npm --workspaces --if-present` so libraries (which have no
build target) are skipped silently. Add a new app and `npm run build`
picks it up automatically.

## Go embed and serving

The agents tool embeds the dist tree:

```go
// internal/tools/agents/spa.go
//go:embed all:dist
var SPAFS embed.FS
```

The SPA handler dispatches by the first URL segment:

```
GET /tools/agents/agents-v2/<app>/...
  → splitFirstSegment → fs.Sub(SPAFS, "dist/<app>") → serve index.html or asset
```

Routing rules (see [spa_handler.go](../internal/tools/agents/spa_handler.go)):

- `/<app>/assets/<file>` → served from `dist/<app>/assets/` with long
  cache headers.
- `/<app>/<anything else>` → returns `dist/<app>/index.html` so the SPA
  router resolves the client-side route.
- `/<app>/` with missing `index.html` → 404 with "SPA shell not built
  yet" — fresh clones see this until `npm run build` runs.

A second Go tool with its own SPA follows the same pattern, just under a
different tool path: `internal/tools/<tool>/dist/<app>/` embedded at
`internal/tools/<tool>/spa.go`.

## Gitignore

Vite output is treated like generated Go files (`*_templ.go`,
`web/public/css/app.css`): regenerated at build/release time, not
carried in git.

[.gitignore](../.gitignore):

```
internal/tools/*/dist/*/index.html
internal/tools/*/dist/*/assets/
```

The `.gitkeep` at the embed root keeps `//go:embed` happy on a clean
checkout where nothing has been built yet.

## Release pipeline

[`.github/workflows/release.yml`](../.github/workflows/release.yml)
runs `npm ci` then `npm run build` at `fe/`, then commits every
`internal/tools/*/dist` directory into the tag commit (alongside
`*_templ.go` and `web/public/css/app.css`). Glob:

```bash
git add -f \
  $(find . -name '*_templ.go' -not -path './vendor/*') \
  web/public/css/app.css \
  $(find internal/tools -type d -name dist)
```

This means: add a workspace, add a tool, add a route — the release
workflow does not change. The tag commit always carries a working
binary; the master branch stays free of generated artifacts.

## Tests

```bash
cd fe
npm run test           # vitest, unit tests across workspaces
npm run e2e            # playwright, end-to-end
```

Per-app tests live next to source under `src/**/*.test.ts`.
