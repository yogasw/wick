# Provider Storage

Provider Storage backs up local credential and config files from disk to the database and restores them on startup. It solves two problems:

- **No persistent volume** — credentials written by Claude, Codex, Gemini, or Wick after login survive container restarts.
- **OAuth re-login** — on boot the saved snapshot is restored to the original paths before agents start, so CLIs find their credentials without prompting.

## How it works

```
Boot
  DB → filesystem   (RestoreAll, disk-wins guard)
  filesystem → DB   (SyncAll, populates rows immediately)

Every minute (background job)
  filesystem → DB   (SyncOne per enabled source, skip unchanged via SHA-256)
```

File rows are keyed by their **absolute filesystem path** (e.g. `/home/app/.claude/credentials.json` or `C:/Users/x/.wick-lab/agents/foo.yml`). The same physical file always maps to a single row, regardless of how many sources cover it.

**Disk-wins guard.** RestoreAll only writes when the disk file is missing or has identical hash. If the disk diverged (you edited it between syncs), the disk copy is kept and the difference is logged. RestoreSelected (manual UI action) still force-overwrites.

## Opening Provider Storage

**Tools → Provider Storage** in the sidebar.

The page opens in **Explorer mode** — a drill-down tree starting at top-level absolute path segments:

```
Storage
└── C:/
    └── Users/
        └── x/
            └── .wick-lab/
                ├── agents/
                │   └── foo.yml
                └── logs/
                    └── gate-2026-05-13.log
```

Click any folder to drill in. Click **Delete** on a folder row to remove the subtree from the database (disk is untouched). Switch to **List** (top-right) for a flat file table with provider/instance filters.

## Adding sync sources

1. **Settings** tab → **Add Source**.
2. Enter the provider type (`claude`, `codex`, `gemini`, `wick`, or anything else).
3. Click **Detect** for known paths, or paste a path under **Custom path**.
4. In the file browser:
   - **+ Add** stages an include source (folder or single file).
   - **− Ignore** stages an exclude rule for the deepest covering source.
5. Edit each staged card's Path / Retention as needed.
6. **Save All**.

The save runs an immediate sync. If you added an exclude after files were already captured, the matching rows are purged automatically.

## Source modes

| Mode | Purpose |
|------|---------|
| `folder` | Include a directory tree (e.g. `~/.claude/`) |
| `single` | Include exactly one file (e.g. `~/.config/codex/auth.json`) |
| `exclude` | Skip paths matching `SyncPath`. Literal path with no wildcards excludes the dir and every descendant; wildcards (`*`, `**`, `?`) work too |

Exclude examples:

- `C:/Users/x/.wick-lab/logs` — skip the whole `logs` subtree.
- `**/secrets/**` — skip any `secrets` folder anywhere.
- `*.log` — skip any `.log` file (gitignore-style: a slashless pattern matches any path segment).
- `**/node_modules/**` — typical noise filter.

## Retention

Each include source carries a default **Retention (days)**. Files inherit the retention of the **deepest** matching source. Example:

- Source A: `/app/home` retention `0` (lifetime)
- Source B: `/app/home/session` retention `7`
- File `/app/home/notes.txt` → retention `0` (only A covers it)
- File `/app/home/session/log.txt` → retention `7` (B is deeper)

Edit retention on a source via the **Retention** column in Configured Sources (preset modal: Lifetime / 7 / 30 / 90 / Custom). The cascade re-evaluates every existing file row immediately.

## Files tab actions

- **Sync Now** — manual backup pass for every enabled source.
- **Restore Now** — disk-wins guard restore for every covered file.
- **Repair Tree** — re-parent orphan rows from their `rel_path` (rarely needed; migration runs the same logic on every boot).
- **Restore Selected** — force-overwrite selected file rows back to disk.
- **Delete Selected** — drop selected rows. Selecting a folder cascades to descendants.
- **Upload File** — push a file directly into DB without disk sync (seeding).
- **Preview** — view file content (>1 MB and binary files shown as size only).

## Background jobs

| Job | Schedule | What it does |
|-----|----------|--------------|
| Provider Storage Sync | `*/1 * * * *` | Backs up enabled sources from disk to DB |
| Provider Storage Retention | `0 3 * * *` | Deletes file rows past their retention |

Both jobs can be triggered manually from **Tools → Jobs**.

## Migration notes

On first boot after upgrading from an older version, `postgres.Migrate` runs three one-shot data migrations against `provider_storage`:

1. **Wipe legacy rel_path rows** — pre-fix rows with relative paths (e.g. `agents/foo.yml`) are deleted. Files only present in DB (no disk copy) are lost; backup the DB if that matters.
2. **Split `exclude_patterns` column** — text patterns on each source become separate `Mode=exclude` rows. The column is dropped afterward.
3. **Repair orphan parent_id** — descendants whose parent row was deleted are re-parented from `rel_path`.

All three are idempotent — re-runs match no rows.
