# Wick Self-Management Connector — Rencana Internal

Status: draft v9, siap eksekusi.

## Perubahan v9 — namespacing tool name

Semua MCP tool dari connector wickmanager pakai prefix **`wick_manager_*`** (bukan `wick_app_*` / `wick_job_*` / dst).

Alasan: meta-tool MCP existing — `wick_list`, `wick_get`, `wick_search`, `wick_execute`, `wick_info`, `wick_encrypt`, `wick_decrypt` — namanya generic. Kalau plan pakai `wick_manager_connector_list`, semantically overlap sama `wick_list` existing yg juga list connectors. Prefix `wick_manager_*` jelas memisahkan domain wickmanager dari meta-tools cross-connector.

Mapping op connector → MCP tool:

| Op connector wickmanager | MCP tool name |
|--------------------------|---------------|
| `app_list` | `wick_manager_app_list` |
| `app_get_config` | `wick_manager_app_get_config` |
| `app_set_config` | `wick_manager_app_set_config` |
| `app_regenerate_config` | `wick_manager_app_regenerate_config` |
| `job_list` | `wick_manager_job_list` |
| `job_get` | `wick_manager_job_get` |
| `job_set_config` | `wick_manager_job_set_config` |
| `job_set_schedule` | `wick_manager_job_set_schedule` |
| `job_run_now` | `wick_manager_job_run_now` |
| `job_get_run` | `wick_manager_job_get_run` |
| `job_list_runs` | `wick_manager_job_list_runs` |
| `tool_list` | `wick_manager_tool_list` |
| `tool_get` | `wick_manager_tool_get` |
| `tool_set_config` | `wick_manager_tool_set_config` |
| `connector_list` | `wick_manager_connector_list` |
| `connector_get` | `wick_manager_connector_get` |
| `connector_set_config` | `wick_manager_connector_set_config` |
| `system_status` | `wick_manager_system_status` |
| `system_server_start` | `wick_manager_system_server_start` |
| `system_server_stop` | `wick_manager_system_server_stop` |
| `system_worker_start` | `wick_manager_system_worker_start` |
| `system_worker_stop` | `wick_manager_system_worker_stop` |
| `system_prefs_get` | `wick_manager_system_prefs_get` |
| `system_prefs_set` | `wick_manager_system_prefs_set` |

MCP layer rule: emit op connector wickmanager sebagai `wick_manager_<op_key>`. Connector lain tetep meta-tool pattern (`wick_list/get/execute`).

Owner: yoga.
Tanggal: 2026-05-07.

## Akses control — full per-op rule

Tabel detail tiap MCP tool: siapa boleh manggil, gate apa di handler.

Gate type:
- **admin** → `requireAdmin(c)`. Non-admin → error `access denied`.
- **tag-visible** → list di-filter ke resource yg user-nya punya tag-akses. Admin liat semua. Logic mirror UI dashboard manager existing.
- **per-job-access** → `requireJobAccess(c, jobKey)` sama logic kayak middleware `RequireJobAccess`.
- **per-tool-access** → path `/tools/{key}` di tool_permissions.
- **per-connector-access** → `connectors.Service.IsVisibleTo(connectorID, tagIDs, isAdmin)`.
- **any auth** → user wajib login; ngak ada gate spesifik tag/role.
- **tray-only** → `processctl.IsManaged()` harus true.

### wick_manager_app_*

App = variable global (Owner=""). Variable lintas (encryption_key, session_secret, app_url) terlalu sensitif. **Semua admin-only**, cocok sama UI `/admin/variables`.

| MCP tool | User boleh? | Admin boleh? | Gate | Catatan |
|----------|:-----------:|:------------:|------|---------|
| `wick_manager_app_list` | ❌ | ✅ | `admin` | Output cuma list key + meta. Value disensor by `IsSecret` mask helper. Locked row tetep keluar dgn flag `locked:true`. |
| `wick_manager_app_get_config` | ❌ | ✅ | `admin` | Secret di-mask (`***` / `wick_enc_` pass-through). |
| `wick_manager_app_set_config` | ❌ | ✅ | `admin` | Reject row `Locked=true`. Validasi `Required`. Log diff before/after (juga di-mask). |
| `wick_manager_app_regenerate_config` | ❌ | ✅ | `admin` | Regenerate (e.g. session_secret) high-impact — admin-only. |

Catatan: variable app-level **ngak** punya tag system — beda dari job/connector/tool. Admin-only buat semua op-nya ngikutin pattern UI dashboard `/admin/variables`.

### wick_manager_job_*

