# Widget CSP Config — per-directive policy, global + project

HTML artifact widgets render inside a sandboxed iframe with a hardcoded, maximally strict CSP. That CSP blocks nested iframes (`frame-src 'none'`), so a widget cannot embed Google Maps, and the sandbox omits `allow-popups`, so `target="_blank"` links do nothing. This plan makes the policy configurable per directive, globally and per project, with today's behaviour as the default.

## TODO

- [x] `internal/agents/config/widget.go` — `WidgetPolicy`, `Preset*` + `Mode*` consts, the `widgetDirectives` table, `Resolve()` with preset expansion, `ValidateAllowlist()`, `NormalizeHostSource()`, `ParseAllowlist()`, `ValidateConfigValue()`
- [x] `internal/agents/config/general.go` — 8 `Widget*` fields (group `Widget`, `mode` first) + defaults
- [x] 3-way toggle in the project editor; `script_src` as a configurable directive
- [x] `internal/agents/project/project.go` — `Widget` on `Meta`, `CreateOptions`, `Project.ResolveWidget()`
- [x] `internal/tools/agents/widget_policy.go` — read global knobs, resolve per session's project
- [x] `SessionMetaDTO.Widget` — resolved policy shipped on `/api/sessions/{id}/meta`
- [x] `richRender.ts` — `ARTIFACT_CSP` → `artifactCSP(policy)` + `artifactSandbox(policy)` + observable policy context
- [x] `HtmlArtifact.svelte` — reactive `sandbox`, rebuild + remount on a late policy
- [x] `MediaLightbox.svelte` — `sandbox={artifactSandbox()}` (`FileViewerModal` needed nothing — it renders `HtmlArtifact`)
- [x] `internal/configs/service.go` — `ConfigValidator` hook on the shared write path (see the deviation below)
- [x] Project API — `widget` + `widget_inherited` on detail, validated `widget` on update
- [x] `WidgetPolicyEditor.svelte` + wiring in `ProjectSettingsForm.svelte`
- [x] Go tests: resolve matrix, allowlist validation, append, validator hook on all write paths
- [x] FE tests: CSP per tri-state, byte-identical default, injection sanitising, sandbox, late-policy rebuild, editor behaviour
- [x] Global config UI — no work needed; the `wick:` tag reflection renders the `Widget` group
- [ ] `graphify update .` after code edits

## Deviations from the original design

Two things landed differently than first specified. Both were forced by what
the code actually does, and both are load-bearing.

**1. Validation sits on `configs.Service`, not in a handler.** The plan said
"backend validates on write". A config row turned out to have three write
doors — the manager SPA (`POST /manager/api/tools/{key}/configs/{configKey}`),
a legacy form POST, and the `wickmanager` MCP tool — and none of them
validated anything. Putting the check in one handler would have left the other
two open, so a new `ConfigValidator` hook was added to
`configs.Service.setOwned`, which all three funnel through. It runs after the
secret keep-existing shortcut and before persisting, so a rejected value
leaves the stored one untouched. Registered per owner, so `agents` cannot
police another module's rows.

**2. Artifacts react to a late policy instead of the transcript waiting for
it.** The policy ships with session meta, which lands after the first
transcript render. Making `loadConversation` wait on meta did work, but it
delayed the entire conversation behind a request that only matters to widgets —
and broke three existing tests that (correctly) assert the transcript loads
synchronously. Instead `setWidgetPolicy` is observable: artifacts subscribe,
and a policy change rebuilds the srcdoc and bumps `reloadKey` so the iframe
remounts and re-runs its scripts under the new CSP. An identical policy is a
no-op, so the common case costs nothing.

## Why

Two separate limits, both deliberate, both currently hardcoded:

1. `frame-src 'none'` in `fe/agents/conversation/src/lib/richRender.ts:559` — no nested iframes at all.
2. `sandbox="allow-scripts"` in `fe/agents/conversation/src/lib/components/HtmlArtifact.svelte:289` — no `allow-popups`, so links cannot open a tab.

