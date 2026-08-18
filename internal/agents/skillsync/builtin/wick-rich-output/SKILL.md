---
name: wick-rich-output
description: Use BEFORE writing any HTML artifact, widget, diagram, chart, image gallery, or dashboard into a wick chat reply. Covers which fence to use (html vs htmlfile vs svg vs mermaid vs imagecard), the theme bridge variables an artifact must use, why fetch() is blocked inside an artifact and what to use instead (wickReadFile, wickDataTable), and the mistakes that keep slipping through — pasting a big file's markup inline, guessing image URLs, and hard-coding colors.
---

# Rendering rich output in wick chat

The web chat renders GitHub-flavored markdown plus a set of rich fences. This skill is the operational detail behind them — the general rules already in your system prompt are the summary; **this file is what you follow when actually writing one.**

Everything degrades to plain text on Slack and Telegram, so the raw source must still read sensibly.

## Pick the right fence first

| You have | Fence | Not this |
|---|---|---|
| A small self-contained HTML doc you are writing now | ` ```html ` | — |
| An `.html` file already on disk | ` ```htmlfile ` + the path | **never** paste its markup |
| A node-and-edge diagram (flow, state, ER, tree, architecture) | ` ```svg ` | mermaid |
| Sequence / Gantt / pie / journey | ` ```mermaid ` | svg |
| Real image URLs from a tool result | ` ```imagecard ` | a bullet list of links |
| Code | fence **with a language tag** | untagged fence |

Two rules decide most cases:

- **Diagram**: if you could lay it out by hand on a grid, use SVG — it reads better and you control styling. If the geometry is algorithmic (message lanes, time axes, proportional slices), let Mermaid compute it.
- **HTML**: writing it fresh and small → inline `html`. Already saved to disk → `htmlfile` by path.

If the user names a format, honor it regardless.

## The mistake that keeps happening: pasting a file's markup

When the HTML already exists on disk, referencing it by path is not a
style preference — pasting the document inline dumps tens of KB into the
transcript, burns context, and on Slack degrades to an unreadable wall of
markup.

````
```htmlfile
palembang-house-dashboard.html
```
````

One line, session-relative path. The UI fetches and renders the same sandboxed preview with Full screen / Show code / Download. Cost is one line regardless of file size.

## HTML artifacts: use the theme bridge

An artifact renders in a sandboxed iframe inside a chat that has a light and a dark mode. The runtime injects CSS variables on `:root`:

`--wick-bg`, `--wick-surface`, `--wick-fg`, `--wick-muted`, `--wick-border`, `--wick-accent`

Style with these, not hard-coded colors:

```css
body { background: var(--wick-bg); color: var(--wick-fg); }
```

`color-scheme` is set too, so native controls adapt, and `<html>` carries a `dark` class in dark mode if you prefer `.dark` overrides.

Hard-code a palette **only** when the design genuinely needs a fixed look (a brand mock-up). Otherwise a hard-coded white page glares in dark mode. Do not set an opaque full-bleed background unless you mean to — leaving it as `var(--wick-bg)` lets the artifact sit seamlessly in the conversation.

## Artifacts cannot fetch — this is the other frequent failure

The sandbox gives an artifact an opaque origin and sets `connect-src 'none'`. Any `fetch()` or `XHR` — to a wick endpoint, a file, or an external URL — is refused with "Failed to fetch" or a CSP violation. This is deliberate: it stops an artifact from phoning home.

So `fetch("data.json")` inside a dashboard **always fails.** Use one of these three instead.

**1. Embed the data inline** — simplest, best when the data is known now and small-to-medium:

````
```html
<script type="application/json" id="data">{ "users": 42 }</script>
<script>
  const d = JSON.parse(document.getElementById("data").textContent);
</script>
```
````

**2. `window.wickReadFile(path)`** — returns a Promise of the file's text. The artifact asks; the *parent* reads the file and passes bytes over `postMessage`, so the sandbox stays intact:

````
```html
<div id="out">loading…</div>
<script>
  wickReadFile("artifact.json")
    .then(txt => { document.getElementById("out").textContent = JSON.parse(txt).users; })
    .catch(e => { document.getElementById("out").textContent = "error: " + e.message; });
</script>
```
````

The path is session-relative, same as `htmlfile`. Absolute paths, URLs, and `..` traversal are rejected. Any text file works. Use it when the data lives in a file, is large, or spans several files.

**3. `window.wickDataTable`** — for a live, DB-backed widget with CRUD. The parent proxies each call with the signed-in user's session, and access is enforced server-side:

```js
const { rows } = await wickDataTable.query("tasks", { sort: "id:desc", limit: 50 });
await wickDataTable.insert("tasks", { title, done: false });
await wickDataTable.update("tasks", id, { done });
await wickDataTable.delete("tasks", id);
```

Signatures: `query(slug, {sort?, limit?, offset?, filters?}) → {rows,count}`, `insert(slug,row) → {ok}`, `update(slug,id,patch) → {ok}`, `delete(slug,id) → {ok,deleted}`. `filters` is `{col:{op,v}}` with ops equals / contains / gt / gte / lt / lte / in / is_empty / is_not_empty. `id`, `created_at`, `updated_at` are engine-managed. The table must already exist.

Use `wickDataTable` for editable persistent widgets, `wickReadFile` for read-only file-backed data.

## SVG

Wrap in a ` ```svg ` fence or write the bare `<svg>…</svg>` inline — both render, and both paint progressively while streaming, so you need not buffer the whole thing.

For node/edge diagrams: pick a `viewBox` big enough for the whole graph up front, space nodes on a consistent grid, route connectors so they miss labels, keep padding generous and arrowheads clear.

The renderer sanitises for safety: `<script>`, `<foreignObject>`, `on*` handlers, and external / `javascript:` URLs are stripped. Keep SVGs self-contained — inline shapes, gradients, filters, `data:` images, in-document `#id` refs. No external fonts or network resources.

## Image cards

Only when the user wants to *see* something and you have **real image URLs from a tool result**.

````
```imagecard
https://example.com/guy-crimson.jpg | Guy Crimson
https://example.net/clayman.png | Clayman
https://example.org/dino.jpg
```
````

Format is `url | caption | ratio | focus`; only `url` is required, and `ratio` / `focus` are rarely needed.

Three rules, each covering a real failure:

- **One fence for one answer.** Three, five, ten images go in the *same* fence so they form one gallery. Splitting into several fences or a bullet list of links defeats it. (A separate fence per distinct group with its own heading is fine.)
- **Direct image URL only** — the `.jpg` / `.png` / `.webp` file, never the page it sits on. A page URL renders as a broken card.
- **Never guess a URL from memory.** Guessed URLs 404. If a tool result gave you no direct image URL, write a prose link instead of forcing a card.

## Math and code

Inline `$…$` for short expressions, `$$…$$` for standalone equations. The inline detector already treats `$5 and $10` as currency, so reword only if you actually see a misrender.

Always tag a code fence with its language. An untagged fence still renders monospace, just without highlighting — and the tag also tells the reader what they are looking at.
