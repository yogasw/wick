---
outline: deep
---

# Source

`source` exposes **which repository a session is working in** — the same selection the [Source Control panel](/guide/agents/source-control) shows the human — as a fixed connector.

| | |
|---|---|
| **Source** | [`internal/connectors/source/`](https://github.com/yogasw/wick/tree/master/internal/connectors/source) |
| **Key** | `source` |
| **Icon** | 🌿 |
| **Fixed** | ✅ — single row, the repos are the session's own working directory |
| **Default tags** | `tags.Connector`, `tags.Platform` |

## Why it exists

A session's working directory routinely holds many cloned repositories. The Source panel has **one** of them selected, and that selection used to live only in the browser — so "this repo" was a guess the agent made from whichever path was mentioned last, and it guessed wrong the moment the human switched repos.

The selection now lives on the session (`meta.json` → `ScmRepo`), the system prompt names it as `active_repo`, and these ops are how the agent re-reads or moves it.

A connector rather than a hard-coded meta-tool, for the same reason [`notes`](./notes) and [`tickets`](./tickets) are: a connector is taggable and auditable per user, and each op carries its own name and schema instead of being an action string on one overloaded tool.

## Configs

Intentionally empty (`type Configs struct{}`). The repositories are the session's own working directory, and git runs with the credentials already on the machine.

## Scope

Every op takes an optional `session_id` and defaults to the **calling** session, so in normal use they need no arguments at all.

## Operations

### Repositories

| Op | Input | Purpose |
|---|---|---|
| `source_active` | `session_id` | The repository this session is working in: `rel` handle, absolute `dir`, `branch`, and `changed` / `ahead` / `behind` counts. |
| `source_list` | `session_id` | Every git repository under the session's working directory, with branch and change counts, and which one is active. |
| `source_changes` | `repo`, `session_id` | The files changed right now in a repository — path, staged / unstaged / untracked, and git status codes. Defaults to the active repo. |
| `source_select` | `repo`, `session_id` | Switch the repository this session works in. |

`repo` accepts the `rel` handle from `source_list`, the repository's name, or its absolute path.

## What is in the prompt and what is not

The active repo is **already** in the system prompt as `active_repo`, so an agent does not need a tool call to know where it is. `source_active` is for when that line looks stale — after someone may have switched the panel.

`source_changes` is deliberately **not** in the prompt. Its answer has a shelf life of seconds, and a snapshot taken at spawn time would describe files nobody is editing any more. Call it when you need to know what is in flight: before proposing a commit, when the user asks "what did I change", or to check whether your own edit landed.

::: warning `source_select` moves the human's panel
It is not a lookup. Call it when the user asks to work somewhere else — not to read something, which needs no selection at all. Pass an empty `repo` to clear the pick and fall back to the first repository found.
:::

`explicit: false` in a response means nobody has chosen a repository and this is simply the first one discovered — a default worth stating as one, rather than an instruction.

## See also

- [Source Control panel](/guide/agents/source-control) — the human-facing side of the same selection.
- [Git CLI](./git) — running actual git operations against those repositories.
- [Projects](/guide/agents/projects) — how a session's working directory is determined.
