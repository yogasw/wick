# Provider Storage

Provider Storage backs up local credential files from disk to the database and restores them on startup. It solves two problems:

- **No persistent volume** — if your deployment has no mounted volume, credentials written by Claude, Codex, or Gemini after login would be lost on restart. Provider Storage keeps a copy in the database.
- **OAuth re-login** — after a restart, files are restored to their original paths before the agent starts, so tools like `claude` or `codex` find their credentials and skip the login prompt.

<!-- TODO: screenshot of Provider Storage explorer showing wick/wick instance with gate, presets, workspaces folders -->

## How it works

```
First run / restart
  DB → filesystem   (RestoreAll on startup)

Every minute (background job)
  filesystem → DB   (SyncOne per enabled source, skip unchanged via SHA-256)
```

Files are only written to the database when their content changes, so the sync job is cheap after the first run.

## Opening Provider Storage

Go to **Tools → Provider Storage** in the sidebar.

The page opens in **Explorer mode** — a drill-down tree:

```
Storage
└── wick / wick          (provider / instance)
    ├── gate/
    │   └── spec.json
    ├── presets/
    │   └── default/
    │       └── agent.md
    └── workspaces/
        └── default/
            └── meta.json
```

Click an instance or folder name to drill in. Use the breadcrumb at the top to navigate back.

Switch to **List** (top-right toggle) for a flat table with provider/instance filters.

## Adding a sync source

1. Go to the **Settings** tab.
2. Click **Add Source**.
3. Enter the provider type (e.g. `claude`, `codex`, `gemini`, `wick`).
4. Click **Detect** — Wick scans known home directories for that provider and shows candidates.
5. Click **+ Add** next to the path you want to track, or browse into a folder and click **+ Add folder**.
6. Set a label and optional retention, then click **Save all**.

The source is saved and an initial sync runs immediately.

**Mode — folder vs single**

| Mode | When to use |
|------|-------------|
| `folder` | Sync an entire directory (e.g. `~/.claude/`) |
| `single` | Sync one file (e.g. `~/.config/codex/auth.json`) |

## Sync Now

Click **Sync Now** in the toolbar to trigger an immediate backup of all enabled sources. The explorer refreshes automatically after sync.

## Restoring files

Select one or more **files** (checkboxes) and click **Restore Selected**. Wick writes the stored content back to the original filesystem path.

> Folders and instance rows can be selected for **Delete** but not for Restore — select individual files to restore.

## Deleting stored files

Select rows and click **Delete Selected**:

- Selecting a **file row** → removes that file from the database.
- Selecting an **instance row** (top level) → removes all rows for that instance.

This does **not** delete anything from disk — it only removes the database copy.

## File preview

Click **Preview** on any file row to view its content in a modal. Files larger than 1 MB show size information only. Binary files are detected automatically.

## Retention

Each file row has a **Retention (days)** field. Set it to `0` to keep the file forever, or enter a number of days after which the row is automatically deleted by the nightly retention job.

## Manual upload

Click **Upload File** to push a file directly into the database without syncing from disk. Useful for seeding credentials in a new environment.

Fill in provider type, instance name, relative path, and pick a file. An existing row with the same path is overwritten.

## Background jobs

| Job | Schedule | What it does |
|-----|----------|-------------|
| Provider Storage Sync | every minute | Backs up enabled sources from disk to DB |
| Provider Storage Retention | 03:00 daily | Purges file rows past their retention date |

Both jobs can be triggered manually from **Tools → Jobs**.
