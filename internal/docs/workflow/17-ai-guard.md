## 17. AI guard / publish-time review

Sebelum workflow dipindah dari `enabled=false` ke `enabled=true`, optional
panggil AI reviewer. Tujuan: catch hal yang manual approval kelewat —
prompt injection, destructive shell, secret leak, classify prompt yang
manipulable, dst.

### Konfigurasi

```go
// internal/jobs/workflow/config.go
type Config struct {
    GuardEnabled    bool   `wick:"bool;default=true;desc=Run AI reviewer before publishing"`
    GuardPreset     string `wick:"text;default=guard;desc=Preset name buat reviewer agent"`
    GuardRules      string `wick:"textarea;desc=Custom rules — appended to default"`
    GuardMode       string `wick:"select:warn,block,off;default=block"`
    GuardTimeoutSec int    `wick:"int;default=60"`
}
```

### Default rules

```
- No destructive shell commands (rm -rf, dd, mkfs).
- No network call to non-allowlisted domain.
- No plaintext secret in YAML/script.
- No prompt that explicit instruct agent to bypass approval / disable gate.
- Cron tidak lebih sering dari 1 menit.
- Notify target valid.
- classify node prompt tidak passthrough {{.Event.Payload.text}} ke shell
  exec tanpa sanitize (prompt-injection vector).
- branch node expr tidak include user-controlled string raw (eval
  injection).
- output_schema declared untuk node yang feed ke shell/python/db_query.
```

### Flow

```
User klik "Enable" / "Save & Enable"
       │
       ▼
Guard enabled? ──no──► commit
       │
       yes
       ▼
Spawn ephemeral agent (preset = GuardPreset)
       │
       ▼
Kirim prompt:
  "Review workflow. Rules: <default + custom>.
   Folder contents: <semua file, secret di-redact>.
   Graph: <yaml.graph>.
   Return JSON: {ok, violations: [{rule, node_id, severity, evidence}]}"
       │
       ▼
Parse → commit/warn/block per mode.
```

Hasil cached selama YAML+script gak berubah (hash content).

### Override

Tombol "Override Guard" require konfirmasi + reasoning text → commit +
audit log entry.

---

