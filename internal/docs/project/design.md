# Project — Bundle of Defaults + Folder (design)

Status: design only — implementasi belum start.
Update terakhir: 2026-06-01.

**Paradigm:** `Workspace` (concept lama) di-rename jadi `Project` (term
familiar dari Codex/Claude). **1 Project = 1 Folder + Defaults + Pinned
sessions.** Bukan multi-folder — multi-workspace per project dibuang
karena overhead UI/picker yg gak proporsional sama use-case.

Session bound ke 1 project at create. Folder ikut project (gak bisa
ganti folder dalam project — folder = part of project identity). Mau
folder lain = bikin project baru atau move session ke project lain.

Paired mockup: [`mockup.html`](mockup.html). Update keduanya barengan.

---

## TODO

**Deferred (out of v1 scope):**

- ⏸ **Title strategy** — auto-AI title, label_locked, dll. Decide nanti via MCP atau provider side.
- ⏸ **Multi-folder per project** — pernah dipertimbangkan, ditolak: UI overhead (2 dropdown + 3-level move menu) > benefit utk single-user. Bisa di-revisit kalau real use-case muncul.
- ⏸ **Global pinned (luar project)** — fitur lebih luas dari scope project. Defer, mungkin masuk ke fitur "favorites" atau "starred" terpisah nanti.

**v1 locked decisions:**

- ✓ **Default project** — fresh install bikin project `default` (managed folder). Existing `default` workspace auto-migrate jadi `default` project (lewat migration script).
- ✓ **Icon** — free-text emoji (1 grapheme), no curated set. Optional, default = 📁.
- ✓ **Folder required** — project minimal punya 1 folder (managed atau custom). Project-tanpa-folder ditolak — session create harus deterministic punya cwd.
- ✓ **Pinned** — per-project only via `project.PinnedSessions[]`. Visible saat scoped. No global pin.

---

## 1. Tujuan & non-goal

**Tujuan:**

- Rename Workspace → Project di term + pkg (familiar UX)
- Tambah field: defaults (preset/provider/system_addon), pinned_sessions, icon, name (display)
- Scope sidebar (Recent) ke project
- New-session di scoped project: auto-inherit semua defaults
- Move session antar project tanpa filesystem rename
- Auto-migrate data lama (`workspaces/<name>/` → `projects/<id>/`)

**Non-goal:**

- Bukan multi-folder per project (rejected)
- Bukan permission boundary — project = personal organizer
- Bukan SQLite migration — disk + JSONL tetap
- Bukan replace existing preset/provider system

---

## 2. Konsep & terminologi

```
Project
├─ Folder           — 1 cwd (managed atau custom path)
├─ Defaults         — preset + provider + system_addon
├─ Pinned sessions  — fast-access list
├─ Icon + Name      — display
└─ Sessions         — N sessions belong here
```

| Term | Arti baru | Catatan |
|---|---|---|
| **Project** | Bundle: folder + defaults + pinned | Replaces existing Workspace concept |
| **Folder** | Cwd untuk agent (managed atau custom) | 1 per project, integral |
| **Session** | Conversation, bound to 1 project | `meta.project_id` |

**Resolusi cwd saat agent spawn:**

```
session.Meta.ProjectID → project.Meta (managed:projects/<id>/files | custom:project.Path) → cwd
```

---

## 3. Storage layout

### 3.1 Layout

```
<BaseDir>/
  presets/                          ← unchanged
  workflows/                        ← unchanged
  sessions/
    <sid>/
      meta.json                     ← + project_id field
      conversation.jsonl
      ...
  projects/                         ← NEW (replaces workspaces/)
    <project-id>/
      meta.json                     ← project meta (defaults, pinned, folder ref)
      files/                        ← managed cwd (only when CustomPath empty)
```

Custom-path projects: folder `files/` gak dibuat — path absolute di
meta saja. Wick gak own folder itu, jadi delete project = gak hapus
folder external.

