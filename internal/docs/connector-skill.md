# Plan: Connector Skills, Docs, Template Example, Doc Sweep

Status: draft v2 — keputusan locked, ready buat review akhir.
Target tanggal: 2026-05-01.

Scope:
1. Bikin skill `connector-module` (2 versi: wick-core + template).
2. Bundle skill ke `wick skill sync` mechanism (biar `wick upgrade` bawa skill baru).
3. Tulis 5 user-facing docs di `docs/guide/` (connector + MCP + Access Tokens + Connected Apps + Connector Runs Purge).
4. Port crudcrud sbg example connector di `template/connectors/`.
5. Update `template/AGENTS.md` + `template/README.md` ke pattern connector.
6. **Sweep semua docs existing** (intro, getting-started, ai-quickstart, tool-module, job-module, cli, index, env-vars, changelog) — cek typo, gambar, content gap, sebut connector di tempat yg relevan.

Sumber kebenaran desain: [internal/docs/connectors-design.md](./connectors-design.md). Plan ini turunin desain itu jadi material AI-readable + user-facing.

---

## 0. Keputusan (locked)

| # | Keputusan | Nilai |
|---|---|---|
| 1 | Folder name di template | `connectors/` (matches wick internal naming) |
| 2 | Example connector di template | Port `internal/connectors/crudcrud/` verbatim — sudah teruji, GET/SET beneran jalan |
| 3 | Skill name | `connector-module` (matches `tool-module` pattern) |
| 4 | Skill bundling ke wick CLI | In-scope — required biar `wick skill sync` & `wick upgrade` bawa skill baru |
| 5 | Sweep docs existing | In-scope — semua docs di-cek untuk typo + connector mention + version inconsistency |
| 6 | Wick-core skill bundling | **BOTH bundled** — `template/.claude/skills/connector-module/` (downstream priority) + `.claude/skills/connector-module/` (wick-core, separate embed). Template wins di `syncSkill` priority → downstream dapat versi yg bener. |
| 7 | Sec 9 saran tambahan (glossary, api ref, README badge, sync warning, SS checklist) | **Force-include semuanya** — biar sync, gak ada drift |

---

## 1. Skill: `.claude/skills/connector-module/` (wick-core dev)

**Path:** `d:\code\work\wick\.claude\skills\connector-module\SKILL.md`

**Scope:** kerja di `internal/connectors/` (wick lab binary). Touches `pkg/connector/`, `internal/connectors/`, `internal/mcp/`, registry di `internal/connectors/registry.go::RegisterBuiltins()`.

**Frontmatter:**

```yaml
---
name: connector-module
description: Use for ANY work on a connector in the wick core repo — creating new under internal/connectors/, refactoring/improving existing, fixing bugs, adding new operations, editing configs/inputs, or touching anything that affects the MCP surface (internal/mcp/) or the registry (internal/connectors/registry.go). Covers the full module contract: pkg/connector.{Meta, Module, Operation, Op, OpDestructive, ExecuteFunc, Ctx}, the wick:"..." tag grammar reused from tools/jobs, the per-call HTTP client + context-propagation rules, the destructive-opt-in model, the typed-response contract, and the dual registration with Bootstrap auto-seed. Enforces: stateless top-level Operations() func, typed Configs + per-op Input structs, http.NewRequestWithContext for goroutine-leak prevention, JSON-shape stability, error wrapping with %w, and the "ask before adding ops" question.
allowed-tools: Read, Grep, Glob, Edit, Write, Bash
paths:
  - "internal/connectors/**"
  - "internal/mcp/**"
  - "pkg/connector/**"
  - "internal/docs/connectors-design.md"
---
```

**Sections (target outline, ~400 lines):**

1. **Scope warning** — wick core only. Point ke template skill kalau di template repo.
2. **Mental model** — 1 module wraps 1 external API; carry shared Configs + N Operations; user sees 1 row per definition (Bootstrap auto-seed); LLM sees ops via meta-tool dispatch (`wick_list/wick_search/wick_get/wick_execute`).
3. **Before building: ASK** — 4 pertanyaan wajib:
   - Apakah API ini butuh credential/endpoint custom? (→ Configs struct)
   - Operations apa aja yg ke-expose? (list nama + shape input + apakah destructive)
   - Output shape: typed struct atau `map[string]any`? (default rec: typed struct, design 10.1)
   - Tag default buat row baru — group/filter? (default: cuma group, mirip tools/jobs)
