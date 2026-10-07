# Embedding a Tool in an iframe

Any tool page can be dropped into another page with a plain `<iframe>`. There is no
embed URL, no API key, and no per-tool setting to switch on:

```html
<iframe src="https://wick.example.com/tools/convert-text"
        style="width:100%;height:600px;border:0"></iframe>
```

Inside a frame the page renders **without wick's chrome** — no navbar, no tool
header, no setup banner — so it reads as part of the host page instead of an app
nested inside an app. Outside a frame the same URL is the full page it always was.

## How it decides

A navigation into a frame carries `Sec-Fetch-Dest: iframe`, and wick reads it. That
is the whole mechanism, and it buys three things a query parameter would not:

- the first paint is already correct — no flash of a navbar that then disappears;
- it survives **links and form posts made inside the frame**, because the browser
  sends the header on those too. A tool whose second page brought the navbar back
  would defeat the point;
- it costs nothing on a normal page load.

Every browser that speaks Fetch Metadata sends it: Chrome 80+, Firefox 90+, Safari
16.4+.

### Overriding it

| URL | Result |
|-----|--------|
| `/tools/convert-text` | chrome outside a frame, no chrome inside one |
| `/tools/convert-text?embed=1` | never any chrome, even as a top-level page |
| `/tools/convert-text?embed=0` | always chrome, even inside a frame |

`embed=1` is for a browser too old to send the header. `embed=0` is for reading the
full page while you debug one. Both accept `true`/`false`, `yes`/`no`, `on`/`off`.

Note the override rides on the URL, so it applies to the page you asked for, not to
wherever a link inside it goes. The header applies to every request, which is why it
is the primary signal and the query string is the escape hatch.

## What is stripped, and what is not

| Removed in a frame | Kept |
|---|---|
| the navbar | the page shell — stylesheet, theme class, fonts |
| the shared tool header (icon, name, description, Settings link) | everything the tool itself renders |
| the "setup required" banner | the job page's own title strip and **Run Now** button |

The job page keeps its title and its button on purpose: those are the page, not
chrome. A framed `/jobs/{key}` that could not run the job would be a screenshot.

## Full-screen tools are exempt

A tool registered with `FullScreen: true` — the agents console, for instance —
already skips the shared chrome and draws its own workspace switcher, profile menu
and theme picker. Embed mode does not touch it: its layout inside a frame is
byte-identical to its layout outside one. Stripping chrome a tool renders itself
would leave it unusable, not cleaner.

## Access: same-origin vs cross-origin

This is the part that decides whether a frame works at all, and it is not about
embedding — it is about the session cookie.

**Same origin** (the host page is on the same wick domain): the session cookie is
sent as usual, so a private tool works in the frame exactly as it does in a tab.
Nothing extra to do.

**Cross origin** (the host page is a different site): wick's session cookie is
`SameSite=Lax`, so the browser does **not** send it into the frame. The request
arrives as a guest, and a private tool answers with a redirect to the login page —
which then renders inside your frame. So a cross-origin embed only works for a tool
that is reachable **without a session**:

1. open `/admin/tools`;
2. set the tool's visibility to **public**;
3. embed it.

Public means public — anyone who can load the host page can use that tool, and
anyone who guesses the URL can use it directly. Treat the decision as "is this tool
safe to expose to the internet", because that is what it is. Tools that read or
write anything sensitive belong on a same-origin embed.

`/jobs/{key}` is login-only regardless of visibility: it embeds, but it has no guest
mode, because a guest who can reach the page can press Run Now.

## Framing is allowed by default

wick sets no `X-Frame-Options` and no `frame-ancestors` directive, so nothing has to
be relaxed to embed a page. If you want to *restrict* which sites may frame your
instance, add a `Content-Security-Policy: frame-ancestors …` header at the reverse
proxy in front of wick — that is the right layer for a policy that depends on your
deployment rather than on the tool.

## For tool authors

Nothing to implement. The shell is rendered for you, and embed mode only changes
what the shell draws around your body. Two habits keep a tool frame-friendly:

- **Do not render your own `<h1>` and description.** The shared tool header is the
  canonical one, and it is what embed mode removes — a title of your own survives
  into the frame and reads as a duplicate.
- **Let the page size itself.** A frame has whatever height the host page gave it;
  a body pinned to `100vh` will scroll inside it.

If you need the flag in a handler — to hide a "back to tools" link of your own, say —
read it from the request context:

```go
if ui.EmbeddedFromContext(c.R.Context()) {
    // drawn inside a frame
}
```