| MCP tool | User boleh? | Admin boleh? | Gate | Catatan |
|----------|:-----------:|:------------:|------|---------|
| `wick_manager_job_list` | ✅ tag-filtered | ✅ semua | `tag-visible` | Cocok sama UI manager jobs page. |
| `wick_manager_job_get` | ✅ kalau tag-visible | ✅ | `per-job-access` | Meta + configs. Secret di-mask. |
| `wick_manager_job_set_config` | ✅ kalau tag-visible | ✅ | `per-job-access` | **Lebih permisif dari UI** (UI: adminOnly). Reject `Locked`. Validasi `Required`. Log diff. |
| `wick_manager_job_set_schedule` | ✅ kalau tag-visible | ✅ | `per-job-access` | **Lebih permisif dari UI** (UI: adminOnly). |
| `wick_manager_job_run_now` | ✅ kalau tag-visible | ✅ | `per-job-access` | Cocok sama UI `runJob` (`authJob`). |
| `wick_manager_job_get_run` | ✅ kalau tag-visible parent | ✅ | `per-job-access` | Q5: yg boleh run, boleh baca hasil. |
| `wick_manager_job_list_runs` | ✅ kalau tag-visible parent | ✅ | `per-job-access` | Sejajar `get_run`. |

### wick_manager_tool_*

| MCP tool | User boleh? | Admin boleh? | Gate | Catatan |
|----------|:-----------:|:------------:|------|---------|
| `wick_manager_tool_list` | ✅ tag-filtered | ✅ semua | `tag-visible` | Cocok sama UI manager tools. |
| `wick_manager_tool_get` | ✅ kalau tag-visible | ✅ | `per-tool-access` | Meta + configs. Secret di-mask. |
| `wick_manager_tool_set_config` | ✅ kalau tag-visible | ✅ | `per-tool-access` | **Lebih permisif dari UI** (UI: adminOnly). Reject `Locked`. Validasi `Required`. Log diff. |

### wick_manager_connector_*

| MCP tool | User boleh? | Admin boleh? | Gate | Catatan |
|----------|:-----------:|:------------:|------|---------|
| `wick_manager_connector_list` | ✅ tag-filtered | ✅ semua | `tag-visible` | Sama data kayak `wick_list` existing tapi format flat. |
| `wick_manager_connector_get` | ✅ kalau tag-visible | ✅ | `per-connector-access` | Mirror `wick_get` + tambah configs (secret di-mask). |
| `wick_manager_connector_set_config` | ✅ kalau tag-visible | ✅ | `per-connector-access` | **Lebih permisif dari UI** (UI: adminOnly). Reject `Locked`. Validasi `Required`. Log diff. |

### wick_manager_system_*

Semua butuh **tray-only** + **admin**. Bukan tray → error "system management unavailable in this run mode". Bukan admin di tray → error "access denied".

| MCP tool | User | Admin (non-tray) | Admin (tray) | Gate |
|----------|:----:|:----------------:|:------------:|------|
| `wick_manager_system_status` | ❌ | ❌ | ✅ | `admin` + `tray-only` |
| `wick_manager_system_server_start` | ❌ | ❌ | ✅ | `admin` + `tray-only` |
| `wick_manager_system_server_stop` | ❌ | ❌ | ✅ | `admin` + `tray-only` |
| `wick_manager_system_worker_start` | ❌ | ❌ | ✅ | `admin` + `tray-only` |
| `wick_manager_system_worker_stop` | ❌ | ❌ | ✅ | `admin` + `tray-only` |
| `wick_manager_system_prefs_get` | ❌ | ❌ | ✅ | `admin` + `tray-only` |
| `wick_manager_system_prefs_set` | ❌ | ❌ | ✅ | `admin` + `tray-only` |

### Deskripsi MCP & contoh response (full)

Tiap tool harus punya `Description` lengkap di `tools/list` MCP — siapa boleh manggil, apa yg balik, apa yg di-mask, dan link UI dashboard yg setara (biar LLM bisa redirect user kalau perlu). Format `Description` template:

```
<one-line summary>. Returns <data shape>. Access: <gate>. UI dashboard: <url>.
<extra notes for the LLM, e.g. masking, redirect rules>
```

Acuan URL UI (mirror dari `internal/admin/handler.go` & `internal/manager/handler.go`):

| Area | UI dashboard URL (relatif `app_url`) |
|------|--------------------------------------|
| App vars | `/admin/variables` |
| Jobs list | `/admin/jobs` (admin) atau `/manager/jobs/{key}` (per-job) |
| Tools list | `/admin/tools` (admin) atau `/manager/tools/{key}` |
| Connectors list | `/admin/connectors` (admin) atau `/manager/connectors/{id}` |
| System status | tray menu (ngak ada UI HTTP) |

#### wick_manager_app_*

