# Provider Storage Manager

Mounts under `/tools/provider-storage`. Backup + restore provider credential files (Claude, Codex, Gemini, Wick, dll.) dari filesystem ke DB — berguna saat tidak ada akses persistent volume, sehingga files tetap tersedia setelah restart.

## Use cases

- **OAuth / credential persistence** — provider seperti Claude atau Codex menyimpan session/token di disk. Provider Storage sync files ini ke DB sehingga tidak perlu re-login setiap kali container/pod restart.
- **No-volume environments** — bila tidak ada persistent volume mount, DB SQLite menjadi storage pengganti untuk credential files.

## TODO

- [ ] Restore folder/instance selection (select folder row = restore semua children)
- [ ] Per-file versioning / diff view

## Features

- **Explorer mode** (default) — drill-down tree: Storage → instance → folder → file. Breadcrumb navigation. Tiap level hanya query `WHERE parent_id = ?`.
- **List mode** — flat table dengan filter by provider type + instance.
- **Sync Now** — manual trigger backup filesystem → DB.
- **Auto-sync** — background job `provider-storage-sync` (tiap 1 menit) backup semua enabled sources.
- **Startup restore** — `RestoreAll` dipanggil saat server start: DB → filesystem, semua file dikembalikan ke path aslinya.
- **Selective restore** — `POST /restore` body `ids[]`, tulis file dari DB ke filesystem.
- **Delete selected** — hapus rows dari DB. Select instance row = hapus seluruh instance.
- **Preview** — `GET /{id}/preview`, file >1 MB tampilkan size info saja, binary detection via `utf8.Valid`.
- **Manual upload** — `POST /upload` multipart, upsert by `(provider_type, instance_name, rel_path)`.
- **Retention per file** — `retention_days` kolom di `provider_storage`. 0 = tidak pernah purge. Set inline dari UI.
- **Sync sources** — konfigurasi path per instance (mode `folder` atau `single`). Detect otomatis dari provider home dirs.

## Architecture

### DB schema

```
provider_storage
  id             PK
  provider_type  varchar(32)   — "claude", "codex", "gemini", "wick", …
  instance_name  varchar(128)
  rel_path       varchar(512)  — unique per (type, instance)
  parent_id      uint          — 0 = root (RootParentID sentinel, bukan NULL)
  name           varchar(512)  — basename; unique per (type, instance, parent_id, name)
  is_dir         bool
  content        blob          — nil untuk dir rows
  content_hash   varchar(64)   — SHA-256; gating upsert
  synced_at      timestamp
  retention_days int           — 0 = never purge

provider_storage_sources
  id, provider_type, instance_name
  label, sync_path
  mode           "folder" | "single"
  retention_days, enabled, created_at, updated_at
```

### Adjacency list

Folder rows (`is_dir=true`) di-insert via `ensureFolderChain` dengan `ON CONFLICT (provider_type, instance_name, parent_id, name) DO NOTHING` — O(1) di sync cycle berikutnya.

`parent_id=0` = sentinel root (bukan NULL) — menghindari SQLite NULL inequality issue di unique index.

### Sync flow

```
Startup:   RestoreAll()  →  DB rows → filesystem (per configured sources)
Cron:      SyncOne()     →  filesystem → DB (skip unchanged via content_hash)
Manual:    POST /sync    →  trigger SyncOne untuk semua enabled sources
```

### Path resolution (restore)

`resolveDst(srcs, relPath)`:
1. Cek single-mode sources dulu: match by basename → return `syncPath` langsung
2. Fallback folder-mode: `filepath.Join(syncPath, relPath)`

Urutan ini mencegah file single-mode di-restore ke folder path yang salah.

## Routes

```
GET  /tools/provider-storage              — halaman utama
GET  /tools/provider-storage/files        — flat file list (list mode)
GET  /tools/provider-storage/roots        — [{ProviderType, InstanceName, FileCount}]
GET  /tools/provider-storage/tree         — ?provider=&instance=&parent_id=
GET  /tools/provider-storage/{id}/preview
POST /tools/provider-storage/restore      — body: ids[]
POST /tools/provider-storage/upload
POST /tools/provider-storage/sync
POST /tools/provider-storage/delete-selected — body: ids[] + instance[]
POST /tools/provider-storage/{id}/retention  — body: days=N
DELETE /tools/provider-storage/{id}
GET/POST/DELETE /tools/provider-storage/sources[/{id}]
GET  /tools/provider-storage/sources/detect — ?provider=
GET  /tools/provider-storage/sources/ls     — ?path=
GET  /tools/provider-storage/sources/home
GET/POST /tools/provider-storage/settings
```

## Jobs

| Key | Schedule | Function |
|-----|----------|----------|
| `provider-storage-sync` | `*/1 * * * *` | Backup filesystem → DB |
| `provider-storage-retention` | `0 3 * * *` | Purge expired file rows |
