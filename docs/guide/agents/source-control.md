---
outline: deep
---

# Source Control (Git) Panel

The **Source Control** panel is a VSCode-style SCM sidebar mounted on the session detail page (`/tools/agents/sessions/<id>`). It lets you stage, commit, push, pull, view diffs, and browse history for any git repositories inside the session's working directory — without leaving the chat.

::: info Source
Backend: [`internal/agents/scm/`](https://github.com/yogasw/wick/blob/master/internal/agents/scm) — `git.go` (git shell-outs), `scan.go` (multi-repo discovery).
Handler: [`internal/tools/agents/scm.go`](https://github.com/yogasw/wick/blob/master/internal/tools/agents/scm.go), `scm_watch.go` (SSE watcher).
Frontend SPA: [`fe/agents/scm/`](https://github.com/yogasw/wick/blob/master/fe/agents/scm) — Svelte 5, Monaco editor, mounted as an island into the session page.
Endpoints: `/tools/agents/api/sessions/{id}/git/*`.
:::

::: warning Prerequisite
`git` must be on `PATH` on the host running wick. The panel shells out to the real `git` binary — it does not bundle one. Existing SSH / PAT credentials and `~/.gitconfig` settings apply as-is.
:::

## Opening the panel

The session detail page has a right-edge rail with three tabs: **Context**, **Process**, and **Source**. Click **Source** (or the tab label) to open the Source Control panel.

The panel opens as a slide-over overlay by default. To dock it, click the **pin** icon in the panel header. When pinned, the chat content area reflows to the left to make room — the panel does not overlap it.

| State | Behaviour |
|---|---|
| Unpinned | Overlay on top of the chat; click outside or press Esc to close. |
| Pinned | Docked alongside the chat; chat content pushes left. |

Pin state and panel width are persisted in `localStorage`. The panel is resizable: drag the left handle to any width between 240 px and 640 px (default 260 px).

## Multi-repo support

On open, the panel recursively scans the session cwd for git repositories. The scan skips heavy directories (`node_modules`, `vendor`, `dist`, `.cache`, etc.) and caps at a fixed depth. If more than one repository is found, a collapsible **Repositories** section appears at the top of the panel (collapsed by default). Each repo entry shows:

- repo path (relative to cwd)
- current branch name
- ahead / behind count vs the upstream
- number of changed files

Click a repo row to switch the active repository. All other panel sections (Changes, Commit, Branches, History) apply to the active repo.

## Changes view

The Changes section shows two collapsible groups:

| Group | What it contains |
|---|---|
| **Staged** | Files added to the index (`git add`). |
| **Unstaged** | Modified tracked files and untracked new files. |

Each group has a file count badge. Inside each group, files are displayed in either **Tree** view or **List** view — toggle with the icon in the panel header. The view mode is persisted in `localStorage`.

Tree view collapses single-child folders (compact mode, same as VSCode). Click a folder to expand or collapse it.

### Per-file actions

Hover over a file or folder row to reveal action icons:

| Icon | Action | Scope |
|---|---|---|
| **+** | Stage | File, folder, or the entire Unstaged section header |
| **−** | Unstage | File, folder, or the entire Staged section header |
| **↺** | Discard | File or folder (see below) |

::: danger Discard is destructive
**Discard** cannot be undone. For tracked files it runs `git restore <file>`; for untracked files it runs `git clean -f <file>`. A confirmation dialog appears before the operation runs. Files discarded this way are permanently gone from the working tree.
:::

Clicking a file name (in either group) opens the diff viewer (see [Diff viewer](#diff-viewer)).

## Commit

Below the Changes section is a one-line commit message input. Type a message and press **Enter** (or click the Commit button) to commit all staged files. The Commit button is disabled when there are no staged files or the message is empty.

After a successful commit, the Changes section refreshes and the branch ahead/behind counts update.

## Branches

The panel header bar shows the current branch name. Clicking it opens a **Branches** dropdown with:

- a filter input to search local and remote branches
- a list of local branches (current branch highlighted)
- a list of remote branches (prefixed with the remote name)

From the dropdown:

| Action | How |
|---|---|
| **Checkout** | Click a local branch name. |
| **Create + checkout** | Type a new branch name in the filter input and press Enter (or click **Create**). |

The header also shows **Pull** and **Push** buttons. The ahead/behind count (e.g. `↑2 ↓1`) appears next to the branch name when the local branch tracks a remote.

## Diff viewer

Clicking a changed file opens a **Monaco diff editor** in a modal. The diff is git-correct:

| File state | What is diffed |
|---|---|
| Staged | HEAD (original) ↔ index (staged content) |
| Unstaged (tracked) | Index ↔ working tree |
| Untracked (new file) | Empty file ↔ working tree |

Monaco computes and colors the diff. The modal header shows the file path and its state (staged / unstaged / untracked).

You can **edit the file** directly in the right-hand pane of the diff editor. Click **Save** in the modal header to write the changes to disk. The Changes section updates after saving.

## History

The **History** tab inside the panel shows a commit log for the active repository. Each row displays:

| Column | Content |
|---|---|
| **SHA** | Short commit hash (7 chars). |
| **Subject** | First line of the commit message. |
| **Author** | Commit author name. |
| **Date** | Relative date (e.g. "2 hours ago"). |

Click a commit row to expand it and see the list of files changed in that commit. Click a file in the expanded list to open the Monaco diff viewer for that file (parent commit ↔ this commit).

## Live updates

The panel subscribes to the server-sent event stream for the session. A server-side filesystem watcher monitors the session cwd and pushes a `git_status` event over SSE whenever the working tree changes. The Changes section and the **Source** rail tab badge both update in real time — no polling, no manual refresh required.

The rail tab badge shows the current count of changed files (staged + unstaged) across all discovered repositories.

## Endpoint reference

All endpoints are scoped to `/tools/agents/api/sessions/{id}/git/` and require the same `RequireToolAccess` middleware + session-ownership check as the rest of the agents tool.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `.../git/status` | Full git status snapshot for all discovered repos. |
| `GET` | `.../git/diff` | File diff (query: `path`, `staged`, `commit`). |
| `POST` | `.../git/stage` | Stage files. Body: `{paths: [...]}`. |
| `POST` | `.../git/unstage` | Unstage files. |
| `POST` | `.../git/discard` | Discard working-tree changes (destructive). |
| `POST` | `.../git/commit` | Commit staged files. Body: `{message}`. |
| `GET` | `.../git/branches` | List local + remote branches. |
| `POST` | `.../git/checkout` | Checkout or create a branch. |
| `POST` | `.../git/pull` | Pull from upstream. |
| `POST` | `.../git/push` | Push to upstream. |
| `GET` | `.../git/log` | Commit history. |
| `GET` | `.../git/log/diff` | Diff for a specific commit. |

## See also

- [Context file panel](../agents#context-file-panel) — browse, edit, and download files in the session cwd without git.
- [Projects](./projects) — how the session cwd is determined.
- [Pool & Sessions](./pool) — session lifecycle.