4. **Module layout:**
   ```
   internal/connectors/myconn/
     connector.go    # Meta(), Configs struct, per-op Input structs, Operations(), ExecuteFunc impls
     service.go      # split kalau gemuk (optional)
     repo.go         # external I/O kalau butuh refactor (jarang — connector langsung HTTP)
     doc.go          # opsional, godoc package-level
   ```
5. **`Configs` struct** — `wick:"..."` tag grammar (reuse dari tool-module): `secret/required/url/textarea/dropdown=...`. Catat: `key=...` override snake-case kalau perlu.
6. **`Input` struct** — sama tag grammar, tapi maknanya = JSON Schema buat MCP. Per-op Input independent.
7. **`ExecuteFunc` golden rules:**
   - **MUST** `http.NewRequestWithContext(c.Context(), ...)` — never `http.NewRequest`. Reasoning: cancellation prop + goroutine leak (didokumen di `pkg/connector/ctx.go`).
   - **MUST** validate Input awal sebelum HTTP call (fail fast — error message ke `connector_runs.error_msg`).
   - **MUST** pakai `c.HTTP` (carry default 30s timeout). Replace cuma kalau butuh transport beda — comment alasannya.
   - **SHOULD** wrap upstream error dgn `fmt.Errorf("...: %w", err)` — chain readable di history page.
   - **SHOULD** transform response upstream → struct ramping (design 10.1); raw passthrough cuma utk eksperimen.
   - **SHOULD** mark `Destructive: true` (via `OpDestructive(...)`) buat aksi sulit di-undo (delete, post, send). Default off di row baru.
8. **`Op` vs `OpDestructive`** — kapan pakai mana, dgn contoh.
9. **Description discipline** — per-op `Description` itu **load-bearing** (LLM baca ini di `wick_list`). Pakai action verb, eksplisit:
   - GOOD: "Search Loki using LogQL. Returns log lines with timestamp + labels. Empty result = empty array."
   - BAD: "query loki"
10. **Registration** — `RegisterBuiltins()` di `internal/connectors/registry.go`:
    ```go
    extra = append(extra, connector.Module{
        Meta:       myconn.Meta(),
        Configs:    entity.StructToConfigs(myconn.Configs{}),
        Operations: myconn.Operations(),
    })
    ```
11. **Bootstrap behavior** — `CountByKey == 0` → auto-seed 1 row. Existing row gak diutak-atik. Duplicate Key = fatal at boot.
12. **Verifying** — `go build ./...`, boot server, buka `/manager/connectors/<key>/<id>/test?op=<op>`, klik Run, cek result di `/history`. Cek MCP via PAT + Claude Desktop config snippet (point ke docs/guide/mcp.md).
13. **Anti-patterns:**
    - Jangan lupa `http.NewRequestWithContext` (goroutine leak).
    - Jangan return raw `*http.Response.Body` reader — selalu ReadAll + decode.
    - Jangan store Configs di module-level var — Ctx carry per-instance values.
    - Jangan bikin Op key dgn karakter aneh — slug only (`a-z0-9_`).
    - Jangan share state lintas Execute call (race-prone).
14. **When to ask before acting:**
    - New external API yg butuh OAuth lain (selain wick OAuth) — confirm authn flow.
    - Removing Op dari connector existing — bisa orphan `connector_operations` rows.
    - Adding op yg butuh upload binary — wick connector belum support multipart well.

Reference contoh: `internal/connectors/crudcrud/` (5 ops, 1 destructive, JSON validation, helper split).

---

## 2. Skill: `template/.claude/skills/connector-module/` (downstream)

**Path:** `d:\code\work\wick\template\.claude\skills\connector-module\SKILL.md`

**Beda dari wick-core skill:**

| Aspek | Wick-core | Template |
|---|---|---|
| Folder modul | `internal/connectors/<name>/` | `connectors/<name>/` |
| Registration | `internal/connectors/registry.go::RegisterBuiltins()` | `main.go` via `app.RegisterConnector(...)` |
| Built-in example | `internal/connectors/crudcrud/` | `connectors/crudcrud/` (port verbatim) |
| Imports | `github.com/yogasw/wick/pkg/connector` (sama) | sama |
| Build verification | `go build ./...` | `wick dev` (smoke test, kill port 8080 setelah selesai per memori `feedback_kill_port`) |
| Docs link | `internal/docs/connectors-design.md` | `<https://yogasw.github.io/wick/guide/connector-module>` (yg akan kita tulis di sec 5) |

