# Svelte FE Migration — Agents UI

Status: **in-progress** (started 2026-05-24). Target: replace the templ + plain-JS surface under `internal/tools/agents/` with a Svelte 5 SPA at `fe/agents/`, 1:1 component split, JSON-only protocol.

Existing UI keeps working in parallel under the same routes; the new UI mounts at `/tools/agents-v2/*` until full parity, then the flag flips and the old routes redirect.

---

## TODO (driver checklist — top of doc per project doc convention)

- [ ] Phase 0 — design doc this file + integration-test scaffolding (httptest + headless browser)
- [ ] Phase 1 — `fe/agents/` skeleton (Vite + Svelte 5 + Tailwind binary, no npm postinstall in wick build)
- [ ] Phase 1 — embed `fe/agents/dist/` via `internal/tools/agents/spa.go` (separate from existing `static.go`); served at `/agents-v2/`
- [ ] Phase 1 — base layout shell (Sidebar / NavLink / Layout) + 1 page end-to-end (Overview) as the reference contract
- [ ] Phase 2 — convert every page handler to dual-mode (HTML still default; `Accept: application/json` returns the same VM as JSON). Integration test per endpoint asserting JSON shape stable.
- [ ] Phase 2 — port shared components (KebabMenu, LifecycleBadge, StatusDot, Dialog, Picker, Composer, MarkdownRenderer, TraceWrap)
- [ ] Phase 3 — port every page 1:1 (overview, sessions list, session detail, new-session, workspaces, presets, providers, skills, channels, data-tables, settings, approvals, askuser, context_panel)
- [ ] Phase 4 — workflow editor: shell + canvas wrap (keep Drawflow under the hood) + palette + inspector + toolbar + bottom tabs (validation/guard/tests/runs/logs/yaml) + executions + run + test_manager + test_results + argform + list
- [ ] Phase 4 — per-node UI split: `BaseNode.svelte` + 1 `.svelte` per node type (classify/branch/datatable/db_query/end/go_script/http/session_init/shell/switchnode/transform + agent + connector grouped item)
- [ ] Phase 5 — SSE payload migration to JSON `{type, payload}` (backward-compat parser one release)
- [ ] Phase 5 — `<user_input>` + similar prompt-envelope tags → JSON envelope `{kind, content, meta}` (engine-side, not UI)
- [ ] Phase 6 — flip default route, redirect legacy templ pages; delete `js/` + `view/` after one stable release

Each phase ends with: `go test ./internal/tools/agents/...` green + browser smoke + screenshot diff vs mockup.

---

## Why

`internal/tools/agents/js/` has grown to ~5k LoC plain JS across `agents.js` (1719), `context.js` (572), `skills.js` (190), `workflow/editor.js` (4385), bottom tabs (~420), widgets (~260). Plus ~6.8k LoC of templ in `view/` and `view/workflow/`. Edit blast-radius is huge — touch the kebab menu, retest every page.

Svelte split gives:

1. **One component, one file** — `Sidebar.svelte`, `KebabMenu.svelte`, `LifecycleBadge.svelte`, `BaseNode.svelte`, `ClassifyNode.svelte`. Edits stay scoped.
2. **Reactive state** — kill `data-*` attribute + querySelector spaghetti in `agents.js`. SSE deltas update stores, components re-render.
3. **JSON protocol** — every endpoint returns the same VM shape, regardless of UI. MCP/external clients consume the same thing the canvas does.
4. **AI-edit-friendly** — JSON in/out makes wick_workflow MCP ops match what the canvas sees byte-for-byte; right now MCP returns YAML and the canvas eats HTMX fragments.

## Constraints

- **Tailwind binary** (no npm postinstall in the wick build). FE folder may use npm for dev, but `go build wick` must not need node.
- **No design drift** — Svelte port must render pixel-for-pixel against the existing templ. Diff tool: screenshot per page, manual review.
- **No breakage** — existing routes stay live until Phase 6. New routes mount under `/tools/agents-v2/` so users can A/B.
- **Integration tests gate every phase.** No phase merges without `go test ./internal/tools/agents/...` and the new SPA smoke suite green.
- **Wick complex, user simple** — build complexity buried in Makefile; user runs `wick run` and gets the SPA.

## Target architecture