### 3.2 Project meta.json

```json
{
  "id": "01J...",
  "name": "Wick Backend",
  "icon": "📁",
  "description": "Main wick repo work",
  "custom_path": "/d/code/work/wick",
  "defaults": {
    "preset": "engineer",
    "provider": "claude/claude",
    "system_addon": ""
  },
  "pinned_sessions": ["01J..."],
  "tags": [],
  "created_at": "2026-06-01T...",
  "updated_at": "2026-06-01T..."
}
```

**Folder resolution:**
- `custom_path != ""` → that path is the cwd (validated abs + exists at create)
- `custom_path == ""` → managed at `projects/<id>/files/`

**Field provenance (vs lama workspace.Meta):**

| Field | Source |
|---|---|
| `id` | NEW (UUID) — lama: workspace.Name was identifier |
| `name` | NEW (display name, mutable) |
| `icon` | NEW |
| `description` | unchanged from workspace.Meta.Description |
| `custom_path` | unchanged from workspace.Meta.CustomPath |
| `defaults.preset` | from workspace.Meta.DefaultPreset |
| `defaults.provider` | from workspace.Meta.DefaultProvider |
| `defaults.system_addon` | NEW |
| `pinned_sessions` | NEW |
| `tags` | unchanged |
| `created_at` | unchanged |
| `updated_at` | NEW |

### 3.3 Session meta.json (changes)

```diff
  {
-   "workspace": "wick",
+   "project_id": "01J...",
    "origin": "ui",
    "preset": "engineer",
    ...
  }
```

`session.Meta.Workspace` field name **dropped** — diganti `ProjectID`.
Migration script handles existing sessions (set `project_id` ke
project yg dibuat dari workspace lama, hapus `workspace` field).

---

## 4. Operations

### 4.1 Create project

```
POST /tools/agents/projects { name, icon?, description?, custom_path?, defaults? }
→ project.Create():
  - id = uuid.New()
  - validate custom_path if set (abs + exists)
  - mkdir projects/<id>/
  - if custom_path == "": mkdir projects/<id>/files/
  - write meta.json
```

### 4.2 Update project (rename / change defaults / change folder)

```
PATCH /tools/agents/projects/<id> { name?, icon?, defaults?, custom_path? }
→ project.SaveMeta():
  - validate; for custom_path change: warn kalau ada live session, but allow
  - rewrite meta.json with UpdatedAt bumped
```

Folder change semantik: kalau project dgn managed dipindah ke
custom_path, managed `files/` ngak ke-touch (data ditinggal di disk
sebagai backup; user-deletable manual). Sebaliknya custom → managed:
managed dir dibuat, custom path gak ke-touch.

### 4.3 Create session in project

```
POST /tools/agents/sessions { project_id, preset?, provider?, ... }
→ session.Create():
  - validate project exists
  - inherit defaults from project if preset/provider not supplied
  - meta.project_id = projectID
  - meta.preset = resolved preset
```

### 4.4 Move session to another project

```
POST /tools/agents/sessions/<sid>/project { project_id }
→ session.SetProject():
  - validate target project exists (or empty for unscope)
  - atomic meta.project_id update
  - registry cache refresh
```

Sessions ID + path stable. No filesystem rename.

### 4.5 Delete project

```
DELETE /tools/agents/projects/<id>
→ for each session with meta.project_id == this: set project_id = ""
→ if managed: rm -rf projects/<id>/  (deletes managed files/)
→ if custom: rm meta.json + project dir (external folder UNTOUCHED)
→ registry.deleteProject
```

Sessions are preserved (just unscoped) — project is layer above
sessions, not their owner.

### 4.6 List sessions

```
scoped:     filter registry.sessions by Meta.ProjectID == pid
unscoped:   filter registry.sessions by Meta.ProjectID == ""
all chats:  all registry.sessions (cross-project)
```

In-memory filter cukup utk v1. Sessions count < 10k aman.

---

## 5. Migration (one-shot, boot-time)