**Sections:** sama structure dgn wick-core skill. Sertain section "What this skill does NOT cover" yg tunjuk ke wick-core skill kalau dev mau modify wick framework itself (jarang, tapi guard).

---

## 3. Skill bundling ke wick CLI (in-scope)

**Files yg di-touch:**

### 3.1 `cmd/cli/skill.go`

Tambah entry ke `skillLabels` map:

```go
var skillLabels = map[string]string{
    "tool-module":      "Create/edit a tool or job (`tools/`, `jobs/`)",
    "connector-module": "Create/edit a connector (`connectors/`)",  // NEW
    "design-system":    "UI styling, colors, spacing, components",
}
```

### 3.2 `main.go` — verify embed coverage

Current state:
```go
//go:embed all:template
var templateFS embed.FS

//go:embed all:.claude/skills/design-system
var designSystemFS embed.FS
```

`template/.claude/skills/connector-module/` ke-bundle automatic via `//go:embed all:template` — gak perlu directive baru.

`.claude/skills/connector-module/` (wick-core version) **TIDAK** ke-bundle — itu cuma buat dev wick itself, bukan utk downstream. Sesuai pattern `tool-module` (wick-core version `.claude/skills/tool-module/` juga gak embedded; cuma `template/.claude/skills/tool-module/` yg embedded).

### 3.3 Update `docs/reference/cli.md`

`wick skill list` example output update:
```
$ wick skill list
tool-module
connector-module    ← NEW
design-system
```

### 3.4 Verifikasi

Setelah implementasi:
```bash
wick skill list           # harus include connector-module
wick skill sync           # harus copy connector-module ke ./.claude/skills/
cat ./.claude/skills/connector-module/SKILL.md   # verify content
cat AGENTS.md             # verify skill table updated
```

---

## 4. Update `template/AGENTS.md`

Patches:

### 4.1 Layout block — tambah:

```
connectors/<name>/
  connector.go    # Meta + Configs + Operations + ExecuteFunc — wraps one external API for LLM/MCP
  service.go      # optional split when connector grows
```

### 4.2 Where-to-add table — 2 row baru:

| New connector (LLM via MCP) | `connectors/<name>/connector.go` |
| Register connector | `main.go` — `app.RegisterConnector(meta, configs, ops)` |

### 4.3 Naming rules — paragraf baru:

> Connector shape: one top-level `Meta()` returning `connector.Meta`, one typed `Configs` struct shared across operations, per-op typed `Input` struct, and a `[]connector.Operation` from `Operations()` built via `connector.Op(...)` / `connector.OpDestructive(...)`. ExecuteFunc receives `*connector.Ctx` — read configs via `c.Cfg("key")`, inputs via `c.Input("key")`, **always** pass `c.Context()` into `http.NewRequestWithContext` to prevent goroutine leaks. One call to `app.RegisterConnector` = one connector definition; admin can spawn N rows per definition lewat `/manager/connectors/{key}` (each row carry own credential + tag).

### 4.4 Skills table — row baru:

| Create/edit a connector (`connectors/`) | [`connector-module`](./.claude/skills/connector-module/SKILL.md) |

### 4.5 Rules of thumb — bullet baru:

- Connectors are LLM-facing — keep per-op Description sharp, mark destructive ops, return ramping JSON not raw upstream bytes.

---

## 5. Update `template/README.md`

(Need to read current state in implementation phase — not yet read. Likely add:)
- "Quick start" → tambah baris connector test page URL
- "What's inside" → mention `connectors/crudcrud/` sbg example
- Link ke connector docs

---

## 6. Docs di `docs/guide/` (5 page baru, user-facing)

Semua page mirror density `tool-module.md` existing. Image placeholder format (kamu ganti incremental):

```html
<!-- IMAGE NEEDED: filename.png
     SS instruction: <step-by-step setup + capture>
-->
```

### 6.1 `docs/guide/connector-module.md`

Sections:
- Mental model (1 module = N ops, LLM-consumed via MCP)
- File structure (`connectors/<name>/connector.go`)
- `Meta()`, `Configs`, per-op `Input`, `Operations()` declaration
- `connector.Op` vs `OpDestructive`
- ExecuteFunc + Ctx helpers (`Cfg`, `CfgInt`, `CfgBool`, `Input`, `InputInt`, `InputBool`, `Context`, `HTTP`, `ReportProgress`, `InstanceID`)
- `wick:"..."` tag reference (link ke tool-module untuk full table — DRY)
- Register di `main.go`
- Bootstrap auto-seed behavior + duplicate-key error
- Tips: typed response, http.NewRequestWithContext, error wrapping
- Per-row management UI walkthrough (settings, ops toggle, test, history)
- Cross-link: MCP install (sec 6.2), PAT (sec 6.3), OAuth (sec 6.4)

