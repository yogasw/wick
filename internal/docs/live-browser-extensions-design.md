# Live Browser Extensions — Design

Let an operator install Chrome extensions into a `playwright_browser` connector's live sessions — managed from the connector detail page in the manager UI (drag-drop / file upload a `.zip`/`.crx`, OR paste a Chrome Web Store ID to auto-download). Installed extensions load into every live session the connector spawns.

## Status: implemented

- [x] **Plugin** — extensions stored unpacked under `<sessionDir>/extensions/<id>/`; `openSession` adds `--load-extension` + `--disable-extensions-except` for all installed. `extensions.go`.
- [x] **Plugin** — ops `extension_list`, `extension_install` (base64 .zip/.crx → unpack, handles crx2/crx3 header + single-top-dir wrap + zip-slip guard), `extension_remove`.
- [x] **Plugin** — **forces headed** when ≥1 extension installed (skips `--headless=new`), per decision.
- [x] **Plugin** — VERSION → 0.6.0.
- [x] **Core** — routes `GET/POST .../browser/extensions`, `.../upload` (multipart), `.../from-store` ({id}), `.../{extID}/remove`. `connectors_browser_extensions.go`. Same `canConfigureRow` gate.
- [x] **Core** — Web Store `.crx` fetched in core (`fetchWebStoreCRX`), bytes handed to the plugin install op (one unpack path).
- [x] **Manager UI** — `ExtensionsSection.svelte` on the playwright_browser detail page (after Active sessions): drag-drop + file picker upload, Web Store id input + Add, list + remove.
- [x] **Docs** — extensions + headless caveat documented in `docs/connectors/playwright_browser.md`.
- [x] **Tests** — `toZipBytes` (crx2/crx3/zip/reject) + `validExtID` unit tests (Go), green.

## Gotchas (decide before coding)

1. **`--load-extension` needs headed Chrome.** Classic headless ignores extensions; `--headless=new` supports them but is finicky and some extensions still won't run. Live sessions default `Headless=true`. Options:
   - **A (recommend):** when a connector has ≥1 extension, force the session headed (override headless at spawn) and note it in the UI. Extensions are a "watch/drive it live" feature anyway, so headed is the natural mode.
   - B: keep headless=new and accept that some extensions silently no-op.

2. **`.crx` is not a folder.** `--load-extension` wants an **unpacked directory**. So every install path must **unzip** into `<sessionDir>/extensions/<id>/`:
   - `.zip` → straight unzip.
   - `.crx` → strip the CRX header (magic `Cr24`, version, header length) then it's a zip → unzip. Small, well-documented format; do it in the plugin (Go `archive/zip` after slicing the header).

3. **Web Store download URL.** `https://clients2.google.com/service/update2/crx?response=redirect&prodversion=<v>&acceptformat=crx2,crx3&x=id%3D<ID>%26installsource%3Dondemand%26uc`. Returns a `.crx`. Fetch in **core** (has clean HTTP + can report errors to the UI synchronously), write the `.crx` to a temp, then hand the bytes to the plugin's `extension_install` op (same path as an upload). Keeps one unpack path in the plugin.

4. **Restart to apply.** Chrome loads `--load-extension` only at launch. Installing/removing an extension does **not** affect already-running sessions — it applies to the *next* `session_open`. Surface this ("applies to new sessions").

5. **Security.** Extensions run with broad privileges in that browser profile. Gate the manage routes with the same `canConfigureRow` as the browser proxy. Only unpack into the connector's own `extensions/` dir; validate the extracted paths (no zip-slip / `..`).

## Boundary decision

Upload bytes cross to the plugin via an `extension_install` op taking base64 (extensions are small — MBs). Core does the multipart receive + the Web Store fetch; the plugin owns unpack + storage + spawn wiring. This keeps the loopback/file ownership in the plugin (consistent with sessions living under `<sessionDir>`), and core stays the HTTP/UI edge.

## Manager UI

New `ExtensionsSection.svelte` on the playwright_browser detail page (next to Active sessions):
- Drag-drop zone + `<input type=file accept=".zip,.crx">`.
- Web Store ID field + "Add from store".
- List: name/id, size, remove. Empty + "applies to new sessions" hint.
