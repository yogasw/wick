## 15. Bootstrap & hot-reload

### Boot

`internal/jobs/workflow/registry.go` punya `RegisterAll(svc)`:
- Loop `svc.List()`, register tiap workflow ke `jobs.Register(job.Module{
  Meta.Key: "workflow:<id>:<trigger-idx>", DefaultCron: ..., Run: ...
  })`.
- Idempotent on Key.

Dipanggil dari:
- [internal/pkg/worker/server.go](../pkg/worker/server.go) sebelum
  `configsSvc.Bootstrap`.
- [internal/pkg/api/server.go](../pkg/api/server.go).

### Reload setelah CRUD

CRUD (UI canvas / MCP / hand-edit + fsnotify) → handler panggil
`RegisterAll(svc)` lagi. Worker tick berikutnya pakai schedule baru.

### Delete

Hapus folder + `jobs.Unregister("workflow:<id>:*")` (perlu tambah
method `UnregisterPrefix` di
[internal/jobs/registry.go](../jobs/registry.go) — sekarang cuma ada
`Register`).

### File watcher

Poll-based watcher (3s tick, no fsnotify dep) di
[`setup/watcher.go`](../../agents/workflow/setup/watcher.go) —
scan `<BaseDir>/workflows/*/workflow.yaml` mtimes per tick.

Per tick:
- New/changed id → `setup.HotReload(ctx, Service, Router, Cron, id)`
  (re-parse YAML, re-validate, re-register triggers/cron).
- ID folder disappeared → unregister from Router + Cron.
- Hash unchanged → no-op.

Started inside `Server.startChannels` so it shares the server ctx
lifetime — graceful shutdown cancels the watcher loop.

Trade-off vs fsnotify: 3s latency on edits but zero new dep + no
platform-specific quirks (Windows file lock, macOS rename
semantics). Acceptable since canvas edits go through the
in-process API path (no watcher needed) — the watcher is for
gitops / external editor / MCP filesystem writes.

UI clients that have a workflow open get the SSE `wf:<id>`
session pushes any subsequent run events (live runs), but the
watcher itself doesn't currently push a "yaml changed" signal —
refresh is manual.

---

