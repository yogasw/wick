---
name: wick-html-widget
description: Use when an HTML artifact / widget preview in wick chat misbehaves — blank or clipped iframe, "violates the following Content Security Policy directive", a stylesheet or script that never loads, images blocked by img-src, a slide deck whose navigation is dead, or a preview that needs to read a session file. Covers the one-file rule and why a multi-file page cannot work (srcdoc has no base URL), the default artifact CSP directive by directive, the Widget permissions presets an operator can change (secure / unsecure / custom with block|list|all) and what they do NOT fix, the bridges for reaching session data (wickReadFile, wickDataTable), why .css and .js served from a session are refused, and how to predict breakage from the file before previewing it. Read this BEFORE loosening any CSP or blaming the theme.
---

# HTML artifacts and widgets in wick

`wick-rich-output` covers WHICH fence to write. This file is for when the
preview is already misbehaving, or when you are about to build something that
needs more than plain inline markup.

## The one-file rule

**An artifact preview renders exactly one self-contained file.** A page that
pulls a sibling `.css`, `.js`, or image by URL cannot work — not because it is
forbidden, but because it breaks in two independent layers, and opening the
security policy only addresses one of them.

Layer 1 — **there is no base URL.** The preview injects the markup as an
iframe `srcdoc`, and a srcdoc document has no URL of its own. A relative
`href="deck.css"` therefore resolves against the PARENT page's route, not
against the folder the file lives in:

```
Loading the stylesheet 'https://<host>/tools/agents/sessions/deck.css'
violates ... "style-src 'unsafe-inline'"
about:srcdoc:1 Loading the script '.../tools/agents/sessions/deck-stage.js' ...
```

`/tools/agents/sessions/deck.css` is nowhere near the file. Even with every
directive wide open that URL is a 404. And `<base href>` cannot rescue it: the
session-file endpoints are query-style (`?path=…`), and a relative path cannot
attach to a query string.

Layer 2 — **the default policy has no host source at all** (below).

So: inline everything into one file. Base64 the images and fonts. If a build
step produces both a multi-file `index.html` and a bundled standalone, point
the ` ```htmlfile ` fence at the **standalone** — they usually sit in the same
folder one name apart, which is exactly how the wrong one gets previewed.

## The default CSP, directive by directive

Built by `artifactCSP` and injected as a `<meta http-equiv>` inside the
srcdoc. Under the default (secure) preset:

```
default-src 'none'      style-src  'unsafe-inline'    form-action 'none'
script-src 'unsafe-inline'   img-src   data:          object-src  'none'
font-src   data:        media-src data:                base-uri    'none'
connect-src (empty — no network at all)
```

Read it as one rule: **inline and `data:` are allowed; anything fetched by URL
is not.** That is why a self-contained page with inline `<script>`, inline
`<style>`, `<img src="data:…">` and `@font-face { src: url(data:…) }` renders
perfectly, while a page with one `<link>` loses its whole stylesheet.

The iframe is also sandboxed WITHOUT `allow-same-origin`, so it runs on an
opaque origin and cannot touch the parent's cookies, storage, or DOM. No
preset ever grants `allow-same-origin`.

## Widget permissions (what an operator can change)

Project settings → **Widget permissions**. One knob, three presets — see
[Projects](https://yogasw.github.io/wick/guide/agents/projects) for the
operator-facing description and the per-project override.

- `secure` — everything blocked. The default, and what an empty or
  unrecognised value falls back to. Per-directive fields are ignored.
- `unsecure` — every configurable directive open to any HTTPS host, popups
  allowed. Per-directive fields are ignored. This includes `script-src`, so a
  widget may load and run code from anywhere; combined with an open
  `connect-src` that code can exfiltrate whatever the widget holds, including
  anything handed over the file and data-table bridges.
- `custom` — and only then are the per-directive fields read, each as
  `block` (`'none'`), `list` (named hosts only), or `all` (`https:`).

**Do not reach for this to fix a sibling-asset failure.** It cannot: no preset
emits `'self'`, and even `all` (`https:`) leaves layer 1 untouched — the URL is
still resolved against the wrong path. Loosening the policy widens what
untrusted, model-authored HTML can reach and does not make the preview work.
Inline the assets instead.

## Reaching session data from inside an artifact

An artifact has `connect-src` closed, so `fetch()` and `XHR` fail by design.
Two bridges exist instead; both are injected by the runtime and work by
`postMessage` to the parent, so no network request leaves the frame:

- `wickReadFile(path)` → Promise of the file's **text**. Path is
  session-relative; absolute paths, URLs and `..` are rejected.
- `wickDataTable` → `query` / `insert` / `update` / `delete`, enforced
  server-side against the signed-in user's grants.

For small, known-at-write-time data, skip both and embed it in a
`<script type="application/json">` block.

## Why a session .css or .js is refused even at the right URL

The session-file endpoint deliberately does not serve executable or styleable
types. `.css` is not in the inline-safe MIME whitelist at all, so it comes back
`application/octet-stream` with `Content-Disposition: attachment`; `.js` is
downgraded to `text/plain`. Both arrive with `X-Content-Type-Options: nosniff`,
so the browser refuses them:

```
Refused to apply style from '…' because its MIME type ('text/plain') is not a
supported stylesheet MIME type, and strict MIME checking is enabled.
```

That is the endpoint refusing to hand agent-written code back as something the
browser will run from the app's own origin. Treat it as fixed.

## Predict the breakage before previewing

Count the subresources in the file. Zero of each means the preview will be
clean; any non-zero count tells you exactly which console errors to expect:

```bash
python3 - <<'PY'
import re,sys
t=open(sys.argv[1],errors="replace").read()
for pat,label in [(r'<link\b[^>]*href',"<link> by URL"),
                  (r'<script\b[^>]*src\s*=',"<script src=>"),
                  (r'<img\b[^>]*src\s*=\s*["\'](?!data:)','<img> non-data:'),
                  (r'url\(\s*["\']?(?!data:)[^)"\']+\)',"css url() non-data:")]:
    print(f"{label:22s}: {len(re.findall(pat,t,re.I))}")
PY
```

## Console signature → cause

- `violates … "style-src 'unsafe-inline'"` / `script-src` / `img-src data:` —
  the file loads that asset by URL. One file, inline it.
- URL contains `/tools/agents/sessions/<asset>` — layer 1: a relative path
  resolved against the parent route. Confirms it is a multi-file page.
- `about:srcdoc:1` prefix — confirms the srcdoc path, so there is no base URL
  to fix.
- `MIME type ('text/plain') is not a supported stylesheet` — either the
  endpoint downgrade above, or the stylesheet 404'd and you are seeing the
  error page's content type.
- Nothing in the console but a clipped page — the preview caps iframe height
  at 2400px. A full-viewport slide stage will be cut; that is a layout
  constraint, not a policy one.

## Do not

- Do not widen Widget permissions to make an artifact work. Inline instead.
- Do not conclude "the theme bridge broke it" — the bridge only sets
  `--wick-*` variables and a `dark` class.
- Do not point ` ```htmlfile ` at a multi-file `index.html`.
