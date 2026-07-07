# Browser picker widget (playwright_browser)

Interactive replacement for the plain `browser` dropdown: a list of browsers,
each showing install status + version (live from the plugin), a Download button
when missing (with polled progress), and click-to-select that persists the
choice. Reusable config widget type; playwright_browser is the first consumer.

## TODO
- [ ] Plugin op `browser_status` — returns `[{name, installed, version, downloading}]` for chromium/firefox/webkit. Read-only, cheap.
- [ ] Plugin op `browser_install{browser}` — triggers `playwright.Install` for one browser; returns when done (widget polls `browser_status` for progress).
- [ ] Mark both ops so LLM non-admin can't call them (AdminOnly default, or a code-level admin flag).
- [ ] New config widget type `browsers` (tag `wick:"browsers;desc=..."`) — parsed in config_reflect.go; entity.Config.Type = "browsers".
- [ ] FE: new BrowserPicker.svelte widget in fields/ — driven via the existing `POST /manager/api/connectors/{key}/{id}/test` endpoint (manager-only, non-MCP) to call browser_status / browser_install.
- [ ] Wire BrowserPicker into FieldWidget.svelte dispatch (type === "browsers").
- [ ] Selecting a browser persists to the `browser` config value (same key, replaces dropdown).
- [ ] Download: button → POST test browser_install → poll browser_status every ~2s → progress/spinner → refresh on done. (No SSE in manager; polling.)
- [ ] Config value stays chromium|firefox|webkit so all existing code (browserType()) is unchanged.
- [ ] Docs: config-tags.md + skill — document `browsers` widget.
- [ ] Build: go, templ (if templ path needs it — SPA is primary), FE test + bundle, tailwind, repackage plugin.

## Key constraints (verified)
- Plugin gives data to wick ONLY via ops (gRPC Execute). No non-op RPC without a breaking contract change → use ops, kept off the LLM surface.
- Manager-only, non-MCP call path already exists: `POST /manager/api/connectors/{key}/{id}/test` → Service.Execute (Source=Test), gated by canSeeRow (admin/tag). The widget drives ops through THIS, never MCP.
- No SSE in manager → download progress is POLLED (call browser_status on a timer), not streamed.
- Config value semantics unchanged: still one of chromium|firefox|webkit, so repo.go browserType() and everything downstream is untouched.

## Open question folded in
- "browser status op reachable by LLM?" → No. Keep browser_status/browser_install out of the LLM-callable set. Cleanest: a code-level flag on the Operation that marks it manager-only (not the per-row AdminOnly toggle, which defaults off and needs manual flip). NEEDS a small framework check — if too invasive, fall back to AdminOnly seeded true on these ops at bootstrap.
