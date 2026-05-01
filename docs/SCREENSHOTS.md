# Screenshots checklist

Track which images the docs reference. Every `<!-- IMAGE NEEDED: ... -->` placeholder in `docs/guide/*.md` and `docs/index.md` should land here. Replace each placeholder with `![alt](/screenshots/file.png)` once the PNG is in `docs/public/screenshots/`.

All shots: 1200px wide unless noted. Use a clean theme (light or dark — pick consistent across the doc set), no browser chrome, no real credentials in view.

## Connector module pages

- [ ] `connector-list.png` — `/manager/connectors/{key}` list
  - Boot wick with the crudcrud connector. Create two rows: "Crudcrud Prod" and "Crudcrud Staging" with different tags. Screenshot the n8n-style stacked card list with kebab menu visible.

- [ ] `connector-detail.png` — `/manager/connectors/{key}/{id}` detail
  - Click a row from the list page. Capture identity (label + status badge + ID), top action bar (History · Duplicate · Disable · Delete), Label form, Credentials form (one secret field masked), and the Operations table at the bottom.

- [ ] `connector-test.png` — `/manager/connectors/{key}/{id}/test?op=...`
  - Open the test panel for one operation. Fill an input, click Run. Screenshot with green success pill, latency, and pretty-printed JSON response visible.

- [ ] `connector-history.png` — `/manager/connectors/{key}/{id}/history`
  - Generate mixed success/error runs (run a few times with bad inputs). Open History, expand one error row. Screenshot with Request JSON, Response JSON, run ID, IP, UA, HTTP status, and the Retry link visible.

## Admin overview

- [ ] `admin-connectors.png` — `/admin/connectors`
  - Boot with crudcrud + at least one extra connector registered. Cross-Key list with Disabled toggles, tag pickers, and "Module not registered" badge if applicable.

## MCP

- [ ] `mcp-flow.png` — sequence diagram of the OAuth/MCP dance
  - Render as a sequence diagram (mermaid in vitepress, or excalidraw export). Three lanes: Claude.ai · wick web · wick MCP. Mirror the steps documented in `docs/guide/mcp.md` and `docs/guide/oauth-connections.md`.

- [ ] `mcp-install-page.png` — `/profile/mcp`
  - Log in, open `/profile/mcp`. Full page: OAuth section + Bearer section + 4 install snippets.

- [ ] `mcp-claude-desktop.png` (optional) — Claude Desktop tools dialog
  - After wiring a wick PAT into Claude Desktop, screenshot the Tools dialog showing the 4 `wick_*` tools registered. Bonus: a Claude conversation calling `wick_list` and getting back crudcrud rows.

## Access tokens

- [ ] `tokens-list.png` — `/profile/tokens`
  - Generate ≥2 tokens with different names. Use one for an MCP call so Last used populates. Screenshot the table.

- [ ] `tokens-create.png` — render-once banner
  - Click Create, fill name, submit. Capture the green banner with plaintext `wick_pat_xxx` and the "won't be shown again" warning.

- [ ] `admin-tokens.png` — `/admin/access-tokens`
  - Log in as admin. Full page including stat card row + cross-user table.

## OAuth connections

- [ ] `oauth-consent.png` — `/oauth/authorize`
  - Trigger an OAuth flow from a DCR client (or curl-craft one). Capture the consent page before clicking Approve.

- [ ] `connections-list.png` — `/profile/connections`
  - Authorize 1-2 OAuth clients. Capture the per-row table.

- [ ] `admin-connections.png` — `/admin/connections`
  - Admin cross-user grant table.

- [ ] `oauth-flow.png` — sequence diagram
  - Sequence diagram of the OAuth dance documented in `docs/guide/oauth-connections.md`. Same rendering choice as `mcp-flow.png`.

## Connector runs purge

- [ ] `purge-job-detail.png` — `/manager/jobs/connector-runs-purge`
  - Full page including the "Code-managed" badge, cron field, `retention_days` field, run history table.

- [ ] `purge-run-history.png` — same page after Run Now
  - Click Run Now, wait for completion. Screenshot the new history row showing `Purged N row(s) older than 7 day(s) (cutoff: ...).`

## Tips

- Keep names lowercase-with-hyphens. They map directly to `docs/public/screenshots/<name>.png`.
- When ready to swap, replace each `<!-- IMAGE NEEDED: <name>.png ... -->` block with `![alt text](/screenshots/<name>.png)`. Add a short italicized caption on the line below.
- For diagrams (sequence, flow) prefer mermaid blocks inside the markdown over PNGs — they render natively in vitepress and stay editable. Reserve PNG for actual UI shots.