**IMAGE markers:**
- `connector-list.png` — `/manager/connectors/<key>` list page dgn ≥2 row + tag chip + kebab menu
  - **SS instruction:** boot wick, register crudcrud, bikin row "Crudcrud Prod" + "Crudcrud Staging" dgn tag berbeda, screenshot full page width 1200px.
- `connector-detail.png` — `/manager/connectors/<key>/<id>` settings page dgn Operations table
  - **SS instruction:** klik salah satu row dari list, screenshot section identity + credentials + operations table.
- `connector-test.png` — `/manager/connectors/<key>/<id>/test?op=<op>`
  - **SS instruction:** isi 1 input, klik Run, screenshot dgn result panel terisi (success state).
- `connector-history.png` — `/manager/connectors/<key>/<id>/history` dgn ≥1 expanded row
  - **SS instruction:** run beberapa kali (mix success/error), expand 1 row dgn error → screenshot dgn Request/Response JSON visible + Retry link.

### 6.2 `docs/guide/mcp.md`

Sections:
- Why MCP (1-paragraph: LLM clients consume connectors via standard protocol)
- Endpoint: `POST /mcp` (Streamable HTTP)
- **Meta-tool pattern**: `wick_list`, `wick_search`, `wick_get`, `wick_execute` — dgn rationale "kenapa bukan static tool list" (dynamic instances, smaller token footprint)
- tool_id format `conn:{connector_id}/{op_key}`
- Auth modes (link ke 6.3 PAT, 6.4 OAuth)
- Quick install snippets:
  - **Claude.ai:** paste `<base>/mcp` → OAuth dance auto
  - **Claude Desktop / Cursor / VSCode:** JSON config dgn `wick_pat_xxx` bearer
  - **cURL:** `curl -X POST <base>/mcp -H "Authorization: Bearer wick_pat_xxx" -d '{"jsonrpc":"2.0","method":"tools/list","id":1}'`
- Session model (in-memory, transparent restart)
- Streaming (kapan SSE upgrade dipakai — long-running ops dgn progress events)
- Audit trail (per call ke `connector_runs` — link ke history page)
- Tag filter behavior (hanya row visible ke caller user-tag)

**IMAGE markers:**
- `mcp-flow.png` — diagram flow Claude.ai → wick → upstream API
  - **SS instruction:** ASCII / mermaid sequence diagram OK; bisa juga excalidraw export.
- `mcp-install-page.png` — `/profile/mcp` install snippets
  - **SS instruction:** login, buka /profile/mcp, screenshot full page (OAuth section + Bearer section + 4 install snippets).
- `mcp-claude-desktop.png` — Claude Desktop dgn wick tools terdaftar
  - **SS instruction:** install via PAT, restart Claude Desktop, screenshot dialog "Tools" yg show wick_list/wick_search/wick_get/wick_execute.

### 6.3 `docs/guide/access-tokens.md`

Sections:
- Apa itu PAT — bearer token user-generated, format `wick_pat_<32hex>`
- Kapan pakai PAT vs OAuth (PAT: client gak speak OAuth — Claude Desktop, Cursor, cURL; OAuth: Claude.ai dst)
- Generate flow — `/profile/tokens` → name → render-once banner → paste ke client
- Hash-only storage (server gak bisa re-read setelah generate)
- Revoke flow (per-token, instant)
- Admin override (`/admin/access-tokens` cross-user revoke)
- Security notes: store di password manager, scope = full user permissions

**IMAGE markers:**
- `tokens-list.png` — `/profile/tokens` table
- `tokens-create.png` — render-once banner setelah generate
- `admin-tokens.png` — `/admin/access-tokens` cross-user view

### 6.4 `docs/guide/oauth-connections.md`

Sections:
- OAuth 2.1 flow di wick (DCR + PKCE + refresh rotation)
- "Connected Apps" mental model — 1 row per (user × app)
- Install via Claude.ai: paste URL → consent → done (no token paste)
- Token formats: `wick_oat_<32hex>` (access, 1h TTL), `wick_ort_<64hex>` (refresh, 30d TTL)
- Per-user disconnect via `/profile/connections` (revoke semua token milik (user, client))
- Admin override (`/admin/connections`)
- Security: PKCE S256 mandatory, refresh rotation + replay detection, opaque tokens (bukan JWT — DB leak ≠ token leak)
- Why self-hosted (vs Auth0/Clerk) — link ke design.md sec 8.3

