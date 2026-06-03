---
outline: deep
---

# Skills Manager

The **Skills Manager** is a built-in UI for browsing, previewing, syncing, and deleting skill files (`.md` prompt files) across all agent skill directories on the host.

::: info Source
Core sync logic: [`internal/agents/skillsync/sync.go`](https://github.com/yogasw/wick/blob/master/internal/agents/skillsync/sync.go).
UI handler: [`internal/tools/agents/skills.go`](https://github.com/yogasw/wick/blob/master/internal/tools/agents/skills.go).
Templates: [`internal/tools/agents/view/skills.templ`](https://github.com/yogasw/wick/blob/master/internal/tools/agents/view/skills.templ).
:::

## What are skill directories?

Each AI CLI has its own skill folder on disk:

| Provider | Path |
|---|---|
| Claude Code | `~/.claude/skills/` |
| Codex | `~/.codex/skills/` |
| Gemini CLI | `~/.gemini/skills/` |
| Generic / shared | `~/.agents/skills/` |

Skills are plain `.md` files (or folders containing `.md` files) that the CLI loads as reusable instructions — slash commands, personas, workflow prompts, etc. Each provider has its own copy; keeping them in sync across providers is what this feature solves.

## Skill list (`/skills`)

The root page lists every unique skill entry found across all directories. Each row shows:

| Column | Meaning |
|---|---|
| **Name** | File or folder name. |
| **Present in** | Which provider dirs contain this entry (e.g. `claude`, `codex`). |
| **Missing from** | Dirs that don't have it yet. |

Click a row to open the file viewer or folder explorer. The kebab menu (⋮) on each row exposes:

- **Download** — zip the entry and save locally.
- **Sync to all** — copy from the newest-mtime source into every missing dir (mtime-wins, no overwrite of newer files).
- **Delete** — remove the entry from every dir.

## Folder explorer

Clicking a folder row opens `/skills/{folder}` — a nested file list scoped to that folder. Each entry row works the same as the root list: click to open, ⋮ to act.

**Provider tabs** at the top of the folder page switch to a provider-scoped view (`/skills/{provider}/{folder}`) showing only that provider's copy of the folder. This is useful to compare what `claude` has vs what `gemini` has.

## File viewer

Clicking a `.md` file opens a rendered markdown preview (not raw text). The viewer shows:

- **Breadcrumb** — folder hierarchy back to the root Skills list.
- **Present in** badges — each badge is a link to the provider-scoped view for that file.
- **Content** — markdown rendered inline; scrolls independently of the page chrome.
- **Download / Delete** buttons in the header.

## Provider-scoped views

`/skills/{provider}/{folder}` and `/skills/{provider}/{folder}/files/{file}` scope the explorer to one provider dir. Use these to:

- See exactly what one provider has in a folder.
- Compare versions: open the same file under `claude` vs `codex` tabs to spot differences.
- Sync the entire folder from one provider to all others via the **Sync to all** button.

Provider tab switcher shows all known providers; tabs for providers that don't have the folder are shown with a strikethrough.

## Sync behaviour

There are two sync operations:

| Operation | Trigger | What it does |
|---|---|---|
| **Global sync** | `POST /skills/sync` (Sync All button on the list page) | Runs a full mtime-wins mirror: every file in every dir is compared; the newest copy wins and is written to all dirs missing it or holding an older copy. |
| **Entry sync** | `POST /skills/{name}/sync` (⋮ → Sync to all on a row) | Finds the dir with the newest mtime for this specific entry, then force-copies it to every other dir that is missing it. Does not overwrite newer copies in other dirs. |

Sync is idempotent — running it twice has no effect if nothing changed.

## Upload & delete

- **Upload** (`POST /skills/upload`) — accepts a single `.md` file or a `.zip`. Zip contents are unpacked and placed at the top-level skills dir (all providers). Existing files are only overwritten if the uploaded copy is newer.
- **Delete from all** — removes the file or folder from every provider dir simultaneously.
- **Delete from one dir** — removes the entry only from the selected provider dir, leaving all others intact.

## URL structure

```
GET  /skills                              # root list
GET  /skills/{name}                       # file viewer OR folder explorer (auto-detected)
GET  /skills/{folder}/files/{file}        # file viewer inside a subfolder
GET  /skills/{provider}/{path...}         # provider-scoped view (arbitrary depth)
POST /skills/sync                         # global sync
POST /skills/upload                       # upload file or zip
POST /skills/{name}/sync                  # sync one entry to all dirs
POST /skills/{name}/delete                # delete from all dirs
POST /skills/{name}/delete-from/{dir}     # delete from one dir
POST /skills-sync/{provider}/{path...}    # sync a provider folder to all others
```

## See also

- [Providers](./providers) — provider instances that use these skill dirs.
- [Projects](./projects) — agent `cwd` at spawn time; separate from skill dirs.
