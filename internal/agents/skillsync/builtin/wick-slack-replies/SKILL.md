---
name: wick-slack-replies
description: Use BEFORE writing any reply that will be delivered to Slack — Slack uses mrkdwn, not markdown, and wick sends your text through unconverted. Covers the syntax that differs (bold, italic, strike, links, headings, tables), which rich fences degrade badly, how to write one message that reads well on both Slack and the web chat, and the Slack-specific mention and code-block rules.
---

# Writing replies that land well in Slack

Slack does **not** render markdown. It renders *mrkdwn*, a different syntax that overlaps just enough to be misleading. wick posts your reply text to Slack as-is — there is no markdown-to-mrkdwn conversion in the send path — so whatever you type is what Slack tries to parse.

That single fact causes most bad-looking Slack replies: `**bold**` arrives as literal asterisks, `## Heading` as a literal `##`, and a markdown table as a jumble of pipes.

## The syntax that differs

| Intent | Markdown (web chat) | mrkdwn (Slack) |
|---|---|---|
| Bold | `**bold**` | `*bold*` |
| Italic | `*italic*` or `_italic_` | `_italic_` |
| Strikethrough | `~~gone~~` | `~gone~` |
| Link | `[label](https://x)` | `<https://x\|label>` |
| Heading | `## Title` | *none* — use `*Title*` on its own line |
| Table | pipe table | *none* — use lines or a code block |
| Bullet | `- item` | `• item` (a literal `-` also reads fine) |
| Inline code | `` `code` `` | `` `code` `` (same) |
| Code block | ```` ```lang ```` | ```` ``` ```` — **no language tag** |

Note the trap in the first two rows: `*text*` means **bold** in mrkdwn but *italic* in markdown. They are not interchangeable.

## The practical strategy

You usually do not know for certain which surface a reply lands on, and the same text may be read in both. So write for the **intersection** rather than switching dialects:

- **Prefer plain prose and short paragraphs.** They render identically everywhere.
- **Skip headings.** On Slack a `##` is literal noise. If you need a section label, put a short bolded line or just start a new paragraph.
- **Skip tables** for Slack-bound replies. Use a short list of `label: value` lines, or a code block if alignment genuinely matters.
- **Use `-` bullets.** They read fine on both.
- **Keep it short.** Slack messages are read in a narrow column, usually on a phone. A wall of text that scans fine in the web chat is unreadable there.

When you *know* the reply is Slack-only — a channel notification, an alert, a workflow posting to a channel — write native mrkdwn (`*bold*`, `<url|label>`) and it will look right.

## Links

Markdown link syntax does not work. In mrkdwn:

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
