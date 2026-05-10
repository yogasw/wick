---
outline: deep
---

# Workspaces

A **workspace** is a folder. That's it.

The agent runs as a subprocess; the workspace is the `cwd` it gets. Whatever you (or the agent itself, via Bash) put in the folder is the agent's world.

::: info Source
Code: [`internal/agents/workspace/workspace.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workspace) ([workspace.go:31](https://github.com/yogasw/wick/blob/master/internal/agents/workspace/workspace.go#L31) for `Meta`).
Layout math: [`internal/agents/config/layout.go`](https://github.com/yogasw/wick/blob/master/internal/agents/config/layout.go).
:::

## Two kinds: managed vs custom

| Kind | Where the files live | Created by | Wick deletes on workspace delete? |
|---|---|---|---|
| **Managed** | `~/.<app>/agents/workspaces/<name>/files/` | Wick (`MkdirAll` at create time) — empty folder | Yes |
| **Custom path** | Any absolute path you point at (e.g. `D:/code/myproject`, `~/scratch`) | You (must already exist before you create the workspace) | No — wick never owned it, wick never deletes it |

The custom-path requirement that the directory **must already exist** is enforced at create time ([workspace.go:83-85](https://github.com/yogasw/wick/blob/master/internal/agents/workspace/workspace.go#L83-L85)) — typos surface immediately, not at first spawn.

A workspace can hold zero, one, or many repos. There's no git worktree, no auto-clone, no master-branch model. The agent does the cloning itself via Bash if you ask it to.

## Built-in `default` workspace

Every fresh install has a workspace named `default` ([workspace.go:141](https://github.com/yogasw/wick/blob/master/internal/agents/workspace/workspace.go#L141)). It's created by `EnsureDefault` at boot, can't be deleted ([workspace.go:163](https://github.com/yogasw/wick/blob/master/internal/agents/workspace/workspace.go#L163)), and is what the pool falls back to when a session doesn't specify a workspace and no other default is configured.

This is what makes "first-message-creates-session" work without any pre-setup: a fresh install + a Slack message in a thread = a session bound to `default`, agent spawned in `~/.<app>/agents/workspaces/default/files/`.

## Multi-session sharing

Multiple sessions can share the same workspace and run in parallel. Wick does not lock — coordination is your concern.

> "Aku minta claude clone repoA, minta claude clone repoB. Numpuk di workspace `soluport-ops`. Jadi dia bisa pakai ulang."

The real use case is a folder full of stuff that several conversations need to touch. Two sessions reading files at the same time = fine. Two sessions both editing `package.json` = on you. Most agent traffic is read, so this is rarely a problem in practice.

## Web UI

> **📸 Screenshot needed:** `agents-workspaces-list.png` — capture `/tools/agents/workspaces` showing the workspace cards (at minimum: `default`, plus one custom). Save to `docs/public/screenshots/agents-workspaces-list.png`.

> **📸 Screenshot needed:** `agents-workspace-create.png` — capture the "New Workspace" modal with the Custom Path field visible, helper text showing where managed paths land. Save to `docs/public/screenshots/agents-workspace-create.png`.

The form fields ([handler at](https://github.com/yogasw/wick/blob/master/internal/tools/agents/handler.go), templates at [view/workspaces.templ](https://github.com/yogasw/wick/blob/master/internal/tools/agents/view)):

| Field | Notes |
|---|---|
| **Name** | Required. Used as folder name + form key. Validated by [`storage.ValidateWorkspaceName`](https://github.com/yogasw/wick/blob/master/internal/agents/storage). |
| **Custom Path** | Optional. Absolute path to an existing folder. Empty = managed. |
| **Default Preset** | Preset bound at session-create time. Defaults to `default`. |
| **Default Provider** | Provider type used when sessions in this workspace don't specify one. |
| **Description**, **Tags** | UI-only metadata. |

## Meta on disk

`workspaces/<name>/meta.json`:

```json
{
  "custom_path": "D:/code/myproject",
  "default_preset": "default",
  "default_provider": "claude",
  "description": "Soluport ops scratchpad",
  "tags": ["ops", "scratch"],
  "created_at": "2026-05-09T10:00:00Z"
}
```

`custom_path` is omitted for managed workspaces. Atomic write (tmp file + rename) on every save.

## Resolving the cwd at spawn time

The pool calls [`workspace.ResolvePath`](https://github.com/yogasw/wick/blob/master/internal/agents/workspace/workspace.go#L189) when it's about to spawn an agent:

1. Session has a `workspace` field set → load that workspace's meta.
2. Custom path? Return it as-is.
3. Managed? Return `~/.<app>/agents/workspaces/<name>/files/`.
4. Session has no workspace → pool falls back to `cfg.DefaultWorkspace` (set by tools-config) → if still nothing → per-session temp dir at `sessions/<id>/cwd/`.

The pool `MkdirAll`s managed paths before passing them to `exec.Cmd.Dir`. Custom paths are assumed to still exist; if you deleted yours out from under wick, spawn surfaces a clean error.

## Switching workspaces

`SwitchWorkspace` ([session.go:185](https://github.com/yogasw/wick/blob/master/internal/agents/session/session.go#L185)) is metadata-only — no filesystem work, no agent kill, no log truncation. The next agent spawn picks up the new path. Useful for "the agent was answering in workspace A; now move this conversation to workspace B."

## Slack / Telegram default workspace

Each channel ([Slack](https://github.com/yogasw/wick/blob/master/internal/agents/config/slack.go), [Telegram](https://github.com/yogasw/wick/blob/master/internal/agents/config/telegram.go)) has its own `Workspace` config field. When set, every session auto-created from that channel binds to it.

When **only one workspace exists**, the channel uses it without asking — the operator doesn't need to configure `slack_workspace` for the single-workspace happy path. See [Slack channel decisions PR #209](https://github.com/yogasw/wick/pull/209) (decision S6).

## See also

- [Pool & Sessions](./pool) — how the cwd is actually wired into `exec.Cmd`.
- [Providers](./providers) — `default_provider` field on workspace meta.
- [Channels](./channels) — per-channel default workspace config.