**IMAGE markers:**
- `oauth-consent.png` — `/oauth/authorize` consent page
- `connections-list.png` — `/profile/connections` table
- `admin-connections.png` — `/admin/connections` cross-user
- `oauth-flow.png` — sequence diagram OAuth dance

### 6.5 `docs/guide/connector-runs-purge.md`

Sections:
- Apa itu `connector_runs` (audit trail per MCP call + panel test + retry)
- Kenapa perlu purge (audit log infinite-grow → DB bloat)
- Default behavior: 09:30 daily cron, retention 7 hari
- System tag: kenapa job ini locked (System=true, code-managed, IsFilter+IsGroup combo)
- Cara ubah retention: `/manager/jobs/connector-runs-purge` → edit `retention_days`
- Cara ubah cron: same page, edit cron field
- Admin can't disable (System tag enforcement, design sec 9.8)
- Manual purge (SQL fallback) — gak ada UI, dev-only escape hatch

**IMAGE markers:**
- `purge-job-detail.png` — `/manager/jobs/connector-runs-purge` settings page
- `purge-run-history.png` — same page run history dgn `Purged N row(s)` output

### 6.6 Update `docs/.vitepress/config.ts` sidebar

Tambah di Guide section setelah Background Job:

```ts
{ text: 'Connector Module', link: '/guide/connector-module' },
{ text: 'MCP for LLMs', link: '/guide/mcp' },
{ text: 'Access Tokens (PAT)', link: '/guide/access-tokens' },
{ text: 'OAuth Connections', link: '/guide/oauth-connections' },
{ text: 'Connector Runs Purge', link: '/guide/connector-runs-purge' },
```

Optional: bikin nested group `Connectors & MCP` kalau jadi terlalu panjang — tunda sampai semua page jadi.

---

## 7. Sweep existing docs (in-scope)

Setiap file di-cek: typo, gambar, content gap, connector mention. Dibawah catatan + patch yg ke-detect dari read awal.

### 7.1 `docs/index.md` (homepage)

**Issue:** Features list cuma SSO, Tools/Jobs, Live Config — gak sebut Connectors/MCP. Padahal connector itu kelas modul ke-3 sekarang.

**Patch:** tambah feature card baru:

```yaml
- icon: 🤖
  title: LLM-Ready via MCP
  details: Expose tools to Claude, Cursor, and any MCP client. Built-in OAuth 2.1 + Personal Access Tokens, per-call audit log, no protocol code to write.
```

Posisi: pertama atau kedua (load-bearing differentiator vs framework lain).

### 7.2 `docs/guide/introduction.md`

**Issues:**
- Line 35: "Two Module Types" → harus jadi **Three Module Types** (Tool + Job + Connector).
- Table needs Connector row: `Connector | connectors/{name}/ | Operations() | /mcp (LLM) + /manager/connectors/{key} (admin)`
- "Project Structure" block (line 19-33) gak include `connectors/` folder.
- "What the Framework Handles" bullet list (line 44-51) gak sebut MCP, OAuth, PAT, audit trail. Tambah:
  - **MCP server** — built-in, expose connectors to LLMs
  - **Auth surface** — OAuth 2.1 + PAT, both at `/profile/*`
  - **Per-call audit** — every MCP & test execution logged
- Screenshots section gak include Connectors. Tambah:
  - `IMAGE: admin-connectors.png` — `/admin/connectors` cross-user view
- Kemungkinan tambah `IMAGE: profile-mcp.png` — `/profile/mcp` install page

### 7.3 `docs/guide/getting-started.md`

**Issues:**
- Line 22: `wick install ... @v0.3.0` tapi `cli.md` line 84-85 contoh masih `v0.1.13 → v0.2.0`. **Version inconsistent across docs** — flag for cleanup.
- Project structure block (line 39-50) gak include `connectors/` folder.
- Step 6 ("Let Claude build your tools") cuma sebut tool — tambah connector example prompt.
- Step 4 "Configure environment" — kalau connector butuh env baru (cek!), tambah disini.
- Common commands table (line 105-110) OK.

**Patches:**
- Tambah `connectors/` ke project structure.
- Step 6: kasih contoh prompt buat connector ("add a connector for slack with send_message and list_channels operations").
- Note callout: "After your first boot, generate a Personal Access Token at `/profile/tokens` to wire the LLM client."

