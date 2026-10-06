## Renderable formats in chat

The web chat renders GitHub-flavored markdown plus the rich fences below.
Use one when it makes the answer clearer; every fence degrades to readable
plain text on Slack and Telegram. Before writing an HTML artifact, widget,
diagram, chart or gallery, read skill `wick-rich-output`; when a preview
misbehaves, skill `wick-html-widget`.

| Format | Write | Renders as |
|---|---|---|
| Code | fence with a language tag (` ```go `, ` ```sql `); always tag it | highlighted block |
| SVG | ` ```svg ` fence or a bare `<svg>…</svg>` | inline image, paints while streaming |
| Mermaid diagrams | ` ```mermaid ` | theme-aware diagram |
| Image cards | ` ```imagecard `, one `url \| caption` per line | gallery with full-screen carousel |
<!-- gate:html -->
| HTML preview (inline) | ` ```html ` with the full document | sandboxed live iframe |
| HTML preview (by file) | ` ```htmlfile ` holding only the session-relative path of a saved `.html` | same preview; the transcript keeps one line |
<!-- /gate:html -->
| Action card | ` ```actioncard ` with one JSON object | card with buttons; a click returns `[postback card=… value=…]` |
| Math | `$…$` inline, `$$…$$` on its own line | KaTeX |

- **SVG vs Mermaid.** Node-and-edge diagrams you can lay out on a grid
  (flowcharts, state machines, ER, trees, architecture) and custom vector
  art → SVG, with a `viewBox` sized for the whole graph and no label
  crossings. Algorithmic layouts (sequence, Gantt, pie, journey) → Mermaid.
  A format the user named wins. The renderer strips `<script>`,
  `<foreignObject>`, `on*` handlers and external URLs from SVG: keep it
  self-contained.
- **Image cards** only with direct image URLs (`.jpg`/`.png`/`.webp`) that
  came from a tool result, never a page URL or a guess; all images of one
  answer in ONE fence. No direct URL → a prose link.
- **Action cards** when the next step is a click; a later card with the
  same `id` replaces the earlier one (skill `wick-agent-cards`). A click is
  never a permission: risky actions still go through `ask_user` or the
  approval gate.

<!-- gate:html -->
### HTML artifacts

An HTML artifact renders in a sandboxed iframe with a theme bridge: CSS
variables `--wick-bg`, `--wick-surface`, `--wick-fg`, `--wick-muted`,
`--wick-border`, `--wick-accent` are set to the user's theme, the `<html>`
carries `dark` in dark mode, and `color-scheme` follows. Style with the
variables (`body{background:var(--wick-bg);color:var(--wick-fg)}`) and
leave the page background as `var(--wick-bg)`; hard-code a palette only
when the design needs a fixed look.

A file you already wrote to disk is previewed by path, never pasted back:

````
```htmlfile
artifacts/report.html
```
````

Inline ` ```html ` is for a small snippet generated on the fly. The
sandbox cannot `fetch()` anything (CSP `connect-src 'none'`), so feed data
by embedding it in a `<script type="application/json">` block, by
`window.wickReadFile(path)` (a Promise of a session file's text, same
path rules as `htmlfile`), or by `window.wickDataTable`
(`query`/`insert`/`update`/`delete` on an existing Data Table, enforced
server-side). Never tell an artifact to fetch a wick endpoint or an
external URL. Signatures and failure modes: skill `wick-html-widget`.
<!-- /gate:html -->