Idempotent: skip kalau `projects/` non-empty.

```go
func MigrateWorkspacesToProjects(layout config.Layout) error {
    if hasAnyProject(layout) { return nil }
    workspaces := workspace.List(layout)
    if len(workspaces) == 0 { return nil }

    workspaceToProjectID := map[string]string{}
    for _, name := range workspaces {
        ws := workspace.Load(layout, name)
        pid := uuid.New().String()
        project.Create(layout, project.CreateOptions{
            ID:          pid,
            Name:        name,           // display name = old workspace name
            Description: ws.Meta.Description,
            CustomPath:  ws.Meta.CustomPath,
            Defaults: project.Defaults{
                Preset:   ws.Meta.DefaultPreset,
                Provider: ws.Meta.DefaultProvider,
            },
            Tags: ws.Meta.Tags,
        })
        // Move managed files: workspaces/<name>/files → projects/<pid>/files
        if ws.Meta.CustomPath == "" {
            os.Rename(
                layout.WorkspaceManagedPath(name),
                filepath.Join(layout.ProjectDir(pid), "files"),
            )
        }
        workspaceToProjectID[name] = pid
    }

    // Relink sessions: meta.workspace → meta.project_id
    for _, sid := range session.List(layout) {
        s := session.Load(layout, sid)
        if s.Meta.Workspace == "" { continue }  // unscoped, leave alone
        pid, ok := workspaceToProjectID[s.Meta.Workspace]
        if !ok { continue }
        s.Meta.ProjectID = pid
        s.Meta.Workspace = ""  // drop legacy field
        session.SaveMeta(layout, sid, s.Meta)
    }
    // Legacy workspaces/ dir kept defensively. Cleanup deferred to v1.1.
    return nil
}
```

**Safety:**
- Idempotent — re-run no-op
- `os.Rename` atomic same-FS
- Session meta saved only after project create succeeds
- Failure mid-loop = partial state, but next boot resumes (idempotent check on per-project basis bisa ditambah kalau perlu)

---

## 6. UI states

Detail visual: [`mockup.html`](mockup.html).

| State | Sidebar | Header | New-session form |
|---|---|---|---|
| Unscoped landing | Projects section + All chats | No chip | Project picker dropdown (empty = no project), provider/preset empty |
| Scoped to project | Breadcrumb `← All chats / 📁 X` + filtered Recent + pinned | Chip `📁 X ✕` | Project locked to active, provider/preset prefilled from project defaults |
| Project settings | n/a | n/a | Edit name/icon/desc/defaults/folder + pinned list + delete |
| Move-to menu | Right-click row → "Move to project ▸ [project list]" | n/a | n/a (1-level submenu, no workspace pick) |

**Simplifications vs draft multi-workspace sebelumnya:**

- Workspace dropdown **dihapus** dari new-session form
- Move-to menu **1-level** (project list saja, gak ada workspace sub-pick)
- Project settings: section "Folder" tunggal (managed/custom toggle + path input), bukan list editor

---

## 7. Backward compat / deprecation

- `internal/agents/workspace/` pkg: deprecate setelah migration. Keep shim selama 1 release window utk catch any lingering call sites (warn log + redirect ke project pkg).
- `Layout.WorkspacesDir()` + workspace path helpers: keep, mark deprecated; drop di v1.1.
- HTTP routes `/tools/agents/workspaces/*`: keep utk migration window, deprecate. Canonical: `/tools/agents/projects/*`.
- MCP tools `agent.workspace.*`: alias ke `agent.project.*`, deprecate.

---

## 8. MCP surface (sketsa)

```
agent.project.list()                            → array of Project
agent.project.create(name, icon?, custom_path?, defaults?)  → Project
agent.project.update(id, patch)                 → Project
agent.project.delete(id)                        → ok

agent.session.create(project_id?, ...)          → existing tool + optional project_id
agent.session.move(sid, project_id)             → ok
agent.session.list(project_id?, ...)            → filter by project (empty = unscoped)
```

