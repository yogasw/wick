# Changelog

All notable changes to Wick are documented here.

---

## [Unreleased]

_Nothing yet — notes for the next release go here._

---

## [v0.25.0](https://github.com/yogasw/wick/compare/v0.24.1...v0.25.0) — Platform Updates

_Released on 2026-06-25_

### Added

*   **MCP — `wick_execute` batch mode**: Pass a `calls` array to run up to 100 connector operations in a single round-trip. Calls run in parallel (server-side concurrency fixed at 5); a failing or timed-out call never stops the rest. Each entry in the response carries `{index, tool_id, ok, result|error, timed_out, duration_ms}` plus summary counts. Set `timeout_ms` to cap per-call time (default 3 min, max 5 min). Single-call shape is unchanged. See [Batch execution](/guide/mcp#batch-execution).

### Fixed

*   **Channels — Telegram & REST per-user instances**: Each user can now configure their own Telegram bot or REST endpoint independently. Wick starts one keyed instance per owner at boot and hot-adds new instances when a user saves their config — matching the existing Slack per-user model.
*   **Slack — access-denied DM**: When a message is blocked by the access-control whitelist, wick now DMs the blocked user with a reason (`identity` or `channels`) instead of leaving the 🚫 reaction as a silent dead-end.
*   **Slack — multi-instance bot footer**: The "Sent using @bot" footer now resolves the bot display name from each instance's own token, so per-user Slack instances credit their own bot rather than a stale shared value.
*   **Pool — double-reply on first turn**: The injected origin-context turn is now deferred until after the first user message lands, preventing the agent from being spawned early and producing a duplicate reply (affected Slack, Telegram, and REST sessions).
*   **`MapToStruct` — bool config fields**: Reflected config loading no longer panics when boolean fields are absent from the stored JSON.
*   **Projects — Centralized Visibility Filter**: Admins can view all projects; other users see their own, untagged-shared, and tag-shared projects. Channel default-project dropdowns now list only accessible projects.
*   **Sessions — Owner Stamping**: Session owner is now stamped once upon creation, not on every message, optimizing performance.

### Fixed

*   **PWA service worker — pending-hang on boot**: Static assets (`/sw.js`, `/public/*`, `/modules/*`) are now exempt from the boot gate. Previously, an already-installed service worker would intercept these asset fetches on a reload while boot was still in progress; the gate held every request, leaving `app.css`, `icon.svg`, and similar files stuck at "pending" until the boot restore finished. Because these paths are served from `embed.FS` and depend on nothing the boot gate sets up, exempting them lets the SW resolve its cache immediately regardless of boot state.
*   **PWA service worker — stale-while-revalidate and navigation fetch hang**: Added an 8-second `AbortController` timeout to both the SWR background refresh and the network-first navigation path. Without it, a stalled TCP connection (dead keep-alive socket, momentarily busy server) left `fetch()` hanging indefinitely with no error, so the asset or page never settled — visible as an asset or navigation stuck at "pending" forever. The timeout converts the stall into a rejection, allowing the SW to fall back to cache or surface a real network error instead. The SWR background refresh is now kept alive past `respondWith` via `waitUntil`.
*   **Software Update page — Changelog rendering**: Fixed an issue where the "What's new" changelog rendered as raw Markdown due to a missing `/public/lib/wick-markdown.js` asset.

### Changed

*   **`wick build` — build time always stamped**: `BuildTime` is now injected as an `-X` ldflag (RFC3339 UTC, set at `wick build` invocation time) for every build path. Previously it relied solely on Go's `vcs.time` VCS metadata, which is absent inside the `wick init` scaffold (a git-less directory) used by the release pipeline, leaving the "Built" field showing "unknown" in all release binaries. The "Built" field on the Software Update page and in `wick_info` MCP output now always shows the actual compile time.

### Removed

*   **Admin — Software Update page — Commit row**: The **Commit** field has been removed from the Version panel. Build time is now always available (see above) and more meaningful to end-users; the commit SHA is a build-internals detail not useful at the operator level. The version fields on the Software Update page are now grouped as Application / Wick / Runtime.

---


## [v0.24.1](https://github.com/yogasw/wick/compare/v0.24.0...v0.24.1) — MCP & Connectors

_Released on 2026-06-24_

### Added

*   **Custom MCP connector — tool grouping via `_meta`**: Upstream MCP servers can now specify a top-level `_meta.categories` legend and set `_meta.category` on individual tools in their `tools/list` response. Wick now groups the exposed operations into titled sections matching the server's intended layout. Section order follows the legend; tools with no category collect into a single untitled trailing section. Servers that do not ship `_meta` will retain the historical flat single-section layout, requiring no action.

### Fixed

*   **Custom MCP connector — bearer / secret-header 401 after save**: Connector credentials stored as master-encrypted tokens (`wick_cenc_` prefix, used for server-level secrets) were not being decrypted before outbound requests. Previously, only the per-user `wick_enc_` prefix was matched during decryption, resulting in the ciphertext being sent verbatim as the `Authorization: Bearer` or custom header value, leading to 401 errors from upstream servers. Both prefixes are now recognised and decrypted correctly.

---


## [v0.24.0](https://github.com/yogasw/wick/compare/v0.23.6...v0.24.0) — Software Update UI

_Released on 2026-06-24_

### Added
*   **Software Update page — release notes + update status**: The self-update page (now under **Setup → Advanced → Software Update**) displays a rendered changelog between the running and latest versions, the release date, and an at-a-glance status badge (green **Latest** or amber **Update available → vX**). For official builds, the changelog range is pulled from the published changelog site; downstream apps fall back to their GitHub release notes. A **View full changelog** link opens the full page.
*   **"No build for this platform" notice**: When a newer release exists but ships no asset for the running OS/arch, the page now shows the version and changelog with an informational notice (recommending to build from source or ask the maintainer) instead of a hard error.
*   The page auto-checks for updates on load, so the latest version and changelog populate without a manual click.

### Changed
*   The admin **Configs** section is renamed to **Advanced** (`/admin/advanced`); the self-update card within it is now **Software Update** (`/admin/advanced/software-update`).
*   Changelog and other markdown on the Software Update page now renders as formatted HTML via a shared `@wick-fe/common-md` bundle, served from `/public/lib/` and reusable by any server-rendered page. The shared markdown renderer also learns to interpret thematic breaks (---) as `<hr>`.

---


## [v0.23.6](https://github.com/yogasw/wick/compare/v0.23.5...v0.23.6) — Self-Update

_Released on 2026-06-24_

### Added
*   Added or improved self-update functionality/testing.

---


## [v0.23.5](https://github.com/yogasw/wick/compare/v0.23.4...v0.23.5) — Updater

_Released on 2026-06-24_

### Fixed
- Self-update mechanism for Termux and unprivileged Linux environments. Previously, self-update failed on Termux due to attempts to use `dpkg -i` via `pkexec/sudo`, which are not available or required for user-owned installations in Termux. The application would hang on "Restarting...". The new mechanism now performs an unprivileged installation by extracting the inner ELF binary from the staged `.deb` file and swapping it in place using `syscall.Exec`, mirroring the `install.sh` script's approach.
- The updater now provides a clear error message if the install directory is not user-writable (e.g., a root-owned `/usr/bin` installation), guiding the user to re-run the installer.

### Improved
- Linux relaunch during self-update now preserves the original process arguments. This ensures that headless services started with specific arguments (e.g., `all` or `server`) continue to operate correctly after an update, matching the behavior on Windows.

---


## [v0.23.4](https://github.com/yogasw/wick/compare/v0.23.3...v0.23.4) — Self-Update

_Released on 2026-06-24_

### Improved
*   Internal test release to validate the self-update mechanism.

---


## [v0.23.3](https://github.com/yogasw/wick/compare/v0.23.2...v0.23.3) — Self-Update & Admin

_Released on 2026-06-24_

### Added
*   **Admin System page — web-based self-update**: A new **System** card under `/admin/configs` lets any admin check for updates, watch a live download-progress bar (SSE), and restart the service to apply a new release — all from the browser. Previously, self-update was tray-only; this brings the same flow to headless (`<app> all` / `<app> server`) deployments. The page also shows version detail (app name/version, wick version, commit, build time, access type, DB status) matching what `wick_info` reports over MCP. See [Admin Panel — System](/guide/admin-panel#system-adminconfigssystem).

### Changed
*   **Auto-update default changed to off**: `auto_update` in `config.json` now defaults to `false` (opt-in). Existing installs that previously relied on the default-on behaviour should enable auto-update explicitly — via **Preferences → Auto-update** in the tray, or the **Automatic updates** toggle on the new System page.

### Fixed
*   **Self-update on Termux / unprivileged Linux**: Applying an update no longer shells out to `dpkg` via `pkexec`/`sudo`. Self-update now mirrors the installer — it never escalates privilege. On Linux/Termux it extracts the inner binary from the staged `.deb` and swaps it in place (`syscall.Exec`), so updates work on Termux (user-owned prefix, no `pkexec`/`sudo`) and any unprivileged install. If the install directory isn't user-writable, Apply fails with a clear message instead of prompting for a password.
*   **Relaunch preserves args after update**: Both the Windows (MSI helper) and Linux (binary swap) restart paths now relaunch with the process's original arguments, so a headless `<app> all` / `<app> server` service re-serves after an update without manual intervention.

---


## [v0.23.2](https://github.com/yogasw/wick/compare/v0.23.1...v0.23.2) — Access Control & UI

_Released on 2026-06-23_

### Fixed
*   **PWA Stale Layout**: Non-hashed static assets (`app.css`, `app.js`, `dialog.js`, `palette.js`, `push.js`) are now served stale-while-revalidate instead of cache-first. This ensures updated assets are picked up automatically after a deploy on the next normal page load, resolving the issue where new deploys did not surface until a hard refresh.
*   **Cross-Tenant Project Access Leaks**: Project detail, update, delete, and SSE stream routes now enforce `callerProjectAccess().allowProject()`. This prevents scoped users from reading, modifying, or deleting projects they lack access to, even if the project ID is known. Endpoints return 404 (Not Found) to avoid confirming project existence to unauthorized callers.
*   **Ownerless Projects**: Projects with no owner (`OwnerUserID == ""`) are now treated as admin-only resources. Non-admins can only access such projects if an explicit tag grant covers them, closing a loophole that previously exposed every ownerless project and its sessions to all authenticated users.
*   **SSE Stream Access Control**: The global SSE stream (`/sse`), which lists all active sessions, is now restricted to admins. Session-scoped SSE streams (`?session=<id>`) require the caller to own or have tag-granted access to that specific session.
*   **Session Subroute Access**: Remaining cross-tenant leaks for session subroutes (e.g., approvals, asks, workspace connector configurations, and SCM Git routes) are closed. Access to these routes now requires the caller to own or have tag-granted access to the specific session ID. This was implemented using a new `Router.Use` middleware.
*   **Conversation UI Overlap**: Resolved floating header overlap in Raw, Commands, and Approvals views by adding appropriate top offsets (`pt-14`, `md:pt-16`), ensuring their content starts below the header bar.
*   **Markdown Enrichment Self-Healing**: Improved Markdown rendering for committed-turn bubbles. Blocks like Mermaid/SVG now self-heal and re-enrich correctly after history reloads or content changes (e.g., `innerHTML` reset), preventing them from intermittently displaying as raw "rendering…" text.

---


## [v0.23.1](https://github.com/yogasw/wick/compare/v0.23.0...v0.23.1) — Agents

_Released on 2026-06-23_

### Fixed

*   **Session detail access for tag-granted project members**: Users who could see a session in the sidebar via project tag grants could not open its detail/conversation page (the route returned 404). The `ownsSession()` function now also checks project-scoped access (`callerProjectAccess().allowSession()`), ensuring consistency between session list visibility and detail access. The App Owner, `admin_see_all` admins, and ownerless unscoped sessions are unaffected.
*   Updated `admin-panel.md` to clarify that tag-granted project members can now open session detail pages, not just their own.

---


## [v0.23.0](https://github.com/yogasw/wick/compare/v0.22.2...v0.23.0) — MCP & Connectors

_Released on 2026-06-23_

### Added

*   **Google Workspace Input Structs**: Added input structs for Google Workspace operations across Calendar, Docs, Drive, Gmail, Meet, Sheets, and Slides, enhancing connector capabilities.

### Changed

*   **`wick_get` — three-level drill-down via `selector`**: `wick_get` now navigates connector operations across three levels instead of returning all schemas at once.
    *   Call with `id` only to get the connector's **category list**.
    *   Add `selector=<category title>` to list that category's **operations** (no schemas).
    *   Add `selector=<op key>` to retrieve that **one op's `input_schema`**.
    *   Flat connectors with no named categories list their ops directly at level 1.
    *   The `category` and `op_key` argument names are accepted as aliases for `selector`.
    *   Session-workspace instances follow the same three levels.
    *   This change keeps large connectors (e.g., Google Workspace with 50+ ops) from dumping every schema into the LLM's context on a single call.
*   **Documentation**: Updated `mcp.md` and the changelog to reflect the `wick_get` three-level drill-down with the `selector` argument.

### Improved

*   **Agent UI**:
    *   **Kebab Menu**: Improved behavior to flip up near the viewport bottom and portal the popup to `<body>` to escape per-row stacking contexts.
    *   **Workflow List**: Swapped the manual workflow-list dropdown to use the Kebab Menu, and pinned `<main>` with `min-h-0` for smoother scrolling without a gap.
    *   **Connector List**: Added bottom padding and refined the search input to a bare style, removing the double border.
    *   **Theme Picker**: Introduced a theme picker in the agents sidebar via `UserMenu(showTheme)`.

---


## [v0.22.2](https://github.com/yogasw/wick/compare/v0.22.1...v0.22.2) — Connectors UI

_Released on 2026-06-22_

### Changed

*   **Connectors moved into the Agents UI**: The connectors manager is now hosted at `/tools/agents/connectors` inside the Agents sidebar shell. All browser-facing `/manager/connectors*` URLs now 302-redirect to this new location, preserving deep links via `?deep=`. The `/manager/api/connectors*` JSON routes and all write/mutation routes remain at `/manager` and are unaffected.
*   **Agents sidebar — Connectors link**: A dedicated **Connectors** navigation item has been added to the Agents sidebar, visible to all users.
*   **`/launcher` renamed to `/mini-tools`**: The tools-grid launcher page has been moved from `/launcher` to `/mini-tools`. A **Mini Tools** link is now visible at the bottom of the Agents sidebar for all users (previously, only admins saw a Settings link there).
*   **Home tile — Connectors**: The "Connectors" tile on the Mini Tools home grid now links directly to `/tools/agents/connectors` instead of `/manager/connectors`.
*   **Connectors SPA breadcrumb**: The connectors index page now displays no breadcrumb (as the heading already states "Connectors"). Sub-pages root at "Connectors". The Audit Log page shows only "Audit Log" with no root breadcrumb.
*   **Chat block toolbar — mobile + PNG fixes**: The per-block hover toolbar is now consistently visible at 70% opacity on touch/no-hover devices (phones, tablets), resolving its previous hidden state until hover. PNG export functionality now utilizes the SVG's intrinsic viewBox/width-height rather than the on-screen size, addressing the "looks like my phone screen" export bug. If rasterization still fails (e.g., due to a tainted canvas), the chart is now downloaded as an `.svg` file instead of silently producing no output.

---


## [v0.22.1](https://github.com/yogasw/wick/compare/v0.22.0...v0.22.1) — Connectors

_Released on 2026-06-20_

### Fixed
*   Corrected the `crudcrud` sample template to return `[]connector.Category` when registering operations. This resolves a build failure in the materialized template (`wick-agent`) by aligning with `app.RegisterConnector`'s updated signature, which now expects operations grouped into categories.

---


## [v0.22.0](https://github.com/yogasw/wick/compare/v0.21.0...v0.22.0) — Connectors & Agents

_Released on 2026-06-20_

### Added

*   **Connector list — inline Connect + connected-account rows**: Each connector row on the list page now shows a **Connect** / **Reconnect** / **+ Connect another** button when the instance has SSO enabled and the caller may connect (no need to open the detail page). Connected accounts appear as sub-rows under the connector card; each account row has a **Disconnect** button (confirmation dialog) for users who own that account or admins.
*   **Connector list — per-row kebab (⋮) menu**: The per-row action buttons (History / Disable / Duplicate / Delete) are now collected behind a `⋮` kebab menu, keeping each row compact.
*   **Connector list — Private chip**: Rows that carry only an `owner:<id>` tag and no sharing tag now display a lock **Private** chip instead of the misleading **Everyone** fallback. Adding a filter tag flips the chip back to tag names.
*   **`Module.DefaultAccess` — seeded access-policy defaults**: Connector module authors can now declare `DefaultAccess connector.AccessDefaults` on their `Module` to pre-seed per-row access-policy flags (`EnableSSO`, `AllowOthersConnectSSO`, `MultiAccount`, `AllowOthersConfigure`) onto every freshly created instance. The Google Workspace connector ships `EnableSSO: true, AllowOthersConnectSSO: true` so a new row is ready to use **Connect Account** without a manual Access Policy step. Admins can still change individual rows afterwards. See [Connector Module — DefaultAccess](/guide/connector-module#defaultaccess-seeding-access-policy-defaults).
*   **HTML artifacts — themed, borderless, auto-height preview**: HTML file artifacts and inline `` ```html `` blocks in the conversation now render in a borderless sandboxed iframe that grows to its content (no inner scrollbar), with a floating **⋮** menu offering Full screen / Show code / Download. The iframe receives a **theme bridge** — CSS variables (`--wick-bg`, `--wick-surface`, `--wick-fg`, `--wick-muted`, `--wick-border`, `--wick-accent`), `color-scheme`, and a `.dark` class in dark mode — so generated HTML matches the chat's active light/dark theme. The agent system prompt instructs the model to use `var(--wick-*)` for theming by default. See [Agents — Artifacts](/guide/agents#artifacts).
*   **Artifact kinds — markdown and text**: `.md` files now render as **markdown** artifacts (fullscreen viewer + download); plain text and code files render as **text** artifacts with the same viewer + download. Previously these fell through to the generic downloadable chip.
*   **Connector build profiles**: Operators can now select which builtin connectors register at boot without rebuilding the binary. Three profiles are available:
    *   `full` (default) — all 7 builtin connectors (GitHub, HTTP REST, Slack, Bitbucket, Loki, Phoenix, Google Workspace). Preserves existing behaviour.
    *   `agent` — curated subset: GitHub, HTTP REST, Slack.
    *   `lite` — no builtin connectors registered at boot.
    Set the active profile with `<app> config profile <full|agent|lite>` (takes effect on restart) or via the admin Configs page (`/admin/variables`, key `profile`). The four runtime connectors (Wick Manager, Workflow, Notifications, Custom Connector) are never profile-gated. See [App CLI Reference — config profile](./reference/app-cli#app-config-profile-full-agent-lite).

### Changed

*   **Connector visibility — live tag resolution**: The connector list now resolves each row's filter-tag IDs live from the database rather than from the session-cookie snapshot. A row created or duplicated in the current session is visible immediately without logout/login.
*   **Home — default landing page**: Navigating to `/` now redirects to the agent UI (`/tools/agents/`). The tools/connectors grid previously at `/` is now at `/mini-tools` (was `/launcher` in v0.22.0; renamed in the subsequent release) and remains fully reachable.
*   **Admin nav — Mini Tools dropdown**: The standalone Tools, Connectors, and Jobs tabs in the admin navigation bar are grouped into a single **Mini Tools** dropdown. See [Admin Panel](./guide/admin-panel#mini-tools-tools-connectors-jobs).

---



## [v0.22.0] — Connector Categories

### Added

*   **Connectors — operations grouped into categories**: The connector detail page (Manager → Connectors → {connector}) now renders operations as named section cards instead of a flat list. Each card shows the section title, description, op count, per-card Enable/Disable all, and a paginated op table (5 ops per page). A sticky "Sections" jump sidebar lets you jump between sections without scrolling; a global search box filters across all categories.
*   **Custom connector builder — operation sections**: The manual builder's Operations step is now section-based. Each section has a title and description, and ops can be dragged between sections. The right-hand Jump panel is a collapsible mini-map with scroll-spy highlighting that auto-expands the active section.
*   **`pkg/connector` — `Category` / `Cat()`**: Built-in connector authors now group operations into titled sections using `connector.Cat(title, description, ops...)`. `Module.Operations` is `[]connector.Category`; `Module.AllOps()` flattens for callers that do not care about grouping; `Module.CategoryOf(opKey)` returns the section title for a given op key. See [Connector Module — Operations()](/guide/connector-module#operations).
*   **Google Workspace — Gmail, Calendar, and Meet**: 18 new operations across three new categories on the existing `google_workspace` connector (same OAuth row, one re-consent required):
    *   **Gmail** (6 ops): `gmail_list_messages` (search), `gmail_get_message`, `gmail_send`, `gmail_create_draft`, `gmail_reply` (threaded), `gmail_modify_labels` (archive, star, mark read, etc.).
    *   **Calendar** (7 ops): `calendar_list_calendars`, `calendar_list_events`, `calendar_get_event`, `calendar_create_event` (with optional Google Meet link via `add_meet=true`), `calendar_update_event`, `calendar_delete_event`, `calendar_respond_event` (RSVP accept/decline/tentative).
    *   **Meet** (5 ops): `meet_create_space` (create a standalone Meet link), `meet_get_space`, `meet_list_conference_records`, `meet_list_recordings`, `meet_list_transcripts`.
    *   See [Google Workspace connector](/connectors/googleworkspace) for the full op reference.

### Breaking

*   **Custom connectors — ops storage format changed**: The stored `ops` column is now a nested array of sections (`[{title, description, ops:[...]}]`). The old flat `[{key, ...}]` format is no longer accepted. **Existing custom connectors built before v0.22.0 must be deleted and recreated.** See [Custom Connectors — Operations data format](/guide/custom-connectors#operations-data-format-breaking-change-for-existing-custom-connectors). Built-in and MCP-backed connectors are unaffected.
*   **Google Workspace — existing connected accounts must re-consent**: The OAuth consent now requests Gmail, Calendar, and Meet scopes in addition to the previous Drive/Sheets/Docs/Slides scopes. Accounts connected before this release will have those new ops flagged as `needs scope: …` in the health check until the operator clicks **Connect Account** again to re-run the consent flow.

---

## [v0.21.0](https://github.com/yogasw/wick/compare/v0.20.2...v0.21.0) — MCP

_Released on 2026-06-19_

### Fixed
*   Allow admins and session creators to manage session titles. Previously, only the session owner could use `wick_set_title` and `wick_session_info` to manage a session's title, which prevented administrators (including internal agents) and the original session creator from performing these actions due to ID mismatch. The system now correctly grants these permissions.

---


## [v0.21.0](https://github.com/yogasw/wick/compare/v0.20.2...v0.21.0) — MCP session-title guard fix

_Released on 2026-06-19_

### Fixed

*   **MCP — `wick_set_title` / `wick_session_info` authorization**: Admin principals (including the internal agent principal) can now manage any session's title without being blocked by the owner-only guard. Previously, calls made by the agent on behalf of an admin context were incorrectly rejected. Session creators and admins are now both accepted; ownerless sessions remain admin-only.

---

## [v0.20.2](https://github.com/yogasw/wick/compare/v0.20.1...v0.20.2) — Chat UI

_Released on 2026-06-19_

### Added

*   **Chat — fullscreen diagram lightbox**: Double-clicking any rendered Mermaid or SVG diagram opens a fullscreen zoom/pan viewer.
    *   Gestures: scroll or two-finger trackpad to pan; Ctrl/Cmd+scroll or pinch to zoom toward cursor; drag to pan; double-click inside to reset to fit; Esc, close button, or clicking bare backdrop to dismiss.
    *   On touch devices, the lightbox opens on double-tap.
    *   The backdrop color is switchable (auto-theme → light → dark → checkerboard) and persists across opens, ensuring diagrams on any canvas stay readable.
    *   The viewer recomputes the viewBox from the real bounding box, ensuring any node that spills past the declared viewBox is not clipped.
    *   Diagrams remain crisp vector at any zoom level (2D transform, no GPU-layer bitmap scaling).
    *   Includes a live zoom-level readout.

### Improved

*   **Chat — AI timestamp always visible**: Assistant (AI) response bubbles now show the `HH:mm` stamp at all times instead of only on hover. User bubbles remain hover-only. Day separators in the thread are now static dividers.
*   **Chat — floating day pill**: A WhatsApp-style floating date pill appears at the top of the conversation viewport while scrolling and fades out after ~1.4 seconds of idle. It always shows the label for the topmost visible day group.
*   **Chat — Diagram display**: Wide diagrams and raw fallbacks within chat bubbles are now contained to prevent overflow and overlap with other UI on mobile.

---


## [v0.20.1](https://github.com/yogasw/wick/compare/v0.20.0...v0.20.1) — Chat & Agents

_Released on 2026-06-18_

### Improved

*   **Chat — WhatsApp-style message timestamps**: Each assistant and user bubble now shows an `HH:mm` timestamp on hover/focus, and a sticky centered date separator (Today / Yesterday / weekday name / full date) appears whenever the date changes between turns.
*   **Chat — Mermaid live progressive rendering**: Mermaid diagrams now render progressively during streaming the same way SVG does — partial blocks paint incrementally, a last-good-frame is preserved across token repaints, and raw source never flashes while the diagram is building.
*   **Chat — diagram format selection rule**: The agent system prompt now encodes an explicit SVG-vs-Mermaid selection rule: node/edge graphs → SVG; algorithmic diagrams (sequence, Gantt, pie, journey) → Mermaid. User overrides always win.

### Fixed

*   **Agents — ownerless "system" projects now visible**: Projects with no recorded owner (created directly in the DB or by internal tooling) are now accessible to every authenticated caller, not only when the tags service is absent.
*   **Chat — Pin as default state**: The "Pin as default" button and project pinning state now reflect immediately in the UI without needing a page refresh.

---


## [v0.20.0](https://github.com/yogasw/wick/compare/v0.19.3...v0.20.0) — Agents & Chat

_Released on 2026-06-18_

### Changed
*   **Agents — Admin session visibility now scoped by default**:
    *   Admins are no longer implicitly unrestricted. By default, an admin sees only tag-granted projects and their own sessions, matching the regular-user isolation model.
    *   To restore the legacy unrestricted view, enable `admin_see_all` at `/admin/variables`.
    *   The App Owner tier is unaffected and remains always-unrestricted.
    *   Ownerless sessions (no recorded creator) are now hidden from everyone while `admin_see_all` is off, instead of being reachable by any logged-in user.
    *   See [Admin Panel — Admin session visibility](/guide/admin-panel#admin-session-visibility-admin_see_all).

### Improved
*   **Chat conversation rendering**:
    *   **SVG support**: `svg` fences or bare inline `<svg>…</svg>` now render as inline images. SVGs are sanitized (`<script>`, `<foreignObject>`, `on*` attributes, and external URLs are stripped). Lenient parsing ensures complex SVGs (with patterns, filters, gradients) render instead of falling back to raw source.
    *   **Progressive SVG rendering**: Mid-stream SVGs auto-close their open tags, allowing shapes to appear as they stream rather than waiting for `</svg>`.
    *   **Streaming live turn enrichment**: A dedicated `renderLive` action manages `innerHTML` and transplants already-rendered diagrams between repaints, fixing text↔image flickering with every token.
    *   **Synchronous enrichment**: Committed messages now enrich synchronously on mount (no 120ms debounce delay).
    *   **Placeholder for pending blocks**: Mermaid and SVG blocks show a "rendering…" placeholder instead of flashing raw source on load.
    *   **Streamlined UI**: Borders have been removed from the assistant bubble and rendered blocks for a cleaner, document-like appearance.
    *   **Documentation**: SVG support is now documented in the agent system prompt (`render_formats.md`).

### Added
*   **Per-block hover toolbar**: Rendered diagrams now feature a "···" hover toolbar, providing options to Copy source, Download file, and Download as PNG.

---


## [v0.19.3](https://github.com/yogasw/wick/compare/v0.19.2...v0.19.3) — Conversation Trace

_Released on 2026-06-17_

### Fixed
*   **Conversation — trace view renders correctly** — The "Show trace" panel on assistant turns previously rendered each streamed `thinking_delta` fragment as its own separate bubble, often split mid-sentence, and grouped all thinking blocks at the top before tool cards. Consecutive thinking fragments are now coalesced into a single bubble per reasoning run, and the trace renders in chronological order (thinking interleaved with tool cards) so the agent's actual reasoning flow is visible. This improvement applies to both live snapshots and persisted traces, and it also repairs old traces that were stored with fragmented thinking events.

---


## [v0.19.2](https://github.com/yogasw/wick/compare/v0.19.1...v0.19.2) — Fixes & Improvements

_Released on 2026-06-17_

### Fixed
- **Connectors**: Resolved an issue where `ConnectorRun` string columns, such as `ConnectorID`, were too short (`varchar(36)`) to store session-workspace instance IDs (e.g., `sw_<UUID>`, which are 39 characters). These columns have been widened to `text` to prevent "value too long" errors during run inserts. Existing columns are automatically updated on application boot.
- **New Session UI**: Corrected a visual bug where the "No healthy providers found" banner would briefly flash on the new session page before provider options had finished loading. The banner is now gated to appear only once the provider options request has settled and confirmed an empty list.

### Improved
- **Release Process**: Enhanced the release tag bundle creation to dynamically discover Single Page Application (SPA) distribution directories within `internal/`. This streamlines development by automatically including new SPA hosts in the bundle and managing their `.gitignore` entries, removing the need for manual configuration.

---


## [v0.19.1](https://github.com/yogasw/wick/compare/v0.19.0...v0.19.1) — Artifact Gallery

_Released on 2026-06-17_

### Added
*   **Conversation — Assistant Artifact Gallery** — files the agent writes or edits during a turn are now surfaced as an **artifact gallery** directly below the assistant bubble. Up to 4 items show as a grid; more than 4 switch to a carousel. Per-kind rendering: images open a zoomable/pannable lightbox (mouse-wheel, drag, `Esc`/`+`/`−`/`0`); PDFs open inline in the lightbox; HTML files render as a sandboxed live-preview iframe; any other type shows as a downloadable chip. Detection is retroactive — no schema migration, works for existing sessions. See [Agents — Artifacts](/guide/agents#artifacts).
*   **Backend — `GET /tools/agents/sessions/{id}/files/raw`** — new endpoint serves cwd files inline for the artifact lightbox (images and PDFs with correct MIME type; SVG with a `sandbox` CSP header; HTML and other types forced to download). Path-traversal protection via the same `safeJoin` sandbox as the rest of the agents file API.
*   **Conversation API — `artifacts[]` and `has_artifact` per turn** — each turn in the conversation API response now carries an `artifacts` array (path, kind, MIME type) and a `has_artifact` boolean. Visible in the session detail Raw tab.

### Fixed
*   **Conversation** — Artifacts now render automatically below assistant turns upon completion without requiring a manual page refresh.

---


## [v0.19.0](https://github.com/yogasw/wick/compare/v0.18.7...v0.19.0) — Agents & Manager SPAs

_Released on 2026-06-16_

### Added
*   **Custom connectors — "Definition updated" reload banner** — the connector page (Manager → Connectors → {connector}) now shows an actionable banner when the stored definition is newer than the live module (`needs_reload` state). The banner includes a **Reload** button that rebuilds the live module from the saved definition and clears the dirty state — no page reload required. The banner is visible to any authenticated viewer, independent of edit rights. Previously this state was indicated only by a passive "· needs reload" hint on the connectors index grid.
*   **Custom MCP connector — Re-sync tools** — a **Re-sync tools** button is now shown on a custom MCP connector's page (Manager → Connectors → {connector}). Clicking it re-fetches the upstream server's `tools/list` and atomically swaps in the fresh operation set, refreshing the stored connection status. The operation set is connector-level (shared by every instance), so this is a per-connector action available to any user who can open the connector.
*   **Custom MCP connector — connection status chip** — a custom MCP connector's page now shows a **Connected / Disconnected / Never tested** chip reflecting the last probe of its upstream server.
*   **Skills — hierarchical breadcrumb navigation** — the Skills SPA now displays a clickable breadcrumb trail (`Skills / {folder} / {nested path…}`) instead of the old single back-button. Each ancestor segment is a link, so you can jump directly to any parent folder from a deeply nested skill file.
*   **Consistent breadcrumb navigation across SPAs** — the connector manager, Providers (detail & storage), Presets, and the Workflow editor now render their navigation trail through one shared breadcrumb component, so the separator, hover, and accessibility read the same everywhere instead of each module hand-rolling its own back-link.
*   **Conversation — Raw trace tab** — the **Raw** tab on a session detail page now renders an interactive, collapsible JSON tree of the session's turns. Turns that have a server-side trace (`has_trace`) automatically fetch their full per-turn tool and thinking events on demand when the tab opens, merging them into the tree as a `trace` field. Each node can be expanded or collapsed individually; values are type-colored (string / number / boolean / null). A **Copy** button copies the full JSON to the clipboard.
*   **Providers list: Active Processes panel** — when any agent is running, a table above the provider cards shows every live spawn (session ID, agent name, PID, lifecycle/substate). Hidden when the pool is empty. Also includes a list of recent spawns linking to the server-rendered detail page.
*   **Providers list: per-provider hook actions** — each provider card now has inline Enable / Disable / Test buttons for the `PreToolUse` Command Gate hook (shown only when the master gate is enabled). The status badge distinguishes `enabled ✓`, `enabled (unverified)`, `ready`, and `disabled` states. Clicking Test fires a live probe and refreshes the card without a page reload.
*   **Conversation — inline image thumbnails** — images attached to a user message render as inline thumbnails in the thread; clicking opens a full-screen lightbox. PDF and Markdown attachments open in the file viewer.
*   **Conversation — file viewer previews** — the context-panel file viewer now shows image, PDF, and Markdown previews, and renders code files with syntax highlighting (lazy-loaded Ace editor).
*   **Conversation — resizable Source sidebar** — the SCM dock sidebar can be dragged to any width; the chosen width is persisted in `localStorage` across sessions.
*   **Conversation — confirm before kill/dequeue** — a confirmation dialog is shown before terminating a running agent or removing a queued session, preventing accidental kills.
*   **Conversation — live count badges** — the Context, Processes, and Workspace rail tabs show live item-count badges that update as the agent works.
*   **Conversation — system turn pills** — system/lifecycle turns in the thread render as centered pills with an optional step list instead of full message bubbles.
*   **Conversation — lifecycle pill tracks streaming** — the session lifecycle pill transitions to "working" immediately while the agent is streaming, before the subprocess state update arrives, and also reflects working from spawning state.
*   **Conversation — lifecycle pill shows "killed" state** — when a session is terminated (including when the idle auto-kill countdown reaches 0), the header pill now shows a neutral **killed** badge instead of lingering on "idle · 0s".
*   **Conversation — rich assistant message rendering** — assistant chat bubbles now render Mermaid diagrams (flowchart, sequence, class, state, ER, Gantt, pie, journey, and more), syntax-highlighted code blocks (highlight.js, GitHub-style light/dark theme), and KaTeX math (`$…$` inline, `$$…$$` display). `html blocks` now render as sandboxed live-preview artifacts. Previously these all showed as plain text. Renderers are lazy-loaded on first use so they don't affect initial page load.
*   **Chat rendering formats documented** — the full set of rich formats the web Conversation tab can render (GFM markdown, highlighted code, Mermaid diagrams, KaTeX inline/display math, smart links) is now documented in [Agents — Chat rendering](/guide/agents#chat-rendering). The same table is injected into the agent's immutable system prompt via `internal/agents/system-prompt/render_formats.md` so the model knows what it can reach for; editing that one file keeps the prompt and the docs in lockstep.
*   **System-prompt assembly extracted to own package** — the system-prompt builder logic moved from `internal/agents/config/` to `internal/agents/system-prompt/` (package rename). No behavior change; the catalog, default baseline, and immutable sections are now co-located and individually testable.
*   **Overview dashboard rebuilt as a Svelte SPA.**
*   **Presets page rebuilt as a Svelte SPA.**
*   **Project settings rebuilt as a Svelte SPA.**
*   **Common-UI: Reusable KvList row editor component.**
*   **Common-UI: Primitive components** (Button, TextInput, NumberInput, TextArea, LabeledInput, Modal, ConfirmDialog).
*   **Common-UI: Shared Breadcrumb component.**
*   **Manager UI — "Everyone" chip shown for untagged connector rows.**
*   **Manager UI — Per-row "History" action added to connector list.**
*   **Manager UI — Search icon and '/' shortcut restored on connectors index.**
*   **Manager UI — Anchor IDs and jump navigation added to custom connector DraftEditor.**
*   **Manager UI — Two-tab "Jump/JSON" navigator restored in custom connector DraftEditor.**
*   **Manager UI — Mobile "Jump" opener (FAB) added to custom connector DraftEditor.**
*   **Manager UI — Per-account operations editor for OAuth connectors.**
*   **Conversation — Optimistic user turn rendering on send.**
*   **Conversation — Project name shown on session list secondary row.**
*   **Conversation — Empty-state message in conversation thread.**
*   **Conversation — Inline error region in approvals modal.**
*   **Conversation — Esc and backdrop dismiss for approvals modal.**
*   **Conversation — Auto-focus current wizard step input.**
*   **Conversation — Red border on invalid required wizard field in wizard.**
*   **Conversation — Folder path shown in project landing header.**
*   **Conversation — Browser tab title now set from session metadata.**
*   **Conversation — New file/directory names validated, parent expanded on create.**
*   **Conversation — Ctrl/Cmd+B keyboard shortcut toggles Context rail.**
*   **Conversation — Data-chat-path links in chat now open in file viewer.**
*   **Conversation — FileViewerModal now dismissible with Esc key and backdrop click.**
*   **Conversation — FileViewerModal now shows save-status indicator.**
*   **Conversation — Inline save error shown in WsInstanceCard.**
*   **Conversation — ContextPanel file list now shows loading/error states.**
*   **Conversation — Fallback bubble for interrupted text-less turns.**

### Changed
*   **Manager UI rebuilt as a Svelte SPA** — the connector manager at `/manager/*` is now served as a Svelte single-page application rendered inside the host chrome (shared header, theme, user menu), replacing the previous server-rendered templ pages. URLs, features, and the full `/manager/api/*` surface are unchanged.
*   **Manager UI — visual realignment** — connector list, custom connector builder (DraftEditor, McpServerForm, MCP SSO guidance, access toggles), jobs/tools setup banners, and audit-log headers are visually aligned to match the design-system tokens and the pre-SPA look. Common-UI primitives (Button, inputs, Select, ToastHost, KvList) use the correct radius and focus-ring tokens. This also includes visual parity restores for new-session, overview, providers, skills UIs.

### Fixed
*   **Conversation — artifact gallery now appears without a manual refresh** — assistant-turn artifacts (files written or edited during a turn) were missing from the gallery after a turn completed and only appeared after a full page reload. The conversation is now refetched from the server on each SSE `done`/`error` event, so the server-derived `artifacts`/`has_artifact` data is picked up immediately.
*   **Conversation — workspace file tree auto-syncs as the agent writes files** — the workspace file tree in the session detail page previously only loaded on session open and manual refresh, so files written by the agent mid-session (artifacts, generated output) did not appear until you reloaded. The SSE handler now silently reloads the file tree — debounced at 400 ms — on every `lifecycle` and `git_status` event, so generated files appear on their own without a refresh.
*   **Connector operation toggle no longer silently no-ops on first disable** — `SetOperation` in the connector repo was using `db.Save()` which resolves to an `UPDATE` when the primary key is set, leaving a missing row untouched (and `Enabled=false` was dropped as a zero-value on struct insert). Rewritten as a GORM `OnConflict` upsert with a map payload so the `enabled` column is always written verbatim. Affects both the legacy admin UI and the new manager SPA.
*   **Starting a new agents session no longer fails with 405** — tool root routes now accept POST/DELETE on the trailing-slash form (`/tools/{key}/`), not just GET. The new-session and conversation SPA POSTs to `${base}/` to create a session, which previously matched a GET-only pattern and returned 405 on Send.
*   **Provider Detail — config saves and enable/disable toggle now work** — the API call was sending a JSON body but the Go handler reads `c.Form("value")` (form-encoded). Every provider config save and the enabled/disabled header toggle silently no-op'd; the request now sends `application/x-www-form-urlencoded`.
*   **Provider Detail — UI parity restored after SPA migration** — the detail page now shows the Enabled/Disabled header toggle, a 2-column grid for simple config fields with a single Save All action, a row editor for `extra_args`, and a key-value editor for `env` (previously flattened to plain text inputs by the SPA migration).
*   **Custom connector builder — input focus loss** — typing in the McpServerForm label/key fields no longer loses focus on every keystroke (dropped the `{#key rev}` remount wrapper), and sticky-header now stays on top.
*   **Custom MCP connector — "Edit definition" dead-end fixed** — clicking "Edit definition" on a custom MCP connector previously navigated to a broken URL because the SPA used the connector's definition ID where the MCP server-form route expects the server's row ID. The backend draft endpoint now returns `server_id` in its response, and the SPA redirects to `/custom/mcp/{server_id}/edit`. An explicit error message is shown if the server ID cannot be resolved instead of silently landing on a not-found page.
*   **Conversation — pending ask rehydrated on page load** — an open `AskUser` approval card is now restored when the page is loaded or refreshed mid-turn, so the question is never lost.
*   **Conversation — orphan `tool_result` turns rendered** — tool-result turns that have no matching tool-call in the loaded window are now displayed as collapsed trace entries rather than silently dropped.
*   **Conversation — Normalize null backend arrays to prevent crashes.**
*   **Skills and Providers API calls now correctly prepend base path (fixes 404).**
*   **Manager UI — SPA now applies app theme correctly.**
*   **Installation — `install-rtk` script no longer fails due to non-breaking spaces.**
*   **Conversation — Thinking duplicate removed from trace.**
*   **Conversation — Agent stuck spawning fixed.**
*   **Conversation — Provider/agent label missing fixed.**
*   **Conversation — Composer auto-resize, autofocus, global keydown redirect, click-to-focus.**
*   **Conversation — Jump to latest pill for scrolling, Ctrl+↓ shortcut.**
*   **Conversation — SCM Source badge now correctly reads total_changed count.**
*   **Conversation — Process list updates now rely solely on SSE lifecycle events, removing redundant 5s polling.**
*   **Manager UI — Live-disk SPA hot-reload now works without Go recompile.**
*   **Manager UI — "Access & behavior" section now correctly shown on the Operations wizard step in custom connector builder.**

---


## [v0.18.7](https://github.com/yogasw/wick/compare/v0.18.6...v0.18.7) — Workflows & Agents

_Released on 2026-06-15_

### Added

*   **Workflow parallel execution**: Workflows can now run multiple triggers concurrently. Enable `concurrency.enabled` per workflow and set a global cap via `workflow_max_parallel_global` in Agent Settings. Each workflow has its own FIFO queue; the global semaphore caps total simultaneous runs across all workflows. Serial mode remains the default (`concurrency.enabled: false`). See [Concurrency](/workflow/#concurrency).
*   **Workflow agent node — extended thinking control**: A `thinking` dropdown (`on` | `off`, default `on`) and a conditional `max_thinking_tokens` number field are now available on the workflow **agent node**. `off` sets `MAX_THINKING_TOKENS=0` (extended thinking disabled); `on` with `max_thinking_tokens: 0` leaves the env unset (unlimited / provider default); `on` with `max_thinking_tokens ≥ 1024` caps the budget at that value. The setting is persisted to session meta before each pool send so a reused session always reflects the current node config. This feature is specific to Claude providers; Gemini and Codex ignore these fields. The regular agent chat flow is unchanged. See [Agent node — Extended thinking](/workflow/nodes/agent#extended-thinking).

### Fixed

*   **Workflow node argument bleeding**: Resolved a regression where rendered templates on workflow node arguments (e.g., `Args`, `Headers`, `Query`, `ShellEnv`, `Command`) would persist across multiple runs. This fix ensures that node fields are detached onto fresh copies before rendering for each run, preventing previous renders from affecting subsequent executions.

---


## [v0.18.6](https://github.com/yogasw/wick/compare/v0.18.5...v0.18.6) — Agents

_Released on 2026-06-15_

### Fixed

*   **Agent Spawner Configuration**: Addressed an issue where `ExtraArgs` and `Env` settings configured in the providers UI were not being forwarded from the `Instance` to the agent subprocess during spawning, resulting in agents running without their intended custom configurations.

### Added

*   **Instance-Level Argument Flow**: `ExtraArgs` can now be passed via `SpawnOptions`, mirroring the existing `ExtraEnv` functionality to ensure instance-level arguments are correctly delivered.
*   **Test Injection Utility**: A new `InstanceOverride` in `ClaudeFactory` facilitates test injection without requiring modifications to user configuration files.
*   **Enhanced Testing for Claude**: Dedicated `spawn_test.go` added for the Claude provider to specifically validate `spawner` and `opt ExtraArgs` handling.
*   **Configuration Contract Test**: A new test, `TestFactoryInstanceConfig_ExtraArgsAndEnv`, was implemented to establish a contract, ensuring all future providers correctly process and forward `ExtraArgs` and `Env` configurations.

### Improved

*   **Universal Spawner Compatibility**: All agent spawners (Claude, Codex, Gemini) now correctly append `opt.ExtraArgs` after their inherent `s.ExtraArgs`, preserving compatibility with existing static test fixtures.
*   **Consistent Configuration Forwarding**: `ExtraArgs` are now consistently forwarded through `agent.Options` and both `Spawn` call-sites (`Start` and `respawnWithMessage`).
*   **Extended Provider Test Coverage**: Existing Codex and Gemini spawn tests have been expanded to include `opt.ExtraArgs` cases, ensuring uniform behavior across different providers.

---


## [v0.18.5](https://github.com/yogasw/wick/compare/v0.18.4...v0.18.5) — PWA Notifications

_Released on 2026-06-15_

### Improved
*   Broadcast in-app lifecycle push notifications to all open tabs, ensuring exactly one OS notification is surfaced per push. Repeated pushes are collapsed into a single OS surface using a unique tag to prevent spam.
*   De-duplicate in-app cards by push tag, replacing existing cards instead of stacking.
*   Synchronize dismissals across tabs: Dismissing an in-app card in one tab now clears the same card in all other open tabs and closes the shared OS notification. Auto-dismiss and remote dismiss actions only clear the local card.

---


## [v0.18.4](https://github.com/yogasw/wick/compare/v0.18.3...v0.18.4) — PWA Improvements

_Released on 2026-06-15_

### Fixed
*   **PWA Push Notifications**: The PWA push notification handler no longer suppresses OS notifications when any same-origin window is open. Notifications are now surfaced unless a visible PWA window is already on the push's target path, ensuring users receive notifications even with background or different-page tabs open.
*   **PWA Fetch Handler**: Resolved an issue where the PWA fetch handler could throw 'Failed to convert value to Response' by intercepting cross-origin requests and resolving `respondWith()` with `undefined`. The handler now explicitly skips cross-origin requests and always returns a valid Response.

### Improved
*   **PWA Notification Badge**: Added a dedicated monochrome white-silhouette notification badge (`icon-badge.png`) for Android devices. This prevents the full-color icon from being collapsed into a white blob when masked by Android's badge rendering.

---


## [v0.18.3](https://github.com/yogasw/wick/compare/v0.18.2...v0.18.3) — Workflows & Connectors

_Released on 2026-06-14_

### Fixed
*   **Workflow runs from a freshly-published workflow now execute immediately** — previously, workflows created or published from the UI would accept their trigger (webhook returned `202`, dispatch reported a match) but the run never executed and never appeared in run history until the server was restarted. The per-workflow worker was being spawned bound to the HTTP request context, so it died the instant the response was sent; the queue lingered with no live consumer and runs piled up undrained. Workers are now pinned to the server lifetime, so a publish, toggle, or hot-reload from any HTTP handler produces a worker that survives the request. Publishing a new workflow (or re-publishing one) never interrupts another workflow's in-flight run.
*   **Connector nodes that require an authenticated identity now work from workflow runs** — operations gated on the logged-in user (e.g. `notifications.send_to_push_id`) returned `not authenticated` when fired from a headless workflow run, even though they worked when tested manually through the UI. Connector nodes now run as the workflow's owner.

### Changed
*   **Workflow dispatch is no longer silent** — the router now logs when a run is enqueued, when a matched trigger has no queue or no live worker (the run would otherwise vanish without a trace), and when a worker is spawned or stops. This makes a run that fails to execute debuggable from the logs instead of leaving no evidence.

### Improved
*   **CI performance and reliability**:
    *   **Shared caches**: A new `ci-cache-warm.yml` workflow warms shared caches (Go build, module, and templ binary) on `push:master`, making them available for all PRs.
    *   **Faster PR tests**: PR tests now efficiently restore Go build caches and utilize a two-tier strategy: a fast full suite without `-race`, followed by `-race` only on packages changed in the PR to minimize runtime.
    *   **Conditional job execution**: Go and frontend jobs are now gated by `dorny/paths-filter`, running only when their respective files change.
    *   **New frontend build job**: A dedicated job with caching for Node.js and Vite builds (gated on `fe/**` changes) has been added.
    *   **Nightly race detection**: A new `nightly-race.yml` workflow runs the full `-race` suite daily, ensuring race conditions are caught in packages not frequently touched by PRs.

---


## [v0.18.2](https://github.com/yogasw/wick/compare/v0.18.1...v0.18.2) — GitHub Connector

_Released on 2026-06-14_

### Added
*   Enhanced GitHub connector with support for reviews.
*   Added branch-related functionalities to the GitHub connector.
*   Introduced label management features for the GitHub connector.
*   Enabled search capabilities within the GitHub connector.
*   Integrated GitHub Actions support.
*   Added webhook functionalities.
*   Implemented comment-edit operations for the GitHub connector.

---


## [v0.18.1](https://github.com/yogasw/wick/compare/v0.18.0...v0.18.1) — GitHub Connector

_Released on 2026-06-14_

### Added
*   **GitHub Connector Enhancements**:
    *   **Pull Request Operations**: Introduced new operations including `get_pr_diff` (fetching a PR's unified diff), `merge_pr`, `create_pr`, and `create_or_update_file` (committing a single file, supporting both creation and updates with automatic blob SHA lookup).
    *   **Expanded GitHub API Coverage**: Added comprehensive operations for:
        *   **Repositories**: `get_repo`, `list_branches`, `list_commits`, `list_forks`, `create_fork`, `list_stargazers`, `star_repo`, `unstar_repo`.
        *   **Issues**: `get_issue`, `update_issue`, `list_issue_comments`.
        *   **Pull Requests**: `get_pr`, `list_pr_files`, `update_pr`.
        *   **Releases**: `list_releases`, `get_latest_release`, `get_release`, `create_release`, `update_release`, `delete_release`.
        *   **Tags**: `list_tags`.
        *   **User**: `get_me`.
    *   **Health Check**: Integrated a token-based `HealthCheck` for the connector (`GET /user`), providing an "auth" `OpHealth` entry.
    *   **Documentation**: Added comprehensive documentation for the full operation set, health check, and required OAuth scopes.

### Changed
*   Renamed the `install-rtk-termux.sh` script.

---


## [v0.18.0](https://github.com/yogasw/wick/compare/v0.17.0...v0.18.0) — Core & Admin

_Released on 2026-06-14_

### Added

*   **Conversation UI rebuilt as a Svelte 5 SPA** — the session list and conversation thread (`/tools/agents/sessions` and `/tools/agents/sessions/{id}`) are now served by a self-contained Svelte 5 single-page application. Visible changes: an **Approvals** tab joins Conversation / Commands / Raw in the session header; agent turns with tool events show a **Show trace** toggle for lazy-loading the thinking + event stream without cluttering the thread; the conversation header shows an idle-countdown badge ("kill in Ns") during the idle-timeout window; the Projects landing page scoped to managed and custom projects is now integrated into the SPA; the composer reuses the full action row (provider/project selectors, bell, attachment). The server now exposes three JSON endpoints that back the SPA: `GET /api/sessions`, `GET /api/sessions/{id}/conversation`, `GET /api/sessions/{id}/meta`, and `GET /providers/options`.
*   **Workflow editor — replay-to-editor imports full run state** — the **Copy to editor** button now pins the run's trigger event payload alongside per-node status overlays and output pre-population. Every node inspector's INPUT dropdown gains an entry for the pinned event so `{{.Event.Payload.*}}` expressions resolve to the real run's data during an Execute step (n8n-style "retry with pinned input"). A **Unpin** action on the trigger OUTPUT pane clears the pinned payload. See [Canvas editor — Run timeline](./workflow/canvas#run-timeline).
*   **Workflow editor — per-expression preview table** — template fields that contain multiple `{{...}}` segments now show a breakdown table in the inspector preview: one row per expression with its rendered value or error, isolating a failing ref without blanking the combined output. Autocomplete now suggests `.Event.Payload.*`, `.Node.<label>.*`, `.Env.*`, and `.Trigger.*` paths from the live context, and a manual refresh button re-renders when upstream outputs change.
*   **Workflow editor — node rename cascades `{{.Node.<label>.…}}` refs** — renaming a node label in the inspector rewrites every reference to that label across all other nodes in the workflow automatically. A toast confirms how many references were updated.
*   **Batch template-test expressions endpoint** — `POST /api/workflows/template-test` now accepts an `expressions` array for a per-expression breakdown in one round-trip, replacing N parallel calls that previously triggered rate-limit 429 responses.
*   **Custom connector health check** — a definition can nominate one operation as a health probe (`health_op` + optional `health_expect` in `SourceMeta`). When set, every instance page shows a **Check Permissions** button and a status banner — same as built-in connectors. Healthy when the probe operation runs without error (HTTP 2xx / MCP non-error result) and, when `health_expect` is set, the response contains the expected substring. A failing probe system-disables every operation on that instance (single credential = whole connector verdict) until a passing check clears it. Set from the **Health check** block on the review / edit form. See [Custom connectors — Health check](/guide/custom-connectors#health-check).
*   **Session Workspace tab UX** — the Workspace rail tab on the session slide-over gains: count badge showing active session connectors; inline rename (pencil icon on each card); auto-generated default label when an instance is added; dirty-tracking per field so Save/Test send only edited values; Reset button that appears while edits are pending; single **Test** button that exercises the config currently on screen (live field values overlaid on stored config for the probe, never persisted). See [MCP — Workspace tab (UI)](/guide/mcp#workspace-tab-ui).
*   **Admin — Enhanced Tag Management**:
    *   `owner:` tags now display human-readable names (`display_name`) in pickers and chips, with resolutions for custom connectors and workflows.
    *   `owner:` tags are immutable and cannot be modified or deleted, enforced by `ErrOwnerTagImmutable` guard.
    *   Contextual filtering for `owner:` tags: hidden from the `/admin/tags` page, but visible in resource/connector pickers with display names.
    *   Orphaned `owner:` tags are automatically deleted.
*   **Admin — Agents Navigation Control**: The Agents navigation link now respects tool access control policies.

### Changed

*   **Template engine — `missingkey=zero`** — the workflow template engine switched from `missingkey=error` to `missingkey=zero`. A payload field that is simply absent (e.g. a webhook body without an `action` key) no longer fails the node — it renders the zero value (`<no value>` for map fields) and the run continues. Wrap optional fields with `{{ .Event.Payload.action | default "" }}` for a clean empty string.
*   **Session workspace discoverability** — `wick_list`, `wick_search`, and `wick_get` now accept an optional `session_id` argument. Passing it causes `wick_list` to include this session's `sw_…` workspace instances alongside regular connectors and return a `session_config_bases` array (connectors that can be cloned but haven't been added yet). `wick_search` now also matches workspace instances so a connector spun up for the session is findable. For `wick_get`, `session_id` is a separate argument — never append it to the connector id.
*   **Session instance status** — a workspace instance in `wick_list` / `wick_search` results reports `kind: "session"`. When its config is incomplete the status is `needs_setup_workspace` (distinct from a saved connector's `needs_setup`), directing the user to the session Workspace tab rather than the admin dashboard.
*   **`AllowSessionConfig` auto-on** — the per-instance *Allow per-session config override* toggle now defaults to enabled for any instance whose connector module declares the capability (e.g. httprest). No manual admin toggle required to make an eligible connector available for session cloning; admins can still turn individual rows off.

### Fixed

*   **Agent node `session: "new"` without `session_init`** — agent nodes with `session: "new"` (or any ad-hoc `wf_adhoc_<uuid>` session) no longer fail with "cannot find the path" when there is no `session_init` node upstream. The session directory is created automatically before the first turn.
*   **Execute step — clearer missing-upstream error** — when an Execute step on a node references `{{.Node.<label>.…}}` for a node that has no output yet, the error message now names the blocking node explicitly instead of surfacing a raw Go-template nil-pointer panic.

### Improved

*   **Binary Size Reduction**: Reduced the release binary size from 84MB to 55MB by stripping symbols and DWARF via `-s -w` in `LDFLAGS` and disabling Vite sourcemap output for SPA bundles.

---


## [v0.17.0](https://github.com/yogasw/wick/compare/v0.16.16...v0.17.0) — Connectors & Access Control

_Released on 2026-06-13_

### Added

*   **Custom Connectors** — build LLM-callable connectors from the admin UI, no Go code or redeploy. Three creation paths from **Connectors → + New connector**:
    *   **Paste a cURL** — deterministic parser splits the command into per-instance configs (base URL, secrets) and per-call inputs; an **AI tab** (shown when a structured-output provider is configured) extracts the same shape from `fetch()` snippets, Postman fragments, or prose. The AI tab gains a provider dropdown; the catalog of structured-output-capable provider instances resolves live. Creating a connector now follows a plan-then-confirm contract, with `def_schema` returning full draft reference (supported widgets, template syntax, validation, icon rules, categories, examples, and decision points) and `def_validate` dry-running a draft.
    *   **Connect an MCP server** (streamable HTTP) — one server = one connector. Every tool the server lists becomes an operation automatically; tools added upstream appear after a re-sync. Control the surface with an exclude list instead of an import picker. Auth schemes: `none`, `bearer`, `custom_header`, **`oauth`** (standard MCP authorization: discovery, dynamic client registration, PKCE browser login, RFC 8707 resource indicator, per-instance accounts with transparent token refresh, and generic OAuth2 code exchange with `TokenURL` and `ExtraParams` support), and **`sso`** (forwards the calling wick user as a signed JWT validated against `/.well-known/wick-pubkey.pem`).
    *   **Manual builder** — Meta → Configs → Operations stepper with Go `text/template` request recipes.
*   **Multi-instance custom connectors** — instances behave exactly like built-ins (`+ New row`, Duplicate, per-row credentials); no row is auto-created until you add one. Opt into "single instance only" per definition. See [Custom Connectors](./guide/custom-connectors).
*   **Ownership contract** — any approved user can create a custom connector; editing or deleting a definition is admin-or-creator only, and instance creators are marked with an `owner:` tag. The new `custom-connector` management connector exposes the same lifecycle as MCP operations (scoped per caller) so an agent can build connectors without the dashboard.
*   **Connection status & live catalog** — MCP definitions show a Connected/Disconnected chip, re-sync per instance (probes run under that instance's account), refresh their tool catalog lazily on `wick_get`, and connect in the background at startup behind the boot gate.
*   **Connector icons** — pick an emoji (emoji-mart picker, fully vendored) or paste an inline SVG / base64 image (32KB cap, rendered safely via `<img>`).
*   **Google Workspace connector** — one Google OAuth account now drives **Drive, Sheets, Docs, and Slides** through a single connector (`google_workspace`, 20 operations). The 8 Drive ops carry over from the old code-only `google_drive` connector, joined by 12 new ops: file creation (`create_doc`, `create_sheet` from CSV, `create_slides`), Sheets API v4 (`sheets_read_range`/`append_rows`/`update_range`/`clear_range`), Docs API v1 (`docs_append_text`/`replace_text`), and Slides API v1 (`slides_get_content`/`add_slide`/`duplicate_slide`). OAuth scopes expand to `drive`, `spreadsheets`, `documents`, `presentations`, `userinfo.email`; the health check probes the granted scopes and reports per-op availability. See [Google Workspace](./connectors/googleworkspace).
*   **Session workspace** (`wick_session_workspace` MCP tool + session Config tab) — spin up ephemeral connector instances scoped to one session: a private clone of a base connector (point it at staging, use a different key) that appears in `wick_list`/`wick_get`/`wick_execute` for that session only and is purged when it ends. The saved connector rows are never touched. Actions: `list` / `add` / `duplicate` / `configure` / `test` / `remove`. The agent creates blank instances and can open the fill modal, but the **user** types the config; secrets are stored under a system-only master key, decrypted only at execution time, and never returned to the agent. A connector is eligible only when its module declares `AllowSessionConfig` **and** an admin enables the per-instance toggle (the custom-connector definition carries the same `allow_session_config` flag). The Session Config tab in the agent slide-over now lists instances, allows adding from a base picker, editing/testing/duplicating/removing, with collapsible cards. See [Session workspace](./guide/mcp#session-workspace).
*   **`ask_user` multi-question wizard** — pass `questions[]` instead of a single `question` to collect multiple answers in one step-by-step modal. Each question has a `key`, `type` (`choice` / `multi` / `rank` / `dropdown` / `text` / `secret`), optional `options` with per-option `description`, `required`, `placeholder`, and `help`. Single-select options auto-advance; Enter also advances. Response is `{"values": {"key": "answer", ...}}`.
*   **`ask_user` from stdio** — an `askuser.sock` Unix socket bridges `ask_user` calls from stdio MCP processes (e.g. a spawned Codex agent) to the running server's web-UI modal without HTTP auth. The gate allowlist now includes `ask_user` and `wick_session_workspace` so they are never blocked at the hook level.
*   **Per-channel `ask_user_enabled`** — Slack, Telegram, and REST channels each gain an `ask_user_enabled` config field (default `false`). Web UI and interactive MCP clients use the global `AskUserMode` setting. See [AskUser policy](./guide/command-gate#askuser-policy).
*   **Attention notifications** — when an `ask_user` or `approval_request` SSE event fires while the session tab is in the background, wick plays a short two-tone chime and (with permission) fires a browser `Notification`. Audio and notification permission are unlocked on the first user gesture.
*   **Access control — App Owner tier & per-user isolation** — a new `is_owner` tier sits above admin: the first registered user is auto-promoted, `IsAdmin()` is true for both owners and admins, but only the App Owner can see every user's sessions. Non-admin users now see only their own sessions, projects, workflows, and skills in the agents UI; session, send, kill, and trace routes are gated by ownership and return **404** (not 403) when a session belongs to someone else. The same ownership check guards the `wick_session_info` and `wick_set_title` MCP tools. The `Providers` section of the UI is now restricted to admin-only access.
*   **Tag-based ownership for projects, workflows & skills** — each resource gets an `owner:{resourceID}` tag at create time, with `created_by` kept as an audit trail; sharing is done by assigning group tags. New admin pages — **`/admin/projects`**, **`/admin/workflows`**, and **`/admin/skills`** — expose a tag picker to manage ownership and sharing. Skills now persist in their own DB table with `created_by` stamped on upload. Personal projects are auto-created for each user on their first access and project lists are scoped to the user's owned projects.
*   **Per-user Slack channels** — each wick user can configure their own Slack bot credentials independently. Every non-owner user gets a separate `agent_channels` row (`user_id = <id>`); the App Owner row uses `user_id = NULL`. Saving a bot token hot-adds a new keyed registry instance immediately — no server restart required. Removing the token removes the instance. The Channels menu is now accessible to all logged-in users (admin gate removed).
*   Realtime session-title sync: `agentctl refresh_session` op + `PublishSessionMeta` SSE; `stdio set_title` relays to the daemon, which reloads the registry and pushes the new title to open tabs live.

### Changed

*   Connectors no longer auto-create their first instance at boot (single-instance/Fixed modules excepted) — rows are created explicitly via **+ New row** and deleted rows stay deleted across restarts.
*   Outbound MCP requests now emit a debug log trail (URL, RPC method, payload, response, latency) carrying the originating `request_id`.
*   **Gate / ask_user decoupled** — `GateEnabled` now controls only the `PreToolUse` command-gate hook. Turning the gate off no longer disables `ask_user`.
*   **`google_drive` → `google_workspace`** — the code-only Google Drive connector was replaced by the broader Google Workspace connector under a new key. Existing `google_drive` instance rows are orphaned on restart and must be re-created under the new key.
*   The custom connector builder and manager UI have been overhauled: A sticky action toolbar (Save/Disable/Delete top-right) is now present on the cURL and MCP edit pages, with auto-reload on save and inline "Saved ✓" feedback. Field rows are now labeled cards (Key/Type/Default/Description, Required/Secret switches, trash delete) that stack on mobile. A hideable Navigator (Jump list + JSON tabs) is now docked sticky on desktop and an off-canvas slide-over on mobile. Responsive fixes have been applied across the manager UI.

### Fixed

*   **Codex `model_instructions_file`** — the per-session `soul.md` preset was previously passed via the unknown key `instructions_files` (silently ignored by Codex), so the model never received the session identity block or wick rules. Fixed to use `model_instructions_file`. The file now lives in the per-session `.codex/` dir so concurrent sessions no longer clobber each other's preset.
*   **Connector access-control hardening** — toggling a connector operation, flipping its admin-only flag, and deleting a connector are now gated server-side (admin-or-owner, returning 403); writing a config field tagged `secret` likewise requires admin or the connector's owner. The OAuth `ConnectorAccount` schema separates `ExternalUserID` (provider-side) from `WickUserID` (wick platform), fixing session-ownership stamping for connections such as Slack. The `wick_skill_sync` MCP tool is restricted to admins.
*   The `allow_session_config` flag for custom connector definitions now correctly persists on edit.
*   Stale broadcaster keys are deleted when the last subscriber unsubscribes.
*   The buffer map is cleaned on slot release, and preempt goroutines are tracked in the wait group.
*   Background rescan goroutines are deduplicated using singleflight.
*   `WICK_STRICT_MCP` environment variable has been added to the server configuration.
*   Generic OAuth path: `response_type=code` is added to `ExtraParams`, and `GetUserIdentity` failures are handled correctly. Ignored errors in `uploadMultipart` for Google Workspace are fixed.
*   Session ownership checks (`ownsSession` helper) are now used consistently, and the `sessionProcesses` guard pattern is fixed.
*   `OwnerUserID` is now correctly set when creating a project via the UI.
*   The Agents link in the navigation is now hidden when the agents tool is not running.
*   `SetSkillStore` is now correctly wired at server startup.
*   `created_by` is now stamped in the database on skill upload, alongside the owner tag.
*   `addSlide` for Google Slides now uses actual API-assigned placeholder IDs for title/body text.
*   Interface maps in the registry are now copied using `maps.Copy` instead of manual loops.
*   Unused `loadChannelRows` removed; all callers now use `loadChannelRowsForUser`.
*   All Slack channel instances are now wired at boot; a break that skipped multi-instance wiring has been removed.
*   The `seedOwner` migration logic now promotes the oldest admin-role user to App Owner when `is_owner` is unset, handling pre-migration installs correctly.
*   Channel status, health, and lookup handlers now correctly resolve a user's own channel instance.
*   Admin's integration status is no longer leaked to unconfigured users; `HasAnyKeyed` guards per-user channel fallback.
*   The Channels menu is now visible to all users, not just admins, aligning with per-user channel configuration.
*   The `seedOwner` migration logic now promotes only the oldest user with the admin role to App Owner.

---


## [v0.16.16](https://github.com/yogasw/wick/compare/v0.16.15...v0.16.16) — TTY

_Released on 2026-06-12_

### Fixed
*   Resolved an issue where WebSocket upgrades for the terminal (tty) could fail when reverse proxies stripped the `upgrade` token from the `Connection` header. The system now correctly re-injects the `upgrade` token if `Upgrade: websocket` is present, ensuring successful terminal connections.

---


## [v0.16.15](https://github.com/yogasw/wick/compare/v0.16.14...v0.16.15) — Systemd & Phoenix

_Released on 2026-06-12_

### Added

*   **Daemon auto-enablement of systemd linger on headless installs**: The `service install` command now automatically enables systemd linger for the current user, ensuring the daemon and its child processes persist after SSH session logout on headless Linux servers. `systemdStatus` now reports the live linger state and provides the exact command if manual enablement is needed.
*   **Active Processes panel scoped to current session**: The "Active Processes" slide-over panel now filters processes to the current session only. It also displays "queued" requests (pre-PID) with a dequeue action, providing better visibility for accepted-but-not-yet-running requests.

### Improved

*   **Phoenix `get_span` surfaces tool catalog and span metadata**: The `get_span` function now returns four previously unexposed signals that were already on the wire:
    *   `tools`: The catalog of tools the model considered, including each tool's name, description (carrying selection preconditions), and raw parameter schema. This allows comparison with a message's `tool_calls` to understand why a model picked or ignored a tool.
    *   `invocation_parameters`: Model parameters such as `temperature`, `reasoning_effort`, and `tool_choice` (the redundant tools array is stripped).
    *   `metadata`: Passthrough of the producing application's span metadata, including `request_id`, `room_id`, `user_id`, `langgraph_node`, and `question_history_id`.
    *   `token_count *_details`: A breakdown of `cache_read` and `reasoning` tokens.

---


## [v0.16.14](https://github.com/yogasw/wick/compare/v0.16.13...v0.16.14) — Memory, Gate, Wick

_Released on 2026-06-12_

### Fixed

*   Fixed memory leak in stream broadcaster by deleting stale keys when the last subscriber unsubscribes.
*   Cleaned buffer map on slot release and tracked preempt goroutines to prevent resource leaks.
*   Deduplicated background rescan goroutines in provider using singleflight to prevent excessive resource usage.
*   Eliminated data race with `PartialText` by guarding `turnBuf` writes with a mutex.
*   Propagated request context to `PublishAndReload` and `ToggleAndReload` in workflows to ensure proper cancellation and timeout.
*   Prevented timer leak in `terminateProc` by replacing `time.After` with `NewTimer` and `Stop`.
*   Resolved agent deadlock by releasing agent mutex before stdin write in `send()` to prevent conflict with `drainPending`.

### Improved

*   **Gate auto-approves wick read-only / info MCP tools**: The `PreToolUse` gate binary now has a built-in always-allow list for wick's non-mutating tools — `wick_list`, `wick_search`, `wick_get`, `wick_info`, `wick_list_providers`, `wick_skill_list`, `wick_session_info`, and `wick_set_title`. These no longer trigger the per-tool approval prompt. `wick_execute` and `wick_skill_sync` remain gated.
*   **Title state in agent system prompt**: The "This session" identity block injected into every agent's system prompt now includes the session's current `title` and `title_custom` flag. The agent reads these from the prompt at spawn time instead of making a `wick_session_info` round-trip on every first turn.

---


## [v0.16.13](https://github.com/yogasw/wick/compare/v0.16.12...v0.16.13) — Daemon & Session Titles

_Released on 2026-06-12_

### Added

*   **Daemon Systemd Integration**: Daemon lifecycle commands (`start`, `stop`, `status`, `restart`) now delegate to `systemctl --user` when a systemd-user unit is installed and enabled (Linux/Termux only). This resolves conflicts between PID-file and systemd management, preventing double spawns and ensuring accurate status reporting. The daemon now self-registers its PID and spawn source (`systemd INVOCATION_ID`) on boot for an accurate PID file, reporting the origin (systemd / CLI). PID-file based management remains unchanged for Windows and macOS.
*   **`wick_session_info` MCP tool**: A read-only tool that returns an active session's `session_id`, `title`, `title_custom`, `origin`, `status`, and `project_id`. This allows agents to determine if a session already has an explicit title before attempting to set one.
*   **`wick_set_title` MCP tool**: Sets the session's sidebar label and marks `title_custom = true`. This prevents the auto-derived first-message label from overwriting a chosen title.
*   **`session.Meta.TitleCustom` flag**: A new boolean on session metadata. When `true`, the `setLabelIfEmpty` process skips the auto-label step, preserving any title set by a human or by an agent via `wick_set_title`.
*   **Auto-title via system prompt**: The immutable agent system prompt now instructs the agent to call `wick_session_info` at conversation start and set a short descriptive title using `wick_set_title` when `title_custom` is `false`. Titles set by a previous turn or by the user are left untouched.
*   **Session ID and Channel in System Prompt**: The system prompt now includes a "This session" identity block (`session_id` + `channel`) on every agent spawn. This ensures agents always have the necessary session context to utilize tools like `wick_session_info` and `wick_set_title`.
*   **Documentation**: Updated documentation for the new `wick_session_info` and `wick_set_title` MCP tools and the auto-title behavior.

---


## [v0.16.12](https://github.com/yogasw/wick/compare/v0.16.11...v0.16.12) — Boot Gate

_Released on 2026-06-12_

### Added

-   **Boot gate — "Booting…" holding page during restore**: To prevent an empty sidebar or broken session list and 404 errors during the asynchronous provider-storage boot restore, all HTTP requests (except `/health` and `/boot-status`) now receive an HTTP 503 page. This holding page displays a spinner and live phase label, auto-polls `GET /boot-status` every 1.5 seconds, and reloads automatically once the server reports readiness. The `/health` endpoint remains exempt, ensuring load balancer and Kubernetes readiness probes continue to succeed throughout the restore window. This robust gating mechanism ensures a consistent user experience during the boot process.
-   **`GET /boot-status`**: A new JSON endpoint (`{"ready":bool,"message":string}`) has been added to report whether the asynchronous boot restore has finished. This endpoint is used by the boot gate page and can also be consumed by external health checks that require a deeper "application ready" signal beyond the basic liveness `/health` check.
-   **Agents registry auto-reload after restore**: Upon completion of the boot restore process, the agents registry is now immediately rescanned from disk. This ensures that sessions and projects appear in the sidebar without delay, addressing a previous issue where the sidebar remained empty until the next restart due to the registry being scanned before restore had written data. Manual restores initiated from the Provider Storage UI (**Restore Now**, **Restore Selected**) also now trigger an immediate registry reload, preventing stale in-memory registry data.

---


## [v0.16.11](https://github.com/yogasw/wick/compare/v0.16.10...v0.16.11)

_Released on 2026-06-11_

### Fixed

-   **Provider storage boot restore drops most rows**: The `iterAll` function used GORM's `FindInBatches` with a custom `ORDER BY (provider_type, instance_name, rel_path)`. `FindInBatches` paginates using a primary-key cursor (`WHERE id > last_max_id`), which is only correct when the results are ordered by `id`. This custom ordering misaligned the cursor, causing iteration to stop silently after the first batch (a known issue, gorm #5027). This led to most rows being dropped during the restore process; for example, on one production instance, ~729 of 8440 rows were processed, and every wick session file was skipped, resulting in empty session directories after restart. The fix replaces `FindInBatches` with a plain `Rows()` cursor iterator, which streams every row in a single query pass. This ensures memory usage remains constant while safely allowing custom ordering by including `id` as a tie-breaker for stable results.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.16.10](https://github.com/yogasw/wick/compare/v0.16.9...v0.16.10) — Memory Management & Tree Repair

_Released on 2026-06-11_

### Added
*   Introduced `WICK_MEMORY_LIMIT` environment variable for the server, enabling an opt-in soft memory limit that prompts Go's garbage collector to return memory to the OS on constrained hosts, preventing RSS from being pinned at high-water marks.

### Improved
*   Optimized both boot-time and manual tree repair processes (`RepairProviderStorageTree` and `store.repairOrphans`) to no longer load `Content` blobs into memory. This drastically reduces transient memory allocations and overall RAM consumption during tree repair operations.
*   Refactored the boot-time provider storage tree repair, moving it out of `Migrate()` and into the `providersync` gate. It now runs in a background goroutine only when the sync job is enabled and `provider-storage` is in use, optimizing startup and resource utilization.
*   Consolidated the tree repair code paths for boot-time and manual UI operations, ensuring both use the same optimized, blob-free `RepairProviderStorageTree` implementation.

### Removed
*   Dropped several one-shot legacy migrations from the `Migrate()` function, simplifying the database migration process as these migrations are no longer necessary on current database schemas.

### Documentation
*   Added `WICK_MEMORY_LIMIT` to the `docs/reference/env-vars.md`.
*   Included changelog entries for the new soft memory limit and the `repairOrphans` memory optimization.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.16.9](https://github.com/yogasw/wick/compare/v0.16.7...v0.16.9) — Memory Optimization

_Released on 2026-06-11_

### Improved

- **Memory hardening — provider storage**: listing, explorer, retention, and purge queries no longer load file content blobs into memory. A new `size` column is persisted on write and backfilled on migrate, so the file-list endpoint reports sizes without fetching content. Folder-zip downloads fetch blobs individually by ID. Fixes an ~800 MiB memory peak on deployments with large backup corpora.
- **Bounded HTTP reads**: the `http` workflow node and the generic internal HTTP client now cap response buffering at **64 MiB** and return an error for larger responses. Previously these were unbounded `io.ReadAll` calls.
- **Webhook body cap**: inbound webhook trigger requests are now rejected with `413` if the body exceeds **10 MiB**.
- **Access-log middleware**: large or streaming request bodies (`multipart/form-data`, `application/octet-stream`, `text/event-stream`, or `Content-Length > 64 KiB`) are no longer buffered by the logger — the downstream handler reads them directly.
- **Opt-in pprof** (`WICK_PPROF=1`): set this env var to expose Go pprof endpoints on `127.0.0.1:6060` for heap/CPU profiling. Loopback-only; never exposed on the public listener.
- **Opt-in soft memory limit** (`WICK_MEMORY_LIMIT`): set to a size string (e.g. `1200MiB`, `2GiB`) to tell the Go GC to return memory to the OS aggressively on constrained hosts. Helps boot-time provider-storage restore not pin RSS at its high-water mark on small VMs. Off by default; independent of `GOMEMLIMIT`. See [Environment Variables ▶ WICK_MEMORY_LIMIT](./reference/env-vars#wick_memory_limit).
- **`repairOrphans` no longer loads file content**: the two `FindInBatches` passes in the provider-storage tree-repair path now omit the `Content` column, cutting peak memory during orphan repair for large corpora.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.16.7](https://github.com/yogasw/wick/compare/v0.16.6...v0.16.7) — Boot Restore & Observability

_Released on 2026-06-11_

### Improved
*   **Asynchronous Boot Restore:** Boot restore now runs in a goroutine, preventing server startup blockage for large source sets. The HTTP layer comes up immediately, and restore progress streams to logs.
*   **Enhanced Restore Observability:**
    *   Every restore-skip path logs its specific reason (e.g., environment kill switch, missing job row, disabled job) for easier diagnosis from logs alone.
    *   `RestoreAll` completion reports detailed metrics including `total_files`, `processed`, `restored`, `skipped_match`, `skipped_diverged`, and `skipped_uncovered` to explain the gap between total and restored files.
*   **Faster Force Boot Restore:** Force boot restore now skips identical files (based on hash match) instead of rewriting every file, making wide restores significantly faster.

### Fixed
*   **Manual Restore Overwrite:** Manual `Restore` operations now correctly use `RestoreAllForce` to overwrite diverged files, ensuring desired changes are applied instead of retaining disk copies.
*   **Error Handling:** `iterAll` batch errors are now properly returned instead of being swallowed.
*   **Postgres Compatibility:** `purgeExpired` now uses a Postgres-native interval query, resolving an error caused by the SQLite-only `datetime()` function on Postgres.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.16.6](https://github.com/yogasw/wick/compare/v0.16.5...v0.16.6)

_Released on 2026-06-11_

### Fixed
- **Agent node `max_turns` not cleared on reuse**: When a workflow reused an existing session and the agent node had `max_turns: 0` (unlimited), the previously-persisted cap from an earlier run was silently kept. The node now always writes the value — including `0` — so switching back to unlimited works correctly.

### Added
- **Bitbucket connector — PR review actions**: Three new destructive operations for acting on pull requests. These operations are `OpDestructive` (default off, admin opt-in):
    - `approve_pull_request`: Approve a PR as the authenticated user (idempotent).
    - `request_changes_pull_request`: Flag a PR as needing changes (mutually exclusive with approve).
    - `merge_pull_request`: Merge a PR into its destination branch, with optional `merge_strategy` (`merge_commit`, `squash`, `fast_forward`), `message`, and `close_source_branch`. This operation is irreversible.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.16.5](https://github.com/yogasw/wick/compare/v0.16.4...v0.16.5) — Provider Storage Sync

_Released on 2026-06-10_

### Changed
- **Provider Storage Sync is now opt-in**: The job ships disabled by default on fresh installs. Enable it from **Tools → Jobs → Provider Storage Sync → Settings → Enabled**.

### Added
- **`WICK_PROVIDERSYNC_DISABLE` env var**: Set to `true` to disable sync, boot restore, and the realtime watcher for a specific instance without touching the DB. Useful when multiple servers share one database. See [Provider Storage → Per-instance kill switch](./guide/provider-storage#per-instance-kill-switch) and [Environment Variables](./reference/env-vars#wick_providersync_disable).

### Fixed
- **Bounded boot restore memory**: `restoreAll` now iterates files in batches of 50 instead of loading the full set into memory, preventing OOM on large credential trees. Progress is logged at each percentage point.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.16.4](https://github.com/yogasw/wick/compare/v0.16.3...v0.16.4)

_Released on 2026-06-10_

### Improved

*   **Provider Sync Memory & Progress**: Optimized `providersync` restore operations for reduced memory usage and enhanced user feedback.
    *   Switched from `listAll` to `iterAll` with `FindInBatches(50)` to process content blobs in batches, significantly reducing peak memory consumption.
    *   Implemented `countAll` to accurately track the total number of files for restoration.
    *   Added detailed progress logging, displaying restore percentage updates (up to 100 lines) and clearly marking the start and completion of the process.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.16.3](https://github.com/yogasw/wick/compare/v0.16.2...v0.16.3) — AI Agents

_Released on 2026-06-10_

### Features
*   **Workflow Environment Management via MCP:** Introduced `workflow_env_get`, `workflow_env_set`, and `workflow_env_delete` operations for the workflow connector.
    *   Enables AI agents to read, update, and remove workflow environment variables without touching the UI.
    *   Secret values retrieved by `workflow_env_get` are masked as `••••••••`.
    *   When setting secret fields via `workflow_env_set`, callers must provide `wick_enc_` tokens.

### Fixed
*   **Provider Storage Sync:** The `provider-storage-sync` job is now disabled by default (`AutoEnable: false`) on new installations.
    *   This prevents the service from automatically activating unless explicitly enabled by the user.
    *   Boot restore and watcher startup for `provider-storage-sync` now correctly check if the job is enabled before initiating, ensuring resources are only utilized when intended.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.16.2](https://github.com/yogasw/wick/compare/v0.16.1...v0.16.2) — Sub-Agent Delegation & Installation

_Released on 2026-06-10_

### Improved
*   Refactored sub-agent delegation logic for simplicity and clarity.
*   Improved sub-agent configuration loading and validation.

### Fixed
*   Adjusted Dockerfile and entrypoint script to correctly set the working directory.
*   Corrected agent dependency installation within the sub-agent setup script.
*   Updated `requirements.txt` to include missing dependencies for core agents.

### Changed
*   Updated documentation links.

### Removed
*   Unused script files.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.16.1](https://github.com/yogasw/wick/compare/v0.16.0...v0.16.1) — Install Wrappers

_Released on 2026-06-10_

### Added
- Introduced thin `install.sh` and `install.ps1` wrappers for scaffolded projects, which delegate to Wick's canonical install scripts. This centralizes installation logic and ensures projects automatically benefit from script improvements without re-scaffolding.
- Enhanced Wick's canonical install scripts to honor `APP` and `REPO` environment variables, allowing them to function correctly both standalone and when invoked via the new wrappers.

### Fixed
- Updated the canonical install scripts to use the correct GitHub Pages URL.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.16.0](https://github.com/yogasw/wick/compare/v0.15.8...v0.16.0)

_Released on 2026-06-10_

### Added
- **VSCode-style diff editor**: The Source Control panel (`full` mode) now features an inline Monaco diff editor as the primary surface, with a file list on the left and the diff filling the remainder.
- **Auto-select first file**: Opening the SCM panel automatically loads the first changed file into the diff editor.
- **Unified diff by default**: Diffs now render in unified (inline) mode by default, with a toggle for side-by-side view in the diff header.
- **Hidden unchanged regions**: Unchanged lines are collapsed with an "N hidden lines" expand bar, matching VSCode behavior (3-line context).
- **Inline Stage / Unstage / Discard buttons**: Diff headers now include per-file Stage, Unstage, and Discard actions directly, removing the need to hover file rows.
- **Auto-show Save button**: When editing directly in the diff editor (without needing an "Edit" button), a Save button automatically appears when content changes, applied to both full-mode inline diff and the sidebar DiffModal.
- **Per-session active repository persistence**: The selected repository is saved to `localStorage` keyed by session ID and restored on subsequent opens.
- **Visible whitespace diff**: Trailing newline and whitespace differences are now shown in the diff (`ignoreTrimWhitespace: false`).
- **Wick MCP server pre-approval**: The entire Wick MCP server is now pre-approved for spawned agents using `--allowedTools mcp__wick`, eliminating the need for a static per-tool allowlist.
- **Workflow environment variables and secrets**: A new system for managing workflow environment variables and secrets via a dedicated Settings modal. Secrets are encrypted, decrypted at runtime, accessible via `{{.Env.KEY}}`, and masked in previews and outputs.
- **Workflow webhook trigger**: Implemented a webhook trigger with dual endpoints (`/webhook/` for published, `/webhook-test/` for draft), path-based routing, and a dedicated inspector in the UI.
- **`webhook_respond` node and `RespondMode`**: Added a `webhook_respond` node for custom HTTP responses (status, body, headers) from workflows and introduced `Trigger.RespondMode` (immediately/last_node/respond_node) to control webhook response behavior.
- **Phoenix connector**: A new read-only Phoenix connector for LLM span debugging, supporting listing spans by room/app and retrieving full span details.
- **Per-instance OAuth accounts for connectors**: OAuth app credentials are now configured per-instance, enabling multiple sub-accounts for a single connector. New access policy flags (MultiAccount, EnableSSO, AllowOthersConfigure, AllowOthersConnectSSO) and an owner tag system are introduced.
- **Admin user creation**: Admins can now create new user accounts directly from the Admin panel by entering an email, with a system-generated 5-word passphrase shown once for copy.

### Fixed
- **Repository without commits**: `git show HEAD:<path>` on a repository without any commits no longer returns a 400 error; an "invalid object name 'HEAD'" is now treated as an empty original side.
- **4xx request logging level**: HTTP middleware now logs 4xx responses at the `warn` level instead of `error`, reducing noise for expected client errors.
- **GORM `record not found` log noise**: GORM logger is set to `Silent`, preventing `record not found` queries from printing to stdout.
- **Diff not updating after save**: After saving a file, the diff editor now correctly shows the updated content.
- **Diff race condition on file select**: The Monaco diff editor is now mounted only after diff data is loaded, preventing empty-content rendering on first file selection.
- **Mobile autofocus**: Skipped autofocus on inputs for touch devices (mobile, tablet) to prevent the on-screen keyboard from popping up unexpectedly on page load. On desktop, the "Ask anything…" composer is now preferred over the search box when both are present.
- **Workflow publishing with deleted nodes**: Workflows with deleted scaffolded start/end nodes can now publish successfully, requiring only at least one trigger. Dangling graph entry references are now treated as warnings instead of publish-blocking errors.
- **UI theme issues**: Various UI theme fixes for dark mode hover states, status/kind chip selection, and toolbar buttons were implemented.
- **Multi-trigger dispatch**: Fixed a bug where only the first webhook trigger per workflow was dispatched correctly.
- **Slack connector token visibility**: The `bot_token` and `user_token` fields in Slack connector configuration are now always visible.

### Improved
- **Internal documentation structure**: Reorganized internal design documentation into status-based folders (`archive`, `todo`, `in-progress`) and updated all references.
- **Workflow documentation clarity**: Cleaned up internal comments and documentation, replacing `yaml` and file-based references with format-neutral descriptions to reflect DB-primary JSON storage for workflows.
- **Workflow data storage optimization**: Removed unused `yaml:"..."` struct tags and `MarshalYAML` methods from workflow types, as workflow storage is now DB-primary JSON.
- **Canvas experience**: Enhanced the workflow canvas with trigger validation badges, edge hover highlighting, and an "Open inspector" context menu option for nodes and triggers.
- **Connector operations management**: The operations section in connector detail pages now includes pagination, search, multi-select, and bulk Enable/Disable actions.
- **MCP connector/account listing**: The MCP `wick_list` command now supports filtering by `kind=connector|account` with composite IDs for account entries.

---
*This summary was automatically generated by Gemini AI*

---


### Per-instance OAuth accounts & MCP multi-identity

#### Added
- **Per-instance OAuth app credentials**: `ClientID` and `ClientSecret` moved from a shared server-wide setting to each connector instance's `Configs`. Every instance now carries its own OAuth app registration, so different instances can use different OAuth apps.
- **`ConnectorAccount` table**: connected OAuth accounts are stored as sub-records of a connector instance, not as duplicate rows. Each account stores `DisplayName`, `AccessToken`, and a `DisabledOps` JSON list of per-account op overrides.
- **Access policy flags on connector instances**: four new boolean fields control per-instance sharing:
  - `EnableSSO` — activates the "Connect Account" OAuth flow on this instance.
  - `MultiAccount` — when true, each user connect adds a new account entry; when false, reconnect replaces the existing token.
  - `AllowOthersConfigure` — non-admin users with tag access may edit credentials and settings.
  - `AllowOthersConnectSSO` — non-admin users with tag access may initiate the OAuth flow.
- **Owner tag**: every connector instance records an `owner:{rowID}` tag so the creating user retains access even when filter tags change.
- **Access Policy section** in the connector detail UI — surface for the four flags above.
- **Operations section redesign** — paginated op list with search, checkbox multi-select, and bulk enable/disable. Shared `OpsSection` component is reused across the detail page and per-account op views.
- **Per-account operation disable list** — each `ConnectorAccount` can carry a JSON list of op keys to disable. `wick_execute` with an account-scoped `tool_id` rejects those ops before reaching `ExecuteFunc`.
- **MCP `wick_list` — `kind` and `parent_id` fields**: every entry now includes `kind` (`"connector"` for a standard instance, `"account"` for a connected OAuth account) and `parent_id` (the connector row ID when `kind="account"`).
- **MCP `wick_get` — composite id**: accepts a `connectorID/accountID` composite id returned by `wick_list` account entries; tool IDs are scoped with an `@accountID` suffix when a specific account is targeted.
- **MCP `wick_execute` — account token injection**: `AccountID` is extracted from the composite `tool_id`; the selected account's `AccessToken` is injected as `user_token` / `auth_mode=user_token` before `ExecuteFunc` runs. Per-account disabled ops are enforced before execution.
- **Destructive op warning in MCP responses**: ops marked `OpDestructive` now append `⚠ DESTRUCTIVE: Always confirm with the user before executing this operation.` to their description in `wick_list`, `wick_search`, and `wick_get` results.
- **Slack connector**: `BotToken` and `UserToken` are now always visible in the admin form (removed `visible_when` conditional display). `ClientID` and `ClientSecret` are new per-instance fields for OAuth app setup used by the Connect Account button.

#### Changed
- **Destructive ops default ON**: `connector.OpDestructive` ops now default to `Enabled=true` on every new row (previously defaulted `false`). The LLM is responsible for confirming destructive intent with the user; the system-level default-off gate is removed.
- **`SystemDisabled` is advisory-only**: a health-check lock (`system_disabled=true`) no longer hard-blocks execution. If the admin has explicitly set `Enabled=true`, the call proceeds; the advisory is recorded in run history. Previously `SystemDisabled` was an irresistible gate.

---

### Phoenix connector, Webhook trigger & Canvas improvements

#### Added
- **Phoenix connector** (`phoenix`): built-in read-only connector for [Arize Phoenix](https://phoenix.arize.com/) LLM observability. Three operations — `list_spans_by_room` (list LLM spans for a conversation session), `list_spans_by_app` (list root spans by `metadata['app_id']`), and `get_span` (full detail: messages, tool calls, token usage, cost). Registered under the `Observability` tag. See [Phoenix connector](/connectors/phoenix).
- **Spawned-agent tool pre-approval widened to the whole wick MCP server**: agents now spawn with `--allowedTools mcp__wick` (server-level) instead of a static five-tool list, so `wick_manager_*` (and `wick_info`, `ask_user`, `wick_skill_*`, `wick_encrypt`/`wick_decrypt`) no longer hit the command gate's "always ask" prompt on the gated path. Not a security change — wick still enforces per-op access server-side; see [Wick Manager → Command gate & management ops](/connectors/wickmanager#command-gate-management-ops).
- **Workflow env & secrets**: per-workflow key-value environment variables, configurable via **⋮ → Settings** in the canvas editor. Values are accessible in every node template as `{{.Env.KEY}}`. Marking a var as **Secret** encrypts it at rest (`wick_cenc_` token in `workflows.env_values` DB column); the engine decrypts with a per-run cache so plaintext only lives in memory during execution.
- **Secret masking**: secret values are automatically masked as `••••••••` in template preview (`workflow_template_test`), execute-step output, SSE events, and stored run state. The mask is applied with the existing single-pass algorithm, with overlapping-secret protection.
- **Themed UI components**: `<Select>` dropdown and toolbar ⋮ more menu are now fully theme-aware with click-outside close. The ⋮ menu exposes the new Settings action alongside existing workflow actions.
- **Webhook trigger — dual endpoints**: every webhook trigger now gets two distinct HTTP endpoints. `POST /webhook/{wf_id}/{slug}` targets the **published** workflow (production traffic). `POST /webhook-test/{wf_id}/{slug}` targets the **draft** workflow for testing without publishing. Both URLs are shown side-by-side in the trigger inspector with copy buttons and a tabbed Test / Live preview.
- **Webhook trigger — slug-based path storage**: the trigger's `path` field now stores only the URL-safe slug (no leading slash, no `wf_id` prefix). The engine constructs the full request path at runtime, keeping trigger JSON portable across workflow IDs.
- **Webhook trigger — `respond_mode`**: new field controlling when and what the HTTP endpoint returns to the caller. `immediately` (default) returns `202 Accepted` at enqueue and runs async. `last_node` blocks until the workflow finishes (≤ 30 s) and returns the last node's JSON output with `200`. `respond_node` blocks until a `webhook_respond` node fires, then returns that node's custom status, body, and headers. Both blocking modes time out with `504` after 30 seconds.
- **`webhook_respond` node**: new node type (`type: webhook_respond`) that sends a custom HTTP response back to the webhook caller. Fields: `respond_status` (int, default 200), `respond_body` (Go template string), `respond_headers` (map, values are template-rendered). Active only when the firing trigger has `respond_mode: respond_node`; acts as a no-op pass-through in all other modes so the workflow still validates cleanly. The first node to complete in a run wins; subsequent ones are ignored.
- **Publish-time respond_node validation**: publishing a workflow with `respond_mode: respond_node` on a webhook trigger but no reachable `webhook_respond` node from the trigger's `entry_node` now raises a Warning in the Validation panel. The publish still proceeds; at runtime the caller receives `502 Bad Gateway` if no respond node runs.
- **Canvas validation badges on trigger cards**: trigger cards now show inline warning badges when a trigger has a configuration issue detectable at validation time (e.g. `respond_mode: respond_node` with no reachable respond node). Validation also runs on canvas load.
- **Canvas edge hover highlight**: hovering an edge in the canvas highlights it for easier graph tracing.
- **Canvas context menu — "Open inspector"**: right-clicking a node now includes an "Open inspector" option alongside delete, opening the inspector panel directly.

#### Fixed
- **Webhook multi-trigger dispatch**: workflows with more than one webhook trigger (different slugs) now correctly dispatch each inbound call to its matching trigger and entry node. Previously, the dispatch loop keyed candidates on workflow ID alone, causing all but one trigger to be silently dropped.
- **Webhook entry-node routing**: each webhook trigger routes to its own entry node; prior to this fix every inbound webhook always started at the first entry node regardless of which trigger matched.
- **Publish enabled-flag preservation**: publishing a workflow no longer resets the enabled/disabled flag set before the publish action.
- **Theme / dark-mode fixes**: executions panel, toolbar, and history tab now render correctly in all themes including dark mode.

---

## [v0.15.8](https://github.com/yogasw/wick/compare/v0.15.7...v0.15.8)

_Released on 2026-06-07_

### Added
- **Workflow fixed-mode template guard**: Publishing a workflow now fails with an error (previously a warning) if any field with `arg_modes` set to `fixed` contains a Go template (`{{...}}`), preventing the silent shipment of broken URLs, bodies, or prompts. Draft saving is unaffected.
- **Workflow auto-switch to Expression**: The workflow canvas editor automatically switches a fixed field to expression mode when the user types `{{` into it.
- **Workflow `wick:"mode=fixed|expression"` config tag**: Connector and channel op authors can use this new tag to lock the Fixed ⇄ Expression toggle for a schema field, greying out the toggle in the editor. The toggle now renders on every field kind in the inspector.
- **Wick Manager top-level MCP tools**: The `wickmanager` connector's operations are now surfaced directly in `tools/list` as `wick_manager_<op>` tools (e.g., `wick_manager_app_list`). This allows LLM clients to discover and call manager operations without the `wick_list` → `wick_get` → `wick_execute` discovery cycle. These tools work over both stdio and the Streamable HTTP/SSE transport. To avoid double-exposure, `wickmanager` is excluded from `wick_list` and `wick_search`.

### Fixed
- **MCP SSE transport dispatch**: Tools such as `wick_info`, `ask_user`, `wick_list_providers`, `wick_skill_list`, and `wick_skill_sync` now work correctly over the Streamable HTTP/SSE transport. Previously, they were advertised in `tools/list` but returned "unknown tool" on `tools/call`. The SSE dispatcher now delegates all non-streaming tools to the canonical handler, ensuring identical behavior across all transports.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.15.7](https://github.com/yogasw/wick/compare/v0.15.6...v0.15.7)

_Released on 2026-06-07_

### Fixed
-   **Chat Layout**:
    -   Implemented a FullBleed layout for the session/chat page, resolving issues with incorrect padding, misaligned headers, and the composer sliding under the keyboard.
    -   Adjusted the agents shell to use `h-[100svh]` for improved composer positioning, preventing it from being cut off on first paint before the dynamic viewport stabilizes.
-   **Netboot Setup**: Ensured `netboot.Setup()` is properly executed for the released `wick-agent` binary by integrating its call into `postgres.NewGORM`. This prevents dead-code elimination and guarantees that DNS/CA fallback is correctly initialized, addressing issues particularly observed on Termux.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.15.6](https://github.com/yogasw/wick/compare/v0.15.5...v0.15.6)

_Released on 2026-06-07_

### Added
- Source Control panel now renders as a full-screen overlay on mobile and retains pin/resize behavior on desktop.
- Native DNS and CA fallback implemented for Android/Termux, resolving network and SSL certificate issues for the pure-Go binary.

### Fixed
- Termux/Android DNS and CA fallback now actually runs in the released `wick-agent` binary. The previous fix (#589) wired `netboot.Setup()` only into the in-repo entry points; Go's internal-package rule prevented the separate `wick-agent` wrapper module from importing it, so the fallback was dead-code-eliminated from the shipped binary. Setup is now called inside `postgres.NewGORM()` — a chokepoint every entry point reaches before the first DNS lookup — and is guarded by `sync.Once` so it runs exactly once.
- Empty Slack pings (bare `@bot` mentions) are now normalized, ensuring the agent greets the user instead of stalling.
- System-turn pills (e.g., `[Slack thread context]`) are now responsive on mobile, wrapping text and fitting within container width to prevent overflow.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.15.5](https://github.com/yogasw/wick/compare/v0.15.4...v0.15.5) — AI Agents

_Released on 2026-06-07_

### Added
- Wire agent-node `max_turns` to `claude --max-turns` for limiting agent conversation turns.
- Per-app PWA name and session cookie support to prevent collisions when co-hosting instances on different ports.
- One-click re-run functionality for past workflow runs, using the original trigger event.
- Source Control (git) panel for agent sessions, offering status, diff, stage/unstage, commit, discard, branch management, and history viewing.

### Fixed
- Pre-approve Wick MCP meta tools for headless agent spawns and surface agent error subtypes in `result(is_error)` events.
- Agents can now read their own skill bundle files located under `~/.claude/skills`.
- Improved agent-node failure diagnosis by logging subprocess exit code and stderr tail on abnormal exit.
- Self-heal stale agent `--resume` sessions by clearing the `cli_session_id` if the conversation is not found.
- Ensured agent session's current working directory (CWD) remains stable after a conversation exists to prevent resume failures.
- Resolved toolbar dropdowns clipping on mobile and desktop in workflows.
- Admin navigation tabs now wrap on mobile viewports instead of overflowing.
- Admin data tables now stack into responsive cards on mobile viewports, including avatar dropdown visibility.
- Enabled Tailwind's `hoverOnlyWhenSupported` to apply hover styles only on hover-capable devices, preventing stuck hover states on touch screens.
- Prevented mobile sidebar burger from overlapping page content in agents and tidied workflows list display on mobile.
- Made the workflow Run tab usable on mobile by switching to a single-pane view (runs list OR run detail).
- Corrected transparent surfaces in light theme and ensured bottom tabs panel is visible in the workflow editor.
- Stopped auto-adding `ALLOWED_ORIGINS` to `~/.bashrc` during installation to prevent unintentional host allowlist widening.

---
*This summary was automatically generated by Gemini AI*

---

### Native DNS + CA fallback for Android/Termux

#### Added
- The wick binary now configures DNS and TLS automatically on Termux/Android — no proot or manual `SSL_CERT_FILE` required. When `/etc/resolv.conf` has no usable nameserver, a pure-Go fallback resolver is installed using `$PREFIX/etc/resolv.conf`, Android system properties (`net.dns1`/`net.dns2`), or public defaults (1.1.1.1, 8.8.8.8) — in that order. A new `WICK_DNS_SERVERS` env var (comma/space-separated) overrides all of these. TLS: when `$PREFIX` is set and no system cert store exists, `SSL_CERT_FILE` is pointed at `$PREFIX/etc/tls/cert.pem`. Both are no-ops on normal Linux/macOS hosts.

---

### Slack empty-ping fix & session-context pill responsiveness

#### Fixed
- Slack connector: a bare `@bot` mention with no message text no longer leaves the agent stalled. The empty turn is normalized to a short greeting instruction so the agent responds naturally.
- Session-context pill (the injected system-turn badge in the session view) now wraps long text and is width-capped, preventing overflow on mobile viewports.

---

### Source Control panel for agent sessions

#### Added
- **Source Control** panel on the session detail page (`/tools/agents/sessions/<id>`): a docked, pinnable VSCode-style SCM sidebar. Features: multi-repo discovery of all git repos inside the session cwd; tree or list view for staged / unstaged changes (toggle persisted in `localStorage`); per-file stage (+), unstage (−), and discard (↺) actions (discard is destructive — confirms before running `git restore` / `git clean`); one-line commit input; branch dropdown with checkout and create+checkout; Pull / Push buttons with ahead/behind count; Monaco diff editor modal (git-correct diffs for staged, unstaged, and untracked files); commit History tab with per-commit file list and diffs; edit + save from the diff modal. Pin state and panel width (240–640 px, default 260 px) persist in `localStorage`. Live updates via a server-side fs watcher that pushes `git_status` SSE events — the Changes section and th e **Source** rail tab badge refresh with zero polling. Backend: new Go package `internal/agents/scm/`; endpoints under `/tools/agents/api/sessions/{id}/git/*`. Requires `git` on `PATH`.

---

### Workflow run re-run

#### Added
- One-click **re-run** button in the workflow run detail panel. Re-fires the current draft with the original run's trigger payload (same input, fresh timestamp). Endpoint: `POST /api/workflows/runs/{id}/{runID}/rerun`. The UI jumps to the newly created run immediately after firing.

---

### Admin & Workflow Mobile Responsiveness

#### Fixed
- Admin nav tabs wrap to a second row on narrow screens instead of overflowing.
- Admin data tables (connector instances, users, tools, jobs) and the connector-detail operations table render as stacked cards below 768 px via new `.resp-table` / `.resp-table-wrap` CSS utilities.
- Hover styles are now only emitted for devices that support hover (`hoverOnlyWhenSupported`), preventing buttons from staying stuck in `:hover` state after a tap on touch screens.
- Agents content area and the workflows SPA header receive top padding on mobile so the fixed sidebar burger no longer overlaps page titles.
- Workflows list search input is now fluid and the count label no longer wraps; card metadata is hidden on very small screens to avoid overflow.
- Workflow Run tab renders a single-pane list-or-detail view on mobile with a Back button; the run-detail Nodes/Events grid collapses to a single column below md.

---

## [v0.15.4](https://github.com/yogasw/wick/compare/v0.15.3...v0.15.4) — Mobile & Workflow

_Released on 2026-06-06_

### Added
- Enhanced Canvas interaction for touch devices: enabled one-finger touch pan, two-finger pinch-zoom, and node dragging via `touch-action:none`. Tap-to-add functionality from the Palette with nodes dropping at viewport center. Connection ports are now visible on coarse/no-hover pointers.
- Optimized Node/Trigger detail modals for mobile, collapsing to a single pane with an Input/Editor/Output switcher and near-fullscreen display.
- Restored drawer behavior for the Agents sidebar on short/landscape-phone viewports.
- Implemented progressive disclosure for the Toolbar on mobile, folding Save/Discard/Unpublish into a "More" menu and hiding secondary chips/History.
- Synchronized PWA `theme-color` with the active theme (all 12 themes) via runtime CSS variable sync.

### Fixed
- Resolved 403 errors for internal agent MCP connections by exempting `/mcp` requests from loopback hosts (`127.0.0.1`, `::1`, `localhost`) in the `hostAllowlistHandler`.
- Corrected workflow version history display and functionality:
    - Added JSON tags to `entity.WorkflowVersion` for correct field serialization (id, workflow_id, kind, body, message, created_by, created_at).
    - Enabled per-row deletion and "Clear all" functionality in the History tab, with auto-refresh after autosaves.
    - Integrated a reusable JSON diff viewer for comparing workflow versions, featuring changed-line highlighting, an All/Diff-only toggle, and scroll synchronization.
    - Prevented redundant workflow versions by having `SaveDraft` skip new snapshots when the body is identical to the last draft.
    - Introduced new API endpoints: `DELETE /api/workflows/versions/{id}/{versionID}` and `DELETE /api/workflows/versions/{id}`.
    - Adjusted autosave debounce from 800ms to 2000ms.

### Improved
- Replaced the free-text "Workspace override" input on the `session_init` node inspector with a dropdown populated from existing projects, preventing runtime errors from invalid project IDs.

---
*This summary was automatically generated by Gemini AI*

---


### Agent fixes & workflow version history

#### Added
- Delete a single history snapshot via the trash button in the History list.
- **Clear all** button removes every snapshot for the current workflow (with confirmation).
- History list auto-refreshes after each autosave.
- New REST endpoints: `DELETE /api/workflows/versions/{id}/{versionID}` (single snapshot) and `DELETE /api/workflows/versions/{id}` (all snapshots).
- Version compare now renders a real colored diff with an "All / Diff only" toggle; the same `JsonDiff` component is reused by the JSON preview tab.

#### Fixed
- Agent-node `max_turns` is now wired to Claude's `--max-turns` flag. Previously the field was stored but never forwarded to the subprocess, making it a silent no-op. `0` continues to mean unlimited (provider default).
- Spawned Claude agents no longer stall on an interactive permission prompt for wick's own MCP tools. All five meta-tools (`wick_list`, `wick_search`, `wick_get`, `wick_execute`, `wick_list_providers`) are pre-approved via `--allowedTools` at spawn time.
- Spawned Claude agents can now read skill files in `~/.claude/skills/` (and the matching `~/.codex/skills/`, `~/.gemini/skills/`, `~/.agents/skills/` paths). Claude spawns with `--add-dir ~/.claude/skills` when the directory exists; the system-prompt path table carves out `skills/**` as read-allowed while the rest of `~/.claude/**` stays denied.
- Persistent (workflow_global) sessions now self-heal a stale `--resume` ID. When Claude exits with "No conversation found" the pool clears the stored CLI session ID so the next spawn starts fresh instead of retrying a dead ID.
- Project backfill is skipped once a session already has a CLI conversation, preventing a cwd change from breaking `--resume`.
- Agent subprocess failures are now diagnosable: exit code and a stderr tail are logged on abnormal exit, and an `error_during_execution` result subtype is no longer surfaced as a blank "agent error: " message.
- Workflow version history (History tab) now correctly receives `id`, `kind`, `message`, `created_at`, and `body` fields; a missing JSON serialization on the entity caused the tab to display empty rows.

#### Changed
- Autosave debounce raised from 800 ms to 2 s.
- Identical-body autosaves no longer create a new draft snapshot (dedup).

#### Improved
- Workflow editor: `session_init` node "Workspace override" field is now a dropdown populated from existing projects instead of a free-text input, preventing the `ensure session: project not found` runtime error caused by typing a non-existent project ID.

---

## [v0.15.3](https://github.com/yogasw/wick/compare/v0.15.2...v0.15.3)

_Released on 2026-06-06_

### Added
- Automated crediting of contributors with `@mentions` in GitHub release bodies.
- Enhanced release preparation to paginate commit lists and extract GitHub `@login` handles for contributors.
- Updated release creation to include a "Contributors" section in the release body.

### Fixed
- Resolved HTTP 500 errors for the `GET /mcp` SSE handler by correctly probing and using `http.Flusher` support via the middleware Unwrap chain with `http.NewResponseController`.
- Corrected the `tools/call` SSE path, which was silently falling back to JSON due to the same `http.Flusher` detection issue.
- Added a regression test to ensure `GET /mcp` properly opens SSE streams through `Unwrap`-only writers.

### Improved
- Documentation for workflow/agent loopback MCP access and connection troubleshooting.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.15.2](https://github.com/yogasw/wick/compare/v0.15.1...v0.15.2) — AI Agents

_Released on 2026-06-06_

### Added
- Implemented a `doc-sync` subagent with a `PreToolUse(Bash)` hook to keep `docs/` synced with code changes before pull requests.
- Introduced a per-provider `SendMode` configuration (default, append, queue, spawn) for agent instances.
- Added a cross-session `Active Processes` panel (accessible from session view and providers page) for real-time visibility into provider, PID, queued count, and agent lifecycle.
- Integrated `internal/processctl` for canonical PID liveness checks across Unix and Windows.

### Fixed
- Resolved an issue where spawned Claude agents could not fully connect due to incomplete MCP Streamable HTTP transport, by implementing GET and DELETE methods for `/mcp` and setting an `Mcp-Session-Id` header during initialization.
- Addressed a double-spawn bug in Codex agents by ensuring the agent slot follows the agent, not the process, and is not released prematurely on turn-end or internal respawn kills.
- Corrected `Stop()` behavior for respawn-mode agents between turns to ensure proper slot release.
- Updated `ReconcileDead` to correctly skip respawn-mode agents when they are idle between turns.
- Wired Codex `stdin` to the null device to prevent hangs on Windows.
- Resolved a data race in `capacity_test` by injecting provider capacity directly instead of relying on asynchronous `provider.Save` operations.

### Improved
- Relaxed Claude spawn behavior to drop `--strict-mcp-config` by default, merging the Wick server with existing MCP servers, while offering `WICK_STRICT_MCP` for isolation.
- Updated the contributing guide to include details about the `doc-sync` workflow.
- Synchronized public documentation with `v0.15.1` features, including `transform` (jq engine), `slack` (channel-node workflow actions), `workflow` (import/publish), `workflow/state` (run detail/delete), `branch/classify`, and the `bitbucket` connector.
- Documented `WICK_STRICT_MCP` and `WICK_DISABLE_SHARED_MCP` environment variables.
- Enhanced agent message handling by replacing the single-slot `pendingMsg` with a `pendingQueue` to process messages sent mid-turn in order.
- Implemented per-provider and global concurrency caps for agent pools, with 0 indicating unlimited capacity.
- Improved rendering of multi-line configuration descriptions in the management view by interpreting `\n` as real line breaks.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.15.1](https://github.com/yogasw/wick/compare/v0.15.0...v0.15.1) — Workflow Enhancements

_Released on 2026-06-06_

### Added
- Import workflows from exported JSON files directly within the UI.
- Agent nodes now connect to the live MCP HTTP server over loopback, eliminating per-run cold-start latency and duplicate DB connections.
- Enhanced workflow execution management in the UI, including the ability to delete runs, view richer event and node details, and preview full run JSON.
- Bitbucket `create_pull_request_comment` operation now supports inline comments by specifying `inline_path` and `inline_to`/`inline_from` parameters.
- Workflow events API now supports `events_limit` for tailing the last N events, returns total and truncated counts, and evicts the index cache upon workflow deletion.
- Transform nodes now include a `jq` engine for advanced JSON processing using `gojq`.
- Slack workflow action nodes are fully wired to the live Slack API, supporting all 12 action operations (e.g., send message, reply thread, open modal).
- The workflow canvas now allows setting branch and classify node edge labels, enabling correct routing configuration from the UI.
- The in-app lifecycle chime now correctly primes the AudioContext on the first user gesture, ensuring sound plays on macOS Chrome.

### Fixed
- Improved robustness for skill .zip imports, correctly handling various archiving layouts from different operating systems and tools, and filtering junk entries.
- Publishing workflows or MCP/connectors now triggers an immediate hot-reload of the router, ensuring live runs use the freshly published definitions.
- Agent node skill validation no longer incorrectly rejects skills that are actually available.
- Agent nodes now incorporate `timeout_sec` and `require_status` contracts, preventing silent successes when agents stall or fail to produce a final status.
- The workflow enable/disable toggle now takes effect immediately without creating an unintended draft.
- Encryption masking now uses a single-pass algorithm, fixes an overlapping-secret leak, and improves performance.
- The in-app lifecycle card chime no longer remains silent on macOS Chrome due to WebAudio autoplay policy by calling `AudioContext.resume()` from a user gesture.
- The transform node's "jsonpath" engine now explicitly errors when a query is invalid or not applied, instead of silently returning the original input.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.15.0](https://github.com/yogasw/wick/compare/v0.14.25...v0.15.0) — PWA Notifications

_Released on 2026-06-05_

### Added
- Implemented PWA push notifications for agent lifecycle events.
- Introduced agent auto-start functionality after installation.
- Split conversation trace into per-turn index and lazy-loaded event files.
- Added `TraceEventInlineKB` and `TraceEventMaxKB` agent configurations to control trace event storage.
- Created new API endpoints (`GET /sessions/:id/turns/:turn_id` and `/events/:event_id`) for lazy fetching of conversation traces.
- Integrated UI lazy-loading of conversation traces when "show trace" is clicked.
- Added an `Interrupted` field to `ConversationTurn` to distinguish kill-truncated turns from text-cap truncated turns.
- Included a Copy button for fenced code blocks in the markdown renderer.
- Implemented per-session subscription for agent lifecycle notifications, allowing users to opt-in for specific sessions.
- Added a bell affordance on queue rows to subscribe to session notifications.
- Introduced a pre-subscribe bell on the new-session composer for immediate subscription upon session creation.

### Fixed
- Resolved token-too-long errors in `ReadJSONL` for large conversation files by replacing `bufio.Scanner` with `json.Decoder`.
- Updated tests to reflect the trace split storage refactor.
- Ensured `proot` is always installed on Termux, not only when codex is present, for broader compatibility.
- Corrected a race condition where lifecycle push notifications fired before the assistant turn was appended to `conversation.jsonl`.
- Eliminated duplicate notifications by immediately closing OS notifications when the Wick application is open and displaying an in-app card.

### Improved
- Clarified notification connector copy, account state messages, and subscription removal UI.
- Synchronized the workflow registry via an observer hook to ensure all connectors are registered regardless of order.
- Separated `server_mcp.go` to distinguish lifecycle management for stdio and HTTP server paths.
- Changed notification operations to no longer be marked as destructive.
- Enhanced push notification banner UX: now hidden when subscribed, uses a soft CTA card for initial prompts, shows a persistent warning for denied permissions, and uses floating toasts for transient feedback.
- Refined notification bell icon and adjusted layout to prevent page content pushing.
- Optimized placement and behavior of the notification bell, now anchored to the chat composer's top-right corner for better context.
- Modified lifecycle pushes to fire only on "idle" state transitions, reducing notification noise.
- Enriched push notification bodies with a preview of the agent's actual response.
- Introduced in-app rich cards to display push notifications when Wick is open and focused.
- Added an audible chime for in-app notification cards.
- Updated the notification bell's 'setup' state to directly prompt for browser permission inline, simplifying the user flow.
- Removed the `MaxAssistantTurnBytes` cap as text and events are now stored separately.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.25](https://github.com/yogasw/wick/compare/v0.14.24...v0.14.25)

_Released on 2026-06-04_

### Added
- Slack connector support for the `upload_file` operation using the Slack v2 three-step upload flow.

### Improved
- Optimized `providersync` performance by eliminating N+1 queries in folder pruning using a single subquery.
- Reduced peak memory usage in `providersync` by batch-fetching orphan repairs in chunks of 500.
- Batched subtree deletions in `providersync` to fetch children per BFS level instead of querying per individual node.
- Added a bounded glob regex cache with a maximum size of 512 and eviction to prevent unbounded memory growth.

### Fixed
- Resolved cross-platform matching issues in `globMatch` where Windows-style backslash patterns failed to match on Linux and macOS environments.
- Fixed a pool process termination bug where `Kill` failed on stale agent names by falling back to session prefix scanning.
- Fixed a 422 'no agent' error in channel sessions on `sendMessage` by implementing an `OnAgentAdded` callback to refresh the in-memory registry.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.24](https://github.com/yogasw/wick/compare/v0.14.23...v0.14.24)

_Released on 2026-06-04_

### Fixed
- Resolved a 404 routing error on the `/projects/new` endpoint by consolidating it into the `/projects/{id}` handler.
- Corrected SPA integration tests to pass proper context configuration.
- Fixed the launch configuration settings.

### Improved
- Replaced the plain-text fallback 404 error in tool handlers with the app's styled 404 page.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.23](https://github.com/yogasw/wick/compare/v0.14.22...v0.14.23) — Slack Integrations

_Released on 2026-06-04_

### Added
- Slack Socket Mode connection lifecycle tracking (connecting, connected, error, disconnected) and self-healing reconnection capabilities.
- Integration status panel for Slack channels displaying transport mode, subscription state, bot/workspace identity, and webhook URLs.
- Connection health probe to verify Slack credentials (signing secret and public URL) and report subscription states.
- New HTTP API endpoint (`/tools/agents/channels/{slug}/status`) to dynamically fetch channel integration status.

### Improved
- Refactored the Slack connector cache to securely share authenticated bot user IDs with the connector, ensuring consistent identity presentation in messages.

### Fixed
- Preserved scoped sidebar navigation when transitioning to the session detail page by utilizing the session's project ID.
- Fixed clipping of the picker dropdown inside the Manager UI card container by removing restrictive overflow styles.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.22](https://github.com/yogasw/wick/compare/v0.14.21...v0.14.22)

_Released on 2026-06-03_

### Fixed
- Guarded against OS PID reuse causing false `ErrAlreadyRunning` errors by validating that the running process executable matches the expected daemon binary.

### Improved
- Enhanced the `wick status` command to display HTTP status (`ok`/`unreachable`), surfacing instances where the daemon process is active but the HTTP server failed to bind the port.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.21](https://github.com/yogasw/wick/compare/v0.14.20...v0.14.21) — Agent Projects

_Released on 2026-06-03_

### Added
- Introduced **Projects** to replace the Workspace concept, featuring automatic boot-time migration, customizable folders, defaults, icons, names, and pinned sessions.
- Added a Claude-style project landing page, a sidebar Projects navigation section with pinning and drag-to-move capabilities, and a dedicated project settings page.
- Added support for per-user pinned projects that auto-scope the agents landing page.
- Added robust queue management capabilities, including search filters, select-all checkboxes, and a bulk "Kill selected" action in the queue panel.
- Added origin tracking (e.g., REST, Telegram) for sessions and spawns, displaying the session ID and channel source in the Recent Spawns table.

### Improved
- Bounded spawn logs to a maximum of 50 files, implementing automatic pruning on boot, on provider page loads, and on each new spawn.
- Enhanced the queue dequeue mechanism to clear all pending input and remove every queued entry for a session across all agents.
- Polished the Recent Spawns UI to remember its expanded/collapsed state via localStorage and format session IDs into a shorter, cleaner representation.
- Moved hidden-field filtering from templates to callers, making hidden channel configuration fields visible on channel config pages.

### Fixed
- Fixed queue cancellation failures caused by mismatches between the UI's agent name and the queued entry.
- Fixed hidden variable rendering issues on the admin variables page.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.20](https://github.com/yogasw/wick/compare/v0.14.19...v0.14.20) — Workflow Editor V2

_Released on 2026-06-03_

### Added
- Svelte 5-based Workflow Editor (V2) mounted as a SPA at `/workflows-v2/edit/{id}`, featuring an interactive canvas with SVG bezier edges, snap-to-align, marquee multi-select, and multi-drag capabilities.
- Database-backed workflow storage and backend APIs for managing drafts, versions, testing, execution, and validation.
- Schema-driven inspector forms for triggers, channels, and connectors, dynamically rendered from backend configuration schemas.
- Comprehensive testing framework featuring an inline assertion builder, test history tracking, and a JSON event test stager.
- Backend-owned palette structure via the `/api/workflows/palette` endpoint, enabling dynamic registration of executors, channels, and connectors without frontend modifications.
- Keyboard shortcuts for canvas operations, including saving (`Ctrl/Cmd+S`), canceling actions (`Esc`), and deleting nodes or triggers (`Delete`/`Backspace`).
- Dedicated LiveDiskFS development loop allowing local frontend changes to reflect instantly without binary recompilation when `WICK_DEV_REPO_ROOT` is enabled.

### Improved
- Refactored chat panels to remove assistant avatars, allowing message bubbles to occupy full width on mobile devices.
- Enhanced chat bubble headers to display the specific AI provider alongside the agent name (e.g., `{agent}.{providerName}`).
- Prevented the chat header from scrolling off-screen by locking viewport overflow on the Agents shell during session switches and reloads.
- Implemented auto-incrementing default node labels (e.g., `http_1`, `http_2`) to prevent immediate canvas validation conflicts upon node creation.
- Integrated a lazy-loaded Ace Code Editor for Go and Python script nodes, syncing with system light/dark themes and retaining scroll/cursor positions.
- Optimized CI pipelines by keying the `templ` cache on its pinned version in `go.mod` instead of `go.sum`, preventing redundant reinstalls.
- Generalized monorepo plumbing and release pipelines to support automated bundling of multiple frontend SPA packages.

### Fixed
- Resolved installer write failures (`ETXTBSY` - text file busy) on Linux and Termux by stopping any active agent binary prior to executing raw `curl` writes.
- Fixed a Windows binary resolution bug in `safeexec` by properly traversing `%PATHEXT%` for bare executable names.
- Fixed a canvas persistence bug where node drag positions were discarded during JSON/YAML serialization roundtrips.
- Fixed canvas deletion logic to ensure triggers are properly cleaned up alongside nodes when multi-selecting and deleting elements.

---
*This summary was automatically generated by Gemini AI*

---


### Workflows: DB-primary JSON

#### Added
- Version history panel with side-by-side compare. Pick two versions, the editor shows both bodies for diff.
- MCP ops: `workflow_lock` (canvas freeze), `workflow_guard` (standalone safety review), `workflow_versions` + `workflow_version_detail` + `workflow_restore_version`, `workflow_diff_versions`, `workflow_exec_node` (single-node execute).
- Test fixture ops gained name-only addressing — `workflow_save_test_case` / `workflow_list_test_cases` / `workflow_delete_test_case` no longer take file paths.

#### Changed
- Workflow body now lives in the database as JSON. Three tables: `workflows` (current state), `workflow_versions` (append-only history), `workflow_test_cases` (named test fixtures). YAML codec dropped; `parse.Parse` / `parse.Marshal` are JSON-only.
- Run state, run events, and env values stay on disk (`runs/<id>/state.json`, `events.jsonl`, `env.json`) — same place, JSON content.
- Workflow editor is the Svelte SPA at `/tools/agents/workflows/edit/<id>`. The legacy templ+Drawflow editor is removed.

#### Removed
- MCP ops: `workflow_read_file`, `workflow_write_file`, `workflow_list_files`, `workflow_delete_file`. Workflow body is not file-addressable anymore — use `workflow_get` and the dedicated edit ops (`workflow_add_node`, `workflow_set_triggers`, etc).
- `prompt_file: nodes/<file>.md` on agent and classify nodes. Use the inline `prompt` field; templates resolve against `.Event`, `.Node`, `.Trigger` as before.

---

## [v0.14.19](https://github.com/yogasw/wick/compare/v0.14.18...v0.14.19) — Mobile UX & PWA

_Released on 2026-06-01_

### Added
- Service worker and PNG/maskable icons to enable browser PWA installation prompts.
- Custom vector wrench icon to replace the standard emoji for consistent rendering across platforms.
- A new `bool`/`boolean` configuration widget that renders as a toggle switch.
- Split log files per component (app, server, worker, mcp) for the headless daemon.

### Improved
- Mobile chat experience by preventing auto-refocus after sending and allowing Enter to insert a newline on touch devices.
- Mobile layout behavior using `100dvh` and interactive-widget resizing to prevent the keyboard from pushing the header or composer off-screen.
- Chat bubble width for assistant messages on mobile screens to reduce cramping.
- App orientation flexibility by removing the forced "any" orientation lock in the manifest.
- Settings UI by migrating config options to the new toggle widget, applying conditional visibility to `startup_script`, and hiding internal configuration rows.

### Fixed
- Prevented the internal `admin_password_changed` flag from being accidentally toggled in the admin panel, which previously caused the default password to re-seed on boot.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.18](https://github.com/yogasw/wick/compare/v0.14.17...v0.14.18) — Termux & Attachments

_Released on 2026-05-31_

### Added
- File attachment support in the agent chat composer with features for drag-and-drop, clipboard pasting, upload limits (25 MiB/file), and an iframe-sandboxed preview modal.
- New `--host` and `--localhost` CLI flags (and `WICK_HOST` environment variable) to restrict the server's bind address.
- Support for sequential boot-time shell scripts via new `startup_script` and `startup_script_enabled` admin variables to facilitate vendor tunnel (e.g., ngrok) configuration.

### Fixed
- App-level `ALLOWED_ORIGINS` environment overrides failing to render in the kvlist configuration UI due to a JSON unmarshaling mismatch.
- Codex CLI handshake failures on Termux/Android by auto-installing `proot` and wrapping spawned processes with necessary host-file bind mounts.

### Improved
- Revamped documentation structure, landing page, and navigation to prioritize Wick Agent use cases and partition installation guides into platform-specific pages.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.17](https://github.com/yogasw/wick/compare/v0.14.16...v0.14.17)

_Released on 2026-05-30_

### Fixed
- Resolved a crash on Termux (Android kernels < 5.8) where the `gotty` subprocess would fail with a `SIGSYS` error during `LookPath` execution by passing the absolute shell path instead of a bare name.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.16](https://github.com/yogasw/wick/compare/v0.14.15...v0.14.16) — Termux Compatibility

_Released on 2026-05-30_

### Added
- Introduced `safeexec.Command` and `safeexec.CommandContext` wrappers to safely resolve binary paths without triggering `faccessat2`.
- Added an AST-walking unit test (`TestNoDirectOSExec`) to prevent future direct usage of `os/exec` functions across the codebase.

### Fixed
- Resolved a critical process crash (`SIGSYS`) on Termux/Android systems running kernels < 5.8 by routing all command executions through `safeexec`.
- Fixed a release asset naming mismatch for the gate sidecar (`wick-agent-gate`), resolving 404 download errors during installation.
- Corrected path resolution in `safeexec.LookPath` on Windows by properly recognizing backslash path separators.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.15](https://github.com/yogasw/wick/compare/v0.14.14...v0.14.15) — Agent Routing

_Released on 2026-05-30_

### Improved
- Tightened routing rules between Wick and other Model Context Protocol (MCP) connectors by moving them to the immutable configuration section.
- Implemented a read/write split fallback mechanism for Wick connector failures, requiring user confirmation for write operations to prevent unintended privilege escalation.
- Added specific error-handling paths for Wick routing, including retrying on server errors (5xx/timeouts) and halting on authentication failures (401/403) or gate denials.
- Simplified the Wick connector catalog header to act purely as a cold-start discovery hint.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.14](https://github.com/yogasw/wick/compare/v0.14.13...v0.14.14) — Agent Connectors

_Released on 2026-05-30_

### Added
- Injection of a ready-only connector catalog into the agent system prompt, providing models with available connector keys and descriptions to optimize tool selection.
- Initial credential banner display and log tracing hints when running the daemon start or restart commands in the background.

### Improved
- CLI installer experience by showing a curl progress bar during binary downloads and enforcing a 15-second timeout on GitHub API resolution.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.13](https://github.com/yogasw/wick/compare/v0.14.12...v0.14.13) — Idempotent Installer

_Released on 2026-05-30_

### Added
- Added a `version` subcommand and `-v` / `--version` flags to `wick-agent` and `wick-agent-gate` to facilitate version tracking and automated upgrades.
- Added documentation covering CLI version commands for both the main agent and gate sidecar binaries.

### Improved
- Enhanced the installer (`install.sh` and `install.ps1`) to skip downloads and execution when the already-installed binary matches the latest resolved version tag.
- Replaced silent curl downloads with a visible progress bar and added a 15-second timeout on GitHub API calls to prevent indefinite hangs on slow connections.
- Improved the Termux LAN installer to detect existing managed `ALLOWED_ORIGINS` in `~/.bashrc`, prompting users to keep, edit, or clear rather than force-reprompting on every run.

### Fixed
- Fixed version probing in the installer to run with redirected stdin, preventing syntax errors caused by child processes consuming script lines from `curl | sh`.
- Fixed version probe sequencing to prevent `gotty` from attempting to bind to port 8080 during version checks.
- Resolved an issue where gate binary verification failed and polluted the installer status table with hook-error JSON.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.12](https://github.com/yogasw/wick/compare/v0.14.11...v0.14.12) — Mobile & Headless Support

_Released on 2026-05-30_

### Added
- Daemon mode and background service management (`start`, `stop`, `status`, `restart`, and `service install/uninstall`) for headless hosts, supporting systemd (Linux), Termux (Android), and Windows.
- Progressive Web App (PWA) support with custom icons, theme-color configuration, Apple mobile web app tags, and standalone display mode.
- Dynamic PWA manifest handler that reflects custom configured application names and descriptions in browser-installed instances.
- Environment detection for headless hosts to prevent CLI execution from stalling when a graphical system tray is unavailable.

### Fixed
- Resolved `SIGSYS` system call crashes in Termux on older Linux/Android kernels (< 5.8) by routing executable lookups through a custom safe-execution layer instead of standard `exec.LookPath`.
- Fixed a 30-second stall during installation on virtual machines with unresolved hostnames by removing hardcoded `sudo` prefixes from the installer script.
- Fixed chat composer clipping on mobile browsers by locking the viewport styling to small viewport height (`100svh`).

### Improved
- Enhanced the installation script to probe existing component versions and skip redundant downloads when matching release versions are already installed.
- Redesigned the agent interface for mobile devices, introducing a collapsible navigation drawer, a two-tier collapsible "More" navigation group, and notch-safe top padding.
- Unified branding across the desktop system tray, Windows executable icon, and PWA assets using a consistent wrench logo.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.11](https://github.com/yogasw/wick/compare/v0.14.9...v0.14.11) — Network & Configs

_Released on 2026-05-29_

### Added
- Support for an `ALLOWED_ORIGINS` host allowlist configuration and environment variable to allow secure remote and LAN access alongside the canonical `APP_URL`.
- "Detect LAN URLs" utility in the Admin UI to automatically discover reachable RFC1918 IPv4 addresses and add them with one click.
- Dedicated CLI commands (`config list/get/set` and `config allowed-origins ...`) for managing application configuration and allowed origins from the terminal.
- Interactive LAN IP whitelisting and automated `gotty` web terminal installation in the setup script (`install.sh`).
- Automated bot attribution footer ("Sent using <@BotID>") appended to Slack connector `send_message` payloads using Block Kit.

### Improved
- Environment variables (`APP_URL` and `WICK_ENC_KEY`) now override database-stored configurations at read time, featuring read-only indicators and write protections in the Admin UI.

### Fixed
- Inline markdown rendering in agents and skills to require word boundaries for underscores, preventing snake_case identifiers from being incorrectly stripped or formatted as italics.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.9](https://github.com/yogasw/wick/compare/v0.14.8...v0.14.9) — Wick-Agent Rename

_Released on 2026-05-29_

### Added
- Standalone gate sidecar release asset, with automatic installation support for raw Linux and Termux environments to ensure end-to-end command gate functionality.

### Improved
- Renamed the runtime binary from `wick` to `wick-agent` to prevent naming conflicts with the `wick` CLI development tool.
- Updated installation scripts, CI configurations, and documentation to use the new `wick-agent` binary name and streamlined the first-run credential generation.

### Fixed
- Resolved a CI artifact upload issue where nested directories caused release uploads to fail, restoring missing assets like the raw `linux-arm64` binary.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.8](https://github.com/yogasw/wick/compare/v0.14.7...v0.14.8) — Connectors & Installation

_Released on 2026-05-28_

### Added
- Built-in Bitbucket and Loki connectors.
- Dedicated Connectors launcher and index page featuring search, category filtering, and connector instance status tracking.
- Floating "Jump to latest" button and `Ctrl+Down` shortcut in the agent chat interface.
- Universal `install.sh` and `install.ps1` scripts, automatically scaffolded into new projects via `wick init`.

### Improved
- Documentation for the connector HealthCheck hook, `OpHealth` contract, and system disabling model.
- CI workflows to upload raw Linux binaries alongside Debian packages to support dpkg-less environments.

### Fixed
- Chat panel auto-scroll behavior to avoid yanking the viewport down while users are actively reading history above.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.7](https://github.com/yogasw/wick/compare/v0.14.6...v0.14.7) — OpenAI REST API

_Released on 2026-05-25_

### Added
- Full OpenAI-compatible REST API surface, including the new `/integrations/rest/api/v1/openai/responses` and `/integrations/rest/api/v1/openai/models` endpoints.
- Live model validation against active providers, returning standard OpenAI-formatted 404 error responses for unknown model IDs.
- Per-session in-flight locks returning a 409 Conflict status on concurrent requests within the same conversation.
- Rewritten documentation panel featuring three dedicated tabs for Chat, Responses, and Models.

### Improved
- Standardized the chat completions endpoint path to `/integrations/rest/api/v1/openai/chat/completions`.
- Switched from `session_id` to the standard `conversation` key for tracking sessions across REST endpoints.
- Gated the agents settings page (`/tools/agents/settings`) and sidebar navigation link to admin users only to secure sensitive configuration data.
- Updated the REST developer guide and channels documentation table.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.6](https://github.com/yogasw/wick/compare/v0.14.4...v0.14.6)

_Released on 2026-05-25_

### Added
- Slack Canvas connector operations.

### Improved
- Support for clickable file paths in agent chat markdown. File paths within the current workspace session open directly in the preview/edit modal, while paths outside the workspace display a raw path popup with a copy option.

### Fixed
- Database compatibility issue where stuck job runs failed to recover on SQLite due to Postgres-specific interval SQL syntax.
- Issue where panic events or timed-out contexts prevented job status updates, ensuring cleanup tasks execute properly to prevent runs from remaining permanently stuck in a "running" state.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.4](https://github.com/yogasw/wick/compare/v0.14.3...v0.14.4) — Realtime Storage Sync

_Released on 2026-05-25_

### Added
- Real-time filesystem watcher based on `fsnotify` that monitors storage sources dynamically and updates kernel watch sets immediately upon configuration changes.
- Configurable watcher settings including `watcher_status` (enabled by default) and `watcher_debounce_ms` (defaults to 1000ms).

### Improved
- Redesigned the storage backup mechanism to stream files via `WalkDir` and `io.Copy` into constant-memory SHA-256 hashes, loading full contents only when database writes are required.
- Integrated debounce logic to collapse rapid editor save events into a single sync operation.
- Streamlined deletion handling by directly removing database rows on file removal or rename events.

### Fixed
- Resolved out-of-memory (OOM) container crashes on large directory trees by replacing the memory-intensive polling map serialization with streaming sync.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.3](https://github.com/yogasw/wick/compare/v0.14.2...v0.14.3) — AI Agents

_Released on 2026-05-25_

### Added
- Live text streaming and thinking deltas for Claude and Codex in the web UI.
- Backend-driven agent lifecycle state machine exposed via Server-Sent Events (SSE).
- Crash-recovery mechanism for active sessions using provider-agnostic `inflight.jsonl` logging.
- Collapsible Context file panel to view, edit, download, and preview (Markdown/HTML) files in the agent's working directory.
- Automatic chat composer focus triggered by typing anywhere on the session detail page.

### Fixed
- Incorrect dark-mode background rendering on trace cards.
- Double-broadcasting of agent exit events in the connection pool.

### Improved
- Agent documentation covering provider features, SSE channel schemas, and file sandbox security.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.2](https://github.com/yogasw/wick/compare/v0.14.1...v0.14.2) — MCP Improvements

_Released on 2026-05-23_

### Added
- `db_type` and `db_status` fields to the `wick_info` MCP tool to allow clients to securely monitor database connection status without exposing sensitive credentials.
- An environment variable allowlist (`WickEnvVars`) for MCP installation in Codex CLI to ensure runtime environment variables are preserved.

### Fixed
- Bug where the agent provider cache was not refreshed after switching providers.

### Improved
- Centralized database status derivation and consolidated `WickInfo` tests directly within the handlers package.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.1](https://github.com/yogasw/wick/compare/v0.14.0...v0.14.1) — Job & Skill Management

_Released on 2026-05-22_

### Added
- Job timeout configuration (`MaxTimeoutMin` field, defaulting to 30 minutes) with automatic context-based cancellation.
- Startup bootstrap routine to reset stuck job runs that have exceeded their timeout threshold.
- User interface option to configure `max_timeout_min` in job settings.
- Detailed tracking for provider storage sync jobs, displaying changed and skipped file counts per source.
- MCP handler modularization under a new `handlers/` subpackage, introducing `wick_skill_list` and `wick_skill_sync` tools.
- Skills Manager documentation in the agents guide.

### Fixed
- Test suite compilation by updating `SyncOne` caller signatures to match the new return structure.

### Improved
- Updated system prompts.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.14.0](https://github.com/yogasw/wick/compare/v0.13.5...v0.14.0) — Skills Explorer

_Released on 2026-05-22_

### Added
- Integrated a new `skillsync` package to mirror skill files across provider directories (`~/.claude/skills`, `~/.codex/skills`, `~/.gemini/skills`, and `~/.agents/skills`) using modification times instead of symlinks.
- Added a Skills explorer page in the agents sidebar with clickable rows, subfolder navigation, and a kebab menu for syncing, downloading, and deleting skills.
- Introduced provider-scoped views with a tab switcher to compare the same skill file across different providers.
- Added Markdown preview rendering on the skill file detail page.
- Added a `CancelJob` capability to the job manager via `POST /manager/jobs/{key}/cancel`.

### Improved
- Refactored the provider storage sync job to remove `RestoreAll` from the cron tick and added a 60-second hard timeout with error reporting.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.13.5](https://github.com/yogasw/wick/compare/v0.13.4...v0.13.5)

_Released on 2026-05-22_

### Fixed
- Fixed a workspace bootstrap fatal error that occurred when a workspace directory existed without a `meta.json` file, by aligning the duplicate check in `Create` to check for `WorkspaceMeta`.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.13.4](https://github.com/yogasw/wick/compare/v0.13.3...v0.13.4) — Agents & MCP Updates

_Released on 2026-05-22_

### Added
- Provider switching capabilities across channels using the `#provider` prefix command.
- An `agentctl` Unix socket for interacting with the running daemon pool from MCP stdio.
- Granular, per-instance sandbox modes (`read-only`, `workspace-write`, and `danger-full-access`) for Codex configurations.
- Verbose per-file logging toggle and run ID correlation for provider-storage sync and restore runs.
- A new `system_turn` SSE event and UI handler to append system turns during agent interactions.

### Fixed
- Missing builtin connectors (Slack, GitHub, and HTTP/REST) on downstream MCP stdio deployments by registering them prior to bootstrap.
- Hidden connectors requiring setup from showing up in `wick_list` and `wick_search`.
- Codex MCP auto-install config format errors, uninstall persistence issues, and re-installation behavior.
- Tool result tracking to correctly extract MCP result text from content arrays and forward it via SSE.
- Codex parser error handling, ensuring non-JSON lines are output as Thinking events instead of failing.

### Improved
- Split the immutable system prompt into distinct global, Claude-specific, and Codex-specific variants.
- Optimized provider-storage file syncs by performing pre-upsert hash checks to skip unchanged files.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.13.3](https://github.com/yogasw/wick/compare/v0.13.2...v0.13.3) — Interactive Workflow Builder

_Released on 2026-05-21_

### Added
- Added interactive inspector panels for branch, end, shell, transform, classify, and database query workflow nodes.
- Added click-to-add functionality for canvas nodes from the palette as a reliable alternative to drag-and-drop.
- Added interactive filter condition, row, and order-by builders, alongside table and column selectors, for datatable nodes.
- Added a `/stream/snapshot` JSON endpoint to replay agent lifecycle and trace events on page refresh.

### Fixed
- Fixed agent idle-kill timeout by pausing the idle timer during long-running tool executions.
- Fixed rendering of datatable nodes (which previously fell back to shell nodes) and resolved label and expression mode persistence issues.
- Fixed database persistence for `datatable_create` and `datatable_insert` by wiring the PgService to the MCP stdio service.
- Fixed provider storage boot restore and upload retag issues.
- Fixed canvas stacking context issues by adjusting the palette drawer position.

### Improved
- Overhauled the datatable node UI with grouped palette entries, a column combobox, and Fixed/Expression toggle previews.
- Improved agent trace replay on page refresh by utilizing SharedWorker to fetch current state snapshots.
- Streamlined the node palette by removing duplicate hardcoded entries in favor of registry-backed module registration.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.13.2](https://github.com/yogasw/wick/compare/v0.13.1...v0.13.2) — Provider Storage

_Released on 2026-05-20_

### Added
- UI actions to download individual files and download folders as .zip files.
- Support for dismissing modals via backdrop click or the Escape key.

### Fixed
- Storage boot sequence to overwrite disk from the database using `RestoreAllForce` and remove redundant `SyncAll` operations.
- Cron sync execution flow to run a guarded `RestoreAll` before `SyncOne` so missing files are refilled prior to capture.
- Upload routing to auto-retag files to the deepest covering enabled source, ensuring manual uploads are assigned to the correct provider and instance to maintain restorability.

### Improved
- Explorer upload modal to be contextual, displaying a target path banner and requiring only the filename.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.13.1](https://github.com/yogasw/wick/compare/v0.13.0...v0.13.1) — Data Tables

_Released on 2026-05-20_

### Added
- High-performance Data Tables system featuring a Postgres-backed JSONB schema, auto-incrementing row IDs, system columns, and 9 matching Model Context Protocol (MCP) operations.
- n8n-style spreadsheet grid UI for managing Data Tables, featuring column-sorting, a 10-operator filter popover, CSV import/export, and inline column management.
- Seven dedicated database nodes in the workflow canvas palette (`datatable_*`) for querying, counting, inserting, upserting, and deleting records.
- Split-bottom editor panel containing a dedicated "Validation" tab to display real-time Go template syntax check results.
- Server-Sent Events (SSE) reconnect status pill in the session header to visually surface EventSource connection states.

### Fixed
- Fixed binary builds to correctly compile the app name and version via LDFLAGS variables instead of falling back to "dev".
- Fixed an issue in manual triggers where saving a human-readable button caption overrode the unique validation slug label.
- Fixed drawflow canvas deletion clicks by exempting the `.drawflow-delete` X chip from the marquee background selection handler.
- Fixed editor node metadata mapping to properly handle underscore-separated trigger names (e.g., `trigger_manual`).

### Improved
- Transitioned workflow node and trigger IDs to internal UUIDs while maintaining clean, cascading, user-facing label slugs.
- Replaced full-page reloads on workflow manual saves with smooth background POST requests that preserve canvas state, scroll positions, and SSE connections.
- Split the workflow validation pipeline into lenient draft saves and strict publish-blocking checks that highlight syntax errors in the Validation panel.
- Enhanced agent chat UI to automatically linkify URLs, apply overflow wrapping on long links, and systematically instruct AI models to wrap URLs in Markdown link syntax.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.13.0](https://github.com/yogasw/wick/compare/v0.12.2...v0.13.0) — Agent Workflows

_Released on 2026-05-19_

### Added
- Comprehensive workflow engine and canvas-based editor for agent orchestration.
- Support for multiple trigger types including Cron schedules, Webhooks, Slack events, and Manual execution.
- n8n-style debug modal featuring real-time SSE run progress, execution step isolation, and input/output inspection.
- Draft/Publish lifecycle allowing users to test unsaved changes before deploying to production.
- Run replay functionality to visualize historical executions and debug payload data directly in the editor.
- Interactive "Fixed/Expression" toggle for node arguments with drag-and-drop support from the input pane.
- Advanced canvas UX features: alignment snapping, marquee selection, multi-node dragging, and fit-to-view.
- Sharded run index for high-performance history lookup and storage.
- Multi-level palette drawer built dynamically from the live connector and channel registry.
- Inline-chip multi-select picker for trigger filtering with support for ID lookup and bulk pasting.

### Fixed
- Resolved an issue where trigger fan-out edges were lost during workflow save and reload cycles.
- Fixed a bug preventing the toggling of workflows that contained validation errors in their draft state.
- Closed three security bypass vectors in the gate loader: enforced socket guards, relative path scope resolution, and quote-aware command tokenization.
- Fixed UI overlap where empty state placeholders appeared on top of populated data panes.
- Corrected trigger hint labels on canvas cards to accurately reflect channel and event types.

### Improved
- Migrated the node argument inspector to use shared fieldtype widgets for consistent UI across the platform.
- Refactored the Slack integration into a modular, file-based architecture for better extensibility.
- Enhanced the workflow router with a trigger index for O(1) event dispatching.
- Optimized canvas rendering by moving the palette drawer to a transform-based overlay, preventing layout reflows.
- Structured documentation into a multi-part roadmap and design specification.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.13.0](https://github.com/yogasw/wick/compare/v0.12.2...v0.13.0) — Workflows

_Released on 2026-05-19_

### Added
- **Workflows** — multi-step YAML DAG automations under `<BaseDir>/workflows/<id>/`, with typed nodes (`classify`, `agent`, `connector`, `channel`, `http`, `shell`, `db_query`, `transform`, `go_script`, `branch`, `switch`, `parallel`, `merge`, `datatable_*`, `session_init`, `end`) and triggers (`cron`, `channel`, `webhook`, `manual`, `schedule_at`, `error`). See [Workflows guide](/workflow/).
- **Canvas editor** at `/tools/agents/workflows/<id>` — Drawflow-based visual editor with palette, per-field inspector reflected from each executor's `Describe()`, top-down auto-layout, marquee select, fit-to-view, node search (Ctrl+K), and a run timeline that replays each run node-by-node.
- **MCP workflow surface** — self-documenting catalog (`workflow_list`, `workflow_describe`, `workflow_node_types`, `workflow_node_detail`, `workflow_diagnose`, `workflow_watch`, `workflow_scaffold`, `workflow_connect`, `workflow_patch`, `workflow_delete`) so LLMs can author and inspect workflows over MCP.
- **Slack channel actions** as workflow nodes — `send_message`, `add_reaction`, `open_dm`, `open_modal`, `push_modal`, `update_modal`, `send_ephemeral`, `publish_home`, `respond_url`, `update_message` — plus typed event triggers (`event_message`, `event_app_mention`, `event_command`, `event_block_action`, `event_view_submission`, `event_shortcut`, `event_app_home_opened`, `event_view_closed`).
- **Gate umbrella policy** — `GateConfig` now carries `PermissionMode` (per-tool prompts) + `AskUserMode` (MCP `ask_user`) sub-policies. Master switch snaps both to their unguarded defaults when off. MCP `ask_user` short-circuits with a clean tool error when disabled instead of stalling the run. See [Command Gate ▶ Umbrella policy](/guide/command-gate#umbrella-policy).
- **Slack OAuth on the connector row** — global OAuth credentials moved to a Slack connector row with a "Connect with Slack" button; user-token auto-detected in `buildSessionContext`; DM via connector user token with signed footer.
- **Live agent streaming trace** — tool calls, thinking, and history are streamed into the session detail page in real time.
- **MCP provider UI** + session full-height layout + markdown table rendering.
- **Provider-storage sync** — exclude-mode rows, glob matcher, folder cascade, retention recompute, repair tree.
- **Loki push adapter** for async run events.
- **Self-updater + build metadata** baked into `wick_info`.

### Changed
- Workflow `slug` field renamed to `id` across the codebase.
- Channel config layer — manual field mapping replaced with `MapToStruct`; `decryptFn` callback removed from `GetChannelConfigMap`; `wick_cenc_` tokens decrypted in-place; channel configs hidden from settings page and encrypted at rest.
- Gate `AppName` derives from the binary stem only when it ends in `-gate`, otherwise the ldflag is the single source of truth.

### Fixed
- Slack bot replies no longer carry a duplicate signed footer (only `sendHandler` signs).
- Slack `cannot_dm_bot` — detect bot users before calling `conversations.open`.
- Slack: post with `xoxp` token without overriding the username, so the real user identity is preserved.
- Slack: auto-promote `U...` channel IDs to `target_user_id` for session headers; init `userTokenCache` and pre-build the token map at startup.
- Canvas: reject duplicate edges in `Connect`; guard against deleting the entry node.
- `wick_info` uses the baked app name instead of the cwd basename.
- Provider-storage: `strings.ReplaceAll` for cross-platform backslash normalisation.

### Migration notes
- The legacy `agents.bypass_permissions` checkbox is gone. Its value is one-shot migrated to `gate.permission_mode` at boot — no operator action required.

---

## [v0.12.2](https://github.com/yogasw/wick/compare/v0.12.1...v0.12.2) — Release Infrastructure

_Released on 2026-05-14_

### Added
- Automated self-updater configuration using baked-in repository metadata in the installer build.

### Fixed
- Build failure on darwin/arm64 by enabling CGO for systray Objective-C bindings.

### Improved
- CI pipeline visibility through per-target status gates and detailed run summaries for disabled targets.
- Release process robustness by allowing artifact attachment during partial-success matrix builds.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.12.1](https://github.com/yogasw/wick/compare/v0.12.0...v0.12.1) — Darwin Release

_Released on 2026-05-14_

### Fixed
- Resolved an issue with the release process for Darwin platforms.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.12.0](https://github.com/yogasw/wick/compare/v0.11.15...v0.12.0) — Agent Orchestration

_Released on 2026-05-14_

### Added
- Preset support for agent sessions, allowing system prompts to be loaded from local files on spawn.
- Global system prompt configuration to append organization-wide instructions to every agent preset.
- Idle subprocess preemption to immediately free pool slots for queued sessions when the agent pool is full.
- `all` CLI command to run the HTTP server and cron scheduler within a single process.
- Visual status indicators and badges to represent queued states in the sidebar and session list.
- Manual triggers for provider storage synchronization in the settings UI.

### Improved
- User messages are now persisted to disk immediately upon sending to ensure visibility while sessions are queued.
- Queue deduplication per session and agent to prevent redundant entries in the queue panel.
- Background preemption logic now retries every second while the queue is non-empty.
- UI layout for Settings and Channels pages expanded to full width with improved clickable surfaces for channel cards.
- Application name resolution in default system prompts to support correct file paths in branded builds.
- Automatic provider storage restoration from the database when booting standalone worker nodes.

### Fixed
- Database migration logic.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.15](https://github.com/yogasw/wick/compare/v0.11.14...v0.11.15)

_Released on 2026-05-13_

### Fixed
- Restored non-root user and home directory configuration in the Dockerfile.
- Fixed unit test failures.
- Resolved database migration issues.

### Improved
- Added inline documentation and annotations to Dockerfile build and runtime stages.
- Pinned the `wick` installation to the version specified in `go.mod` for more predictable builds.
- Refined sidecar naming logic by deriving the gate binary stem from the configuration output.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.14](https://github.com/yogasw/wick/compare/v0.11.13...v0.11.14)

_Released on 2026-05-13_

### Fixed
- Corrected content provider storage type handling.
- Resolved database migration issues.

### Improved
- Synchronized go.mod templates and documentation.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.13](https://github.com/yogasw/wick/compare/v0.11.12...v0.11.13)

_Released on 2026-05-13_

### Fixed
- Resolved build artifact generation issues.
- Reverted CI/CD workflow and Dockerfile configurations to restore build stability.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.12](https://github.com/yogasw/wick/compare/v0.11.11...v0.11.12)

_Released on 2026-05-13_

### Fixed
- Resolved issues with build artifacts.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.11](https://github.com/yogasw/wick/compare/v0.11.10...v0.11.11)

_Released on 2026-05-13_

### Fixed
- Resolved build artifact issues by removing unnecessary caching.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.10](https://github.com/yogasw/wick/compare/v0.11.9...v0.11.10)

_Released on 2026-05-13_

### Fixed
- Resolved issues with build artifact generation.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.9](https://github.com/yogasw/wick/compare/v0.11.8...v0.11.9)

_Released on 2026-05-13_

### Added
- New `wick init` command for project initialization.

### Fixed
- Issues related to build artifact generation.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.8](https://github.com/yogasw/wick/compare/v0.11.7...v0.11.8)

_Released on 2026-05-13_

### Fixed
- Build artifact generation issues.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.7](https://github.com/yogasw/wick/compare/v0.11.6...v0.11.7)

_Released on 2026-05-12_

### Improved
- Optimized the build release caching process.

### Fixed
- Resolved issues related to build artifacts.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.6](https://github.com/yogasw/wick/compare/v0.11.5...v0.11.6)

_Released on 2026-05-12_

### Fixed
- Added the Go bin directory to the PATH in the binary build process to ensure the `templ` tool is correctly located and accessible.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.5](https://github.com/yogasw/wick/compare/v0.11.4...v0.11.5) — CI/CD Optimization

_Released on 2026-05-12_

### Added
- New `ci-timing.sh` script for workflow performance and timing analysis.

### Fixed
- Issues with build artifact generation.

### Improved
- Optimized release workflows by building the wick CLI once and sharing it as an artifact across build-binaries and build-docker jobs.
- Implemented binary caching for wick and templ in CI and pull request test workflows.
- Added caching for wixl via apt cache to reduce workflow runtimes.
- Configured separate wick CLI caching for macOS jobs to support cgo runners.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.4](https://github.com/yogasw/wick/compare/v0.11.3...v0.11.4) — Agent Hosting

_Released on 2026-05-12_

### Added
- New "agents-only" quickstart guide covering system tray, headless modes, binary downloads, and Docker/Compose configurations.
- Comprehensive contribution guide including a commit style guide, build instructions, and repository structure mapping.
- Two-use-case framing to documentation to distinguish between the development framework and agent-host functionality.

### Fixed
- Issues with build artifact generation.

### Improved
- Documentation hero section and README tagline for better clarity on project use cases.
- VitePress sidebar and navigation organization to incorporate new agent-focused content.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.3](https://github.com/yogasw/wick/compare/v0.11.2...v0.11.3)

_Released on 2026-05-12_

### Fixed
- Corrected issues with build artifact generation.

### Improved
- Optimized CI pipeline to initialize projects from tags and share scaffolding via artifacts.
- Removed the mockery dependency.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.2](https://github.com/yogasw/wick/compare/v0.11.1...v0.11.2)

_Released on 2026-05-12_

### Fixed
- Resolved issue where `build-docker` and `build-binaries` jobs failed to checkout from the correct tag reference.
- Fixed build artifact generation process in CI workflows.

### Improved
- Optimized CI pipeline to run pull request tests only when Go or templ files are modified.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.1](https://github.com/yogasw/wick/compare/v0.11.0...v0.11.1)

_Released on 2026-05-12_

### Fixed
- Resolved an issue with build artifact generation.

### Improved
- Upgraded Go version to 1.25.0 in project templates.
- Removed Mockery dependency from the codebase.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.11.0](https://github.com/yogasw/wick/compare/v0.10.2...v0.11.0) — Storage & TTY Support

_Released on 2026-05-12_

### Added
- Provider Storage Manager tool featuring explorer and flat list modes, sync source auto-detection, and background synchronization jobs.
- New `providersync` package and database entities for provider storage and sources.
- Comprehensive documentation and user guides for Provider Storage and Web Terminal.
- Automated release-artifacts CI workflow for binary and Docker builds.
- Upgrade checks for Dockerfile wick versions with interactive update prompts.

### Fixed
- Connectivity issues related to TTY.
- Upgrade prompt behavior to default to "yes" `[Y/n]`.

### Improved
- Dockerfile runtime base switched to `debian:bookworm-slim` to support glibc requirements for the Claude CLI.
- Integrated Gotty cross-compiled binaries and Claude CLI into the standard Docker image.
- Added non-root `app` user with sudo privileges for secure credential management.
- Integrated `app-gate` sibling binary into the application runtime image.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.10.2](https://github.com/yogasw/wick/compare/v0.10.1...v0.10.2)

_Released on 2026-05-12_

### Added
- Hijack functionality.
- Additional logging for improved diagnostics.

### Fixed
- Unit test failures.

### Improved
- TTY handling and configuration.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.10.1](https://github.com/yogasw/wick/compare/v0.10.0...v0.10.1) — Command Gate Enhancements

_Released on 2026-05-12_

### Added
- Web Terminal tool with HandleRaw router support.
- Red and amber color tokens to UI for gate-related components and banners.
- Automatic injection of `.claude/settings.local.json` during workspace creation and switching.

### Fixed
- Command gate interactions including the X button, click-outside behavior, and Block button rendering.
- Logic for timeout auto-blocking and Telegram notification deletion on resolution.
- Workspace hook injection and GateBinLoader wiring in the server.

### Improved
- Expanded command gate interception scope to include Bash, file tools, and MCP tools using a catch-all matcher.
- Refined tool approval workflow with auto-allow logic for workspace-scoped file tools and interactive approval for unknown tools.
- Documentation for command gate intercept scope and approval mode labels.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.10.0](https://github.com/yogasw/wick/compare/v0.9.6...v0.10.0) — Channels & Slack Integration

_Released on 2026-05-12_

### Added
- OpenAI-compatible REST channel supporting both stateful and stateless sessions via Personal Access Tokens.
- Built-in Slack connector featuring 15 operations for message management, reactions, and lookups.
- Health check framework for connectors and integrations to validate API permissions and connectivity.
- SharedWorker-based SSE implementation to maintain persistent agent connections across page navigations.
- Searchable picker UI component for granular Slack entity selection (users, groups, and channels).
- Assistant API integration for Slack including "is thinking" status banners and thread-based activity signals.

### Improved
- Redesigned chat composer featuring an overlay style with inline provider and workspace switchers.
- Session performance through persistent cache probing and in-memory metadata label caching for faster sidebar rendering.
- Slack access control logic supporting complex whitelist combinations for users, groups, and channels.
- Channel management interface with theme-aware documentation, configuration cards, and sample code blocks.
- Refined Slack UX by deferring queued reactions and removing redundant status emojis.
- Session detail layout with approvals moved to a dedicated tab for better space utilization.

### Fixed
- Encrypted fields tool now defaults to private visibility to ensure authenticated access.
- Claude settings injection narrowed to project-scope to avoid affecting global machine configurations.
- Session history and provider dropdown loading delays.
- CSS layout issues causing scrollbar flashing on session detail pages.
- SSE connection timeouts by removing server-side deadlines and sending immediate connection headers.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.9.6](https://github.com/yogasw/wick/compare/v0.9.5...v0.9.6) — Slack Mention Trigger

_Released on 2026-05-11_

### Added
- Support for `app_mention` events in public and private Slack channels.
- The `app_mentions:read` scope to the application manifest.

### Improved
- Slack message dispatching logic to require explicit mentions in channels while maintaining direct passthrough for DMs.
- Automatic stripping of the `<@BOTID>` prefix from mention events before dispatching to the message handler.
- Application manifest configuration by replacing broad channel and group message event subscriptions with targeted app mentions.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.9.5](https://github.com/yogasw/wick/compare/v0.9.4...v0.9.5) — Multi-Provider Command Gate

_Released on 2026-05-11_

### Added
- Multi-provider support for Command Gate with per-provider capability detection for Claude, Codex, and Gemini.
- Global Command Gate master switch that cascades state changes to all configured provider instances.
- Per-instance hook configuration allowing granular opt-in/opt-out for tool execution gating.
- Asynchronous capability probing system with UI status badges for verified, testing, and unverified states.
- Support for `--probe-deny` and provider-specific flags in the gate command to verify capability layer enforcement.
- Capability registry for self-registering provider hooks and runtime probe verification.

### Fixed
- Enforced mutual exclusivity between Command Gate and Permission Bypass mode to prevent conflicting behaviors.
- Added missing runtime imports to ensure Codex and Gemini providers correctly self-register for capability lookups.
- Replaced native title tooltips with theme-aware custom tooltips that respect dark and light modes.
- Removed duplicated Command Gate descriptions across provider cards to clean up the interface.
- Resolved a regression where Claude would ignore deny envelopes when specific permission flags were set.

### Improved
- Implemented an in-memory instance cache for providers to eliminate redundant disk reads during agent spawning.
- Refactored the spawner factory to dispatch by ProviderType, enabling cleaner integration of future providers.
- Optimized CI workflows to skip PR tests when targeting the release branch.
- Updated technical documentation for Command Gate architecture, adapter patterns, and channel integration guides.
- Enhanced the provider UI to display "locked (bypass)" states when permissions are globally bypassed.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.9.4](https://github.com/yogasw/wick/compare/v0.9.3...v0.9.4) — Channels & Command Gate

_Released on 2026-05-10_

### Added
- Per-transport subpackages for Slack and Telegram integrations to improve modularity.
- New `agent-channel-module` skill to enhance agent capabilities.
- Diagnostic "Test gate" button on the Providers page to verify command gate enforcement.
- `--probe-deny` subcommand for the gate binary to detect contract drift in production.

### Fixed
- Command gate compatibility with Claude Code 2.1.138+ to prevent silent permission bypass.
- Issue where the gate failed to block sessions due to incorrect exit code and stdout handling.
- Sandbox blocking of approved tools by ensuring explicit allow signals are emitted to the permission system.
- Permission mode conflict where `--permission-mode bypassPermissions` was incorrectly forced when a gate was attached.

### Improved
- Refactored channel infrastructure into a modular root registry with dedicated subpackages.
- Streamlined transport registration and setup using a centralized composer.
- Enhanced context for new chat threads by injecting session-specific turns for first messages.
- Updated the Command Gate rejection contract to utilize exit-0 with specific JSON output for reliability.
- Renamed the Command Gate modal action from "Block" to "Reject" for better clarity.
- Migrated the Slack application manifest to JSON format.
- Updated documentation to reflect 2.1.x compatibility, failure modes, and new testing tools.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.9.3](https://github.com/yogasw/wick/compare/v0.9.2...v0.9.3)

_Released on 2026-05-10_

### Added
- GitHub Actions workflow to run unit tests on all pull requests.

### Fixed
- Crash in `wick.yml` tasks when using plain `run` blocks without background execution flags.
- Template version mismatch during build processes.
- Data races in the provider agent and pool worker logic identified during concurrent testing.
- Permission denied errors during test cleanup on Linux caused by read-only module cache files.
- Various broken unit tests across Slack channels, CLI doctor command, and gate manager integration.

### Improved
- Task execution now utilizes a real POSIX shell (or Bash on Windows) to support multi-line scripts, pipes, and command substitution.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.9.2](https://github.com/yogasw/wick/compare/v0.9.1...v0.9.2)

_Released on 2026-05-10_

### Fixed
- Resolved application crashes.
- Pinned templ CLI to the version specified in go.mod to ensure consistent release builds.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.9.1](https://github.com/yogasw/wick/compare/v0.9.0...v0.9.1) — AI Agents

_Released on 2026-05-10_

### Added
- Slack app manifest for the Wick agent bot.
- Comprehensive documentation for AI Agent workspaces, providers, channels, and pools.
- Detailed guide for Command Gate architecture, covering IPC, auditing, and the `wick doctor` utility.
- Reference documentation for gate sidecar bundling.

### Fixed
- Connector registration idempotency on `Meta.Key` to ensure stability across server restarts.
- Critical application crash in the core service.

### Improved
- Documentation structure to highlight AI Agent features across Slack, Telegram, and Web.
- Environment variable management, removing deprecated `GATE_` prefixes.
- Release workflow automation to synchronize headlines within the changelog.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.9.0](https://github.com/yogasw/wick/compare/v0.8.11...v0.9.0) — AI Agents

_Released on 2026-05-10_

The headline release: wick can now host **AI coding agents** — Claude, Codex, Gemini — as long-lived subprocesses, reachable from **Slack threads, Telegram chats, and the web UI** at the same time. Per-command [Command Gate](/guide/command-gate) intercepts every Bash call. Multi-instance providers (two PATs, side-by-side). Workspaces on disk. AskUser MCP tool. State persisted under `~/.<app>/agents/` — backup is `tar`, restart re-scans.

Plus: a generic HTTP connector, a GitHub connector, per-connector rate-limiting and per-operation access control, `wick doctor`, and a /metrics endpoint.

### Added — AI Agents subsystem

- **Multi-channel routing.** Slack (Socket Mode default + HTTP Event API), Telegram (long polling with inline-keyboard approvals), and the always-on web UI at `/tools/agents`. Each thread / chat / conversation = one wick session, automatically created on first message. See [AI Agents](/guide/agents) and [Channels](/guide/agents/channels).
- **Multi-session subprocess pool.** Slot cap (default 2), FIFO queue, idle-kill (default 120s), `--resume <cli_session_id>` revive. Per-session message buffer survives wick restart via `meta.PendingInput`. See [Pool & Sessions](/guide/agents/pool).
- **Workspaces.** Folders on disk used as the agent's `cwd` — managed at `~/.<app>/agents/workspaces/<name>/files/` or any custom absolute path. Multi-session sharing without locks. Built-in `default` workspace seeded on fresh install. See [Projects](/guide/agents/projects).
- **Multi-instance providers.** `claude/work` + `claude/personal` with different PATs. Per-instance binary override, extra args, env vars, disabled toggle. Persistent status cache with manual / 24h-stale / boot-prime rescan. Page render never blocks on `--version`. See [Providers](/guide/agents/providers).
- **Binary scan.** `--version` lookup walks registry → PATH → known install locations (npm prefixes, nvm, fnm, volta, asdf, Homebrew, MacPorts, Claude / Codex installer paths). Closes the gap between tray-launched wick (Explorer PATH) and shell PATH.
- **Console hiding on Windows.** Tray-spawned `claude.exe` / `codex.exe` / npm shims no longer flash a console window — `CREATE_NO_WINDOW` applied to provider probe + spawn paths.
- **Command Gate.** Sidecar binary `<app>-gate` intercepts every Bash command via Claude's `PreToolUse` hook. Whitelist via glob, escalate to interactive approval modal with **4 modes**: `approve_once` / `approve_session` / `approve_always` / `block`. Approval surfaces in whichever channel the conversation lives in (web modal, Slack approval message, Telegram inline keyboard). 25-second daemon deadline (under Claude's 30s hook timeout). See [Command Gate](/guide/command-gate).
- **Gate IPC.** Unix domain socket at `~/.<app>/agents/gate/gate.sock`, raw newline-delimited JSON, `chmod 0600`. Single shared spec / socket / audit log per app — daemon routes approvals to the right session by matching the hook's `cwd` against known workspace paths.
- **Gate audit.** Multi-stage entries to `~/.<app>/agents/gate/commands.jsonl` (`received` → `socket_dial` → `socket_sent` → `socket_recv` → `terminal`), all tied by `RequestID`. Plus a human-readable daily tail log at `~/.<app>/logs/gate-YYYY-MM-DD.log`.
- **Gate binary resolution — zero env vars.** Sibling-of-executable (`<app>-gate[.exe]` next to the main binary, shipped by `wick build --installer`) → embedded `//go:embed` extract → PATH. `WICK_GATE_BIN` / `GATE_BIN` / `WICK_GATE_SPEC` / `GATE_SPEC` all dropped.
- **Installer ships the gate sidecar.** Windows MSI ships `<App>-gate.exe`, Debian `.deb` ships `/usr/bin/<app>-gate`, macOS `.app` bundle ships `Contents/MacOS/<App>-gate`. Builder absorbs the gate compile step (no separate CI job); soft-skips on downstream forks without `cmd/gate/`.
- **AskUser MCP tool.** Agent-initiated mid-turn question — wick registers the question, broadcasts SSE, blocks the MCP call, surfaces an inline card in the web UI composer. Default 5min timeout. Works in pipe mode (`-p`) where Claude Code's harness `AskUserQuestion` doesn't.
- **Provider spawn log.** Per-spawn JSONL at `~/.<app>/agents/providers/spawns/<type>__<name>__<session>__<unix-ms>.jsonl` with `start` (PID, argv, binary, first user message) and `exit` events. `ls`-friendly filter without reading file bodies.
- **Slack channel.** Reaction lifecycle (⏳ → ⚙️ → ✅ / 🚫 / ❌), chunked replies at 3800 chars (under Slack's 4000 hard limit), access control (`everyone` / `users` / `groups`) checked per-message, hot-reload on 30s config poll, `pool.OnSessionCreated` hook so dashboards see new sessions immediately.
- **Telegram channel.** Long polling, dormant-mode on missing/invalid token, inline-keyboard approvals with short-token mapping (Telegram's 64-byte `callback_data` limit), edit-in-place approval message on resolve.
- **Meta-commands** intercepted in every channel before dispatch: `/agent <name>`, `/reset`, `/status`, `/dashboard` (`/link`), `/log`. Both `/` and `!` prefixes accepted.
- **`wick doctor [binary]` diagnostic.** Verifies environment + gate setup. Pass a binary path to inspect a specific branded build — derives its `AppName`, locates the matching `<app>-gate` sidecar, dials the socket with a probe request that auto-replies without bothering a human, verifies socket / spec paths align.
- **`AppName` single source of truth.** `internal/appname.Resolve()` is the only chain: `BuildAppName` ldflag → `wick.yml` `name:` → `"wick"`. `APP_NAME` env is now a display label only (`~/.<app>/` namespace stays slug-safe). Gate binary derives `<app>` at runtime from its own filename (strip `-gate[.exe]`), so a branded `wick-lab-gate.exe` lands in `~/.wick-lab/agents/gate/` automatically.

### Added — Connectors

- **Generic HTTP/REST connector** for calling JSON APIs with configurable authentication and methods.
- **GitHub connector** supporting repository listing, issue management, file retrieval, and pull request tracking.
- **Per-operation access control** restricting specific connector operations to administrators.
- **Sliding-window rate limiting** per connector with admin UI for quota tuning.
- **Cross-connector audit log API** and admin UI for monitoring run history and status across all connectors.
- **`/metrics` endpoint** (Prometheus-compatible) for connector execution telemetry and latency.
- **`pkg/conntest` helper package** to simplify unit testing for custom connector authors.

### Fixed

- **Updater reliability** on Windows and Linux: verify post-install state, use a detached helper script to swap binaries, prevent partial installs.
- **Agent pool race on Windows.** `markStatus(idle)` now runs **before** `releaseSlot` ([pool.go:378](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go#L378)) so a fast `Send` arriving right after `Active==0` cannot collide its meta.json write with the trailing idle write (two `os.Rename` to the same target). Killed flaky `TestPipeline_ResumeAfterIdleKill` + `TestQueueWhenPoolFull`.
- **Double-spawn / slot-count race.** Pool now tracks an in-flight `spawningKeys` set; concurrent `Send` calls cannot each see "slot free" and call `spawn` simultaneously. Same guard prevents two exit hooks from popping the queue at the same time.
- **SSE delivery & flush.** Switch to `ResponseController` for proper chunked flushing in the agent dashboard. Larger subscriber buffer + dropped-message logs.
- **Agent kill-on-respond + SSE timeout.** Lifecycle FSM now correctly distinguishes "still responding" from "idle" so the kill timer doesn't fire mid-response.
- **Bypass permissions logic.** `--permission-mode bypassPermissions` is now passed to Claude only when a gate is wired (`allowed_cmds` non-empty), preventing permission-less Slack sessions.
- **Gate hook injection.** Hooks injected via Claude's user `settings.json` (per-spawn `--settings` flag), with fail-open behavior when no socket is present so the agent can still start during gate setup.
- **Configs back-fill.** Empty config rows now back-fill from seed defaults instead of leaving the value blank — fixes "field is blank after upgrade" reports.
- **Server banner** now shows the configured `app_url` and logs host mismatches during 403 rejections (was logging the bare listen address).
- **Lab / CLI request logs.** Component-tagged logger correctly injected into the execution context — request lines no longer disappear in lab mode.

### Improved

- **Claude parser + spawner aligned with real Claude 2.1.x.** Verified against the live `claude` CLI's stream-json protocol and long-lived process lifecycle (multi-turn within one process, no respawn per message). Real-claude E2E test gated by `WICK_CLAUDE_E2E=1`.
- **Approval flow architecture.** Two patterns of approval are now both available: **system-intercept** (gate, mandatory) and **voluntary ask** (AskUser MCP tool). Wick uses gate for security enforcement and AskUser for UX questions the agent decides to ask.
- **Workspace model rewrite.** Replaced the project-centric model (1 project = 1 git repo, session = git worktree) with a workspace-centric model (folder shared across sessions, no git ops, custom paths supported). Fixes the "session without project fails to spawn" bug and matches how teams actually work — one folder full of stuff that several conversations touch.
- **Provider rename** from "backend" — `session.AgentEntry.Provider`, `pool.FactoryOptions.ProviderType/Name`, `userconfig.ProvidersConfig`. Single package `internal/agents/provider/` consolidating driver + spawner + per-instance config + spawn logger.
- **Registry split.** `RegisterBuiltins` (default-on agents tools) vs `RegisterLabSamples` (lab-only); `cmd/lab/` renamed to `cmd/wick-lab/`.
- **Multi-turn + multi-session integration tests** via simulated spawners; 91 tests across 21 packages green at release.
- **Design docs synced** to implementation for agent phases 1–7 (foundation, pool, gate, UI, providers, Slack, mid-session approval). Stage 9 follow-ups (env vars dropped, single shared spec/socket, installer-shipped sidecar) captured in [command-gate-architecture.md](https://github.com/yogasw/wick/blob/master/internal/planning/archive/command-gate-architecture.md).

### Migration

No DB migration required — `agent_channels`, `provider_statuses`, and the gate spec/audit files are auto-created on first boot.

If you ship a downstream branded build, **rebuild with the new `wick build`** so the installer ships `<app>-gate[.exe]` next to the main binary. Without it the gate falls back to embedded extract on first use, which still works but loses the installer-managed sibling location. There is no env-var override (`WICK_GATE_BIN` / `GATE_BIN` were removed).

---
*Curated for the v0.9.0 release.*

---


## [v0.8.11](https://github.com/yogasw/wick/compare/v0.8.10...v0.8.11)

_Released on 2026-05-07_

### Fixed
- Resolved an issue where the application version was incorrectly baked during builds by aligning CI environment variables with the expected build flags.

### Improved
- Updated installer filenames to include version, operating system, and architecture details for better visibility and management.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.8.10](https://github.com/yogasw/wick/compare/v0.8.9...v0.8.10)

_Released on 2026-05-07_

### Fixed
- Resolved an issue where the Windows updater failed to locate assets by switching from `.exe` to `.msi` format.
- Fixed Windows silent installation failures by utilizing `msiexec` to ensure the update properly overwrites the existing installation.

### Improved
- Added diagnostic logging for Windows updates, saving logs to `msiexec-install.log` in the cache directory to aid in troubleshooting.
- Updated the release template configuration.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.8.9](https://github.com/yogasw/wick/compare/v0.8.3...v0.8.9)

_Released on 2026-05-07_

### Added
- Secure first-boot flow requiring password rotation and email setup upon initial login.
- Automatic generation of a 5-word (CVCVC) admin passphrase stored in a secure local file.
- "wickmanager" built-in connector providing 24 operations for managing app, job, tool, and connector configurations.
- Host allowlist middleware to restrict HTTP requests to the configured application host.
- OS toast notifications triggered upon system tray launch.
- Auto-launch functionality for Windows systems after installation.
- Tray menu items for quick access to initial credentials and the server URL.
- Sensitive data redaction in logs for authentication and configuration endpoints.
- Dedicated `mcp.log` for auditing management plane activities.

### Fixed
- Log file initialization on Windows to prevent 0KB log files when running without a console.
- Windows pipe communication for MCP by replacing `-H=windowsgui` with dynamic console management, allowing 'mcp serve' to function correctly with external clients.

### Improved
- Migrated standard library logging to structured `zerolog` calls throughout the application.
- Enhanced logging strategy to prioritize file output to ensure logs are captured in GUI environments.
- Relocated application data storage to the user's home directory.
- Refined process management lifecycle for server and worker components.
- Build process feedback with status indicators for MSI and DMG packaging.
- Documentation for environment variables, system tray usage, and secure-by-default workflows.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.8.3](https://github.com/yogasw/wick/compare/v0.8.2...v0.8.3)

_Released on 2026-05-06_

### Added
- Installer-friendly artifact generation via the `--installer` flag, providing per-user `.msi` packages for Windows and `.dmg` drag-to-install images for macOS.
- Automatic detection of MSYS2 environments on Windows to auto-register `gemini-msys2`, `codex-msys2`, and `claude-code-msys2` configurations for MSYS2 shells.

### Improved
- Refactored MCP configuration to utilize a global `~/.claude.json` user config instead of project-specific `.mcp.json` variants.
- Standardized Windows installation paths to `%LocalAppData%\Programs` to ensure reliable self-updates and autostart functionality without requiring UAC elevation.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.8.2](https://github.com/yogasw/wick/compare/v0.8.1...v0.8.2)

_Released on 2026-05-06_

### Fixed
- GitHub updater behavior to treat 404 errors as "no releases yet" instead of a hard error.
- Build documentation.

### Improved
- Release workflow and GitHub Actions configuration by renaming environment variables to `RELEASE_*` prefixes to prevent reserved keyword collisions.
- Updater security by obfuscating embedded GitHub Personal Access Tokens (PAT) using XOR and base64.
- Logging architecture by splitting app, server, and worker logs into dated files and routing via `zerolog.Ctx(ctx)`.
- Log management functionality to open the logs directory instead of individual files to improve compatibility.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.8.1](https://github.com/yogasw/wick/compare/v0.8.0...v0.8.1)

_Released on 2026-05-06_

### Fixed
- Documentation build configuration.

### Improved
- Automated build processes.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.8.0](https://github.com/yogasw/wick/compare/v0.7.1...v0.8.0)

_Released on 2026-05-06_

### Added
- `AUTO_VERSION` repository variable to automate version bumping, tagging, and committing changes back to the repository during CI.
- `wick version next` subcommand to increment the last numeric segment of the `wick.yml` version.
- Windows executable metadata embedding, including brand icons, product descriptions, and version information.
- Automatic bundling of binaries into platform-native distributables: `.exe` for Windows, `.dmg` for macOS (host-only), and `.deb` for Linux.
- Support for multi-platform build targets using `--target`, `--goos`, `--goarch`, and `--all` flags.
- Native self-updater support for extracting binaries from `.dmg` and `.deb` packages.

### Fixed
- Issue where the application would fail to launch from Windows Explorer due to Cobra's automatic CLI mousetrap check.
- Console window flashing on Windows when triggering the "Open in editor" command.
- Port collision issues by implementing synchronous pre-flight checks and reflecting failures directly in the tray menu.

### Improved
- Consolidated application logs, databases, and configuration files into the platform-specific `UserConfigDir`.
- Redirected standard output and error streams to log files for Windows GUI builds to ensure diagnostic data is captured.
- Replaced the global TCP-based single-instance lock with per-app PID files to allow different Wick-built applications to run concurrently.
- Changed the default server behavior to opt-in, with `auto_start_server` now defaulting to false.
- Refactored build orchestration into platform-specific modules to support better maintainability and future package formats.
- Updated CI/CD release workflows and Docker multi-arch build templates to support native bundling.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.8.0](https://github.com/yogasw/wick/compare/v0.7.1...v0.8.0)

_Released on 2026-05-05_

### Added
- `AUTO_VERSION` repository variable support to automatically bump versions in `wick.yml` and commit changes back to the repository.
- `wick version next` subcommand to increment the last numeric segment of the application version.
- Windows executable resource embedding to include brand icons and file metadata such as FileDescription, ProductName, and Version.
- `--bundle` flag for `wick build` to generate native macOS `.app` bundles (including `Info.plist` and icons) and Linux `.deb` packages.
- Automatic bundle identifier derivation based on the `go.mod` module path.

### Fixed
- Issue where Windows binaries failed to launch from Explorer due to the default CLI double-click guard.
- Global single-instance lock conflict by replacing the fixed TCP port with a per-app PID file and liveness check.
- Console window "flash" when opening editors on Windows by suppressing the command wrapper window.
- Sample release workflow configuration.

### Improved
- Application data organization by consolidating logs, databases, and configuration files under a single `UserConfigDir` tree.
- Windows logging by piping `stdout` and `stderr` to log files, ensuring output is captured for GUI-only builds.
- System tray server management with synchronous port collision pre-flight checks and inline failure reporting in the menu.
- Sample Docker configuration and release YAML files.
- Documentation for CLI subcommands, single-instance locking mechanisms, and server default settings.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.7.1](https://github.com/yogasw/wick/compare/v0.7.0...v0.7.1)

_Released on 2026-05-05_

### Improved
- Automated the `generate` task (templ, tailwind, and go generate) to run during `wick build`, streamlining CI workflows and minimizing project configuration.
- Updated and synchronized documentation files.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.7.0](https://github.com/yogasw/wick/compare/v0.6.4...v0.7.0)

_Released on 2026-05-05_

### Added
- System tray architecture for desktop applications, replacing the previous GUI implementation with a lightweight tray-based control center.
- `wick build` subcommand to handle cross-compilation with automatic metadata injection for application name, version, and repository info.
- Integrated self-updater for desktop binaries with support for GitHub Release tracking, SHA256 verification, and stepwise UI feedback.
- OS-level autostart support for Windows (Registry), macOS (LaunchAgents), and Linux (XDG) via user-scoped configuration.
- Automatic SQLite database path resolution that prioritizes environment variables, user config, or local binary paths.
- CI/CD templates for GitHub Actions to automate version tagging and multi-platform release builds.
- Headless build tag support to compile binaries without system tray dependencies.
- Daily log rotation and retention management, storing logs in user cache directories.
- Stateful tray icons that provide visual feedback on server and worker status.
- "About" submenu in the tray displaying application version, framework version, commit hash, and build time.

### Improved
- SQLite concurrency performance by enabling Write-Ahead Logging (WAL) mode and `busy_timeout` settings.
- Default application port changed from 8080 to 9425.
- `wick init` process now automatically substitutes the project name into the generated `wick.yml`.
- Task execution now respects double quotes in commands, allowing complex `-ldflags` to be passed during builds.
- Single-instance lock mechanism using a local TCP port to prevent conflicting background processes.
- MCP server identification now advertises the downstream application version rather than the framework version.
- Expanded documentation for desktop tray architecture, build workflows, and environment variable references.

### Fixed
- Issue where task command parsing incorrectly split arguments containing spaces or quotes.
- Database connection failures when running desktop binaries from arbitrary working directories.
- Menu display errors where update status or PAT expiration feedback was not surfaced to the user.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.6.4](https://github.com/yogasw/wick/compare/v0.6.3...v0.6.4)

_Released on 2026-05-04_

### Fixed
- Bind Stdio MCP context to a real admin identity to enable decryption of tokens in the web UI.
- Resolved an error occurring during database migrations.
- Updated generated template files.

### Improved
- Refined internal application workflows.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.6.3](https://github.com/yogasw/wick/compare/v0.6.2...v0.6.3)

_Released on 2026-05-04_

### Added
- MCP instructions for wick_enc_.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.6.2](https://github.com/yogasw/wick/compare/v0.6.1...v0.6.2)

_Released on 2026-05-04_

### Improved
- Automated the registration of `encfields` for all consumers by moving the registration logic into the package initialization.

### Fixed
- Resolved an issue where consumer applications lacked the `/tools/encfields` route, which previously caused `wick_encrypt` and `wick_decrypt` MCP redirects to fail.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.6.1](https://github.com/yogasw/wick/compare/v0.6.0...v0.6.1)

_Released on 2026-05-03_

### Added
- At-rest encryption for configuration values tagged as secrets using a master-keyed system.
- Per-field metadata for connector credentials, allowing for individual tracking of field types, requirements, and descriptions.

### Fixed
- Potential plaintext credential leaks in connector error messages and audit logs through a centralized masking interface.
- Data leak paths where decrypted tokens could be passed into non-secret fields.

### Improved
- Connector configuration architecture, migrating from a single JSON blob to a normalized per-field storage schema for better queryability and performance.
- Database migration logic to automatically backfill legacy connector configurations into the new centralized configuration table.
- Secret field handling in the UI to allow keeping current values when input fields are left blank.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.6.0](https://github.com/yogasw/wick/compare/v0.5.6...v0.6.0)

_Released on 2026-05-03_

### Added
- Implemented an encrypted-fields layer using AES-256-GCM and per-user HKDF salts to secure credentials flowing between LLMs and connectors.
- Introduced `wick_enc_` tokens to ensure credentials issued for one user cannot be decrypted by another.
- Added automatic decryption of input configurations and masking of sensitive plaintext in connector responses and audit logs.
- Created a security-tagged tool at `/tools/encfields` for manual JSON-based encryption and decryption.
- Added `wick_encrypt` and `wick_decrypt` MCP tools that redirect to the UI for secure processing.
- Introduced the `encrypted-fields` skill, embedded into the binary for propagation via `wick skill sync`.
- Added `Mask` and `MaskIgnoreCase` methods to `connector.Ctx` to allow connectors to mask sensitive data dynamically.
- Integrated encryption key bootstrapping with support for auto-generation and `WICK_ENC_KEY` environment overrides.

### Improved
- Refined the encryption API by splitting `MaskSensitive` into `Mask` and `MaskIgnoreCase` to improve call site clarity and eliminate boolean traps.
- Updated the sample connector and `crudcrud` template to demonstrate response masking for secret keywords and ignore-case configurations.
- Enhanced system documentation, including a new reference page for encrypted fields and updated guides for MCP and connector modules.
- Updated `AGENTS.md` and skill labels to include and cross-link the new encryption capabilities.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.5.6](https://github.com/yogasw/wick/compare/v0.5.4...v0.5.6)

_Released on 2026-05-03_

### Added
- New `config-tags` skill as a standalone, single source of truth for configurations.
- Automatic injection of `config-tags` alongside `design-system` during `wick init`.
- Support for `config-tags` in `wick skill sync` and `wick skill list` commands.

### Fixed
- Issue where `wick:"default=..."` seed values were not applied when Go fields were zero.

### Improved
- Chart functionality with the implementation of limits.
- UI for default and secret fields, including masking set values with bullet characters.
- Module architecture by referencing the `config-tags` sibling folder in `tool-module` and `connector-module`.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.5.4](https://github.com/yogasw/wick/compare/v0.5.3...v0.5.4)

_Released on 2026-05-02_

### Added
- New `kvlist` editable table widget for storing values as JSON arrays.
- Support for `wick:"kvlist=..."` tags to define table columns.
- Per-field status indicators showing saving, success, and error states.
- New `config_helpers.go` utility for input handling and list processing.
- Dedicated `reference/config-tags.md` documentation.

### Fixed
- Non-deterministic ordering in `ListOwned` by implementing a `declOrder` slice.

### Improved
- Revamped configuration forms to use always-visible inputs instead of click-to-edit.
- Implemented per-field auto-saving with 800ms debounce for text and immediate updates for other input types.
- Synchronized `wick` tag grammar across tool, connector, and design-system modules.
- Streamlined module documentation by centralizing configuration tag references.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.5.3](https://github.com/yogasw/wick/compare/v0.5.2...v0.5.3)

_Released on 2026-05-02_

### Fixed
- Updated upgrade logic to fetch the latest version from both Go proxy and GitHub to ensure real-time accuracy and resolve proxy sync delays.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.5.2](https://github.com/yogasw/wick/compare/v0.5.1...v0.5.2)

_Released on 2026-05-02_

### Added
- Added a status field to `wick_list` and `wick_search` responses to identify connectors requiring manual configuration before execution.

### Fixed
- Resolved an issue on Windows where active binaries were locked during upgrades by implementing a rename strategy for the running executable.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.5.1](https://github.com/yogasw/wick/compare/v0.4.2...v0.5.1)

_Released on 2026-05-02_

### Added
- Model Context Protocol (MCP) support via stdio transport, enabling integration with LLM clients.
- CLI subcommands for `mcp serve`, `mcp config`, and `mcp install`.
- Support for four MCP build modes: `auto`, `dev`, `build`, and `rebuild`.
- Automated MCP configuration installation for Claude Desktop, Cursor, Gemini, Codex, and Claude Code.
- `wick_info` tool to provide version, build time, and commit metadata to LLM clients.
- CLI commands to start the server and worker directly.

### Improved
- Automatic directory resolution to the project root during MCP startup to ensure correct loading of `.env` files and SQLite databases.
- Detection for Windows Store installation paths for Claude Desktop.
- Binary execution logic on Windows to bypass extension requirements for PE binaries.
- MCP `auto` mode using mtime-based staleness checks instead of roundtrip flags.
- Documentation for local MCP setup, including guide sections for all build modes and installation targets.
- Metadata field naming in `wick_info` for better clarity in LLM responses.

### Fixed
- Versioning inconsistencies and build flag propagation across CLI tools.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.4.2](https://github.com/yogasw/wick/compare/v0.4.1...v0.4.2)

_Released on 2026-05-02_

### Improved
- Reorganized the crudcrud connector into a three-file layout to improve maintainability.
- Updated the connector skill functionality.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.4.1](https://github.com/yogasw/wick/compare/v0.4.0...v0.4.1)

_Released on 2026-05-01_

### Added
- New functionality for the `wick upgrade` command to self-install the CLI binary.

### Improved
- Upgrade process split into distinct prompts for the CLI binary and go.mod dependencies.
- Support for binary-only upgrades when no `go.mod` file is present, preventing errors during the upgrade process.

---
*This summary was automatically generated by Gemini AI*

---


## [v0.4.0](https://github.com/yogasw/wick/compare/v0.3.0...v0.4.0) Connectors + MCP

_Released on 2026-05-01_

### Added

- **Connector module** — third class of wick module beside Tool and Job, designed for LLM consumption via MCP (Model Context Protocol). Each module wraps one external API with a typed `Configs` struct + N typed `Operations`. See [Connector Module](/guide/connector-module).
- **MCP server** at `POST /mcp` with the four-tool meta-dispatch pattern (`wick_list`, `wick_search`, `wick_get`, `wick_execute`). Tool IDs are opaque (`conn:{connector_id}/{op_key}`) and stable across admin renames. See [MCP for LLMs](/guide/mcp).
- **Personal Access Tokens** at `/profile/tokens` — `wick_pat_<32hex>`, hash-only stored, render-once banner. For MCP clients that cannot speak OAuth (Claude Desktop, Cursor, cURL). See [Access Tokens](/guide/access-tokens).
- **OAuth 2.1** with Dynamic Client Registration (RFC 7591), PKCE S256 mandatory, refresh rotation + replay detection. Access `wick_oat_<32hex>` (1h TTL), refresh `wick_ort_<64hex>` (30d TTL). For browser-based MCP clients (Claude.ai). See [OAuth Connections](/guide/oauth-connections).
- **Connected Apps** at `/profile/connections` — per-grant disconnect (revokes every token for one user × client pair).
- **Admin pages**: `/admin/connectors`, `/admin/access-tokens`, `/admin/connections` for cross-user management.
- **Built-in System job** `connector-runs-purge` — daily cleanup of `connector_runs` audit rows older than 7 days (configurable). Code-managed; cannot be disabled. See [Connector Runs Purge](/guide/connector-runs-purge).
- **Per-row test panel + history** at `/manager/connectors/{key}/{id}` — Postman-style runner with URL-synced operation dropdown, prefill from history runs, paginated audit log with filter chips, expand-row inline detail, manual Retry navigation.
- **Bundled skill** `connector-module` — added to `wick skill sync` and the template's bundled skill set. The example `connectors/crudcrud/` ships in scaffolded projects.
- **Three-module mental model** in docs — introduction page now lists Tool, Job, and Connector side by side.

### Changed

- `template/AGENTS.md` documents the `connectors/` folder, `app.RegisterConnector` registration site, and the `connector-module` skill row.
- `template/README.md` lists the connector test page URL and `/profile/mcp` install snippets in the Quick Start.

### Migration notes

- Existing wick deployments upgrading to this version: the `connectors`, `connector_operations`, and `connector_runs` tables are auto-created on first boot. The `connector-runs-purge` job auto-registers and auto-enables. No manual action required.

---

## [v0.3.0](https://github.com/yogasw/wick/compare/v0.2.0...v0.3.0)

_Released on 2026-04-22_

### Added

- SSO domain allowlisting to restrict sign-ins to specific email domains, including a chip-based editor in the admin UI for management.
- A `wick upgrade` command to facilitate internal version updates.

### Improved

- Default theme resolution for new users and guests, providing GitHub-styled themes for unauthenticated or unset user sessions.
- Tool operator interface via a unified `ToolHeader` component and standardized setup-required banners across all tool pages.
- Tool rendering architecture to pull configuration state directly from context rather than manual service injection.
- Release workflow automation to synchronize version references across documentation, templates, and agent installation hints.
- Agent session initialization with a preflight check to verify local `go` and `wick` toolchain installations.
- Internal Go dependencies to their latest versions.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.2.0](https://github.com/yogasw/wick/compare/v0.1.13...v0.2.0)

_Released on 2026-04-21_

### Added

- `wick skill list` and `wick skill sync` commands to manage bundled skills and synchronize the `AGENTS.md` skill table.
- MobilePrompt component to display inline prompts specifically for mobile users.
- Downstream `tool-module` skill template featuring flat tool paths and mandatory clarify+plan loops.
- Comprehensive CLI reference documentation categorizing built-in commands and YAML task shortcuts.
- AI-agent quickstart section and skill sync pointers in the template `README` and `AGENTS.md`.

### Fixed

- License synchronization logic.
- Text alignment for prompts on mobile devices to ensure left-alignment.

### Improved

- CI/CD pipeline configurations.
- Homepage hero layout responsiveness for both mobile and desktop viewports.
- `wick init` scaffolding to include downstream tool-module skills and shared design-system components.
- Documentation structure by renaming `agent.md` to `AGENTS.md` across all guides and pages.
- Prompt instructions updated to utilize the `wick dev` command and version v0.1.13.
- Desktop-specific visibility for installation components via CSS optimizations.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.1.13](https://github.com/yogasw/wick/compare/v0.1.12...v0.1.13)

_Released on 2026-04-19_

### Fixed

- Corrected project license to MIT.
- Synchronized license documentation and repository metadata.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.1.12](https://github.com/yogasw/wick/compare/v0.1.11...v0.1.12)

_Released on 2026-04-19_

### Added

- MIT license for the repository.

### Improved

- Project versioning and internal metadata.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.1.11](https://github.com/yogasw/wick/compare/v0.1.10...v0.1.11)

_Released on 2026-04-19_

### Added

- Added license information to the repository.

### Improved

- Configured CI to trigger documentation and pkg.go.dev synchronization on version tags.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.1.10](https://github.com/yogasw/wick/compare/v0.1.9...v0.1.10)

_Released on 2026-04-19_

### Fixed

- Fixed changelog formatting and removed duplicate entries for version v0.1.0.

### Improved

- Automated CI workflow to merge trigger PRs to the release branch upon release completion.
- Enhanced changelog documentation with version comparison links for v0.1.1 through v0.1.7.
- Synchronized documentation and `go.mod.tmpl` templates for version v0.1.9.
- Performed general cleanup and synchronization of project documentation.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.1.9](https://github.com/yogasw/wick/compare/v0.1.8...v0.1.9)

_Released on 2026-04-19_

### Fixed

- Changelog formatting and duplicate entry for version 0.1.0.

### Improved

- CI workflow to automatically trigger pull requests to the release branch after a release completes.
- Project documentation through synchronization and the addition of comparison links for versions v0.1.1 to v0.1.7.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.1.8](https://github.com/yogasw/wick/compare/v0.1.7...v0.1.8)

_Released on 2026-04-19_

### Added

- Retry logic with rate-limit backoff for Gemini API requests.
- Tag comparison links to documentation changelog entries.

### Fixed

- CI reliability issues by fetching full repository history and origin/master before merging.
- Stale branch errors by ensuring the release-sync branch is deleted prior to pushing updates.

### Improved

- Release pipeline architecture by splitting tasks into five modular jobs for easier retries.
- CI automation by utilizing the GitHub API for PR merges and branch deletions instead of local git operations.
- Documentation workflow by automatically syncing changelog updates directly to project docs.
- CI security and permission management through the use of ADMIN_TOKEN for checkout and merge actions.
- Workflow efficiency by pushing sync updates directly to the master branch and removing redundant PR steps.
- Repository structure by removing the root CHANGELOG.md and centralizing logs within documentation.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.1.7](https://github.com/yogasw/wick/compare/v0.1.6...v0.1.7)

_Released on 2026-04-19_

### Fixed

- Logic for automatic documentation version updates.

### Improved

- CI/CD workflow to automatically delete release-sync branches after merging.
- Synchronization process for `go.mod.tmpl` and `CHANGELOG` files.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.1.6](https://github.com/yogasw/wick/compare/v0.1.5...v0.1.6)

_Released on 2026-04-19_

### Fixed

- Synced `go.mod.tmpl` before tagging new versions.
- Enabled automatic merging of release-sync pull requests to the `master` branch using `ADMIN_TOKEN`.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.1.5](https://github.com/yogasw/wick/compare/v0.1.4...v0.1.5)

_Released on 2026-04-19_

### Improved

- Synced `go.mod.tmpl` and `CHANGELOG` files for v0.1.4.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.1.4](https://github.com/yogasw/wick/compare/v0.1.3...v0.1.4)

_Released on 2026-04-19_

### Improved

- Improved auto release process.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.1.3](https://github.com/yogasw/wick/compare/v0.1.2...v0.1.3)

_Released on 2026-04-19_

### Fixed

- Bumped `go.mod.tmpl` before tagging and fixed version order in the release process.
- Resolved issue with pushing to `refs/heads/release` instead of `HEAD` in a detached state.
- Checked out the release branch before commit and tag to prevent detached HEAD push errors.
- Synchronized Go modules.

### Improved

- Updated pipelines.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.1.2](https://github.com/yogasw/wick/compare/v0.1.1...v0.1.2)

_Released on 2026-04-19_

### Added

- Add version command.

### Fixed

- Resolve CI/CD issues.
- Resolve documentation build issues.
- Correct `wick init` setup call.
- Address version-related issues.

### Improved

- Update `README.md` documentation.
- Enhance CI/CD processes.
- Update project pipelines.
- Update `go.mod` template version during releases.
- Optimize package JSON location.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.1.1](https://github.com/yogasw/wick/compare/v0.1.0...v0.1.1)

_Released on 2026-04-19_

### Improved

- Update pipelines.
- Update `README.md` documentation.
- Update CI/CD for documentation builds.
- Update `go.mod` template version during release.

---

*This summary was automatically generated by Gemini AI*

---

## [v0.1.0](https://github.com/yogasw/wick/releases/tag/v0.1.0)

_Released on 2026-04-19_

Initial public release.

### Added

- `wick init <name>` — scaffold a new project from template, auto-run `go mod tidy` + `go run . setup`
- `wick.yml` cross-platform task runner — `setup`, `dev`, `build`, `test`, `tidy`, `generate`
- Tool modules (`tools/<name>/`) — mount at `/tools/{key}`, typed `Config` with `wick:"..."` tags
- Background job modules (`jobs/<name>/`) — operator surface `/jobs/{key}` + admin surface `/manager/jobs/{key}`
- Tag system — group and filter tools/jobs with `DefaultTag`, admin-managed
- Visibility control — `VisibilityPublic` / `VisibilityPrivate` per tool
- Runtime config — `Config` structs reflected into admin-editable `configs` table rows
- SSO support — configurable from `/admin/configs`, no redeploy needed
- AES-GCM stateless sessions — per-job access, theme cookie persistence
- Tailwind CSS + templ — standalone Tailwind CLI (no Node.js), type-safe Go templates
- Claude Code integration — `agent.md` + Claude skills shipped with every `wick init` project
- External link cards — register URL shortcuts as tool cards via `RegisterToolNoConfig`
- Dark/light theme — user preference persisted via cookie
