---
outline: deep
---

# Projects

A **project** bundles a folder with its defaults. One project = **1 folder + defaults + pinned sessions + a display name and icon**. Sessions belong to a project; the project's folder is the `cwd` the agent subprocess runs in.

The agent runs as a subprocess; the project's folder is the `cwd` it gets. Whatever you (or the agent itself, via Bash) put in the folder is the agent's world.

::: info Source
Code: [`internal/agents/project/project.go`](https://github.com/yogasw/wick/blob/master/internal/agents/project) (`Meta`, CRUD, `ResolvePath`).
Migration from the old workspace model: [`internal/agents/project/migrate.go`](https://github.com/yogasw/wick/blob/master/internal/agents/project/migrate.go).
Layout math: [`internal/agents/config/layout.go`](https://github.com/yogasw/wick/blob/master/internal/agents/config/layout.go).
:::

::: tip Renamed from "Workspace"
Projects replace the old **Workspace** concept (familiar term from Codex/Claude). On first boot after upgrade, every existing workspace is migrated 1:1 into a project with the same folder + defaults, and sessions are re-linked automatically — no data loss. The old `workspaces/` directory is kept on disk as a defensive backup.
:::

## Two kinds: managed vs custom

| Kind | Where the files live | Created by | Wick deletes on project delete? |
|---|---|---|---|
| **Managed** | `~/.<app>/agents/projects/<id>/files/` | Wick (`MkdirAll` at create time) — empty folder | Yes |
| **Custom path** | Any absolute path you point at (e.g. `D:/code/myproject`, `~/scratch`) | You (must already exist before you create the project) | No — wick never owned it, wick never deletes it |

The custom-path requirement that the directory **must already exist** is enforced at create time — typos surface immediately, not at first spawn.

A project can hold zero, one, or many repos. There's no git worktree, no auto-clone, no master-branch model. The agent does the cloning itself via Bash if you ask it to.

The folder is part of the project's identity: there's no multi-folder project. Want a different folder? Create another project, or move the session to one.

## Built-in `default` project

Every fresh install has a project named `default`. It's created by `EnsureDefault` at boot, can't be deleted, and is what the pool falls back to when a session doesn't specify a project.

This is what makes "first-message-creates-session" work without any pre-setup: a fresh install + a Slack message in a thread = a session bound to `default`, agent spawned in `~/.<app>/agents/projects/<default-id>/files/`.

Personal projects (auto-created per user, tagged `personal`) are protected from deletion the same way — the delete button is hidden and the API rejects the request for any project where `project.IsProtected` returns true (built-in `default`, or tagged `personal`).

## Defaults

Each project carries defaults that new sessions inherit when you don't override them per-session:

| Default | Effect |
|---|---|
| **Preset** | Preset bound at session-create time. Falls back to `default`. |
| **Provider** | Provider instance (`type/name`, e.g. `claude/work`) used when a session doesn't specify one. The dropdown lists every healthy instance, not just base types. Bare type values from older projects are promoted to the canonical default instance automatically. |
| **System prompt addon** | Free-text appended to the preset's system prompt for every session in this project. |

In the New Session composer, picking a project pre-fills the provider + preset from these defaults; you can still override either per session. The provider dropdown in the composer also shows full `type/name` instances — selecting a project auto-selects its saved default provider when that instance is available.

If the saved default provider instance is renamed or deleted after a project is created, the settings form shows it as `type/name (unavailable)` so the value isn't silently overwritten.

## Web UI

Projects live in the **left sidebar** — there's no separate list page. The `PROJECTS` section lists every project with its session count; clicking one opens the project (a Claude-style landing: compose box on top, the project's chats below). Hover a row to reveal a 📌 pin toggle.

- **+ New** (sidebar) → `/tools/agents/projects/new` — the create page.
- **⚙ Settings** (on a project's landing) → `/tools/agents/projects/<id>` — the full settings page: icon + name, folder (managed/custom radio), defaults, pinned sessions, a `meta.json` preview, and delete.

The settings form fields:

| Field | Notes |
|---|---|
| **Icon** | One emoji. Optional; defaults to 📁. |
| **Name** | Required. Display name (mutable — the id never changes). |
| **Folder** | Radio: Custom path (absolute, must exist) or Managed (`projects/<id>/files/`). |
| **Default Preset / Provider** | Inherited by new sessions. |
| **System prompt addon** | Appended to the preset system prompt for every session. |
| **Description** | UI-only metadata. |

## Pin a project as your default

Each user can **pin one project** as their personal default (stored in their user metadata, `pinned_agent_project_id`). When set, opening the Agents tool lands you straight in that project's compose page.

Pin/unpin from the 📌 toggle on the sidebar row or the `📌 Pin as default` button on the project landing. One pin per user — pinning another replaces it.

## Meta on disk

`projects/<id>/meta.json`:

```json
{
  "id": "01J...",
  "name": "Wick Backend",
  "icon": "📁",
  "description": "Main wick repo work",
  "custom_path": "/d/code/work/wick",
  "defaults": {
    "preset": "engineer",
    "provider": "claude",
    "system_addon": ""
  },
  "pinned_sessions": ["01J..."],
  "tags": [],
  "created_at": "2026-06-01T...",
  "updated_at": "2026-06-01T..."
}
```

`custom_path` is omitted for managed projects. Atomic write (tmp file + rename) on every save; `updated_at` is bumped automatically.

## Resolving the cwd at spawn time

The pool calls `project.ResolvePath` when it's about to spawn an agent:

1. Session has a `project_id` set → load that project's meta.
2. Custom path? Return it as-is.
3. Managed? Return `~/.<app>/agents/projects/<id>/files/`.
4. Session has no project → per-session temp dir at `sessions/<id>/cwd/`.

The pool `MkdirAll`s managed paths before passing them to `exec.Cmd.Dir`. Custom paths are assumed to still exist; if you deleted yours out from under wick, spawn surfaces a clean error.

## Moving a session between projects

A session stores its binding as `meta.project_id`. Moving is **metadata-only** — no filesystem work, the session id and path stay stable so workflows / channels / spawn references don't break. Two ways:

- **Drag** a chat row (sidebar or list) onto a project in the sidebar.
- The **Move to project** menu on the session detail page.

The new project's folder becomes the cwd at the next spawn. A live subprocess keeps its old cwd until it's killed and respawned. Deleting a project doesn't delete its sessions — they're just unscoped (`project_id` cleared).

## Multi-session sharing

Multiple sessions can share the same project and run in parallel. Wick does not lock — coordination is your concern. Most agent traffic is read, so two sessions touching the same folder is rarely a problem in practice; two sessions both editing `package.json` is on you.

## Slack / Telegram / REST default project

Each channel ([Slack](https://github.com/yogasw/wick/blob/master/internal/agents/config/slack.go), [Telegram](https://github.com/yogasw/wick/blob/master/internal/agents/config/telegram.go), [REST](https://github.com/yogasw/wick/blob/master/internal/agents/config/rest.go)) has its own `project_id` config field. When set, every session auto-created from that channel binds to it. When **only one project exists**, the channel uses it without asking.

The REST (OpenAI-compatible) channel additionally lets a request **override** the channel default per call with a top-level `"project": "<id>"` field (or `metadata.project` / `metadata.project_id`). See the [REST channel docs](./channels).

## See also

- [Pool & Sessions](./pool) — how the cwd is actually wired into `exec.Cmd`.
- [Providers](./providers) — the `provider` default on project meta.
- [Channels](./channels) — per-channel default project config.