---

## 9. Rejected alternatives

- **Multi-folder per project (Workspaces nested)** — UI overhead (2 dropdown + 3-level move menu) > benefit for single-user. Multi-repo use case rare and solvable via separate projects.
- **Keep `Workspace` pkg name, no rename** — paradigm misalignment dgn Codex/Claude. Familiar term wins.
- **Folder-per-project layout (`projects/<id>/sessions/<sid>/`)** — move = expensive rename, breaks workflow/channel/spawn paths.
- **SQLite sessions table** — overkill saat ini. Sessions flat + in-memory filter cukup sampai ~10k.
- **Project name as ID** — name = identifier breaks rename; UUID + display name decouples.

---

## 10. Refactor surface — impact zones

Ini foundation-level refactor (Workspace → Project). Banyak file akan
ke-senggol meskipun secara konseptual cuma rename + tambah field.
Scan + fix dikerjain **pas implementasi**, bukan upfront — strategi:
go build + go test sebagai discovery loop, fix per compile/test error.

Below adalah peta high-level zona berdasarkan grep awal (Jun 2026,
~100 file match `workspace|Workspace`):

### 10.1 Core (rename + schema)

| Zona | File / pkg | Catatan |
|---|---|---|
| Pkg rename | `internal/agents/workspace/` → `internal/agents/project/` | Replace, bukan rename folder mentah (struct + fields beda) |
| Layout | `internal/agents/config/layout.go` | `WorkspacesDir`/`WorkspaceDir`/`WorkspaceMeta`/`WorkspaceManagedPath` → `ProjectsDir`/... |
| Validator | `internal/agents/storage/validate.go` | `ValidateWorkspaceName` → `ValidateProjectID` (UUID-friendly) |
| Session meta | `internal/agents/session/session.go` | Drop `Workspace`, add `ProjectID` |
| Registry | `internal/agents/registry/*.go` | Cache map, accessors, Manager CRUD |
| Bootstrap | `internal/agents/registry/bootstrap.go` | EnsureDefault project + run migration |

### 10.2 Agent runtime (cwd resolution)

| Zona | File / pkg | Catatan |
|---|---|---|
| Pool | `internal/agents/pool/{pool.go,factory.go}` | Resolve cwd via project.Meta, not workspace.ResolvePath |
| Providers | `internal/agents/provider/{claude,codex,gemini}/spawn.go` | Pass cwd from project |
| Spawner | `internal/agents/provider/{spawner.go,agent.go,provider.go,spawnlog.go}` | Replace workspace param with project |
| Gate hook | `internal/agents/gate/claude_hook.go` | `WorkspaceHookWriter` → `ProjectHookWriter` (or rename interface) |
| Capability init | `internal/agents/provider/{claude,codex,gemini}/capability_init.go` | Cwd resolution path |

### 10.3 Workflow / channels / connectors

| Zona | File / pkg | Catatan |
|---|---|---|
| Workflow nodes | `internal/agents/workflow/nodes/{agent.go,session_init.go,db_query.go}` | Node options may reference workspace |
| Workflow types | `internal/agents/workflow/types.go` | Field rename |
| Workflow MCP | `internal/agents/workflow/mcp/mcp.go` | Tool params |
| Workflow provider | `internal/agents/workflow/{provider/provider.go,template/template.go}` | Workspace placeholders |
| Channels | `internal/agents/channels/{channel.go,setup/setup.go}` | Channel-to-session bind references workspace |
| Channels rest test | `internal/agents/channels/rest/rest_test.go` | Test fixtures |
| Channel handler | `internal/tools/agents/channels_handler.go` | UI bind |
| Connectors | `internal/connectors/{slack,bitbucket}/*.go` | Workspace mentions (mostly identifier strings, audit each) |

### 10.4 HTTP + UI