```
wick_manager_app_list
  Description: List app-level configuration variables (session secret, app URL, encryption key, etc).
               Returns array of {key, type, description, is_secret, is_set, is_locked, can_regenerate, value}.
               Secret values are masked: "***" for plaintext-stored, "wick_enc_..." passes through.
               Access: ADMIN ONLY.
               UI dashboard: <app_url>/admin/variables.

wick_manager_app_get_config
  Input: { "key": string }
  Description: Get one app-level config row by key.
               Returns {key, type, description, is_secret, is_set, value} (value masked if secret).
               Access: ADMIN ONLY.
               UI: <app_url>/admin/variables.

wick_manager_app_set_config
  Input: { "key": string, "value": string }
  Description: Update one app-level config value. Rejects rows where is_locked=true.
               Validates Required field is non-empty if marked required.
               Returns {ok: true, key, before, after} (both masked if secret).
               Access: ADMIN ONLY.
               UI: <app_url>/admin/variables.

wick_manager_app_regenerate_config
  Input: { "key": string }
  Description: Regenerate the value of a regenerate-able app config (e.g. session_secret).
               High-impact — regenerating session_secret logs out other admins.
               Returns {ok: true, key, regenerated_at}.
               Access: ADMIN ONLY.
               UI: <app_url>/admin/variables.
```

#### wick_manager_job_*

```
wick_manager_job_list
  Description: List background jobs visible to the caller. Tag-filtered: admin sees all,
               non-admin sees only jobs their tags grant access to.
               Returns array of {key, name, description, icon, schedule, enabled, last_status,
                                 last_run_at, total_runs, max_runs, has_config}.
               Access: any authenticated user (filtered by tag).
               UI: <app_url>/admin/jobs (admin) or <app_url>/manager/jobs/{key} (per-job).

wick_manager_job_get
  Input: { "key": string }
  Description: Get one job's full detail — meta + configs.
               Returns {meta, configs: [{key, type, description, is_secret, is_set, value, ...}]}.
               Secret config values masked.
               Access: per-job-access (caller must have a tag that grants access to this job).
               UI: <app_url>/manager/jobs/{key}.

wick_manager_job_set_config
  Input: { "key": string, "config_key": string, "value": string }
  Description: Update one of a job's config values. Rejects rows where is_locked=true.
               Returns {ok: true, key, config_key, before, after} (masked if secret).
               Access: per-job-access. NOTE: UI dashboard restricts edit to admin; MCP is more
               permissive — caller with tag access can edit here.
               UI: <app_url>/manager/jobs/{key} (configs section).

wick_manager_job_set_schedule
  Input: { "key": string, "schedule": string, "enabled": bool, "max_runs": int }
  Description: Update a job's cron schedule and toggle enabled/max_runs cap.
               schedule is standard 5-field cron expression.
               Returns {ok: true, key, schedule, enabled, max_runs}.
               Access: per-job-access. Same MCP-permissive note as set_config.
               UI: <app_url>/manager/jobs/{key} (settings tab).

wick_manager_job_run_now
  Input: { "key": string }
  Description: Trigger an out-of-cycle run of the named job. Returns immediately with the run id;
               run executes in background. Errors if job is already running or max_runs reached.
               Returns {run_id, status: "started", started_at}.
               Access: per-job-access.
               UI: <app_url>/manager/jobs/{key} (Run button).

wick_manager_job_get_run
  Input: { "run_id": string }
  Description: Get one job run's status + result. Caller must have tag access to the parent job.
               Returns {id, job_key, status, result, triggered_by, started_at, ended_at}.
               Access: per-job-access (on parent job).
               UI: <app_url>/manager/jobs/{key} (runs tab).

wick_manager_job_list_runs
  Input: { "key": string, "limit": int (default 20) }
  Description: List recent runs of a job, newest first.
               Returns array of {id, status, triggered_by, started_at, ended_at, duration_ms}.
               Access: per-job-access.
               UI: <app_url>/manager/jobs/{key} (runs tab).
```

#### wick_manager_tool_*

```
wick_manager_tool_list
  Description: List tools (UI modules) visible to the caller. Tag-filtered.
               Returns array of {key, name, description, icon, category, has_config}.
               Access: any authenticated user (filtered).
               UI: <app_url>/admin/tools (admin) or <app_url>/manager/tools/{key}.

wick_manager_tool_get
  Input: { "key": string }
  Description: Get one tool's full detail — meta + configs.
               Returns {meta, configs: [...]} (secret masked).
               Access: per-tool-access.
               UI: <app_url>/manager/tools/{key}.

wick_manager_tool_set_config
  Input: { "key": string, "config_key": string, "value": string }
  Description: Update one of a tool's config values. Rejects locked rows.
               Returns {ok: true, key, config_key, before, after} (masked if secret).
               Access: per-tool-access. MCP-permissive vs UI (UI: admin-only).
               UI: <app_url>/manager/tools/{key} (configs section).
```

#### wick_manager_connector_*

