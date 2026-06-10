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

## Layout (full mode)

In full mode the panel uses a two-column layout:

- **Left column (220 px)** — file list grouped into Staged and Changes sections, commit message input, and branch bar at the bottom.
- **Right column (flex)** — Monaco diff editor as the primary surface. The first changed file is selected automatically on load.

The active repo selection is persisted per session in `localStorage` and restored on next open.

## Multi-repo support

On open, the panel recursively scans the session cwd for git repositories. The scan skips heavy directories (`node_modules`, `vendor`, `dist`, `.cache`, etc.) and caps at a fixed depth. If more than one repository is found, a dropdown appears in the left column header. Select a repo from the dropdown to switch; all sections apply to the active repo.

## File list

The left column shows two flat sections:

| Section | What it contains |
|---|---|
| **Staged (N)** | Files added to the index (`git add`). |
| **Changes (N)** | Modified tracked files and untracked new files. |

Click a file row to open it in the diff editor. The active file is highlighted.

Each file row shows a status badge (`M`, `A`, `D`, `?`) on the right edge.

## Diff editor

Clicking a file opens it inline in the right-column Monaco diff editor — no modal. The diff is git-correct:

| File state | What is diffed |
|---|---|
| Staged | HEAD ↔ index (staged content) |
| Unstaged (tracked) | Index ↔ working tree |
| Untracked (new file) | Empty ↔ working tree |

Unchanged regions are collapsed by default with a "N hidden lines" expand bar (3-line context, same as VSCode). Click the bar to expand.

The diff renders in **unified (inline) mode** by default. Click the split-view icon in the diff header to toggle side-by-side mode.

### Editing files

The diff editor is directly editable — no "Edit" button required. Start typing in the modified (right) side and a **Save** button appears automatically in the diff header. Click **Save** to write the file to disk. Click **Revert edit** to abandon unsaved changes without touching the file.

### Per-file actions (diff header)

| Button | Action |
|---|---|
| **Stage** | Stage the current file (`git add`). |
| **Unstage** | Unstage the current file (`git restore --staged`). |
| **Discard** | Discard working-tree changes (see warning below). |
| **Save** | Write in-editor edits to disk (visible only when there are unsaved edits). |
| **Revert edit** | Abandon in-editor edits without touching the file (visible only when there are unsaved edits). |
| Split-view icon | Toggle unified ↔ side-by-side diff layout. |

::: danger Discard is destructive
**Discard** cannot be undone. For tracked files it runs `git restore <file>`; for untracked files it runs `git clean -f <file>`. A confirmation dialog appears before the operation runs.
:::

## Sidebar mode (mobile / overlay)

On mobile or when the panel is opened as a slide-over overlay, a compact **sidebar mode** is used instead. The file list, commit box, and branch bar stack vertically. Clicking a file opens a **full-screen diff modal** (Monaco diff editor, same editing and save behavior as full mode).

## Commit

The commit message input is at the bottom of the left column. Type a message and press **Enter** (or click **Commit (N)**) to commit all staged files. The button shows the staged file count and is disabled when staging is empty or the message is blank.

## Branches

The branch bar at the bottom of the left column shows the current branch name and the ahead/behind count (`↑N ↓N`). Click the branch name to open a dropdown with:

- a filter input to search local and remote branches
- local branches (current highlighted)
- remote branches

| Action | How |
|---|---|
| **Checkout local** | Click a local branch name. |
| **Checkout remote** | Click a remote branch — creates a local tracking branch automatically. |
| **Create + checkout** | Type a new name in the bottom input and click **+**. |

**Pull** and **Push** buttons are below the branch picker.

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