```
wick/
├── fe/
│   └── agents/
│       ├── package.json            # vite + svelte 5 + typescript
│       ├── vite.config.ts          # outDir → ../../internal/tools/agents/dist
│       ├── tailwind.config.js      # mirror root tailwind config (named colors etc.)
│       ├── index.html              # SPA shell
│       └── src/
│           ├── main.ts
│           ├── app.svelte          # router + AgentsLayout shell
│           ├── lib/
│           │   ├── api/            # typed fetch helpers per endpoint group
│           │   │   ├── sessions.ts
│           │   │   ├── workflows.ts
│           │   │   ├── providers.ts
│           │   │   └── …
│           │   ├── stores/         # svelte stores
│           │   │   ├── sse.ts          # SSE → reactive event stream
│           │   │   ├── sessions.ts     # session registry mirror
│           │   │   └── workflow.ts     # current editor state
│           │   ├── types/          # TS types matching Go VM structs
│           │   ├── components/
│           │   │   ├── layout/
│           │   │   │   ├── Sidebar.svelte
│           │   │   │   ├── NavLink.svelte
│           │   │   │   └── AgentsLayout.svelte
│           │   │   ├── shared/
│           │   │   │   ├── KebabMenu.svelte
│           │   │   │   ├── LifecycleBadge.svelte
│           │   │   │   ├── StatusDot.svelte
│           │   │   │   ├── StatusBadge.svelte
│           │   │   │   ├── Dialog.svelte
│           │   │   │   ├── Picker.svelte
│           │   │   │   ├── MarkdownRenderer.svelte
│           │   │   │   └── TraceWrap.svelte
│           │   │   ├── sessions/
│           │   │   │   ├── Composer.svelte
│           │   │   │   ├── Turn.svelte
│           │   │   │   ├── TurnEvent.svelte
│           │   │   │   └── …
│           │   │   └── workflow/
│           │   │       ├── EditorShell.svelte
│           │   │       ├── Canvas.svelte           # drawflow wrap
│           │   │       ├── Palette.svelte
│           │   │       ├── Inspector.svelte
│           │   │       ├── Toolbar.svelte
│           │   │       ├── BottomTabs.svelte
│           │   │       ├── tabs/
│           │   │       │   ├── ValidationTab.svelte
│           │   │       │   ├── GuardTab.svelte
│           │   │       │   ├── TestsTab.svelte
│           │   │       │   ├── RunsTab.svelte
│           │   │       │   ├── LogsTab.svelte
│           │   │       │   └── YamlTab.svelte
│           │   │       └── nodes/
│           │   │           ├── BaseNode.svelte         # shared shell — head, ports, status
│           │   │           ├── BaseInspectorPanel.svelte
│           │   │           ├── ClassifyNode.svelte
│           │   │           ├── BranchNode.svelte
│           │   │           ├── DatatableNode.svelte
│           │   │           ├── DbQueryNode.svelte
│           │   │           ├── EndNode.svelte
│           │   │           ├── GoScriptNode.svelte
│           │   │           ├── HttpNode.svelte
│           │   │           ├── SessionInitNode.svelte
│           │   │           ├── ShellNode.svelte
│           │   │           ├── SwitchNode.svelte
│           │   │           ├── TransformNode.svelte
│           │   │           ├── AgentNode.svelte
│           │   │           └── ConnectorNode.svelte
│           │   └── routes/                # one .svelte per top-level page
│           │       ├── Overview.svelte
│           │       ├── SessionsList.svelte
│           │       ├── SessionDetail.svelte
│           │       ├── NewSession.svelte
│           │       ├── Workspaces.svelte
│           │       ├── Presets.svelte
│           │       ├── Providers.svelte
│           │       ├── Skills.svelte
│           │       ├── Channels.svelte
│           │       ├── DataTables.svelte
│           │       ├── Settings.svelte
│           │       ├── Approvals.svelte
│           │       ├── AskUser.svelte
│           │       └── workflow/
│           │           ├── List.svelte
│           │           ├── Editor.svelte
│           │           ├── Executions.svelte
│           │           ├── Run.svelte
│           │           ├── TestManager.svelte
│           │           └── TestResults.svelte
└── internal/tools/agents/
    ├── spa.go                       # NEW — //go:embed dist
    ├── spa_handler.go               # NEW — serve SPA + JSON endpoints under /agents-v2
    ├── json_views.go                # NEW — VM → JSON helpers (mirrors view/models.go shape)
    └── …                            # existing templ + JS untouched until Phase 6
```

## Per-node split (BaseNode + per-type)

User explicitly asked for this — current `editor.js` has the canvas card + inspector + codec for every node type interleaved in 4385 LoC. The Svelte split:

- **`BaseNode.svelte`** — owns the visual shell: card frame, head label, css-type colour, port slots, status pill, kebab. Props: `{node: NodeVM, selected, running, error, slot input, slot output}`.
- **`BaseInspectorPanel.svelte`** — owns the modal shell: tabs (parameters / on_failure / notes / advanced), header, save/cancel. Slot for the per-type form body.
- **Per-type node component** wraps `BaseNode` and supplies type-specific config:
  - `ClassifyNode.svelte` — input form for `cases[]`, output port per case
  - `BranchNode.svelte` — input form for `if`/`then`/`else`
  - `HttpNode.svelte` — input form for method/url/headers/body
  - `ShellNode.svelte` — script editor + dangerous-op flag
  - …(one file per type, ~80-150 LoC each, replacing scattered switch cases)
- **`nodes/index.ts`** — registry mapping `NodeType → SvelteComponent`. Canvas reads node JSON, looks up the component, mounts it. Adding a node type = drop one `.svelte` + one line in `index.ts`. Mirrors the existing Go `nodes.Register` pattern.