The strictness exists because artifact HTML is model-authored, therefore untrusted. Sandbox without `allow-same-origin` puts the widget in an opaque origin; the CSP then closes every exfiltration channel. Relaxing either one re-opens a channel, so the relaxation must be an explicit, auditable operator decision rather than a code change.

## Config shape

**One knob decides the posture: `mode`.** Per-directive settings exist, but
they sit behind `custom` and are ignored otherwise. The common answers are
"sealed" and "let it through", and making an operator assemble either one out
of six separate fields invites a half-configured policy nobody can read off a
screen.

| `mode` | Effect |
| --- | --- |
| `secure` | Every directive blocked, popups off, allowlist dropped. Default. |
| `unsecure` | Every directive `https:`, popups on. Per-directive fields **ignored**. |
| `custom` | And only then are the per-directive fields read. |

An empty or unrecognised `mode` resolves to `secure`, so a cleared row and an
upgraded install both fail closed.

`Resolve` **expands** the preset, so the policy it returns always states every
directive explicitly. Nothing downstream — including the renderer — knows a
preset existed: a resolved `secure` policy and a hand-built all-blocked one are
byte-identical, by design.

`unsecure` includes `script-src`, so a widget may load and run code from any
host. With `connect-src` open too, such a script can read whatever the widget
holds — including anything handed to it over the file and data-table bridges —
and send it anywhere. The opaque origin still keeps it out of the parent's
cookies, storage, and DOM. Fit for trusted internal projects; `custom` exists
for everything else.

### Per-directive settings (`custom` only)

Each configurable directive takes one of:

| Value | CSP emitted | Meaning |
| --- | --- | --- |
| `block` | `'none'` | today's behaviour |
| `list` | resolved allowlist | partial: named hosts only |
| `all` | `https:` | unrestricted over TLS |

Configurable directives: `frame-src`, `img-src`, `media-src`, `connect-src`, `script-src`. Plus one boolean, `allow_popups`, controlling the sandbox attribute.

`script-src` governs **external** scripts only. The emitted directive always
keeps `'unsafe-inline'` — the artifact's own inline scripts, the height
reporter, and both postMessage bridges are inline, so the directive can only
ever gain hosts, never lose inline. Under `custom` it stays `block` unless an
operator deliberately opens it.

`img-src`, `media-src`, and `font-src` allow `data:`. `data:` stays in the emitted value for `img`/`media` in every mode, including `block`, so inline images keep working exactly as they do now. `font-src` is not configurable and stays `data:`.

### The directive table

Everything that walks the directives — preset expansion, global-config
projection, per-key validation — drives off `widgetDirectives` in
`internal/agents/config/widget.go`. Adding a directive later means one row
there plus the struct field and config knob it names. Nothing else is edited
directive by directive, and the Go tests enumerate the table rather than
hard-coding names, so a new row is covered on arrival.

### Global (configs table, group `Widget`)

Added to `config.GeneralConfig`, reflected via `entity.StructToConfigs`:

```go
WidgetMode        string `wick:"dropdown=secure|unsecure|custom;group=Widget|...;desc=..."`
WidgetFrameSrc    string `wick:"dropdown=block|list|all;group=Widget;desc=CUSTOM ONLY. ..."`
WidgetImgSrc      string `wick:"dropdown=block|list|all;group=Widget;desc=CUSTOM ONLY. ..."`
WidgetMediaSrc    string `wick:"dropdown=block|list|all;group=Widget;desc=CUSTOM ONLY. ..."`
WidgetConnectSrc  string `wick:"dropdown=block|list|all;group=Widget;desc=CUSTOM ONLY. ..."`
WidgetScriptSrc   string `wick:"dropdown=block|list|all;group=Widget;desc=CUSTOM ONLY. ..."`
WidgetAllowPopups bool   `wick:"bool;group=Widget;desc=CUSTOM ONLY. ..."`
WidgetAllowlist   string `wick:"textarea;group=Widget;desc=CUSTOM ONLY. One host per line..."`
```