```
wick_manager_connector_list
  Description: List connector instances visible to the caller. Tag-filtered.
               Returns array of {id, key, label, description, icon, status, total_tools,
                                 disabled, has_config}.
               status is "ready" (all required configs filled) or "needs_setup".
               Access: any authenticated user (filtered).
               UI: <app_url>/admin/connectors (admin) or <app_url>/manager/connectors/{id}.

wick_manager_connector_get
  Input: { "id": string }
  Description: Get one connector's full detail — meta + configs + operations.
               Returns {meta, configs: [...], operations: [{key, name, description, destructive}]}.
               Secret config masked.
               Access: per-connector-access.
               UI: <app_url>/manager/connectors/{id}.

wick_manager_connector_set_config
  Input: { "id": string, "config_key": string, "value": string }
  Description: Update one of a connector's config values. Rejects locked rows.
               Returns {ok: true, id, config_key, before, after} (masked if secret).
               Access: per-connector-access. MCP-permissive vs UI (UI: admin-only).
               UI: <app_url>/manager/connectors/{id} (configs section).
```

#### wick_manager_system_*

Semua butuh tray-only + admin. Output deskripsi tegasin batasan ini biar LLM ngak ngarahin user "minta admin run ini" pas lagi mode `wick server`.

```
wick_manager_system_status
  Description: Get HTTP server + background worker process status. Only available when wick is
               launched via the system tray (NOT when running `wick server` or `wick worker`
               standalone).
               Returns {server_running, server_port, worker_running, worker_uptime_seconds,
                        run_mode: "tray"|"server"|"worker"|"headless"}.
               Access: ADMIN + tray-only.

wick_manager_system_server_start
  Description: Start the HTTP server in this tray process. Errors if already running or port in use.
               Returns {ok: true, port}.
               Access: ADMIN + tray-only.

wick_manager_system_server_stop
  Description: Stop the HTTP server. Returns {ok: true}.
               Access: ADMIN + tray-only.

wick_manager_system_worker_start
  Description: Start the background worker. Errors if already running.
               Returns {ok: true}.
               Access: ADMIN + tray-only.

wick_manager_system_worker_stop
  Description: Stop the background worker. Returns {ok: true}.
               Access: ADMIN + tray-only.

wick_manager_system_prefs_get
  Description: Read per-machine tray preferences from ~/.<appName>/config.json.
               Returns {auto_start_app, auto_start_server, auto_start_worker, auto_update,
                        port, log_retention_days, database_path}.
               Access: ADMIN + tray-only.

wick_manager_system_prefs_set
  Input: { partial userconfig.Config JSON } (PATCH-style merge)
  Description: Update per-machine tray preferences. Only fields present in input are merged;
               omitted fields keep current value.
               Returns the new full config.
               Access: ADMIN + tray-only.
```

### Ringkasan total per role

- **User non-admin** (13 tool):
  - app_*: ❌ (semua admin-only)
  - job_*: `list`, `get`, `set_config`, `set_schedule`, `run_now`, `get_run`, `list_runs` (7 — tag-filtered)
  - tool_*: `list`, `get`, `set_config` (3)
  - connector_*: `list`, `get`, `set_config` (3)
  - Mutasi config job/tool/connector gate per-resource-access. Secret di-mask, locked di-reject.
- **Admin non-tray** (17 tool): user 13 + 4 `wick_manager_app_*`.
- **Admin tray** (24 tool): admin non-tray 17 + 7 `wick_manager_system_*`.

---

## Keputusan final (jawaban semua pertanyaan)

1. Nama connector: **`wickmanager`** (path `internal/connectors/wickmanager/`, key `wickmanager`).
2. Tambah **`Fixed bool` di `connector.Meta`** — cuma flag bool, bukan `MaxInstances int`. Reusable buat connector lain ke depannya yg mau disable duplikat. `Fixed=true` artinya jumlah instance fixed (cuma 1, di-seed sama wick).
3. Service injection: **Opsi C** — connector beneran di `internal/connectors/wickmanager/`, register lewat helper di `app/app.go` yg capture service lewat closure.
4. Op `system_*` cuma jalan kalau process di-launch via tray. `wick server` / `wick worker` / headless: error "unavailable in this run mode" walau admin. Implementasi: `processctl.IsManaged() bool`.
5. **`jobs_get_run` boleh dipanggil non-admin selama dia punya akses ke parent job-nya.** Konsisten sama `jobs_run_now` — yg boleh trigger, boleh baca hasil.
6. **Akses model = per-resource-access** (lebih permisif dari UI manager existing). Semua `*_list` tag-filtered. Semua `*_get` per-resource-access. **Mutasi config (`*_set_config`, `*_set_schedule`) juga per-resource-access** — bukan admin-only. Yg jaga keamanan: (a) row `IsSecret=true` di-mask saat read, (b) row `Locked=true` reject saat set, (c) field `Required` divalidasi.

   Trade-off: MCP behavior > UI manager (UI tetep `adminOnly` buat edit). User dgn tag-akses ke job/connector X via MCP bisa edit config-nya walau via UI dashboard ngak. Sengaja — LLM butuh permukaan otonom buat re-configure resource. Re-evaluasi kalau ada incident.
