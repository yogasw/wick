---
name: fe-module
description: Use for ANY work on the Svelte SPAs or shared FE libraries under fe/ — adding or editing a component, writing API calls, creating a new SPA workspace, porting templ-embedded JS into a SPA, wiring a new route, or extracting shared code. Covers the npm-workspaces layout (fe/common/* shared libs + fe/agents/* SPAs), the Effect-based HTTP client contract in @wick-fe/common-api, the @wick-fe/common-stores and @wick-fe/common-ui packages, the three TDD layers (Effect mock layer, @testing-library/svelte, store tests), the Go SPA routing/thin-shell contract, and the ENFORCED deduplication rule (2+ copies of an api fn / UI component / store must be extracted to common/* in their own commit).
allowed-tools: Read, Grep, Glob, Edit, Write, Bash
paths:
  - "fe/**"
  - "internal/pkg/spa/**"
  - "internal/tools/agents/dist/**"
  - "internal/tools/agents/view/**"
  - "internal/tools/agents/spa_handler.go"
  - "internal/tools/agents/spa.go"
  - "internal/manager/spa.go"
  - "internal/manager/dist/**"
---

# FE Module — wick

The `fe/` tree is an npm-workspaces monorepo. Shared code lives in `fe/common/*`
(source-only libraries, no build step); the user-facing apps are Svelte 5 SPAs in
`fe/agents/*`. Each SPA is mounted by a Go templ "thin shell" that boots the bundle.

## Workspaces

```text
fe/
  common/       @wick-fe/common-*  — source-only shared libraries (no build script)
    api/        APIError, WickClientLayer, apiGetE/apiPostE/apiDeleteE, apiGet/apiPost/apiDelete
    stores/     toasts, pushToast, dismissToast, toastOk/toastWarn/toastError, snapshotToasts
    ui/         ConfirmDialog, ToastHost, Select   (exports ONLY the package index)
  agents/       @wick-fe/agents-*  — full Svelte SPAs (have build/dev/check scripts)
    workflow/   Workflow editor
    scm/        Git SCM panel
    conversation, overview, presets, project-settings, providers, shell, skills, new-session
  manager/      @wick-fe/manager — Manager SPA (connector builder, job runner, etc.)
                outDir: internal/manager/dist/manager/
                Go host: internal/manager/spa.go (NOT internal/tools/)
```

The workspace globs are `["agents/*", "common/*", "manager"]` in `fe/package.json`.
Shared dev tools (vitest, svelte, @testing-library/svelte, jsdom, typescript, vite)
are declared once at the `fe/` root and hoisted — do NOT redeclare them in
`common/*` packages.

**Adding a common library:** create `fe/common/<name>/package.json` with
`"main": "src/index.ts"` and NO `build` script, then `cd fe && npm install`. Pin
runtime deps in that package; leave dev tools to the root.

**Adding a new agents SPA:** copy `fe/agents/workflow/vite.config.ts`, change `base`
and the out dir, add a `dev:<app>` and `build:watch` entry to `fe/scripts/dev.mjs`
workspace list + `fe/package.json`, then add a templ thin shell (see Routing).

**Adding a brand-new Go tool SPA (outside agents):** one line —
`var spaLoader = spa.New(spaEmbedded, "internal/<path>")` (see `internal/manager/spa.go`).
The `spa.Loader` handles embed-vs-live-disk, asset-URL resolution, and auto-reload
registration. No `init()`, no `spadev` (removed), no per-host reload endpoint.
See § "Go SPA host (internal/pkg/spa)" below.

## Effect API — `@wick-fe/common-api`

The client wraps Effect's `FetchHttpClient` (installed: `effect@^3.21`,
`@effect/platform@^0.70`). It exposes two surfaces:

- **Effect-based** `apiGetE` / `apiPostE` / `apiDeleteE` — use these for ALL new
  code. The caller provides the HttpClient layer, which is what makes them mockable.
- **Promise-based** `apiGet` / `apiPost` / `apiDelete` — backward-compat for the
  existing workflow/scm call sites. They run against `WickClientLayer` internally and
  reject with a real `APIError` (so `e instanceof APIError` and `e.status` work). Do
  NOT reach for these in new code.

```ts
import { apiGetE, apiPostE, WickClientLayer, APIError } from "@wick-fe/common-api";
import { Effect } from "effect";

const listSessions = (base: string) => apiGetE<SessionList>(`${base}/api/sessions`);

/* Run inside a component (provide the real layer at the edge): */
const sessions = await Effect.runPromise(
  listSessions(base).pipe(Effect.provide(WickClientLayer)),
);
```

The Effect functions are scoped internally and surface non-2xx responses as a typed
`APIError` (status + server-extracted `detail`) in the error channel.

## Components & stores

Import shared components from the package **index** — `@wick-fe/common-ui` declares
an `exports` map that exposes only `.`, so deep paths like
`@wick-fe/common-ui/src/ConfirmDialog.svelte` do NOT resolve:

```ts
import { ConfirmDialog, ToastHost, Select } from "@wick-fe/common-ui";
import { toastOk, toastError } from "@wick-fe/common-stores";
```

## Routing (Go side)

Two SPA hosts exist. Pick the right one.

### agents tool (`internal/tools/agents/`)

```text
spaPrefix : "/workflow/"                         (internal/tools/agents/spa_handler.go)
Mount     : /tools/agents/workflow/
App       : /tools/agents/workflow/<app>/        (workflow, scm, overview, …)
outDir    : internal/tools/agents/dist/<app>/
Go embed  : internal/tools/agents/spa.go  (spa.New, multi-app)
```

`spaHandler` reads `dist/<app>/...` via `splitFirstSegment`, so adding a new
`dist/<app>/` directory is all that's needed on the Go side. The templ thin shell
resolves the hashed bundle URL with `spaAssetURL("<app>")` at boot:

```go
templ MyView(vm MyVM) {
    @view.AgentsLayout(vm.Layout) {
        if vm.AssetURL == "" {
            <div class="p-8 text-sm text-rose-600">Bundle not built. Run npm run build in fe/.</div>
        } else {
            <div id="app" data-base={ vm.Base } class="h-full"></div>
            <script type="module" src={ vm.AssetURL }></script>
        }
    }
}
```

### manager (`internal/manager/`)

```text
Mount     : /manager/
outDir    : internal/manager/dist/manager/
Go embed  : internal/manager/spa.go  (spa.New, single-app "manager")
Asset URL : spaAssetURL() in spa.go → spaLoader.AssetURL("manager", ...)
```

### Go SPA host (`internal/pkg/spa`)

Both hosts share `spa.Loader`. A host is one line plus a thin `spaAssetURL`
wrapper:

```go
//go:embed all:dist
var spaEmbedded embed.FS
var spaLoader = spa.New(spaEmbedded, "internal/<path>") // repo-rel dir holding dist/

// single-app:  spaLoader.AssetURL("<app>", "<fallbackBase>/assets")
// multi-app:   spaLoader.AssetURL(app, ...)  // one dist/ root, dist/<app>/ each
```

`spa.New` auto-detects `WICK_DEV_REPO_ROOT`: production serves the embed
(asset URL cached per-app); dev swaps to `os.DirFS`, re-reads index.html per
render, and registers the dist/ dir for auto-reload. **No init(), no spadev
(removed), no per-host reload endpoint.**

**Auto-reload** (live reload, not HMR): `spa.RegisterGlobalHandler(mux)` in
`internal/pkg/api/server.go` mounts `GET /_dev/reload` (SSE, fsnotify on all
dist/ dirs); `spa.DevReloadScript()` injected once in `internal/pkg/ui/layout.templ`
makes every page subscribe + `location.reload()` on rebuild (skipped while a
modal is visibly open). Both no-op in production. Do NOT add a `DevReloadScript`
field to any VM — it lives in `ui.Layout`, not per-page.

## TDD — three layers

Write the test first, run it red, then implement. Every new api fn, store, and
component gets a test.

### Layer 1 — Effect unit test (API, zero network)

Provide a mock `HttpClient` layer; use `Effect.flip` to read the typed error as a value.

```ts
import { HttpClient, HttpClientResponse } from "@effect/platform";
import { Effect, Layer } from "effect";
import { apiGetE, APIError } from "@wick-fe/common-api";

const mockLayer = (status: number, body: unknown) =>
  Layer.succeed(
    HttpClient.HttpClient,
    HttpClient.make((req) =>
      Effect.succeed(
        HttpClientResponse.fromWeb(req, new Response(JSON.stringify(body), { status })),
      ),
    ),
  );

test("returns parsed JSON on 2xx", async () => {
  const out = await Effect.runPromise(
    apiGetE<{ id: number }>("/api/x").pipe(Effect.provide(mockLayer(200, { id: 1 }))),
  );
  expect(out).toEqual({ id: 1 });
});

test("fails with APIError on 4xx", async () => {
  const err = await Effect.runPromise(
    apiGetE("/api/x").pipe(Effect.flip, Effect.provide(mockLayer(404, { error: "no" }))),
  );
  expect(err).toBeInstanceOf(APIError);
});
```

### Layer 2 — Svelte component test

Component tests need the `svelteTesting()` plugin AND a `svelte.config.js` with
`runes: true` in the package — without it, Svelte 5 runes throw `rune_outside_svelte`
inside `render()`:

```ts
/* vitest.config.ts */
import { defineConfig } from "vitest/config";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { svelteTesting } from "@testing-library/svelte/vite";

export default defineConfig({
  plugins: [svelte({ hot: false }), svelteTesting()],
  test: { environment: "jsdom", globals: true, include: ["src/**/*.{test,spec}.ts"] },
});
```

```ts
import { render, screen, fireEvent } from "@testing-library/svelte";
import ConfirmDialog from "../ConfirmDialog.svelte";

test("calls onConfirm", async () => {
  const onConfirm = vi.fn();
  render(ConfirmDialog, { props: { open: true, title: "?", confirmLabel: "Yes", onConfirm, onCancel: vi.fn() } });
  await fireEvent.click(screen.getByText("Yes"));
  expect(onConfirm).toHaveBeenCalledOnce();
});
```

### Layer 3 — store test

```ts
import { get } from "svelte/store";
import { toasts, toastOk, dismissToast } from "@wick-fe/common-stores";

test("toastOk adds then dismiss removes", () => {
  const id = toastOk("Saved");
  expect(get(toasts)).toHaveLength(1);
  dismissToast(id);
  expect(get(toasts)).toHaveLength(0);
});
```

## Deduplication Rule (ENFORCED)

Before writing or editing any API function, UI component, or store:

1. Grep `fe/` for an existing one with the same name, signature, or visual purpose.
2. If **2 or more copies** exist (identical, or same signature / same purpose with
   trivial differences):
   - STOP — do not add a third copy.
   - Move it into `@wick-fe/common-api`, `@wick-fe/common-stores`, or
     `@wick-fe/common-ui` (verbatim where possible; rewire only cross-package imports).
   - Update every consumer to import from the `@wick-fe/common-*` package and delete
     the local copies.
   - Verify byte-equality of the moved file against each source with `cmp` before
     deleting the originals.
   - Land it as its own commit: `refactor(fe): extract <name> to @wick-fe/common-<pkg>`.

## Dev / build / test

```bash
cd fe && npm run dev            # build:watch the workspaces listed in scripts/dev.mjs, in parallel
cd fe && npm run dev:workflow   # per-app Vite HMR (no templ chrome)
cd fe && npm run dev:manager    # manager SPA only
cd fe && npm run dev:scm

cd fe && npm run build          # builds SPAs (libraries have no build script)
cd fe && npm run test           # vitest across all workspaces (test:unit)
cd fe && npm run check          # svelte-check + tsc

# Full-stack with live-disk hot reload (no Go recompile on FE changes):
WICK_DEV_REPO_ROOT=$(pwd)/.. go run ./cmd/lab server
# VS Code: launch "wicklab" (already has WICK_DEV_REPO_ROOT set)
```

`npm run dev` uses `fe/scripts/dev.mjs`, which spawns `build:watch` for the workspaces
in its **hardcoded `workspaces` array** — NOT every workspace automatically. A workspace
absent from that list is never built by `dev`. `npm --workspaces run build:watch` is
BROKEN for watch mode — it runs seri and blocks on the first workspace forever. Always
use `npm run dev`.

Most `common/*` libs are imported into an agents SPA, so they rebuild transitively when
that SPA does. The exception is `@wick-fe/common-md`: it ships its OWN standalone bundle
to `web/public/lib/wick-markdown.js` (served as `/public/lib/wick-markdown.js`, loaded
directly by templ pages like Software Update's "What's new"). Nothing imports it, so it
MUST be listed in `dev.mjs` explicitly — otherwise the file is missing in local dev and
the script 404s, leaving markdown unrendered. Any future `common/*` that emits a
standalone served asset needs the same treatment.

With `WICK_DEV_REPO_ROOT` set, the browser **auto-reloads** on every rebuild via the
SSE endpoint `/_dev/reload` (Go-side fsnotify on dist/, client injected in `ui.Layout`).
No `Ctrl+R`. Live reload (full refresh), not HMR — for state-preserving HMR use
`dev:<app>` (drops templ chrome). See fe/README.md § "Auto-reload".

When adding a new workspace to `dev`, edit `fe/scripts/dev.mjs` and add the workspace
name to the `workspaces` array.

Note: `agents-scm` currently has no unit-test files, so its `test:unit` exits 1 with
"No test files found" — that is the existing baseline, not a regression.

## Design system

All Tailwind/markup must follow the project design system. Activate the
`design-system` skill when creating or editing any UI component.