Codec (YAML ↔ canvas data) stays in Go (already `internal/tools/agents/workflow/nodes/<type>/meta.go`). The Svelte component receives the already-decoded JSON object.

## Endpoint dual-mode (Phase 2)

Every handler in `handler.go` Register() currently returns templ HTML. Migration:

1. Extract VM build to a pure function (no `c.HTML` call) returning the existing VM struct.
2. New thin wrapper: `func overviewHandler(c *tool.Ctx) { vm := buildOverviewVM(c); if c.WantsJSON() { c.JSON(vm); return }; c.HTML(view.OverviewPage(vm)) }`.
3. `tool.Ctx.WantsJSON()` checks `Accept: application/json` OR `?format=json`. Defaults to HTML so existing UI unaffected.
4. Integration test per endpoint: hit `Accept: application/json`, decode VM, assert shape.

This means the JSON contract is **the same struct** as templ consumes. Zero schema drift between the two surfaces. Pages move to Svelte one by one; the SPA fetches JSON, the legacy URL still serves templ for anyone hitting the old path.

## SSE migration (Phase 5)

Current `stream.go` already emits JSON envelopes (the `Event` struct → `JSON()`). What's HTML-flavoured is the **consumption side** — `agents.js` does `el.outerHTML = htmlFromTrace(ev)` style mutation. The SPA replaces that with a Svelte store: events stream in, components subscribe by sessionID, no DOM string concatenation anywhere.

No breaking change to the SSE wire format; just stop emitting HTML fragments anywhere (none currently do — earlier grep confirmed).

## Prompt envelope migration (Phase 5)

The `<user_input>` tag referenced in `internal/planning/archive/workflow/20-security.md` and the mockup is a prompt-injection mitigation: channel-driven workflows wrap external user text with the tag before handing to the LLM, so the LLM treats it as data not instructions.

Migration target: replace the tag with a JSON envelope so MCP consumers and skill prompts get structured input:

```json
{ "kind": "user_input", "source": "slack:#support", "user_id": "U123", "content": "..." }
```

Engine-side change in `internal/agents/channels/*/setup/*.go` where the session-context-prefix is written. UI surfaces nothing user-visible; this is pure prompt-engineering. Old `<user_input>` consumers (skills, system prompts) get a backward-compat shim for one release.

## Test strategy

Three layers, all run in CI per phase:

1. **Go integration tests** — `internal/tools/agents/spa_handler_test.go` spins up `httptest.NewServer(tool.Mux(handler.Register))`, asserts:
   - JSON endpoints return the documented shape (one assertion per VM field).
   - SPA shell `/agents-v2/` serves `index.html`.
   - Static asset paths (`/agents-v2/assets/*.js`, `*.css`) resolve from embed.
   - Old templ routes still 200 (no regression).
2. **Component unit tests** — `fe/agents/src/**/*.test.ts` via Vitest. Each shared component has a render+assert test (mount, pass props, assert DOM).
3. **Browser smoke** — `fe/agents/tests/smoke.spec.ts` via Playwright (headless), drives:
   - Boot wick at `:8080` with seed fixtures.
   - Visit every page under `/agents-v2/`, screenshot, assert no console errors.
   - Diff screenshot vs `fe/agents/tests/__snapshots__/<page>.png` (manual baseline on first run).
   - Workflow editor: drag a node from palette, connect two nodes, save, reload, assert state persisted.

Phase exit criteria: all three layers green. Phase failure rolls back the phase commit.

## Build pipeline

- `make fe-agents` → `cd fe/agents && npm ci && npm run build` → outputs to `internal/tools/agents/dist/`.
- `make build` → `make fe-agents && go build ./...`.
- `internal/tools/agents/spa.go` declares `//go:embed dist` (file present even when empty so the build never fails on a clean checkout — committed `.gitkeep` + `index.html` stub).
- CI: `make build` is the green gate.

## Rollback

Each phase ships behind a config flag `agents.spa_v2_enabled` (default false until Phase 6). Setting false hides `/tools/agents-v2/*` and the user only sees the legacy templ surface. No DB schema changes in any phase.

## Open items

- [ ] **Router**: `svelte-spa-router` vs `@sveltejs/kit`. Lean toward `svelte-spa-router` — hash-based, no SSR needed, smaller bundle, plays nice with Go-embedded static.
- [ ] **Drawflow wrap**: do we keep Drawflow or port to a Svelte-native graph lib (`@xyflow/svelte`)? Drawflow first (avoid scope creep); evaluate replacement at Phase 4 end.
- [ ] **Auth**: existing pages run inside the login-protected handler. SPA inherits via cookie-on-fetch. Verify the SSE long-poll endpoint stays authed.
- [ ] **Bundle size budget**: target < 300 KB gzipped for the SPA shell. Workflow editor lazy-loaded.