7. **Audit log semua op (read + write)**, secret di-mask, mutating tambah `before`/`after`. Tujuan log: **file baru `mcp.log`** sebar di `~/.<appName>/logs/` — sejajar `app.log`/`server.log`/`worker.log`. Logger keempat di `logSet`.
8. **MCP surface: top-level meta-tools** (`wick_manager_app_list`, `wick_manager_job_list`, dst). Implementasi internal tetep connector `wickmanager` (dapet UI manager + audit gratis), tapi MCP layer **expand** ops connector jadi tool descriptor top-level. LLM ngak liat `conn:wickmanager/...` — liat `wick_<area>_<verb>` langsung.
9. **Ngak ada list-all global** lintas resource. User pasti di-scope lewat list per area (`wick_manager_app_list`, `wick_manager_job_list`, `wick_manager_connector_list`, `wick_manager_tool_list`).

## Tujuan

Buka management plane wick (apps, jobs, tools, connectors, lifecycle server/worker) sebagai **connector wick biasa** — bukan MCP meta-tools baru.

Connector dinamain `wick-manager` (placeholder, ganti kalau perlu). Satu instance built-in di-seed otomatis sama wick. Setiap operasi adalah `connector.Operation` normal, jadi langsung dapet:

- MCP discovery via `wick_list` / `wick_search` / `wick_get` / `wick_execute`.
- Halaman admin UI di `/manager/connectors/wick-manager/...` lengkap sama edit form, run history, test panel.
- Tag-based visibility, dukungan encrypted-fields, audit log via `connector_runs`.
- Opt-in destructive op (`OpDestructive`) → MCP annotation hint ngalir otomatis.

Bikin management plane sama persis kayak connector lain — ngak ada code path MCP paralel.

## Kenapa pakai shape connector (vs MCP meta-tools baru)

Draft sebelumnya nawarin `wick_apps_list`, `wick_jobs_set_schedule`, dll. sebagai MCP tool top-level baru. Trade-off:

| Aspek | Meta-tools baru | Connector baru |
|-------|-----------------|----------------|
| Kode MCP net-new | Banyak handler + descriptor di `internal/mcp/handler.go` | Ngak ada — `wick_list/get/execute` udah jalan |
| Admin UI buat edit/inspect | Butuh halaman terpisah | Gratis — halaman detail connector udah ada |
| Tag visibility | Ditulis ulang manual per tool | Gratis — sistem tag connector udah cover |
| Run history / audit | Surface baru | Gratis — `connector_runs` udah catat tiap op |
| Encrypted fields | Wire `wick_enc_` manual | Gratis — `Configs` connector hormatin tag `secret;` |
| Destructive hint | Set manual per tool | Gratis — `OpDestructive(...)` ngalir lewat |
| Discoverability buat LLM | Harus tahu nama tool baru | Muncul di `wick_list` / `wick_search` existing |

Connector menang di semua yg udah ada. Trade-nya cuma satu modul connector baru.

## Constraint

- Ngak boleh bypass scoping existing. Setiap operasi harus panggil **service method yg sama** sama UI handler-nya. Ngak ada logic paralel.
- Flag `Destructive` per op nyetir annotation MCP (pipeline connector existing udah ngelakuin ini).
- Operasi admin-only enforce admin di dalam handler op — connector belum punya role gating bawaan, jadi tambah satu helper (`requireAdmin(c) error`) yg di-reuse antar op.
- Instance built-in di-seed sama `DefaultTags=[tags.System]` biar non-admin ngak liat kecuali admin kasih tag. Pola sama kayak tool `encfields`.

## Bentuk modul

Connector `wickmanager` jadi **back-end**. MCP layer **expand** ops connector jadi top-level tools `wick_<area>_<verb>`.

Path: `internal/connectors/wickmanager/`

```
wickmanager/
    connector.go      — Meta (Fixed:true), Configs, struct Input per-op, Operations, handler op
    service.go        — validasi murni + wiring panggilan service
    repo.go           — panggil DB / configs.Service / manager.Service langsung (di-inject via Ctx)
    audit.go          — helper logging zerolog buat tiap op (output ke mcp.log)
    access.go         — helper requireAdmin / requireJobAccess / requireTray buat handler op
```

MCP layer (`internal/mcp/handler.go`) tambah:

```go
// Loop ops connector wickmanager (yg punya Meta.Key=="wickmanager"),
// emit jadi tool descriptor top-level dengan nama "wick_<op_key>".
// Contoh: op `app_list` → tool MCP `wick_manager_app_list`.
// handleToolsCall buat tool `wick_<x>` translasi ke wick_execute internal
// dengan tool_id=conn:<wickmanager_instance_id>/<x>.
```

LLM ngak perlu manggil `wick_get` buat tau ops wickmanager — semua udah expand di `tools/list`. Connector lain tetep pakai meta-tool pattern existing (`wick_list/get/execute`).

### Fixed flag (perubahan public API)

Tambah ke `pkg/connector/connector.go`:

```go
type Meta struct {
    Key         string
    Name        string
    Description string
    Icon        string
    // Fixed, kalau true, jumlah instance connector ini fixed (cuma 1).
    // Wick auto-seed satu row pas first boot, dan admin UI hide tombol
    // "Add new instance". Bootstrap nolak insert kedua dgn ErrFixedInstanceViolation.
    //
    // Useful buat connector yg ngomong ke single in-process resource (e.g.
    // wickmanager) atau external service yg cuma satu konfigurasi.
    //
    // Default false = boleh banyak instance (behavior connector existing).
    Fixed bool
}
```

Side-effect:
- `internal/connectors/service.go` Bootstrap: kalau `Fixed`, panggil `EnsureInstance(key, label)` (auto-seed satu row kalau belum ada).
- `internal/connectors/repo.go` Create: tolak insert kedua dgn error `ErrFixedInstanceViolation` kalau `Fixed`.
- `internal/manager/connectors.go` UI: hide tombol "Add" buat row connector dgn `Fixed`.

### Configs

Connector-nya sendiri ngak butuh row config (ngak ada endpoint eksternal, ngak ada API key). Cuma penanda kosong:

```go
type Configs struct {
    // Kosong — wickmanager ngomong ke service in-process.
    // Disimpen sebagai struct eksplisit biar form admin render "no config required".
}
```

### Service injection

Handler op butuh `configs.Service`, `manager.Service`, `connectors.Service`, writer `userconfig`, dan handle process-lifecycle. Op connector cuma dapet `connector.Ctx` hari ini. Dua opsi:

**Opsi A — accessor global.** Package baru `internal/wickmgr/` nyimpen setter package-level (`SetConfigsSvc`, `SetManagerSvc`, …) yg dipanggil sekali pas boot. Handler op panggil `wickmgr.Configs()`. Simpel, mirror cara `app.RegisterConnector` udah wire global. Risiko: susah di-test, coupling tersembunyi.

**Opsi B — extend `connector.Ctx`.** Tambah `Ctx.AppServices()` yg return struct berisi service wick-internal. Sentuh public API; tiap connector skrng punya backdoor ke wick internal. Sinyal jelek.

**Opsi C — registrasi khusus.** `app.RegisterWickManagerConnector(svc *configs.Service, mgr *manager.Service, …)` baru wire modul connector berbasis closure: handler di-bind pas registrasi, service di-capture closure. Ngak ada state global, ngak ngubah public API.

Proposal: **Opsi C**. Cocok sama cara project downstream udah passing dependency per-app ke connector mereka. Semua tetap scoped.

### Aturan visibility

Acuan: tabel "Akses control — full per-op rule" di awal dokumen (definitif). Ringkasan:

- Semua `*_list` tag-filtered. Admin liat semua, user liat yg dia punya akses. Resource ngak related → hide total.
- `wick_manager_app_*` cuma `regenerate` admin-only.
- `wick_manager_system_*` admin-only + tray-only.
- Mutasi `*_set_config` / `*_set_schedule` per-resource-access (ngak admin-only, lebih permisif dari UI).

Filter visibility ngak boleh ditulis ulang. Panggil yg existing:
- Jobs: walk `manager.Service.ListJobs(ctx)` → cek `tool_permissions` row buat path `/jobs/{key}` lewat method yg sama dgn `RequireJobAccess` middleware.
- Tools: pola sama, path `/tools/{key}`.
- Connectors: `connectors.Service.ListVisibleTo(ctx, tagIDs, isAdmin)`.

### Surface operasi

Op key di connector wickmanager pakai pola `<area>_<verb>` (singular). MCP layer emit-nya jadi `wick_<area>_<verb>`.

Contoh:
- Op connector `app_list` → MCP tool `wick_manager_app_list`
- Op connector `job_run_now` → MCP tool `wick_manager_job_run_now`
- Op connector `system_server_start` → MCP tool `wick_manager_system_server_start`

Detail siapa-boleh-apa, gate, dan service call ada di tabel **"Akses control — full per-op rule"** di awal dokumen (single source of truth). Section ini cuma list nama ops biar gampang di-cross-ref.

Op connector wickmanager:

- `app_list`, `app_get_config`, `app_set_config`, `app_regenerate_config`
- `job_list`, `job_get`, `job_set_config`, `job_set_schedule`, `job_run_now`, `job_get_run`, `job_list_runs`
- `tool_list`, `tool_get`, `tool_set_config`
- `connector_list`, `connector_get`, `connector_set_config`
- `system_status`, `system_server_start`, `system_server_stop`, `system_worker_start`, `system_worker_stop`, `system_prefs_get`, `system_prefs_set`

Total: **24 op** (4 app + 7 job + 3 tool + 3 connector + 7 system). MCP layer expand jadi `wick_manager_app_list` / `wick_manager_job_list` / dst.

(Catatan: `wick_list` / `wick_get` / `wick_execute` existing tetap dipertahankan buat connector lain — wickmanager pelanggan top-level pengecualian.)

### Ekstrak process lifecycle

Sama kayak v2: copot `startServer/stopServer/startWorker/stopWorker` dari `internal/systemtray/systray.go` (di-gate build-tag `!headless`) ke package baru `internal/processctl/` yg dikonsumsi `systemtray` dan `wick-manager`. Tray simpan side-effect UI-nya via callback.

Ini satu-satunya potongan infrastruktur net-new. Sisanya wiring service existing ke handler op.

## Seed instance built-in

Wick auto-seed satu instance `wick-manager` pas first boot (biar admin ngak perlu nambahin manual). Pola cocok sama blok `init()` di `internal/tools/registry.go` buat `encfields`:

```go
// di init() internal/connectors/registry.go
extra = append(extra, connector.Module{
    Meta:        wickmanager.Meta(),
    Configs:     entity.StructToConfigs(wickmanager.Configs{}),
    Operations:  wickmanager.Operations(svcs),  // di-bind closure (Opsi C)
    DefaultTags: []connector.DefaultTag{tags.System},
})
```

Default System tag → cuma admin yg liat by default. Admin bisa re-tag buat user non-admin terpilih.

## Masking & encrypted fields

Udah di-handle pipeline connector:

- `apps_list_configs` / `jobs_get` / `tools_get` / `connectors_get` return row `entity.Config`. Apply mask helper (`***` buat plaintext-secret, pass-through buat `wick_enc_`, empty-stay-empty) **sebelum** balikin ke framework connector — logic sama kayak rencana v2.
- Tag `secret;` di field `Input` per op auto-encrypt nilai masuk via mesin connector existing. Saat ini ngak ada field `Input` yg butuh `secret;` (secret yg di-handle udah at-rest di `configs`), tapi plumbing-nya udah ada kalau perlu.

## Audit logging (Q-B + Q-C)

**Tujuan log: file `mcp.log` baru.** Sejajar sama `app.log` / `server.log` / `worker.log` yg udah ada di `~/.<appName>/logs/`.

Perubahan ke `internal/systemtray/logs.go`:

```go
type logSet struct {
    App    zerolog.Logger
    Server zerolog.Logger
    Worker zerolog.Logger
    MCP    zerolog.Logger      // BARU
    Dir    string
}
```

Tambah `fMCP, _ := openLog("mcp")` + `mwMCP` + `MCP: zerolog.New(mwMCP)...`. Pruning otomatis ngikutin pola filename (`mcp-YYYY-MM-DD.log`) — `pruneOldLogs` udah generic via prefix.

Logger MCP di-pass ke `wickmanager` lewat closure (Opsi C):

```go
app.RegisterWickManagerConnector(WickManagerDeps{
    Configs:    configsSvc,
    Manager:    managerSvc,
    Connectors: connectorsSvc,
    ...
    Logger:     logset.MCP,   // zerolog.Logger; default global kalau nil
})
```

Mode non-tray (`wick server` / headless): logger fallback ke `log.Logger` global (stdout/file existing).

### Structure log

Tiap op — **read maupun write** — log lewat helper `audit.logOp`:

```go
logger.Info().
    Str("op", "jobs_set_schedule").
    Str("user_id", user.ID).
    Bool("is_admin", user.IsAdmin()).
    Interface("args", maskedArgs).         // secret di-mask
    Str("result", "success").              // atau "error"
    Dur("duration", elapsed).
    Msg("wickmanager op")
```

Buat op mutating tambahin:

```go
    Interface("before", maskedBefore).
    Interface("after", maskedAfter).
```

Helper `audit.go`:

```go
func logOp(logger zerolog.Logger, c *connector.Ctx, op string, args any, err error, elapsed time.Duration)
func logOpDiff(logger zerolog.Logger, c *connector.Ctx, op string, args, before, after any, err error, elapsed time.Duration)
```

Pola defer-elapsed di tiap handler op — semua op route lewat helper, ngak ada yg log manual.

