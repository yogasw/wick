# Provider Storage Manager

Mounts under `/tools/provider-storage`. Backup + restore provider credential and config files (Claude, Codex, Gemini, Wick, etc.) between filesystem and DB — useful when there is no persistent volume so files survive restarts.

## Use cases

- **OAuth / credential persistence** — provider tokens stored on disk get mirrored to DB and replayed on boot, no re-login per container restart.
- **No-volume environments** — DB (SQLite or Postgres) becomes the durable store; disk is recreated from it on every boot.

## TODO

- [ ] Restore-folder selection (select dir row → restore every descendant)
- [ ] Per-file versioning / diff
- [ ] Glob preview before saving an exclude

## Data model

```
provider_storage
  id              PK
  provider_type   varchar(32)
  instance_name   varchar(128)
  rel_path        varchar(1024)  — ABSOLUTE filesystem path (slash form)
  parent_id       uint           — 0 = root (RootParentID sentinel, not NULL)
  name            varchar(512)   — basename
  is_dir          bool
  content         blob           — nil for dir rows
  content_hash    varchar(64)    — SHA-256, "" for dirs; gates upsert
  synced_at       timestamp
  retention_days  int            — 0 = never purge
  UNIQUE (provider_type, instance_name, rel_path)            — idx_provider_path
  UNIQUE (provider_type, instance_name, parent_id, name)     — idx_storage_tree (managed in postgres.Migrate)

provider_storage_sources
  id, provider_type, instance_name
  label, sync_path
  mode            "folder" | "single" | "exclude"
  retention_days  (include-mode only; exclude rows ignore it)
  enabled, created_at, updated_at
```

### Why absolute paths

Pre-fix rows used `filepath.Rel(SyncPath, file)`. Two overlapping sources for the same instance produced different `rel_path` values for the same physical file → restore wrote to the wrong location → next sync re-captured the duplicate → repeat → `agents/agents/agents/...` stacking on disk.

Absolute path = one row per physical file regardless of how many sources cover it. Restore writes to `rel_path` directly via `absToOS = filepath.FromSlash(relPath)`.

### Mode = exclude

Exclude lives as a first-class row, not a property on an include source. Matcher is glob-based with three conveniences:

- Slashless pattern (`*.log`, `node_modules`) — matches any path segment, gitignore-style.
- Wildcard-free pattern with slashes (`/home/app/logs`) — matches the dir AND every descendant.
- `**` matches across segments, `*` within a segment, `?` single non-slash char.

`backup()` calls `collectExcludePatterns(sources)` to assemble the patterns, then `collectFiles(sc, excludes)` prunes the walk (`filepath.SkipDir` when a dir matches).

`pickRetention` and `sourceCovers` ignore exclude-mode rows.

## Boot sequence

```
postgres.Migrate(db)
  AutoMigrate schema
  wipeLegacyRelPathRows         (rel_path NOT LIKE '/%' AND NOT LIKE '_:%')
  migrateExcludePatternsToRows  (split text column → Mode=exclude rows, drop column)
  repairProviderStorageTree     (re-parent orphans from rel_path)
  CREATE UNIQUE INDEX idx_storage_tree (soft-fail on duplicates)

providersync.New(db)
syncMgr.SyncAll(ctx)
  for each enabled include source: SyncOne → backup → upsert files
  for each (provider, instance) once: PurgeExcluded (self-heal pre-fix rows)

syncMgr.RestoreAll(ctx)
  for each file row covered by enabled include sources:
    read disk; skip if same hash; warn-skip if diverged; write if missing
```

`ensureFolderChain` is SELECT-then-INSERT (orphan-recovery fallback: if SELECT by `(parent_id, name)` misses but a row exists at the same `rel_path`, re-parent it). No `ON CONFLICT` is used because SQLite errors when the named conflict target is one of multiple unique indexes hit by the insert.

## Manager API

| Method | Purpose |
|--------|---------|
| `New(db)` | Construct, owns the per-table store |
| `SaveSource(ctx, src)` | Upsert source; cascades SyncOne (include) → RecomputeRetention → PurgeExcluded |
| `DeleteSource(ctx, id)` | Delete source + RecomputeRetention so retentions fall back |
| `GetSource(ctx, id)` | Single source row |
| `ListSources(ctx)` | All source rows |
| `SyncOne(ctx, ins)` | Backup pass for one include source |
| `SyncAll(ctx)` | Iterate all enabled include sources + self-heal purge |
| `RestoreAll(ctx)` | DB → disk with disk-wins guard |
| `RestoreSelected(ctx, ids, _)` | Force-overwrite specified file rows |
| `RecomputeRetention(ctx, p, i)` | Re-derive `retention_days` for every file row from the deepest covering source |
| `PurgeExcluded(ctx, p, i)` | Delete file rows matching any enabled `Mode=exclude` row; prune empty folders |
| `RepairTree(ctx)` | Re-parent orphan rows from `rel_path` (also runs in postgres.Migrate) |
| `ListAll`, `ListChildren`, `ListRoots`, `GetByID`, `DeleteByID`, `Upload`, `SetRetention`, `RunRetention`, `CheckSource` | Utility CRUD |

## Routes

```
GET    /tools/provider-storage
GET    /tools/provider-storage/files                — flat file list (excludes dir rows)
GET    /tools/provider-storage/roots                — top-level rows across all instances
GET    /tools/provider-storage/tree                 — ?provider=&instance=&parent_id=
GET    /tools/provider-storage/{id}/preview
POST   /tools/provider-storage/restore              — body: ids[]
POST   /tools/provider-storage/upload
POST   /tools/provider-storage/sync-now
POST   /tools/provider-storage/restore-now
POST   /tools/provider-storage/repair-tree
POST   /tools/provider-storage/delete-selected      — body: ids[] + instance[]
POST   /tools/provider-storage/{id}/retention       — body: days=N
DELETE /tools/provider-storage/{id}                 — file row, OR folder (cascades subtree)
GET    /tools/provider-storage/sources
POST   /tools/provider-storage/sources              — JSON body, used for create + update
POST   /tools/provider-storage/sources/{sid}/retention — body: days=N
DELETE /tools/provider-storage/sources/{sid}
GET    /tools/provider-storage/sources/detect       — ?provider=
GET    /tools/provider-storage/sources/ls           — ?path=
GET    /tools/provider-storage/sources/home
GET    /tools/provider-storage/sources/presets
GET    /tools/provider-storage/sources/check        — ?mode=&path=
GET/POST /tools/provider-storage/settings
```

## Jobs

| Key | Schedule | Function |
|-----|----------|----------|
| `provider-storage-sync` | `*/1 * * * *` | Backup filesystem → DB (skips exclude-mode rows) |
| `provider-storage-retention` | `0 3 * * *` | Purge expired file rows |

## Testing

Two test files cover the package end-to-end:

- `sync_test.go` — backup / restore / retention / repair / cascade delete / migration regressions.
- `cross_platform_test.go` — Windows/POSIX path handling, unicode + spaces, concurrent SyncOne, symlink (POSIX-only), case-sensitive FS (Linux-only), permission-denied (POSIX-only), volume-style mounts, idempotency, exclude edge cases.

Migration tests live in `internal/pkg/postgres/migrate_test.go` (fresh DB, exclude_patterns split, duplicate-row non-fatal).