### 7.4 `docs/guide/ai-quickstart.md`

**Issues:**
- Sample Prompts cuma cover Tool + Job + External Link + Tag. Gak cover Connector.
- "How Claude uses AGENTS.md and skills" section line 86-89 cuma sebut `tool-module` + `design-system`. Gak sebut `connector-module`.

**Patches:**
- New section "Create a new connector" dgn 2-3 sample prompt:
  ```
  add a connector for the GitHub REST API with operations: list_repos,
  get_repo, list_issues, create_issue (destructive), close_issue
  (destructive). credential is a personal access token (secret, required).
  base url defaults to https://api.github.com.
  ```
  ```
  add a connector for our internal Loki at https://loki.example.com.
  one operation: query (LogQL string input). add a token field (secret).
  ```
- Section "How Claude uses AGENTS.md and skills": tambah bullet `connector-module` dgn one-liner deskripsi.
- Tips section (line 112-118): tambah bullet "For connectors, list operations explicitly: 'with operations: a, b, c (destructive)' — saves Claude from guessing the surface."

### 7.5 `docs/guide/tool-module.md`

**Issues minor:**
- Cross-link ke connector-module.md gak ada — worth add di intro paragraf ("for LLM-facing modules, see Connector Module").

**Patch:** 1 sentence cross-link.

### 7.6 `docs/guide/job-module.md`

**Issues:**
- Line 117-119 "Worker vs Web" sebut `go run . worker` butuh second terminal — tetep relevan.
- `connector-runs-purge` adalah contoh job system — worth sebut sbg "see also: built-in system job example".

**Patch:** cross-link ke `connector-runs-purge.md`.

### 7.7 `docs/reference/cli.md`

**Issues:**
- Line 50-52: `wick skill list` example gak include `connector-module`.
- Version inconsistency: line 84-85 contoh `v0.1.13 → v0.2.0`, line 108 `v0.1.12`. `getting-started.md` pakai `v0.3.0`. Pilih satu canonical version (current pinned), atau ganti ke generic placeholder `vX.Y.Z`.

**Patches:**
- Line 50-52: tambah `connector-module` ke output.
- Version examples: ganti ke `vCURRENT → vNEXT` placeholder atau commit ke 1 version aktual.
- `wick init` description (line 27): tambah "and example connector" kalau `template/connectors/crudcrud/` di-include.

### 7.8 `docs/reference/env-vars.md`

**Verify needed:** apakah connector + OAuth + PAT introduce env baru? Cek `internal/oauth/` + `internal/accesstoken/` untuk env reads. Kalau iya:
- Mungkin OAuth refresh token TTL, code TTL → editable via env? Cek dulu.
- PAT prefix `wick_pat_` hardcoded — gak env.
- Mungkin tambah `OAUTH_SESSION_SECRET` atau equivalent kalau ada.

Likely: gak ada env baru (semua opaque tokens, TTL hardcoded di service.go). Verify pas implementasi.

### 7.9 `docs/changelog.md`

**Issue:** connector subsystem itu huge feature — worth release entry detail. Gak sempat baca full changelog tapi standar: tambah entry top-section untuk version yg ship connector (v0.3.x?).

**Patch:** entry baru:
```md
## v0.3.x — Connectors + MCP

### Added
- **Connector module** — third class beside Tool + Job, designed for LLM consumption via MCP (Model Context Protocol)
- **MCP endpoint** — `POST /mcp` with meta-tool dispatch (`wick_list`, `wick_search`, `wick_get`, `wick_execute`)
- **Personal Access Tokens** at `/profile/tokens` for bearer auth (Claude Desktop, Cursor, cURL)
- **OAuth 2.1** at `/oauth/{authorize,token,register}` with DCR + PKCE for Claude.ai-style clients
- **Connected Apps** at `/profile/connections` for per-grant management
- **Admin pages**: `/admin/connectors`, `/admin/access-tokens`, `/admin/connections`
- **System job** `connector-runs-purge` for audit log retention (default 7 days)
- New skill `connector-module` bundled — `wick skill sync` to pull
- Example connector `connectors/crudcrud/` in scaffolded projects

### Changed
- Three-module mental model in docs (Tool + Job + Connector)
- AGENTS.md template includes connector layout + skill row
```

### 7.10 `docs/contributing.md` & other reference pages

Likely no change. Spot-check during implementation.

### 7.11 Root `README.md`