| Zona | File / pkg | Catatan |
|---|---|---|
| Handler | `internal/tools/agents/handler.go` | Routes `/workspaces` → `/projects`, session create payload |
| Compose form | `internal/tools/agents/view/new_session.templ` | Project picker replaces workspace picker |
| Sessions sidebar | `internal/tools/agents/view/sessions.templ` | Projects section + scoped state |
| Layout | `internal/tools/agents/view/layout.templ` | Sidebar shell |
| Workspaces page | `internal/tools/agents/view/workspaces.templ` | Rename → projects.templ; settings page restructure |
| View models | `internal/tools/agents/view/models.go` | VM struct rename |
| JS | `internal/tools/agents/js/agents.js` | activeProjectID localStorage + scoped fetch + move menu |
| Uploads | `internal/tools/agents/uploads.go` | Path resolution may go through project |
| Service | `internal/tools/agents/service.go` | Bootstrap wiring |

### 10.5 Tests

| Zona | File / pkg | Catatan |
|---|---|---|
| Unit | `internal/agents/{session,storage,registry,workspace}/*_test.go` | Fixtures + rename |
| Pool tests | `internal/agents/multiturn_*_test.go` | Cwd resolution |
| Provider tests | `internal/agents/provider/**/{spawn_test,real_e2e_*test}.go` | Workspace setup helpers |
| Workflow tests | `internal/agents/workflow/nodes/session_init_test.go` etc. | Node fixtures |
| Provider sync | `internal/agents/providersync/*_test.go` | Cross-platform test paths |

### 10.6 Docs (cross-ref update setelah code ship)

- `internal/docs/agents-design.md` — mention workspace concept; update post-impl
- `internal/docs/workflow/*.md` — workflow docs reference workspace; sweep after code
- `internal/docs/command-gate-*.md` — hook injection target naming
- Mockups: workflow mockup mentions workspace (low priority)

### 10.7 Strategi implementasi

1. **Mulai dari core** (10.1) — bikin `project/` pkg + Layout + session meta + migration jalan dulu, gak nyentuh runtime
2. **Verify migration** dgn fixture data — pastiin existing workspaces lossless rebadge ke projects
3. **Sweep 10.2 (runtime)** — kompile-driven: replace workspace refs satu-per-satu, fix build errors
4. **10.3 workflow/channels** — same kompile-driven approach
5. **10.4 HTTP/UI** — last, karena templ regen + JS test loop paling slow
6. **10.5 tests** — paralel saat sweep masing-masing zona; jangan tunda ke akhir (regression catcher)
7. **10.6 docs** — sweep terakhir, setelah feature stabil

**Anti-pattern hindari:** big-bang rename via global find-replace. Workspace term ada di doc comments + identifier names + path strings yg artinya beda-beda. Sweep harus per-zone supaya gak nge-typo refactor.

---

## 11. Acceptance checklist (implementation gate)

- [ ] `internal/agents/project/` package: Meta + CRUD (Create/Load/Save/Delete/List/Exists)
- [ ] `session.Meta.ProjectID` field; drop `session.Meta.Workspace`
- [ ] `Layout.ProjectsDir()`, `Layout.ProjectDir(id)`, `Layout.ProjectManagedPath(id)`
- [ ] Boot-time auto-migrate `workspaces/` → `projects/` (idempotent)
- [ ] Registry: projects cache + accessors + Manager CRUD + MoveSession
- [ ] HTTP routes `/tools/agents/projects/*` + session move endpoint
- [ ] Sidebar: Projects section + scoped state (localStorage activeProjectID) + filtered Recent
- [ ] New-session form: project picker → preset/provider prefill from project defaults
- [ ] Header chip on scoped pages
- [ ] Right-click context menu: Move to project ▸ project list
- [ ] Project settings page: edit defaults + folder field + pinned list + delete
- [ ] MCP tools `agent.project.*` + `agent.session.move`
- [ ] Tests: migration idempotent, move-then-list, delete-project-unscopes-sessions, folder change managed↔custom