The global page is the generic tag-reflected form, so `mode` is a dropdown
there rather than three buttons; the `CUSTOM ONLY.` prefix on every dependent
field is what makes the dependency legible in a flat form. The project editor
is a purpose-built component and renders the real 3-way toggle.

Defaults in `DefaultGeneralConfig()`: `WidgetMode` `secure`, every directive `block`, `WidgetAllowPopups` false, `WidgetAllowlist` empty. A fresh install and an upgraded install both behave exactly as today.

Empty string is read as `secure` / `block`, so rows written before this change (or a cleared field) fail closed rather than open.

### Project override (`project.Meta`)

```go
type WidgetPolicy struct {
    Override    bool     `json:"override,omitempty"`
    Mode        string   `json:"mode,omitempty"`
    FrameSrc    string   `json:"frame_src,omitempty"`
    ImgSrc      string   `json:"img_src,omitempty"`
    MediaSrc    string   `json:"media_src,omitempty"`
    ConnectSrc  string   `json:"connect_src,omitempty"`
    ScriptSrc   string   `json:"script_src,omitempty"`
    AllowPopups bool     `json:"allow_popups,omitempty"`
    Allowlist   []string `json:"allowlist,omitempty"`
}
```

A project carries its own preset, so it can be `secure` inside an `unsecure`
install and vice versa.

Under a preset, the per-directive fields are still **persisted** (inert) so a
Custom setup survives a trip through Secure and back — flipping the toggle does
not wipe what was configured. Nothing is enforced from them while a preset is
active.

`omitempty` throughout, so existing `meta.json` files stay valid and decode to the zero value — `Override: false`, i.e. inherit. No migration needed.

## Resolution

```
Override == false  →  global policy verbatim
Override == true   →  project's mode + directive modes + AllowPopups,
                      allowlist = global.Allowlist ++ project.Allowlist (deduped)

then, in both cases:
  mode secure    →  every directive block, popups off, allowlist dropped
  mode unsecure  →  every directive all,   popups on
  mode custom    →  per-directive fields as set
```

The policy is taken wholesale from the project when `Override` is true — not merged field by field. Field-level merging on a security policy makes the effective value impossible to read off one screen.

The allowlist is the one exception: it appends. A global list of common hosts plus a small per-project addition is the practical case.

**Accepted consequence of append:** a project cannot narrow the global allowlist. If global allows `*.example.com`, no project can allow less while still in `list` mode. A project wanting to be stricter picks `secure`, or sets that directive to `block`. This is a real limitation of append semantics, chosen knowingly over replace.

Dedup is case-insensitive on the normalised host.

Under `secure` the allowlist is **dropped**, not carried: nothing may read it,
and a resolved policy that still lists hosts reads as though something might.

## Validation and sanitising

Two independent layers. The backend validates on write so operators get an error at the point of entry; the frontend sanitises on read so a value that reached storage some other way still cannot inject CSP syntax.

### Backend, on save

Wired via `configs.Service.RegisterValidator("agents", …)` for the global
knobs and `projectWidgetReq.toPolicy()` for the project override. Both reject
before anything is written. A directive mode outside `block|list|all` is
rejected rather than coerced — coercing would silently tighten or loosen the
policy behind the operator's back, and reads already fail closed anyway.

Each allowlist entry must normalise to a bare CSP host source:

- Accept `example.com`, `https://example.com`, `*.example.com`, `https://*.example.com`, optional `:port`.
- Normalise to `https://host[:port]`; a bare host gets the `https://` prefix.
- Reject `*` alone — that is `all`, and choosing `all` explicitly is the honest way to say it.
- Reject plaintext `http://` — a widget in an opaque origin posting to plaintext is a downgrade with no upside.
- Reject any entry carrying a path, query, or fragment. CSP host sources do not path-match, so a path in config would silently not mean what it appears to mean.
- Reject anything containing `;`, `'`, `"`, whitespace, `,`, or control characters.