Need to read in implementation phase. Likely tambah:
- Connector mention di "What is wick"
- Link ke connector docs
- Update screenshot strip kalau ada

---

## 8. Example connector di template

**Path:** `template/connectors/crudcrud/connector.go`

**Approach:** port `internal/connectors/crudcrud/connector.go` verbatim. No file structure change — single `connector.go` file (matches wick internal layout).

**Steps:**
1. Copy `internal/connectors/crudcrud/connector.go` → `template/connectors/crudcrud/connector.go`
2. Verify import path tetap `github.com/yogasw/wick/pkg/connector` (public API — works downstream).
3. Adjust godoc paragraf opening kalau perlu (saat ini sebut "sample connector" — tetep relevan).
4. Delete `template/integrations/sample/` (empty placeholder dari sebelumnya).

**Register di `template/main.go`:**

```go
import "<projectmod>/connectors/crudcrud"

app.RegisterConnector(
    crudcrud.Meta(),
    crudcrud.Configs{},
    crudcrud.Operations(),
)
```

(Need to check current `template/main.go` shape during implementation — kalau pakai `_ "..."` pattern atau langsung Register.)

---

## 9. Things kemungkinan kelewat (saran tambahan)

### 9.1 Glossary page di docs

`docs/guide/glossary.md` — quick lookup buat istilah yg sering muncul: Connector, Operation, Connector Row, Tag Filter, MCP, Meta-tool, PAT, OAuth Grant, DCR, Connector Runs, System Tag. Mirror sec 11 di design.md tapi user-facing.

**Status:** opsional — bisa skip kalau user docs udah self-explain.

### 9.2 `docs/reference/connector-api.md`

Pure API ref dari `pkg/connector/`: type signatures, godoc-style. Bisa auto-derive dari godoc (`go doc -all ./pkg/connector`) atau handwritten. Useful buat advanced users yg mau bypass skill.

**Status:** opsional — `connector-module.md` di guide sudah cover 80%.

### 9.3 "How to test connector locally" mini-guide

Step-by-step end-to-end:
1. Register connector di main.go
2. Boot `wick dev`
3. Bootstrap auto-seed row
4. Buka `/manager/connectors/<key>`, isi credential
5. Buka test page, klik Run
6. Generate PAT di /profile/tokens
7. Add Claude Desktop config snippet
8. Restart Claude Desktop, test via wick_list

**Status:** in-scope — masuk sbg section di `connector-module.md` (sec 6.1) atau standalone subsection di `mcp.md` (sec 6.2). Pilih `mcp.md` (lebih relevan ke install flow).

### 9.4 Tag/access-control narrative di docs

Tag filter sering bingung-in user baru (kenapa row di-share lewat tag, bukan langsung user_id).

**Status:** in-scope — masuk sbg section "Sharing connectors" di `connector-module.md` (sec 6.1).

### 9.5 Migration notes

Kalau ada user existing yg upgrade dari pre-connector wick: sebut bahwa `connectors` table baru bakal di-bootstrap empty + jobs `connector-runs-purge` auto-register. Zero action needed.

**Status:** in-scope — masuk sbg paragraf di changelog entry (sec 7.9).

### 9.6 Sync warning di design.md

Tambahin baris di header `internal/docs/connectors-design.md`:
> **User-facing docs:** see `docs/guide/connector-module.md`, `docs/guide/mcp.md`, dst. Update those when changing user-visible behavior.

**Status:** in-scope — 1 line edit.

### 9.7 Screenshot batch checklist

Bikin `docs/SCREENSHOTS.md` checklist lengkap (semua placeholder dari plan ini di-list jadi 1 batch).

**Status:** in-scope — convenience file, kamu bisa kerjain SS sekali jalan.

### 9.8 README badge / hero update

Root README.md mungkin punya badge atau hero text yg bisa di-extend dgn "MCP-ready". Verify pas implementasi.

**Status:** opsional — depends on current README state.

---

## 10. Order of implementation (sequential)

Tier A (foundational — must finish before docs):
1. Port crudcrud ke `template/connectors/crudcrud/` (sec 8) + delete `template/integrations/sample/`.
2. Register di `template/main.go`.
3. Verify `wick build` (template) jalan.

Tier B (skills — must finish before AGENTS update):
4. Bikin `template/.claude/skills/connector-module/SKILL.md` (sec 2).
5. Bikin `.claude/skills/connector-module/SKILL.md` (sec 1).
6. Update `cmd/cli/skill.go` `skillLabels` map (sec 3.1).
7. Verify `wick skill list` show `connector-module` after rebuild wick.

