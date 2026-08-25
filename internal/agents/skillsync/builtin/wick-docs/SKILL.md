---
name: wick-docs
description: Use when asked anything about wick itself — what it is, what it can do, whether an update is available, what changed in a release, how a feature works, or how to configure something. Points to the live documentation site and release feed so answers come from current sources, never from memory.
---

# Answering questions about wick

You are running inside wick, but your training data does not contain its
current state — features ship weekly. **Never answer a wick question from
memory.** Everything below is a live source; fetch it, then answer. That is
what keeps this skill valid without ever being updated.

## The documentation site

Base: `https://yogasw.github.io/wick/`

Start at the machine-readable index:

```
https://yogasw.github.io/wick/llms.txt
```

It lists every docs page with a one-line description, grouped by topic
(guide, connectors, workflow, reference, …). Pick the relevant page from it —
do not guess URLs.

Every page has a raw-markdown twin at the same path with `.md` appended:

```
https://yogasw.github.io/wick/guide/introduction.md
```

Fetch the `.md` form — clean markdown, no HTML to wade through. There is also
`llms-full.txt` (every page concatenated); it is large, reach for it only when
a broad question genuinely spans many pages.

## "Is there an update?" / "What changed?"

Three sources, use together:

1. **Running version** — `wick version` prints it, if you have a shell.
   Otherwise the human can read it in the admin panel under
   Advanced → Software Update, which also has a one-click check.
2. **Latest release** — `https://api.github.com/repos/yogasw/wick/releases/latest`
   → `tag_name` (vX.Y.Z) and `published_at`. Unauthenticated GitHub API is
   capped at 60 req/hr per IP; on a 403, fall back to the changelog below.
3. **What changed** — `https://yogasw.github.io/wick/changelog.md`.
   Newest first; an `[Unreleased]` section on top holds merged-but-unreleased
   work. To answer "what's new for me", take the entries between the running
   version and the latest tag.

Update available = latest tag newer than running version. Report both numbers
and the highlights from the changelog range — not just "yes".

## Question → source

| Question shape | Fetch |
|---|---|
| what is wick / what can it do | `llms.txt`, then the introduction + relevant topic pages |
| is there an update / what version | releases/latest + running version |
| what changed / release notes | `changelog.md` |
| how do I … / how does … work | `llms.txt`, then that feature's page |
| does wick support X | `llms.txt` — if no page mentions it, say so; do not invent |

## Rules

- Quote the site as it is today; if the docs and your prior knowledge
  disagree, the docs win.
- Link the page you used (the human-readable URL, without `.md`) so the
  reader can go deeper.
- If a fetch fails, say what you could not reach — do not fill the gap
  from memory.
