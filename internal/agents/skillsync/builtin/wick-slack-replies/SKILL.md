---
name: wick-slack-replies
description: Use BEFORE writing any reply that will be delivered to Slack — Slack uses mrkdwn, not markdown. Plain replies go through unconverted, while replies with tables, headings, **bold**, [label](url) links, nested lists or code fences are sent as a Slack markdown block. Covers which path a reply takes, the syntax that differs (bold, italic, strike, links, headings, tables), which rich fences degrade badly, how to write one message that reads well on both Slack and the web chat, and the Slack-specific mention and code-block rules.
---

# Writing replies that land well in Slack

Slack's normal message text is *mrkdwn*, not markdown — a different syntax that overlaps just enough to be misleading. wick picks one of two paths for every reply it posts to Slack (the thread reply, its live edits, and `send_message`/`update_message` called with text only):

- **Markdown path.** When the reply contains a pipe table, a `#` heading, `**bold**`, a `[label](url)` link, a nested list or a ```` ``` ```` fence, wick sends it as a Slack `markdown` block. Slack renders it as real markdown — tables arrive as native Slack tables. A short plain version goes along as the notification text. Long replies are split between paragraphs and table rows (a split table repeats its header), never mid-row.
- **mrkdwn path.** Anything else is posted as-is, with no conversion, and Slack parses it as mrkdwn.

So the reply's dialect is decided by the reply as a whole. Mixing the two goes wrong: in a reply that takes the markdown path, mrkdwn `*bold*` turns into italics and `<https://x|label>` may not render as a link.

## The syntax that differs

| Intent | Markdown (web chat) | mrkdwn (Slack) |
|---|---|---|
| Bold | `**bold**` | `*bold*` |
| Italic | `*italic*` or `_italic_` | `_italic_` |
| Strikethrough | `~~gone~~` | `~gone~` |
| Link | `[label](https://x)` | `<https://x\|label>` |
| Heading | `## Title` | *none* — use `*Title*` on its own line |
| Table | pipe table | *none* — but a pipe table sends the whole reply down the markdown path, where it renders |
| Bullet | `- item` | `• item` (a literal `-` also reads fine) |
| Inline code | `` `code` `` | `` `code` `` (same) |
| Code block | ```` ```lang ```` | ```` ``` ```` — **no language tag** |

Note the trap in the first two rows: `*text*` means **bold** in mrkdwn but *italic* in markdown. They are not interchangeable.

## The practical strategy

You usually do not know for certain which surface a reply lands on, and the same text may be read in both. So write for the **intersection** rather than switching dialects:

- **Prefer plain prose and short paragraphs.** They render identically everywhere.
- **Tables are fine.** A pipe table renders as a native Slack table, so use one when the data is genuinely tabular. Once a reply has a table, write the rest of it in markdown too (`**bold**`, `[label](url)`), not mrkdwn.
- **Headings are optional.** They render on the markdown path, but in a short Slack reply a bolded line or a new paragraph usually reads better.
- **Use `-` bullets.** They read fine on both.
- **Keep it short.** Slack messages are read in a narrow column, usually on a phone. A wall of text that scans fine in the web chat is unreadable there.

When you *know* the reply is Slack-only — a channel notification, an alert, a workflow posting to a channel — either write native mrkdwn (`*bold*`, `<url|label>`) with no markdown constructs, or write full markdown. Both look right; a mix of the two does not.

## Links

In a plain mrkdwn reply, markdown link syntax does not work (it only works when the reply takes the markdown path). In mrkdwn:

```
<https://example.com/very/long/path|the label>
```

A bare URL also auto-links, which is often the better choice — it is readable on every surface. Reach for the `<url|label>` form when the raw URL is long or ugly.

## Mentions

These are not plain text — they must be the special form, or they appear literally and notify no one:

| Target | Write |
|---|---|
| A user | `<@U012ABCDEF>` (the user **ID**, not the display name) |
| A channel | `<#C012ABCDEF>` |
| Everyone here | `<!here>` |
| The whole channel | `<!channel>` |

Writing `@yoga` produces the literal text `@yoga` and pings nobody. If you have a display name but not an ID, look the ID up (the Slack connector can search users) rather than guessing.

These forms are mrkdwn. A reply that takes the markdown path is not guaranteed to turn them into pings, so when a mention must notify someone, keep that message free of tables, headings and other markdown constructs.

Use `<!here>` and `<!channel>` sparingly — they notify real people. Prefer naming the one person who needs to act.

## Code blocks

Slack code fences take **no language tag**. Writing ```` ```go ```` puts a literal `go` on the first line of the block.

For Slack-bound replies use a bare fence. For a reply that may be read in both places, a tagged fence is usually still the better trade — the web chat highlights it, and Slack shows one stray word — but keep the block short.

## Rich fences degrade, they do not render

The web chat's rich fences (`svg`, `mermaid`, `imagecard`, `html`, `htmlfile`) have no Slack equivalent. They fall back to their raw source, which means:

- `imagecard` degrades to readable `url | caption` lines. Acceptable.
- `htmlfile` degrades to a filename. Acceptable.
- `html` degrades to a **wall of raw markup**. Never inline a large HTML document into a reply that may reach Slack — use `htmlfile` and reference the path.
- `svg` and `mermaid` degrade to source. Fine when small; avoid dumping a large diagram.

If the answer's value is genuinely visual, say so in one line and point to the web session rather than pasting source Slack cannot render.

## Threads

A reply posts into the thread that started it, so context is preserved without repeating it. Do not re-quote the user's whole message back at them — say the new thing.

## Editing in place

wick updates a live message as a turn streams rather than posting a new one per chunk. That means very long replies are rewritten in place several times. Another reason to keep Slack replies tight: every edit re-renders the whole message for everyone watching.
