# Tools Config & Connections

Dokumentasi desain dan state implementasi untuk 3 area:

1. Multi-instance tools (logic sama, target beda)
2. Per-tool config (runtime-editable, UI-editable)
3. Connection manager (Slack / Notion / GSheet — OAuth / API key)

---

## 1. Multi-instance tools

**Pilihan: `app.RegisterTool` dipanggil berkali-kali per instance.** ✅ shipped

```go
app.RegisterTool(tool.Tool{Key:"sales", Name:"Sales Sheet", Icon:"📊"}, salesCfg, salesRegister)
app.RegisterTool(tool.Tool{Key:"ops",   Name:"Ops Sheet",   Icon:"📊"}, opsCfg,   opsRegister)
```

Tiap instance dapat route sendiri (`/tools/sales`, `/tools/ops`), bisa beda icon/tag/visibility. Nambah instance = tambah `RegisterTool`, rebuild.

Kalau nanti ada 5+ instance dari logic yang sama → upgrade ke model "instance = data" (preset di DB, no rebuild). Belum perlu sekarang.

---

## 2. Config tool/job

✅ shipped

Tool declare config via typed struct + `wick:"..."` tags. Framework reflect ke `[]entity.Config` saat boot, stamp `Owner = meta.Key`, reconcile ke tabel `configs`.

```go
type Config struct {
    APIURL  string `wick:"api_url,description=Base API endpoint,required"`
    APIKey  string `wick:"api_key,description=Secret key,secret"`
    Timeout int    `wick:"timeout,description=Request timeout in seconds,default=30"`
}

app.RegisterTool(
    tool.Tool{Key: "my-tool", Name: "My Tool", Icon: "🔧"},
    entity.StructToConfigs(mytool.Config{Timeout: 30}),
    mytool.Register,
)
```

Tag grammar: `wick:"key,description=...,type=...,default=...,required,secret,locked"`. Field tanpa tag di-skip.

`entity.Config` fields: `Owner`, `Key`, `Value`, `Type` (text/textarea/number/checkbox/dropdown/url/email/color/date/datetime), `Options` (pipe-separated, buat dropdown), `IsSecret`, `Required`, `CanRegenerate`, `Locked`, `Description`.

Handler baca via `c.Cfg("key")` / `c.CfgInt` / `c.CfgBool` / `c.Missing()` (tool) atau `job.FromContext(ctx).Cfg("key")` (job).

UI edit config ada di `/manager/tools/{key}` dan `/manager/jobs/{key}` — sudah ship. User override per-field (`Locked`) belum ship.

---

## 3. Connection manager

**Status: belum ship.** Tabel `connections` belum ada.

### Pendekatan bertahap

**Sekarang — API key via `configs`:**
Untuk Notion, Slack webhook, GSheet service account JSON → cukup `IsSecret:true` di Config struct. 80% use-case beres tanpa OAuth.

**Nanti — OAuth:**
Kalau butuh Slack user-level / GSheet user-scope → tambah tabel `connections` + 1 provider (Google dulu). Pisah dari `configs` karena OAuth punya lifecycle (refresh, revoke, re-consent).

**Lebih nanti — generic:**
Setelah 2-3 provider ship, baru abstraksi generic.

### Target struktur admin

```
/admin
 ├── /settings      → app-level config           ✅ shipped
 ├── /tool-settings → config per tool/instance   ⬜ belum
 └── /connections   → OAuth providers            ⬜ belum
```

---

## 4. Jalur komunikasi

### Kontrak kode (shipped)

```go
// pkg/tool/tool.go
type Module struct {
    Meta     Tool
    Configs  []entity.Config   // dari StructToConfigs(cfg)
    Register func(r Router)
}

type HandlerFunc func(c *Ctx)

// pkg/job/job.go
type Module struct {
    Meta    Meta
    Configs []entity.Config
    Run     RunFunc
}

type RunFunc func(ctx context.Context) (string, error)
```

### Bootstrap order (startup)

```
DB connect
  ↓
configs.Bootstrap(ctx, ...toolConfigs, ...jobConfigs)
  → reconcile app-level defaults
  → reconcile per-tool/job rows (Owner = meta.Key)
  ↓
tool.Register(mux) + scheduler.Register(job)
  ↓
middleware: session → auth → visibility
  ↓
http.ListenAndServe / worker.Start()
```

### Runtime — Tool (HTTP)

```
request → session → auth → visibility
  ↓
HandlerFunc(c *tool.Ctx)
  c.Cfg("key") → configs.GetOwned(meta.Key, "key")
  ↓
response
```

### Runtime — Job (cron)

```
scheduler tick
  ↓
job.WithCtx(ctx, job.NewCtx(meta.Key, configsSvc))
  ↓
RunFunc(ctx):
  jc := job.FromContext(ctx)
  jc.Cfg("key") → configs.GetOwned(meta.Key, "key")
  ↓
(markdown, err) → run log
```

### Mapping file

| Komponen | File | Status |
|---|---|---|
| Tool contract | [`pkg/tool/tool.go`](../../pkg/tool/tool.go) | ✅ |
| Tool Ctx | [`pkg/tool/ctx.go`](../../pkg/tool/ctx.go) | ✅ |
| Job contract | [`pkg/job/job.go`](../../pkg/job/job.go) | ✅ |
| Job Ctx | [`pkg/job/ctx.go`](../../pkg/job/ctx.go) | ✅ |
| Config entity + StructToConfigs | [`pkg/entity/config.go`](../../pkg/entity/config.go) | ✅ |
| Config service | [`internal/configs/service.go`](../configs/service.go) | ✅ |
| App-level defaults | [`internal/configs/spec.go`](../configs/spec.go) | ✅ |
| Tool registry | [`internal/tools/registry.go`](../tools/registry.go) | ✅ |
| Job registry | [`internal/jobs/registry.go`](../jobs/registry.go) | ✅ |
| Connections service | `internal/connections/` | ⬜ |
| user_overrides | `internal/user_overrides/` | ⬜ |

### Prinsip

- Tool/job gak `import configs` langsung — selalu via `c.Cfg` / `jc.Cfg`.
- Framework reconcile di boot, handler tinggal baca.
- Tool declare `Configs` saat `RegisterTool` → framework handle sisanya.
- Tool tanpa `Configs` (pakai `app.RegisterToolNoConfig`) jalan apa adanya.
