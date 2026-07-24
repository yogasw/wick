# Wick Provider (built-in) — Design

## TODO — captured 2026-07-24, not yet started

Captured so nothing from the last live-testing round gets lost. None of these are
implemented yet — planning/discussion only so far. Roughly ordered by dependency.

1. **Provider picker: 3-level nav (Type → Name → Model).** Composer's "Provider"
   panel is currently a flat list (`claude · claude_default`, `claude · new [AI
   Router]`, `codex`, `codex · gemini_fl… [AI Router]`, `gemini`, `wick`). Restructure
   to: pick **Type** first (claude/codex/gemini/wick, one row each) → submenu of that
   type's **instance Name**s → for `wick` specifically, if that wick instance has
   more than one registered **Model**, a 3rd-level submenu to pick the model (skip
   this level entirely if there's only one model — never force an extra nav step).
   Wick instances themselves can already be duplicated like any other provider type
   (the old "single instance" rule from this doc is superseded — see note below), so
   a user can have `wick · default`, `wick · custom_a`, etc., each with its own model
   list. FE: composer provider-picker component (`fe/agents/conversation` — find via
   the "< Provider" panel header). BE: no new endpoint expected, just FE grouping
   over the existing provider-list response.
2. **Fix the wick icon in the picker.** Screenshot shows a generic person/user icon
   for `wick` next to sparkle/atom-style icons for claude/gemini — looks out of
   place. Find the icon map (keyed by provider type) and swap in something befitting
   "wick" (brand icon already exists per git log `feat(fe): give the wick provider
   its own brand icon` — confirm the picker actually uses it; if the picker has its
   own separate icon map from the one that commit touched, that's the bug).
3. **WickDetail model row: consolidate actions into a kebab (⋯) menu.** Today each
   model row has separate buttons: Test / Edit / Delete (+ implicit "set default" via
   radio). Replace with a single "⋯" dropdown containing: **Edit, Set default, Test,
   Duplicate, Disable/Enable, Delete** (mockup.html View 2 now shows this exact
   layout, including a disabled-row example) — room to grow without the row getting
   wider every time a new action lands. Disabled rows show a muted "Disabled" chip
   and grey out the row; Set default/Test are disabled until re-enabled.
4. **Add per-model Disable/Enable.** New bool field on `WickModel` (BE:
   `internal/agents/provider/wick` model struct + wherever it's persisted/read for
   the composer's model list) — a disabled model is hidden from the composer's
   Model submenu (item 1) and from being auto-picked as default, but stays visible
   (greyed) in the Models table so it's not lost, just parked. FE: toggle in the
   kebab menu + a visual "Disabled" chip on the row.
5. **Real per-model Test button (1-token ping).** Currently
   `WickDetail.svelte`'s `testModel()` is a placeholder toastOk stub ("Test for X is
   not wired yet") — no backend endpoint exists. Build: `POST
   /providers/wick/models/{id}/test` → BE resolves the model's adapter (same
   Kind/APIFormat → adapter mapping `spawn.go` already uses for real spawns),
   decrypts the stored key, sends a minimal 1-token generate call, returns
   `{ok, latency_ms, error?}`. Reuses the adapter code path, not a new HTTP client.
   Wire the FE Test button (now inside the kebab menu, item 3) to call it and toast
   the result (latency on success, vendor error message on failure).
6. **Long-running tool calls (>~2min) go to a background goroutine.** Today every
   tool call blocks the turn synchronously (`engine.dispatch` →
   `handler(ctx, args)`, see `internal/agents/provider/wick/engine.go`). For calls
   that legitimately run long (mainly `shell`, currently capped at a hard 120s
   timeout in `tool_shell.go`), instead of just timing out: at ~90s–2min without
   completion, detach the still-running process into a goroutine that streams
   output to a per-session job file (mirror the workflow engine's `Store` pattern —
   `state.json` + append-only `events.jsonl`/`output.log`, see
   `internal/agents/workflow/engine/engine.go`'s `Save`/`AppendEvent`/`ListEventsTail`
   — new sibling layout helper `SessionJobsDir(id)`/`SessionJob(id, jobID)` next to
   the existing `Session*` methods in `internal/agents/config/layout.go`), and
   return a tool result immediately: "still running in background, job id X — check
   with `job_status`/`job_result` later." Add a small poll tool (`job_status` /
   `job_result`) to `buildTools` so the model can check back in a later turn instead
   of the turn hanging. Chosen approach (decided in this session): tool returns a
   placeholder immediately + the model polls next turn — NOT interrupting the live
   generation to inject the result mid-stream (that would need surgery on the
   engine's streaming/dispatch loop for little benefit).
7. **Oversized tool results get redirected to a file.** Tool call results (shell
   output, connector responses, etc.) above ~50KB should be written to a file
   (session workspace, path surfaced in the tool result) instead of inlined —
   returned tool_result text becomes something like "response too large (212KB),
   saved to `output/shell_1234.txt` — use read_file to inspect it." Applies to tool
   results only, not final assistant text. Natural pairing with item 6 (background
   job output is already file-based) — likely the same size-check helper backs both.
8. **Per-model generation config, trim redundant Global fields.** Advanced-options
   fields already exist per-model in the Add/Edit modal (max output tokens, temp,
   top-p, thinking budget, raw config — see `WickGenConfig` in the Data model
   section below) — confirm the backend actually persists/uses all of them (some
   may be UI-only stubs). Decision (this session): keep these model-specific knobs
   **per-model only**; do NOT also expose them as instance-level Global defaults —
   drop any duplicate Global field for something that's genuinely per-model (a
   model-specific temperature has no sane "global default" — every model's sane
   default differs). Global should keep only genuinely instance-level settings:
   shell tool on/off, connectors, max context tokens (history budget), max turns,
   and gate-adjacent settings — **gate config explicitly stays global**, never
   per-model.
9. **Duplicate model.** The "Duplicate" entry in the kebab menu (item 3) clones a
   `WickModel` row (new id, same config incl. decrypted-then-re-encrypted API key,
   label suffixed "(copy)") so setting up a variant (e.g. same model, different
   temperature) doesn't mean re-entering the API key/model id from scratch. Check
   for an existing clone/duplicate pattern elsewhere in the codebase (connector
   duplication, workflow duplication) to mirror before inventing a new one.

**Superseded note:** the "Naming (decided)" section below says wick is a **single
instance** with no duplicate/rename — that was the 2026-07-23 decision. As of this
session's discussion, that's being revisited: wick instances CAN be duplicated like
any other provider type, which is what makes the 3-level picker (item 1) meaningful
(otherwise "Name" would always have exactly one option). Treat the "single instance"
language below as historical until the picker/duplication work above actually lands
— don't remove the old section, just don't trust it as current truth.


`wick` becomes the 4th provider type alongside `claude` / `codex` / `gemini` — a
**built-in agent runtime that runs inside the wick process**, no external CLI to
install. Operators register custom models (Google Gemini, OpenAI, Anthropic,
OpenRouter, custom endpoints); after an API key is entered wick **discovers the
available model list live from the vendor API** and renders a searchable picker —
the user just picks a model in the UI. The engine is adk-go, hidden as an
implementation detail behind the "wick" name so it can be swapped later.

**Hard requirement (Yoga, 2026-07-23): real tool calling.** The wick provider must
work agentically like the CLI providers — a shell tool (bash / cmd) that actually
executes, plus wick connectors. **No MCP**: tools are wired directly in-process
(function tools calling the connectors service), no MCP server round-trip needed.

Paired docs: [mockup.html](./mockup.html) (UI, primary read) — keep both in sync when
editing one.

## How to test in the morning

The wick provider is wired end-to-end (backend engine + models UI). To try it:

1. `wick build` (or run the dev server) and open the Providers page — there's a new
   **Wick** card (Built-in badge).
2. Open it → **Add model** → pick a provider (start with **Google Gemini**), paste an
   API key, and the model list loads live (searchable). Pick e.g. `gemini-flash-latest`,
   Save. It becomes the default.
3. Start a new session, select provider **wick** in the provider dropdown (it uses the
   default model). Chat. The agent has a working **shell** tool (bash/cmd) + **todo**
   tool; thinking/tool/text all stream into the timeline via the normal pipeline.
4. History persists in `conversation.jsonl` — switching a session from claude → wick
   keeps the conversation.

Known not-yet-wired (see status below): connector/session/skill tools, gate "ask"
approval, prompt caching, auto-compaction, composer nested model picker, context files
(AGENT.md/memory.md/…). Shell runs **unguarded** until the gate checker is wired
(`SetGateChecker`) — fine for local testing, note before any shared deploy.

## Build status (2026-07-23 overnight build)

Legend: [x] done + tested · [~] partial · [ ] not started

### Live-testing round, 2026-07-24 — ✅ DONE (not yet reflected further down this doc)

- [x] **Command gate: full tool coverage, not just shell.** Every wick tool now goes
  through the same approve_once/session/always/block modal CLI providers get, unless
  `permission_mode=bypass` (full access, no gate at all). Read-only tools + fs tools
  always-allow; `shell` checks the whitelist then falls through to approval;
  everything else (`wick_execute`, connector calls, unknown MCP tools) always
  requires approval. New in-process synchronous approval path
  (`ApprovalManager.RequestApproval`) reuses the same daemon state (session-approved
  cache, SSE broadcast, `/approve` HTTP endpoint) as the CLI socket-based gate.
- [x] **Fixed: wrong session id reaching the gate checker**, root cause of "gate
  says bypass but shell still hangs 25s with no modal" — `dispatch()` was passing
  the stream-json protocol's own engine-local id instead of the real wick HTTP
  session id, so approval SSE events fanned out to nobody. New `wickSessionID` field
  distinct from `sessionID`.
- [x] **`AllowShellMetachars` toggle** — lets a whitelisted command carry chain
  operators (`;`, `|`, `&`/`&&`) safely by re-validating each chained sub-command
  independently; redirect/substitution chars (`>`, `<`, backtick, `$(...)`) stay
  hard-blocked regardless (they inject into one command rather than adding a
  genuinely separate one).
- [x] **System prompt split: global vs per-provider.** Wick was silently inheriting
  claude's overlay (missing switch branch in `pool/factory.go`) — now has its own
  `immutable_wick.md` overlay; the persistent-memory (`memory.md`) instructions
  live there, not in the shared immutable base.
- [x] **`todo` tool: merged UI + moved to shared MCP surface.** Was wick-only
  native tool; now defined once in `internal/mcp/handlers/todo.go` /
  `tools.go`/`handler.go`'s `dispatchTool`, reachable by claude/codex/gemini too
  (mirrors `ask_user`'s pattern — reuses `dispatchTool` in-process, no new auth
  surface). FE renders every `todo` call in a turn as ONE merged checklist widget
  (not one card per call), matched by optional `id` (falls back to step-text), with
  per-item click-to-expand showing the tool calls that happened during that step.

## NEXT TARGETS (Yoga feedback while testing, 2026-07-24)

These are the concrete, designed next items — each a real integration, captured so the
next build is precise (not rushed blind overnight).

### A. Full wick MCP tool surface — ✅ DONE (2026-07-24)
The in-process wick agent now gets the ENTIRE MCP tool surface, not just connectors:
`ask_user`, `wick_set_title`, `wick_session_info`, `wick_session_workspace`,
`wick_list`/`wick_search`/`wick_get`/`wick_execute`, `wick_list_providers`,
`wick_skill_list`/`sync`, `wick_schedule_message`, `wick_info`, `wick_encrypt`/`decrypt`,
and the dynamic `wickmanager` tools — same dispatch, same handlers as the HTTP MCP path.

How (reuse, no reimplementation):
- `internal/mcp/handler.go`: extracted the tools/call switch into `dispatchTool` (behavior-
  preserving; HTTP path unchanged, mcp tests green). Added `Handler.AgentToolDescriptors`
  + `Handler.CallAgentTool` (`internal/mcp/agent_tools.go`) — dispatch in-process with a
  capturing `Responder` + discard writer, as the SAME synthetic admin principal the CLI
  providers use over loopback (`internalSystemUser`) → identical visibility, zero new auth.
- `internal/agents/provider/wick/external_tools.go`: `ExternalTool` + `SetToolProvider`
  seam + `jsonSchemaToGenai` (MCP JSON-Schema → genai schema). `buildTools` merges them
  after shell/todo. Session id derived from the spawn dir for session-aware tools.
- `internal/pkg/api/server.go`: wires `wickprovider.SetToolProvider` to
  `mcpHandler.AgentToolDescriptors` + `CallAgentTool`. nil provider (tests) = shell/todo only.
- Tests: external-tool flow through the engine + ClaudeParser, JSON-schema conversion,
  nil-safe. mcp + wick + provider + tools/agents all green; binary builds + runs.

### (historical) A. Connector tools — the wick MCP surface (`wick_list` / `wick_get` / `wick_execute`)
Confirmed by Yoga: the running agent only sees `shell` + `todo`; the connector tools
aren't registered. Approach (reuse, don't reimplement): call the REAL MCP handlers in
process via a synthetic request + capturing `Responder` (see internal/mcp/handlers —
`RPCRequest`, `Responder{WriteResult,WriteError}`, `ToolCallResult{Content[].Text,IsError}`).
- Seam: wick exports `ExternalTool{Name,Description,Params,Handler}` +
  `SetToolProvider(func(scope) []ExternalTool)`; `buildTools` merges them.
- server.go registers the provider (it has `connectorsSvc`, `layout`, resolvers). Each
  tool handler: marshal args → `RPCRequest.Params`; `httptest.NewRequest` with the
  `X-Wick-Session-Id` header = agent session id; capturing `Responder`; call
  `handlers.WickList/WickGet/WickExecute(...)`; return captured text + isError.
- **OPEN (security): which user/tags does the agent act as?** Replicate the MCP server's
  internal-token identity — `internal/mcp/auth.go WithInternalToken` resolves a `user` +
  `m.users.GetUserFilterTagIDs`; use the SAME user/tagIDs so connector visibility matches
  what the MCP server would grant. Must not broaden visibility. This is why it wasn't
  rushed overnight.
- Also gives the session tools (`ask_user`, `wick_session_info`, `wick_set_title`,
  session-workspace) for free via the same synthetic-call adapter.

### B. Wick-specific spawn/session detail (replace "Reproduce" for wick)
The Reproduce box (CLI argv, bash/PS/cmd, Headless/Interactive) is meaningless for the
in-process wick provider — the screenshot shows it empty (`''`). For wick, the detail
page must instead show **every outbound interaction** so errors are debuggable:
- Per model/tool call: the request (as a **curl**, secrets masked, with **hide/show**
  reveal like the env api-key toggle today), the response body, HTTP status, latency,
  and any error.
- Different FE component (not the Reproduce/argv one) and a different stored shape.
- BE: DONE — (1) every outbound HTTP + Gemini call logged masked via zerolog; (2)
  **structured per-call records persisted** to `<SessionDir>/wick-interactions.jsonl`
  (`interactions.go`): seq, kind (generate/compaction), model, latency, system prompt,
  tools offered, the full sent message history (truncated), response text, tool calls,
  prompt/output/cached tokens, and error. This is the durable "why did the model answer
  this" data (confirmed by Yoga as the point). 3 tests.
- FE: ✅ DONE — read endpoint `GET /providers/wick/interactions/{session}`
  (`getWickInteractions` in providers_wick.go, reads the jsonl, admin-gated, path-safe).
  `WickInteractions.svelte`: per-call list (seq, kind badge, latency, tokens + cached
  badge, error) expandable to system prompt + full request messages + response + tool
  calls. Wired into `SessionDetail.svelte` (shown when ProviderType=="wick"); the empty
  Reproduce card is hidden for wick via a new `hideReproduce` prop on `SpawnDetail`. 126
  FE tests pass (+2 interaction-api tests), build green.
  Remaining polish: literal copy-as-curl with a masked-key reveal endpoint (v1 shows the
  logical request/response inspector, which already answers "why did it answer this").
- Repro spec for wick already returns empty (in-process, no argv) — the FE should branch
  on `InProcess`/type=="wick" rather than render an empty Reproduce.

**Latest (session 3f — shell tool fix, critical bug from live test):**
- [x] **Fixed: shell tool was wrapping commands in `cmd /c` on Windows.** Root cause of a
      real bug report: cmd.exe forced a second shell-quoting layer that mangled the
      command before bash saw it — `"` became `\"`, heredocs broke, multi-line scripts
      got corrupted, Windows paths leaked into Unix context. Symptom: an agent asked to
      generate + run a 1–10 bash multiplication-table script via heredoc got stuck for
      10+ tool calls fighting quoting.
      - `tool_shell.go`: removed the cmd.exe branch entirely — every platform now runs
        `bash -c <command>` with the raw string as a single unmangled argument (no
        trimming beyond outer whitespace, no re-escaping, no newline collapsing).
      - `shell_resolve.go` (new): `resolveBash()` — PATH first, then Windows-specific
        fallback search (Git for Windows, MSYS2, WSL's bash.exe shim, scoop). Never
        resolves to cmd.exe/PowerShell.
      - `tool_shell_test.go` (new, 21 tests): the full spec from the bug report — basic
        exec, quoting (double/single/nested/JSON/base64), heredoc (incl. no-expand),
        multi-line scripts, file I/O, path/cwd (no Windows-path-leak assertion), `&&`
        chains, and the exact repro (heredoc-write + run a 1–10 multiplication table,
        verified cell-by-cell). All 5 of the report's acceptance-gate tests pass.
        **Verified passing on this Windows machine** (git-bash resolved via PATH).

**Latest (session 3e):**
- [x] **Command gate wired for wick shell tool** (deny-only). `server.go` sets
      `wickprovider.SetGateChecker`: only the `shell` tool is gated; uses the SAME
      `allowed_cmds` whitelist + `gate.NewMatcher().Decide()` as the CLI providers.
      Gate off (opt-in) → allow. Non-whitelisted command → deny with reason (surfaces as
      an error tool_result). Closes the "shell ran unguarded" gap. Interactive approval
      ("ask" → UI round-trip) is the follow-up — the ApprovalManager isn't synchronous
      in-process. Engine gate-deny path already unit-tested. api + wick + build green.

**Latest (overnight session 3):**
- [x] **Outbound interaction logging** (safe slice of B) — `httpjson.go` + `adapter_gemini.go`
      log every external call masked: URL (Gemini `?key=` redacted), status, latency, and
      the response body on error/retry. "Setiap interaksi keluar di-logs biar bisa cek
      error" — available in app logs now; structured per-spawn records + FE view = next.
- [x] **Prompt caching** — Anthropic `cache_control` (ephemeral) on the system block +
      last tool (the byte-stable prefix); cached-token counts surfaced via
      `CachedContentTokenCount` for both Anthropic (`cache_read_input_tokens`) and
      OpenAI (`prompt_tokens_details.cached_tokens`). Gemini/OpenAI caching is automatic
      server-side (prefix kept stable). Tests updated.
- [x] **Context files (Phase 8, partial)** — `contextfiles.go` loads `AGENT.md`
      (AGENTS.md / CLAUDE.md fallback, first wins) + `memory.md` + `skills.md` from the
      spawn workspace and appends them to the system prompt. 6 tests. Project-first vs
      session-pwd is covered because the spawn workspace already resolves to the right
      dir; explicit project-dir precedence when they differ = later refinement.
- [x] **FE modal UX overhaul** (WickDetail.svelte) per Yoga's live feedback:
      provider-follow defaults (Base URL prefilled with the real vendor endpoint, not a
      grey hint; API-key + model-id example placeholders per provider e.g. `sk-or-…` /
      `qwen/qwen3-coder`); **Base URL moved above API key** and re-triggers model
      discovery on change; **Model ID is always a manual input** with the search list as
      an optional helper (never blocks on discovery); renamed **"Raw ADK config" →
      "Raw model config"** (adk is gone) with a clear explanation; surfaced Provider
      settings defaults (shell on, max turns cap 50, max context 0=off + compaction
      note). 124 FE tests pass, build green, dist regenerated.

**Session 2 (drop adk + compaction):**
- [x] **adk DROPPED.** `google.golang.org/adk/v2` removed from go.mod + go.sum. New
      wick-local `LLM` interface (`llm.go`), Gemini adapter rewritten on the `genai`
      SDK client directly (`adapter_gemini.go`). OpenAI/Anthropic adapters unchanged.
      Kept `google.golang.org/genai`. All 24 wick tests green, binary builds (64M) + runs.
- [x] **Model-driven compaction** (`compact.go`) — Claude-style: at ~80% of the context
      budget, wick asks the MODEL to summarize the oldest ~50% of turns and replaces them
      with the summary note; cut aligned to a user boundary so no tool_result is orphaned.
      Wired into `engine.runTurn` (compacts before appending the new turn); budget from
      `WickConfig.MaxContextTokens`. In-memory v1 (persisting the summary turn = later). 4
      compaction tests green. Replaces the old dumb budget-trim as the primary path.

- [x] **Phase 0** — `wick` provider type: enum, `SupportedTypes`, `InProcess()`,
      userconfig `Wick`/`WickModel`/`WickConfig`/`WickGenConfig`, registry converters,
      Probe short-circuit (built-in, no binary), single-instance guard in Save/Rename,
      capability init (HookSupported:false, "in-process"), empty catalog, factory
      routing, blank import. Tests green.
- [x] **Phase 2** — deps: **`google.golang.org/genai v1.57.0` only** (adk dropped — see
      "DECIDED — DROP adk"). Engine drives a wick-local `LLM` interface; every adapter
      (Gemini via genai SDK, OpenAI/Anthropic via wick HTTP) implements it.
- [x] **Phase 3 (core)** — bridge: `wickProcess` (io.Pipe), `emit.go` (claude
      stream-json), `engine.go` (agentic loop, non-streaming v1), `history.go`
      (conversation.jsonl → genai.Content + budget trim), real `spawn.go`. Harness #2
      (retry/backoff), #3 (per-call timeout), #5 (tool_use-before-exec) done. Interrupt
      flush (#1) partial (ctx-cancel emits done line). **Engine unit tests green**:
      text turn, tool loop, gate-deny, error-surfaced — all round-tripped through the
      REAL `event.ClaudeParser`.
- [x] **Phase 4 (adapters)** — Gemini (adk native), OpenAI-compatible (openai /
      openrouter / other), Anthropic Messages. Usage tokens mapped. **Caching:** prefix
      is structured for stability but explicit `cache_control`/`cachedContent` NOT yet
      emitted — [ ] follow-up.
- [~] **Phase 5 (tools)** — shell (safeexec, timeout, output cap) + todo (TodoWrite
      equiv) DONE & tested; gate enforced via engine before-tool check + injection seam
      (`SetGateChecker`, fail-open until wired). [ ] connector tools, [ ] session-tool
      reuse, [ ] skill/slash, [ ] gate "ask"→approval, [ ] per-model Test ping, [ ]
      media parts — all seams in place, not wired.
- [x] **Phase 1 BE** — model discovery proxy (google/openai/anthropic/openrouter +
      5-min cache), models CRUD (`POST /providers/wick/models`, `DELETE .../{id}`,
      `.../{id}/default`), settings (`POST /providers/wick/settings`), read
      (`GET /providers/wick/config`, masked keys), key encryption, single-default
      invariant, `SetSecretDecryptor` wired from `SetConfigs`. `tools/agents` tests green.
- [x] **Phase 1 FE** — providers SPA wick card + WickDetail (models table + Add/Edit
      modal w/ live discovery + settings). 124/124 FE unit tests pass, `npm run build`
      green, dist regenerated. Gaps: connectors setting shown-but-disabled (needs a
      connector-list endpoint + "none" semantics), per-model Test button is a
      placeholder (no ping endpoint yet), top_p read-back but no field.
- [ ] **Phase 1b** — composer nested model picker (session picks WickModel.ID). Not
      started; the provider dropdown already lists `wick/wick` so a session can select
      wick and use its default model today.
- [x] **Phase 3b** — auto-compaction (`compact.go`, model-driven). Done + tested.
      Remaining: persist the summary as a `kind:"compaction"` turn so it survives respawn.
- [ ] **Phase 6** — remaining tests (discovery fakes, history-replay, cache-prefix).
- [ ] **Phase 7** — docs + changelog.
- [ ] **Phase 8 / 9** — context files (AGENT.md/memory.md/skills.md/todos) + input
      handling (queue/steering/media). See sections below.

### ✅ DECIDED (Yoga, 2026-07-23) — DROP adk, harness = wick, compaction = model-driven

After walking the mechanisms, Yoga confirmed the direction (then went AFK with "buat
sampai selesai, jangan tanya lagi"). Locked decisions:

- **Drop `google.golang.org/adk/v2`.** It was only used for `model.LLM` (an interface),
  its `LLMRequest`/`LLMResponse` types, and `model/gemini.NewModel`. The harness (loop,
  context assembly, thinking-relay, compaction) is wick's own regardless, so adk's real
  value (llmagent/runner) was never used. **Keep `google.golang.org/genai`** (official
  Google SDK: the Gemini client + the shared Content/Part type model every adapter
  speaks). Replace adk's interface with a wick-local `LLM` interface (2 methods); rewrite
  the Gemini adapter on `genai.Client` directly. OpenAI/Anthropic adapters unchanged.
- **Harness = wick.** `engine.go` owns the system loop (call→tools→feed-back→repeat).
  This is what makes long / complex multi-step tasks work — for a simple chat it loops
  once; for a hard task it loops many times executing tools. Not adk, not genai.
- **Thinking** is produced by the model, relayed by wick (`ThinkingConfig` on + emit
  thinking lines). wick doesn't "make" thinking.
- **Compaction = model-driven summary (Claude-style).** When history nears budget, wick
  asks the MODEL to summarize the oldest ~50% of turns, then replays summary + recent
  turns. wick orchestrates (detect threshold, call model, splice); the AI writes the
  summary. Replaces the dumb budget-trim. v1 = in-memory within the engine; persisting
  the summary as a `kind:"compaction"` turn is a later enhancement.
- **Tool confirmation = wick gate**, not adk `toolconfirmation` (deny + ask→approval via
  the existing ApproveFn/modal).

### Historical note — engine was first built on adk's `model.LLM` (now being dropped)

**Your question (2026-07-23): "ini nggak pakai `google.golang.org/adk/v2/tool`, pure
bikin sendiri dari awal?"** — Correct. As built tonight, wick does NOT use
`llmagent.New` / `tool.Tool` / `functiontool` / `runner` / `launcher`. It drives the
low-level 2-method `model.LLM` interface directly, runs its own agentic loop
(`engine.go`), and defines its own minimal `toolDef` (`tools.go`).

**Why I chose this (deliberate deviation from the original plan sketch):**
- Full control over history — rebuilt from wick's `conversation.jsonl` (cross-provider
  continuity), not adk's `session.Service`.
- Tool dispatch with the gate checked in-loop before each call.
- Exact claude stream-json output for the io.Pipe bridge (so parser/store/SSE/spawn-log
  work unchanged).
- One uniform path for all vendors: Gemini via adk's `model/gemini`, OpenAI/Anthropic
  via wick adapters — all implementing the same `model.LLM`.
- It's tested end-to-end (20 unit tests, round-tripped through the real ClaudeParser)
  without needing a live API key.

**What this GIVES UP vs the adk tool system (`llmagent`+`functiontool`+`runner`):**
- adk's built-in tools for free: `geminitool.GoogleSearch`, code execution,
  `tool/mcptoolset`, `tool/toolconfirmation` (which maps neatly to wick's approval flow).
- adk owning the tool-call loop + streaming aggregation (less bespoke loop code from us).
- `BeforeToolCallbacks` / `AfterModelCallbacks` as first-class hooks (gate/usage).

**The alternative (Option B), if you prefer the adk ecosystem:** keep the same
OpenAI/Anthropic `model.LLM` adapters, but wrap them in `llmagent.New` + `runner.Run`,
define tools with `functiontool.New`, enforce the gate via `BeforeToolCallbacks`, and
translate the runner's `session.Event` stream → claude stream-json for the bridge. The
adapters and the io.Pipe bridge stay; only `engine.go`/`tools.go` are replaced.

**My recommendation:** ship the current direct-`model.LLM` engine for v1 (it works, it's
tested, it's minimal, it doesn't depend on adk's session/launcher model which is
oriented toward standalone agent CLIs). Adopt Option B later IF/WHEN we want adk's
built-in tools or `toolconfirmation`. I did NOT rewrite to Option B overnight because
that swap is hard to unit-test without a live key and I won't hand you an untested core
to test in the morning. **Your call — tell me if you want the migration to Option B.**

## Original phase plan (reference)

- [ ] Phase 1b — composer picker: wick appears once, expands to a nested model list;
      session Meta stores the chosen `WickModel.ID`.
- [ ] Phase 4 caching — byte-stable prefix + cache_control / cachedContent.
- [ ] Phase 5 — connector/session/skill tools, gate ask-mode, media.
- [ ] Phase 8 — context files (AGENT.md project-first/session-pwd, memory.md, skills.md, todos).
- [ ] Phase 9 — input handling (queue, mid-turn steering, media in/out).
- [ ] Phase 8 — **context files** (parity with Claude/other AIs; Yoga 2026-07-23): load
      `AGENT.md` scoped project-first then session-pwd; `memory.md`, `skills.md`, and the
      other common project/agent context files; project-scoped `todos`. See "Context
      files" below.
- [ ] Phase 9 — **input handling / steering** (Yoga 2026-07-23): queue when the user
      sends many messages; mid-turn sends while the model is thinking must be handled
      (steer, not dropped), like Claude; **media** send + media creation (multimodal in,
      files/images out). See "Input handling & media" below.

## Naming (decided)

**`wick`** — the built-in provider. "Use wick itself, no CLI needed." Rejected:
`adk` (leaks the engine; wrong once models beyond Gemini land), `native` / `internal`
(generic, no branding).

**Single instance (decided 2026-07-23)**: unlike codex/claude (`codex · grok`,
`codex · grok_agung` in today's picker), wick can NOT be duplicated / renamed — there
is exactly one `wick/wick`. Multiplicity lives in **Models**, not instances. The
create-instance / rename / delete UI is hidden for type `wick`. Door left open:
if per-profile config (different tool allow-lists, different context caps) turns out
to be needed, instance duplication can be enabled later — gated on that real need,
not before.

Still a `provider.Type` architecturally — pool, UI, config, project defaults, session
selection all reuse the existing machinery. The differences: no binary/PATH probe, and
**Models + Provider settings** instead of Binary/ExtraArgs.

## UI (see mockup.html)

1. **Providers page** — a 4th card "Wick" with a `Built-in` badge. Shows model count +
   default model. Status `Ready` when ≥1 model with a valid key exists; `Needs setup`
   otherwise. No binary path row. No Add-instance / duplicate / rename for wick
   (single-instance rule).
2. **Detail page** (`/providers/detail/wick/wick`) — replaces the Binary/Args/Env form
   with two sections:
   - **Provider settings** (instance-level): shell tool on/off, connector selection
     (all-ready vs explicit list), max context tokens (history-replay budget), max
     turns, default generation config, and a **raw ADK config escape hatch** (JSON
     merged into the runner config — the rule is *every knob adk-go supports must be
     reachable from the UI*: common fields structured, the long tail via raw JSON,
     same pattern as `AIRouterRawConfig`).
   - **Models**: table of registered models (default selector, label, provider kind,
     model id, masked key, edit/delete) + `Add model`.
3. **Add/Edit Custom Model modal** (Factory-style, plus live model search):
   - **Provider**: `Google Gemini | OpenAI | Anthropic | OpenRouter | Other provider`
   - **API key**: encrypted at rest (`wick_cenc_` via `configs.EncryptSecret`), used
     server-side only, never re-shown (masked `••••` placeholder on edit)
   - **Model**: once provider + key are set, wick fetches the vendor's model list and
     renders a **searchable picker** (type-to-filter). `Other` has no discovery —
     manual model id + required Base URL.
   - **Display name** (optional; falls back to model id)
   - **Advanced options** (collapsed): API format, Base URL, Max output tokens,
     generation-config overrides (temperature, top-p, thinking budget) + per-model
     raw ADK config
4. **Composer picker (core)**: wick appears ONCE in the session provider dropdown;
   clicking it expands a **nested model list** (its registered models, default
   checked) — "1 provider bisa didaftarin banyak model, di-click ada opsi lagi mau
   pakai model yang mana". The session stores the chosen `WickModel.ID`; empty =
   instance default.

## Model discovery (the "search available models" feature)

The BE proxies vendor list APIs so keys never reach the browser and CORS is a non-issue.

| Kind | Endpoint | Auth |
|---|---|---|
| `google` | `GET generativelanguage.googleapis.com/v1beta/models` | `?key=` |
| `openai` | `GET api.openai.com/v1/models` | `Authorization: Bearer` |
| `anthropic` | `GET api.anthropic.com/v1/models` | `x-api-key` + version header |
| `openrouter` | `GET openrouter.ai/api/v1/models` | optional Bearer |
| `other` | no discovery — manual model id + Base URL | — |

`POST /providers/wick/models/discover` body `{kind, api_key?, base_url?, model_ref?}` →
`{models: [{id, label}]}`.

- `model_ref` lets the edit flow reuse an already-stored (encrypted) key without
  re-entering it — BE decrypts server-side.
- 10s timeout; result cached in-memory 5 min per `(kind, key-hash)` so typing in the
  search box never re-hits the vendor.
- Discovery failure is non-fatal: the picker falls back to a free-text model id input
  with the error shown inline.

## Data model

`userconfig.ProviderInstance` gains a `WickModels` field (used only by type `wick`,
same pattern as `CodexConfig`):

```go
type WickModel struct {
    ID              string // stable id ("m_" + rand) — referenced by Default + sessions
    Kind            string // google | openai | anthropic | openrouter | other
    Label           string // display name; empty = show Model
    Model           string // vendor model id: gemini-flash-latest, gpt-5.2, …
    APIKey          string // encrypted (wick_cenc_)
    BaseURL         string // advanced; required for kind=other
    APIFormat       string // advanced: gemini | openai_chat | openai_responses | anthropic_messages
    MaxOutputTokens int    // advanced; 0 = vendor default
    Default         bool   // exactly one true per instance (enforced on save)

    GenConfig *WickGenConfig // per-model generation overrides; nil = instance default
    RawConfig string         // per-model raw ADK config (JSON), merged last
}

// WickConfig is the instance-level provider settings ("wick sendiri secara
// provider ada config"). One per instance — and there is only one instance.
type WickConfig struct {
    ShellTool        bool     // default true — the bash/cmd tool
    Connectors       []string // connector instance ids; empty = all ready connectors
    MaxContextTokens int      // history-replay budget; 0 = model default
    MaxTurns         int      // agentic-loop cap per user turn; 0 = unlimited
    GenConfig        *WickGenConfig // defaults for models without their own
    RawConfig        string   // raw ADK config (JSON) escape hatch — every adk-go
                              // knob reachable even before a structured field exists
}

// WickGenConfig mirrors adk-go / genai GenerateContentConfig — structured
// fields for the common knobs; the long tail rides RawConfig.
type WickGenConfig struct {
    Temperature     *float64
    TopP            *float64
    ThinkingBudget  *int // thinking tokens; 0 = off, nil = model default
    MaxOutputTokens int
}
```

APIFormat defaults from Kind (google→gemini, openai/openrouter→openai_chat,
anthropic→anthropic_messages); only `other` usually needs it set explicitly.
Merge order at spawn: adk defaults ← instance GenConfig ← instance RawConfig ←
model GenConfig ← model RawConfig (most specific wins).

## Engine — in-process bridge (analysis carried over from the adk plan)

Every existing provider is an external CLI subprocess; adk-go runs inside the wick
process — no PID, no stdout pipe, no `--resume`. Chosen approach: **fake a subprocess
around the library** so the entire existing machinery works unchanged.

```
wickProcess implements provider.Process:
  r, w := io.Pipe()
  Stdout() -> r
  Stdin()  -> WriteCloser feeding user turns into a channel
  Pid()    -> 0 (in-process; factory's onStarted already tolerates 0)
  Binary() -> "wick (built-in)"    Argv() -> nil
  Wait()   -> blocks until runner goroutine returns; closes w (reader sees EOF)
  Kill()   -> cancels the runner ctx

Spawn():
  resolve default (or requested) WickModel; decrypt key server-side
  model  := adapter for Kind/APIFormat  (gemini native | openai-compat | anthropic)
  tools  := [shellTool, connectorTools...]   // see Tools below
  agent  := llmagent.New(llmagent.Config{Model: model, Instruction: opt.Preset,
                                         Tools: tools, ...})
  go run: for each user message from the Stdin channel -> runner.Run(ctx, msg)
          translate each adk event -> claude stream-json line -> w.Write(line + "\n")
          tool calls surface as {"type":"assistant",...tool_use...} +
          {"type":"user",...tool_result...} lines so the UI timeline shows them
          first line: {"type":"system","subtype":"init","session_id":"<uuid>"}
          turn end:   {"type":"result",...}  (parser fires Done)
```

### Tools (decided 2026-07-23 — core requirement, no MCP)

The agent must actually *do* things, like the CLI providers:

- **`shell`** — executes bash / cmd in the session workspace via `safeexec`, with the
  same timeout + output-cap conventions the CLI agents get. **Gate enforcement lives
  in a central `BeforeToolCallback`** (verified adk-go API): it checks `gate.Spec`
  before any tool runs and returns a deny result on violation — the actual tool call
  is skipped. Stronger than the CLI subprocess-hook approach, no probe/verify dance.
  The Providers UI stops saying "gate N/A" and instead shows "gate enforced natively".
- **wick connectors** — every ready connector op is registered as a native adk
  function tool calling `connectors.Service.Execute` directly, mirroring what the MCP
  `wick_execute` handler does but in-process. Same catalog/ready filter the system
  prompt uses. Encrypted-field masking (`wick_enc_` tokens) applies unchanged since
  the same service layer runs.
- **wick session tools — RE-USE the existing MCP tool implementations** (decided
  2026-07-23, "askmodel pakai mcp tools yg wick aja, re-use"): `ask_user`,
  `wick_session_info`, `wick_set_title`, session-workspace tools, etc. — the same
  tool surface CLI agents get via wick's MCP server, exposed to the wick provider as
  adk function tools that call the SAME handler code (`internal/mcp/handlers`)
  in-process. One implementation, two transports (MCP for CLI providers, direct for
  wick). No new tool logic, no drift between providers — an agent on wick behaves
  identically to an agent on claude w.r.t. wick tools. `ask_user` notably covers the
  "model asks the user mid-turn" flow with zero new plumbing.
- **No MCP *protocol* needed** for this provider — the tool SURFACE is wick's
  existing MCP tools (reused above) + connectors, but wired directly in-process;
  there is no MCP client loop, no JSON-RPC round-trip ("cukup pakai wick connector
  udah aman"). External/third-party MCP servers are the only thing genuinely out of
  scope — addable later without touching this design.
- **`todo` (goal/plan tracking, TodoWrite equivalent)** — a lightweight function tool
  the agent calls to record its plan + progress (`[{step, status}]`). Emitted as
  normal tool_use events → the UI timeline shows plan/progress rows exactly like
  claude's TodoWrite does today, no FE work. Session-scoped, lives in the trace
  only. Optional (instance setting), default on.
- **Skills / slash commands (decided 2026-07-23 — "skill yg udah dibuat harus bisa
  di-call + mekanisme jalaninnya")** — the SAME skill set claude/codex get via
  `skillsync` must work on wick. Mechanism mirrors Claude Code, three parts:
  1. **Listing** — available skills (name + description from `skillsync.ListSkills`,
     filtered via the existing `InProviders` labels — reuse `skillInProvider` with
     label "wick"/"agents") injected into the system prompt through the
     `InstructionProvider`, so the model knows what it can call.
  2. **`skill` function tool** — agent calls `skill(name)` → returns the SKILL.md
     body; instructions load into the turn context and the model follows them.
     Claude Code's Skill tool semantics exactly.
  3. **Slash command** — a user message starting with `/<skill-name>` is
     pre-processed by the bridge BEFORE the model call: resolve via skillsync,
     inject the skill body + args as a context block on that turn. Unknown name →
     passthrough as plain text.
  No file-sync step for wick: it reads the canonical skillsync store live
  (in-process) — none of the copy-drift the CLI providers have.

### Agentic behaviors — thinking / planning / goals (status)

| Behavior | How | Status |
|---|---|---|
| Thinking | `GenerateContentConfig.ThinkingConfig` (budget + includeThoughts) → thinking events in UI | ✅ in plan (gen-config UI + vendor matrix) |
| Planning | model's native thinking; no Go `Planner` API (Python-only). Non-thinking models: plan-react instruction via preset | ✅ nothing to build — prompt technique |
| Goal / progress tracking | custom `todo` function tool (above) | ✅ added, Phase 5 |
| Multi-step agentic loop | adk runner loops tool calls until done; capped by `MaxTurns` | ✅ in plan |
| Skills / slash commands | skillsync listing in system prompt + `skill` tool + `/<name>` pre-process | ✅ added, Phase 5 |
| System prompt layering | same layered preset as claude (immutable rules + preset + connector catalog + session identity), passed via `InstructionProvider` (NOT `Instruction` — templating gotcha) | ✅ in plan |

`SendMode = SendAppend` (persistent "stdin" channel, like claude). `ClaudeParser`,
store, state machine, SSE, spawn-log all work unchanged — the only bespoke code is the
adk-event → stream-json translation and the model adapters.

### Verified adk-go API surface (pkg.go.dev, `adk/v2` v2.0.0, checked 2026-07-23)

The key open question — model pluggability — is **resolved: yes**. The interface
(`google.golang.org/adk/v2/model`) is two methods, streaming built in:

```go
type LLM interface {
    Name() string
    GenerateContent(ctx context.Context, req *LLMRequest, stream bool) iter.Seq2[*LLMResponse, error]
}
// LLMRequest{Model string; Contents []*genai.Content; Config *genai.GenerateContentConfig; Tools map[string]any}
// LLMResponse{Content *genai.Content; UsageMetadata *genai.GenerateContentResponseUsageMetadata;
//             Partial, TurnComplete bool; FinishReason genai.FinishReason; ErrorCode/ErrorMessage; ...}
```

Adapter per vendor = translate `genai.Content` ↔ vendor wire format (OpenAI chat /
Anthropic messages) inside one `GenerateContent` — no other integration points.
Other verified packages that slot into this design:

- `tool/functiontool` — wraps plain Go functions → shell + connector tools.
- `tool/toolconfirmation` — human-in-the-loop confirmations → candidate bridge to
  wick's existing approval flow (channel approve modal) in a later phase.
- `tool/mcptoolset` — exists, deliberately NOT used (decision above stands; it's
  there if the direct-wire approach ever needs revisiting).
- `runner`, `session` — runner drives the loop; adk session kept in-memory per spawn
  (wick's conversation.jsonl stays the durable store, see History below).
- `model/gemini` — native Gemini implementation (`gemini.NewModel`).
- Config type is literally `*genai.GenerateContentConfig` — so the UI "raw ADK
  config" escape hatch is a JSON unmarshal into that struct, and
  `UsageMetadata` carries token counts (incl. cached-token fields) for the
  spawn-log usage/caching metrics.

Full `llmagent.Config` findings (checked field-by-field):

- **No `Planner` in Go** (that's Python ADK) — plan-then-act comes from the model's
  own thinking (`GenerateContentConfig.ThinkingConfig`, already exposed via our
  gen-config/raw-config UI). For non-thinking models a plan-react style instruction
  is a prompt technique, not a missing API.
- **`BeforeToolCallbacks`** — returns result/error → actual tool call skipped. This
  is where the **gate** lives: one central callback checking `gate.Spec` for the
  shell tool (and later per-connector policy), returning a deny result on violation.
  Cleaner than embedding the check inside each tool.
- **`AfterModelCallbacks`** — the documented place to collect token usage per call →
  feeds the context-budget calibration + spawn-log usage/cache metrics.
  `BeforeModelCallbacks` can short-circuit with a cached `LLMResponse` (future
  optimization hook).
- **`Instruction` is a TEMPLATE** — `{key}` placeholders resolve from session state
  and error on missing keys. Wick presets routinely contain `{...}` (JSON examples),
  so **use `InstructionProvider`** (raw, no templating) to pass `opt.Preset`. Real
  gotcha, would break spawns with a cryptic error.
- **`IncludeContents`** — controls whether ADK auto-includes its session history;
  wick sets it to feed our own rebuilt history (conversation.jsonl is the truth).
- `SubAgents` / `Toolsets` / `OutputSchema` / `Mode` — exist; future options
  (OutputSchema notably = structured output for the workflow classify node).

## History, thinking & cross-provider continuity (decided 2026-07-23)

Use the existing store — no separate adk session persistence.

- **Source of truth = the existing session store** (verified shape, `internal/agents/store`):
  - `conversation.jsonl` — one `ConversationTurn` per line: `role` (user/assistant/
    system), `text`, `attachments`, `provider` snapshot per assistant turn, `turn_id`.
  - `thinking/<turn_id>.json` (+ `<event_id>.json` for large payloads) — the
    tool_use / tool_result / thinking trace. NOT in conversation.jsonl.
- **Writing needs zero new code**: the bridge emits stream-json → ClaudeParser →
  `store.Apply` — wick turns land in conversation.jsonl + trace files exactly like
  claude turns do today.
- **Reading (history rebuild)**: on every turn wick rebuilds the model message
  history: v1 replays the **text turns** from conversation.jsonl (user/assistant/
  system, translated to the target API format); replaying tool_use/tool_result
  history additionally reads `thinking/<turn_id>.json` — richer grounding but more
  tokens, so it ships behind the instance config (default: text turns only).
- **Cross-provider continuity works by construction**: a session started on
  claude/codex/gemini can switch to wick and keep its history, because
  conversation.jsonl is provider-agnostic ("provider wick bisa pakai history provider
  lain"). The reverse (wick → claude) stays exactly as today's provider-switch
  behaviour — the CLI holds its own internal state; wick doesn't make that worse.
- **Thinking**: wick's own models' thinking streams as `thinking` events through the
  same pipeline (UI timeline unchanged — the ClaudeParser already handles both
  `thinking_delta` streaming frames and whole thinking blocks; see
  `claude_test.go TestClaudeParserFullTurnWithThinkingAndToolUse` for the exact
  thinking → tool_use → tool_result → text → done flow the bridge must emit).
  Vendor support matrix for LIVE thinking display:
  | Kind | Thinking in UI |
  |---|---|
  | google | ✅ `Part.Thought` via genai — enable `includeThoughts` in ThinkingConfig |
  | anthropic | ✅ extended-thinking blocks in Messages API |
  | openai | ⚠️ reasoning not returned over API (summary at best) — bubble may be empty |
  | openrouter | depends on underlying model (`reasoning` field passthrough) |

  Recorded thinking from OTHER providers is
  display-only — never replayed into the new model's context. `tool_use` /
  `tool_result` pairs are replayed best-effort in the target API format; unpairable
  ones degrade to text.
- **ResumeID**: wick mints its own session uuid; "resume" = rebuild from store, so
  stale-resume errors ("no conversation found") structurally can't happen.

## Context budget & compaction (added 2026-07-23 — Yoga: "berapa history dibawa + compact")

The CLI providers get compaction for free from their own CLIs; wick owns the loop so
it must do both budgeting AND compaction itself:

**How much history is carried (budget)**

- `budget = min(model window, MaxContextTokens) − output reserve (MaxOutputTokens + slack)`
- Token counting: cheap estimator (≈ chars/4) **calibrated every turn** against the
  vendor's real `UsageMetadata.PromptTokenCount` from the previous response — so the
  estimate self-corrects per model instead of trusting a static heuristic.
- Everything fits → replay all text turns. Exceeds budget → compaction, NOT dumb
  truncation (trim newest-first stays only as the emergency fallback when a single
  turn is oversized).

**Auto-compaction (the claude `/compact` equivalent)**

- **Trigger**: estimated replay > ~80% of budget at turn start.
- **Action**: summarize the oldest ~50% of turns into one compact block, using the
  session's own model (cheap-model override later).
- **Persist**: as a system turn in conversation.jsonl — `kind: "compaction"`,
  `Text` = the summary, `Extras{covers_until: <turn_id>}`. Reuses the existing
  `ConversationTurn.Kind` mechanism (precedent: `provider_switch`), so the UI renders
  it as a dimmed system row and other providers simply ignore it.
- **Rebuild after compaction** = system prompt + latest compaction summary + turns
  after its watermark. Next compaction folds the previous summary in (stacking).
- **Cache interaction**: prefix only changes at compaction boundaries — one cache
  reset per compaction, byte-stable append-only in between. Acceptable.
- Manual "Compact now" button / endpoint: later, same code path.

## Caching (must handle — decided 2026-07-23)

Wick owns the request loop, so prompt/context caching is wick's job now (the CLI
providers got this for free from their vendors' CLIs):

| Kind | Mechanism | What wick does |
|---|---|---|
| `anthropic` | `cache_control` breakpoints | mark system prompt + tool defs + last history block cacheable on every request |
| `google` | implicit caching (auto) + explicit `cachedContent` | rely on implicit first; explicit cache for the static prefix is a later optimization |
| `openai` / `openrouter` | automatic server-side prompt caching | keep the prefix byte-stable so the cache hits; nothing extra to send |
| `other` | unknown | no-op |

Design rule: build every request so the **static prefix (system prompt, tool
definitions) is byte-identical across turns** and history is append-only — that is
what makes all three vendor cache schemes hit. Vendor-reported cache hit/miss usage
lands in the spawn-log `Usage` so the operator can verify it works.

## Harness hardening (added 2026-07-23 — "biar harness-nya bagus")

Things the CLI providers get for free from their CLIs that wick must build itself.
None are optional — a harness missing these feels broken in daily use:

1. **Interrupt / Stop mid-turn** — UI Stop → `Kill()` cancels the runner ctx. The
   bridge MUST then: flush partial text to the store, emit a final `result` line, and
   mark the turn `Interrupted: true` (field already exists on `ConversationTurn`) —
   so the UI shows the partial bubble instead of hanging on a dead stream.
2. **Retry + error surfacing** — vendor errors are wick's problem now. Policy:
   429/5xx/timeout → exponential backoff, ~3 attempts (mirrors the ADK docs'
   HttpRetryOptions guidance); fatal (401 bad key, 404 bad model) → NO retry, emit
   `event.Error` with the vendor message so the UI shows *why* ("invalid API key for
   model GPT 5.2"), plus a hint to open Providers → wick. `LLMResponse.ErrorCode` /
   `ErrorMessage` carry these.
3. **Per-call timeout** — every `GenerateContent` call gets a ctx deadline (default
   120s, instance-configurable) so a hung vendor connection can't freeze the turn
   forever; timeout counts as retryable.
4. **Gate "ask" mode = real approval flow** — deny-only is a half harness. When the
   gate verdict is "ask", the `BeforeToolCallback` BLOCKS the tool call and raises
   wick's existing approval request (same channel/UI modal the CLI flow uses via
   ApproveFn); approve → tool proceeds, deny/timeout → deny result to the model.
   In-process makes this trivially synchronous — no envelope protocol needed.
   (`tool/toolconfirmation` is the adk-native alternative; evaluate during Phase 5,
   whichever maps cleaner onto wick's ApproveFn.)
5. **Idle-timer compatibility** — the bridge must emit the `tool_use` line BEFORE
   executing a tool and `tool_result` after, because `Agent.run` stops the idle
   timer between those two events (long tool runs would otherwise get idle-killed).
   Same contract the CLI stream satisfies today; cheap to get right, expensive to
   debug if wrong.
6. **Attachments** — v1: pass the upload's `AbsPath` in the message text (exactly
   what CLI providers get today — agent reads it via the shell tool). v2: for
   multimodal models, attach images as inline `genai.Content` parts.
7. **Usage in the result line + per-model Test** — each turn's `result` line carries
   token usage (feeds the existing UI + spawn-log); each model row in the UI gets a
   **Test** button = 1-token ping via that model (validates key + model id + base URL
   in one click, same UX as the CLI providers' probe).

## Context files (Phase 8 — parity with Claude & general AI CLIs)

Wick must load the same ambient context files agents conventionally read, so a wick
agent behaves like Claude/codex in a project. Resolution + injection into the system
prompt (via `InstructionProvider`, appended after the immutable rules / preset):

- **`AGENT.md` — project-first, else session pwd.** If the session runs inside a
  project, read the project's `AGENT.md`; if there's no project (ad-hoc session), read
  `AGENT.md` from the session working directory. "Project" = the resolved project dir
  (see `internal/agents/project`); its `AGENT.md` is the project-scoped instructions,
  the session-pwd one is the fallback for project-less sessions.
- **`memory.md`** — persistent notes the agent carries across sessions (the auto-memory
  convention). Loaded read-only into context; a `memory` write path can follow.
- **`skills.md`** — skill index (complements the `skill` tool + skillsync listing).
- **Other common files** — `README`-style project context, `todos` (project-scoped task
  list, distinct from the per-turn `todo` tool), and whatever the CLI providers already
  read, kept in one resolver so wick and the CLIs agree on precedence.
- Precedence + dedup: one loader returns an ordered list (immutable rules → project
  AGENT.md → memory.md → skills.md → session-pwd fallbacks). Reuse the existing
  project/session resolution rather than re-deriving paths.

## Input handling & media (Phase 9 — like Claude & other AI tools)

- **Message queue** — when the user sends several messages quickly, they must queue and
  run in order, none dropped. The bridge's `msgs` channel already buffers (SendAppend);
  formalize FIFO ordering + a visible backlog count (mirror `Agent.QueuedCount()`).
- **Mid-turn send / steering** — when the model is mid-turn (thinking / calling tools)
  and the user sends more text, handle it like Claude's steering: inject the new message
  into the running turn (or cleanly queue it to run next) rather than dropping or
  double-spawning. Decide interrupt-and-append vs after-turn queue; wire to the engine
  loop (it currently processes one `runTurn` at a time from the channel).
- **Media in** — accept image/file attachments on a user turn (store already has
  `Attachment` with AbsPath): v1 pass the path (agent reads via shell), v2 attach images
  as inline `genai.Part` (multimodal) for models that support it.
- **Media out** — when the agent produces files/images (writes to the workspace), surface
  them as artifacts. The store already derives `Artifact`s from the turn trace at read
  time, so wick gets this for free once tool file-writes land in the trace — verify the
  artifact derivation fires for wick turns.

## Files to touch

### Phase 0–1 (skeleton + models UI)

| File | Change |
|---|---|
| `internal/agents/provider/provider.go` | `TypeWick Type = "wick"`; `SupportedTypes()`; `readList` / `pickList` / `mergeWithDefaults` arms; `WickModels` mapping |
| `internal/userconfig/config.go` | `ProvidersConfig.Wick []ProviderInstance`; `ProviderInstance.WickModels []WickModel` |
| `internal/agents/provider/wick/doc.go` | package doc + experimental banner |
| `internal/agents/provider/wick/capability_init.go` | register capability `HookSupported:false`, scope `"in-process"`; no hook writer/prober |
| `internal/agents/provider/wick/catalog.go` | minimal — models UI supersedes env picker; keep empty catalog registered |
| `internal/agents/provider/wick/spawn.go` | Phase 0: `Spawn()` → `"wick built-in runner not yet wired"` |
| `internal/agents/provider/wick/discover.go` | vendor model-list clients + cache |
| `internal/tools/agents/providers.go` | blank import `_ ".../provider/wick"`; models CRUD + discover handlers |
| `internal/tools/agents/providers_wick.go` | (new) handlers: save/delete model, set default, provider settings, discover proxy |
| `fe/agents/providers/...` | Wick card variant; detail Provider settings + Models table; Add/Edit modal w/ search picker; single-instance guards |
| `fe/agents/conversation/...` | composer picker: nested model submenu under wick |
| `internal/agents/pool/factory.go` | `case provider.TypeWick: spawner = wickpkg.Spawner{...}` |
| `internal/agents/provider/repro_spec.go` | wick branch (repro = "n/a — in-process") |

### Phase 2–4 (engine)

| File | Change |
|---|---|
| `go.mod` / `go.sum` | `google.golang.org/adk/v2`, `google.golang.org/genai` |
| `internal/agents/provider/wick/spawn.go` | real `Spawn()` → `wickProcess` |
| `internal/agents/provider/wick/process.go` | io.Pipe bridge + runner goroutine |
| `internal/agents/provider/wick/translate.go` | adk event → claude stream-json |
| `internal/agents/provider/wick/adapter_*.go` | gemini / openai-compat / anthropic model adapters |
| `internal/agents/provider/wick/tool_shell.go` | shell tool (safeexec, timeout + output cap) |
| `internal/agents/provider/wick/tool_connectors.go` | connector ops → adk function tools (direct service call, no MCP) |
| `internal/agents/provider/wick/tool_todo.go` | `todo` plan/progress tool (TodoWrite equivalent) |
| `internal/agents/provider/wick/tool_session.go` | wick session tools (ask_user, session_info, set_title, …) → thin adapters over `internal/mcp/handlers` |
| `internal/agents/provider/wick/tool_skill.go` | `skill` tool + slash-command pre-processing over skillsync |
| `internal/agents/provider/wick/gate_callback.go` | central `BeforeToolCallback` gate enforcement |
| `internal/agents/provider/wick/history.go` | conversation.jsonl → target-format history replay + context budget |
| `internal/agents/provider/wick/compact.go` | auto-compaction: trigger, summarize, persist `kind:"compaction"` system turn |
| `internal/agents/provider/wick/cache_*.go` | per-vendor caching: cache_control / cachedContent / prefix stability |

## Open questions (need Yoga's call before Phase 2+)

1. ~~adk-go Model pluggability~~ **verified 2026-07-23**: `model.LLM` is a public
   2-method interface with streaming; custom implementations supported (see
   "Verified adk-go API surface"). Adapters are straightforward.
2. ~~Gate~~ **decided**: enforced natively inside the shell tool callback (see Tools).
   Phase 0 still registers `HookSupported:false` (no subprocess hook); the UI copy for
   wick reads "gate enforced natively" once Phase 5 lands.
3. ~~Tools/MCP~~ **decided**: shell + wick connectors direct-wired, no MCP support.
4. **Version probe** — `--version` is meaningless; report the adk/v2 module version
   from build info as `Status.Version` so the card isn't blank.
5. ~~Per-session model override~~ **decided**: core — nested model list under the wick
   entry in the composer picker; session stores `WickModel.ID`.
6. **Instance duplication** — off for now (single `wick/wick`). Revisit only if
   per-profile config (different tool allow-lists / context caps) proves needed.
7. **History replay fidelity** — exact rules for translating claude-format
   tool_use/tool_result history into Gemini/OpenAI formats (lossy cases: images,
   thinking, provider-specific blocks). Prototype in Phase 3.

## Non-goals

- Voice/video streaming (Live API), Interactions API.
- Per-model spend tracking / quotas.
- Vertex/Enterprise auth beyond passing documented env vars through.