Invalid entries fail the save with a per-entry message naming the offending line. No silent dropping.

### Frontend, on build

`artifactCSP()` re-filters every entry against the same host pattern and drops non-conforming ones before joining with spaces. The frontend never trusts the stored list. Directive mode strings are matched against the three known values with anything else falling through to `block`.

## Data flow

Backend resolves the effective policy and ships it to the conversation SPA alongside the existing session/project payload — the SPA does no resolution and never sees the raw global-vs-project split.

`richRender.ts`:

```
const ARTIFACT_CSP = "…"                    →  artifactCSP(policy): string
buildArtifactSrcdoc(html)                   →  buildArtifactSrcdoc(html, policy)
buildAutoHeightSrcdoc(html, id)             →  buildAutoHeightSrcdoc(html, id, policy)
```

Call sites to update: `HtmlArtifact.svelte` (4 calls plus the fullscreen one), `MediaLightbox.svelte:54`, and the preview iframe in `FileViewerModal.svelte`.

Sandbox attribute becomes computed: `allow-scripts` always, `allow-popups` appended when enabled. Never `allow-same-origin` and never `allow-popups-to-escape-sandbox` — the opaque origin is the foundation everything else rests on, and an escaping popup is outside the parent's CSP entirely.

`internal/tools/agents/context.go:514` sets a separate `Content-Security-Policy` header for inline SVG. Out of scope, untouched.

## Testing

Go:
- Preset expansion — `secure` and `unsecure` IGNORE wide-open / all-blocked per-directive fields respectively; `custom` reads them.
- Empty/unknown preset resolves to `secure`; preset matching is case-insensitive.
- A project preset overrides the global preset in both directions.
- `secure` drops the allowlist even when both sides supply hosts.
- `Resolve()` matrix under `custom` — override off, override on, each directive mode, popups on/off.
- Allowlist append — global-only, project-only, both, duplicate across both.
- `ValidateAllowlist()` table — every accept and reject case above.
- `ValidateConfigValue()` — `widget_mode` accept/reject set, every directive key (enumerated from the table), pass-through for unrelated keys.
- The validator hook fires on the shared write path, rejects before persisting, is scoped to its owner, and is skipped on a secret keep-existing no-op.

TypeScript:
- `artifactCSP()` for each tri-state per directive.
- All-`block` policy produces a string byte-identical to the original hardcoded `ARTIFACT_CSP`. This is the regression guard.
- `script-src` keeps `'unsafe-inline'` in every mode, and gains hosts under `list` / `all`.
- An allowlist entry containing `; script-src *` is dropped, not emitted, and does not duplicate the directive.
- `data:` survives in `img-src`/`media-src` in all three modes.

Svelte:
- `sandbox` is exactly `allow-scripts` by default; gains `allow-popups` only when the flag is set.
- A policy arriving after mount rebuilds the srcdoc and remounts; an identical one does not.
- Editor: no toggle while inheriting; `secure`/`unsecure` hide every per-permission control; `custom` reveals them; picking a preset preserves the custom detail; unknown values display as `secure`/`block`.

## UI

Global: `Widget` group on the Agents config page, rendered by the existing `wick:` tag reflection. `mode` is the first field; every dependent field's description starts `CUSTOM ONLY.` because a flat reflected form cannot hide them.

Project: a Widget section in the project editor, showing badge `Inherited from global` and a one-line statement of the inherited posture while `Override` is false. Turning the override on reveals a 3-way segmented toggle — Secure / Unsecure / Custom — and nothing else. Only Custom reveals the five dropdowns, the popups toggle, and the additional-hosts textarea, with the inherited global hosts shown read-only above it so the append is visible rather than implied.

All UI copy in English.

## Out of scope

- Per-agent-profile scope — global plus project only.
- The SVG `Content-Security-Policy: sandbox` header in `internal/tools/agents/context.go`.
- Any change to the postMessage file/data-table bridges.