Catatan: audit ini **di luar** `connector_runs` (yg tetep ngerekam run via pipeline connector existing). Tujuannya beda — `connector_runs` buat user-facing run history, `mcp.log` buat ops/security trail yg gampang di-grep.

## File yg disentuh

```
internal/connectors/wickmanager/
    connector.go                           — BARU (Meta Fixed:true, Operations builder)
    service.go                             — BARU (validasi murni)
    repo.go                                — BARU (forward ke service wick)
    audit.go                               — BARU (helper logging)
    access.go                              — BARU (requireAdmin / requireJobAccess / requireTray)
internal/connectors/registry.go            — register built-in wickmanager
internal/connectors/service.go             — Bootstrap auto-seed Fixed instance
internal/connectors/repo.go                — Create reject duplikat kalau Fixed
internal/manager/connectors.go             — UI hide tombol "Add" buat Fixed
internal/processctl/                       — package BARU; pemilik lifecycle + IsManaged()
internal/systemtray/systray.go             — refactor konsumsi processctl, set IsManaged=true
internal/systemtray/logs.go                — tambah field MCP di logSet, buka mcp.log
pkg/connector/connector.go                 — tambah field Fixed di Meta
app/app.go                                 — wiring RegisterWickManagerConnector (Opsi C)
```

Ngak ada perubahan di `internal/mcp/*` — connector muncul lewat `wick_list/get/execute` existing.

Ngak ada perubahan di `manager.Service` / `configs.Service` — cuma consumer (handler op) yg ditambah.

## Pertanyaan terbuka

Tidak ada — semua udah dijawab. Siap eksekusi mulai dari Fase 0.

## Pem-fase-an

**Fase 0 — Fixed flag (prereq)** (½ hari)
- Tambah `Meta.Fixed` di `pkg/connector/connector.go`.
- Bootstrap auto-seed kalau Fixed, Create reject duplikat kalau Fixed.
- UI manager hide tombol "Add" buat Fixed connector.
- Test: pakai connector dummy, pastiin row kedua di-tolak.

**Fase 1 — skeleton + op read-only** (1–2 hari)
- Bikin skeleton `internal/connectors/wickmanager/` (Fixed:true).
- Implement `apps_list_configs`, `jobs_list`, `jobs_get`, `tools_list`, `tools_get`, `connectors_list`, `connectors_get`.
- Mask helper buat row `entity.Config` secret.
- Helper `access.go` (requireAdmin / tag-visible filter).
- Helper `audit.go` (log semua op).
- Register sebagai built-in via Opsi C (`app.RegisterWickManagerConnector(svcs)`).

**Fase 2 — write config** (1 hari)
- `apps_set_config`, `apps_regenerate_config`, `jobs_set_config`, `tools_set_config`, `connectors_set_config`.
- Diff log buat semua op write (before/after, secret di-mask).

**Fase 3 — operasi job** (½ hari)
- `jobs_set_schedule`, `jobs_run_now`, `jobs_get_run`, `jobs_list_runs`.
- Per-job-access gate buat `run_now` / `get_run` / `list_runs`.

**Fase 4 — process lifecycle (refactor)** (2–3 hari)
- Ekstrak `internal/processctl` + `IsManaged() bool`.
- Migrate `systemtray` konsumsi-nya, set IsManaged=true.
- Implement `system_status`, `system_server_start/stop`, `system_worker_start/stop`, `system_prefs_get/set`.
- Helper `requireTray()` di `access.go`.

## Risiko

- **Plaintext bocor di `apps_list_configs` / `jobs_get` dll.** Mitigasi: satu helper mask, test fokus pastiin ngak ada plaintext lolos.
- **Bypass access-control.** Mitigasi: tiap op delegasi ke service method yg sama kayak UI handler; gate adalah helper `requireAdmin` / `requireJobAccess` di atas tiap handler, ~3 baris, gampang di-audit.
- **Ekstrak lifecycle bikin tray rusak.** Mitigasi: PR sendiri buat fase 4; smoke test manual (start/stop server + worker via tray); kill port abis smoke.
- **LLM bingung connector.** wick-manager + connector beneran "post Slack message" dua-duanya muncul di `wick_list` — LLM bisa salah panggil wick-manager pas user nanya soal Slack. Mitigasi: `Meta.Description` jelas ("Read and edit wick's own configs / jobs / tools / connectors. Use this only when the user asks about wick itself, not third-party APIs.") + deskripsi per op pertegas scope.

## Di luar scope

- Bikin / hapus instance connector via wick-manager (admin pakai UI).
- User management / tag CRUD via wick-manager.
- Manajemen OAuth grant / PAT.
- Baca log file / run history di luar `connector_runs`.
- Installer MCP buat Claude Desktop / Cursor (tray-only).

Flag kalau salah satunya jadi blocker.