Tier C (template metadata):
8. Update `template/AGENTS.md` (sec 4).
9. Update `template/README.md` (sec 5).

Tier D (user docs — heavy writing):
10. Tulis `docs/guide/connector-module.md` (sec 6.1).
11. Tulis `docs/guide/mcp.md` (sec 6.2) dgn end-to-end test guide (sec 9.3).
12. Tulis `docs/guide/access-tokens.md` (sec 6.3).
13. Tulis `docs/guide/oauth-connections.md` (sec 6.4).
14. Tulis `docs/guide/connector-runs-purge.md` (sec 6.5).
15. Update `docs/.vitepress/config.ts` sidebar (sec 6.6).

Tier E (sweep existing docs):
16. Update `docs/index.md` (sec 7.1).
17. Update `docs/guide/introduction.md` (sec 7.2).
18. Update `docs/guide/getting-started.md` (sec 7.3).
19. Update `docs/guide/ai-quickstart.md` (sec 7.4).
20. Cross-link `docs/guide/tool-module.md` (sec 7.5) + `docs/guide/job-module.md` (sec 7.6).
21. Update `docs/reference/cli.md` (sec 7.7) — `wick skill list` output + version cleanup.
22. Verify `docs/reference/env-vars.md` (sec 7.8) — likely no change.
23. Tulis changelog entry (sec 7.9).
24. Spot-check root README.md (sec 9.8).

Tier F (polish):
25. Tambah sync warning di `connectors-design.md` (sec 9.6).
26. Bikin `docs/SCREENSHOTS.md` checklist (sec 9.7).

Tier G (verify):
27. `wick build` di repo root.
28. `wick test`.
29. (Optional) `cd docs && npm run docs:dev` smoke test build.
30. Final review, commit.

**Estimated effort:** Tier A-C: ~2h. Tier D: ~3-4h (5 page baru). Tier E: ~1.5h. Tier F-G: ~30m. Total ~7-8h.

---

## 11. Plan self-review

✅ 4 hal di request user:
1. Skill connector (template + wick-core) → sec 1, 2
2. Docs connector + MCP + Connected Apps + Access Tokens + OAuth + connector-runs-purge → sec 6.1-6.5
3. Instruksi create connector + best practices → embedded di skill (sec 1, 2) + connector-module.md (sec 6.1)
4. Example connector di template → sec 8 (port crudcrud)

✅ 2 hal tambahan dari user reply:
1. Skill bundling sekalian → sec 3
2. Sweep existing docs → sec 7

✅ Saran dari aku yg dimasukkan:
- Glossary (sec 9.1, opsional)
- API reference (sec 9.2, opsional)
- End-to-end test guide (sec 9.3, in-scope di mcp.md)
- Tag/access narrative (sec 9.4, in-scope di connector-module.md)
- Migration notes (sec 9.5, in-scope di changelog)
- Sync warning di design.md (sec 9.6, in-scope)
- Screenshot batch checklist (sec 9.7, in-scope)
- README badge update (sec 9.8, opsional)

✅ Decisions locked di sec 0.

✅ Order of implementation (sec 10) sequential, dependencies clear.

⚠️ Potential gaps yg masih perlu konfirmasi waktu implementasi:
- `template/main.go` shape (sec 8 step "Register di template/main.go") — belum di-baca. Kalau pakai pattern beda dari yg aku asumsiin, adjust register snippet.
- `template/README.md` shape (sec 5) — belum di-baca. Patches concrete-nya pas implementasi.
- `env-vars.md` (sec 7.8) — verify gak ada env baru. Likely no change tapi worth cek.
- Changelog versioning (sec 7.9) — VERSION file di repo root nentuin canonical. Pas implementasi cek `cat VERSION`.

⚠️ Trade-offs yg di-take:
- Wick-core skill `.claude/skills/connector-module/` GAK di-bundle (cuma template version yg di-bundle). Konsisten dgn `tool-module` precedent. Kalau kamu mau wick-core skill juga ke-distribute (mis. utk fork/contributor), perlu separate `//go:embed` directive — flag kalau mau.
- Image SS aku tulis sbg HTML comment placeholder, bukan broken `![]()` link. Reasoning: broken link rendered di vitepress sbg ikon broken, jelek; comment placeholder invisible di output. Trade-off: dev harus replace komen ke `![]()` pas SS jadi.

Ready to execute setelah kamu konfirmasi gak ada yg miss.
