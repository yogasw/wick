## 11. Environment & secrets — workflow config

Workflow butuh config: Slack channel target, GitHub PAT, max retry,
toggle feature, dst.

**Schema** (developer contract, version-controlled) declared di workflow
body `env:` block — ikut `body_draft` / `body_published` di tabel
`workflows`. **Values** (UI/AI-managed, secrets encrypted) disimpan
**terpisah di kolom `workflows.env_values`** sebagai JSON blob — current
only, ga ikut version history.

### TODO

- [ ] DB: tambah kolom `env_values TEXT NOT NULL DEFAULT ''` di
      [`entity.Workflow`](../../entity/workflow.go) + AutoMigrate
- [ ] Migration one-shot: kalau file `<id>/env.json` ada di disk, baca →
      tulis ke kolom → hapus file (file-era cleanup, idempotent)
- [ ] `repository.Repo`: `LoadEnvValues(id)` / `SaveEnvValues(id, map)`
      baca/tulis kolom `env_values` (bukan body — ga lewat `parse.Marshal`)
- [ ] `DBService`: override `LoadEnvValues`/`SaveEnvValues` (sekarang
      masih jatuh ke embedded `FileService` → file). Signature tetap.
- [ ] FE: tab **Settings** di Toolbar (sejajar Editor | Executions),
      reuse config-tags form renderer
- [ ] MCP ops: `workflow_get_env_schema` / `_get_env_values` /
      `_set_env_values`

### Kenapa begini

- **DB, bukan file.** Workflow body + versions + test cases udah pindah
  ke DB ([`entity.Workflow`](../../entity/workflow.go)). Env nyusul biar
  konsisten — satu source of truth, ga ada drift file ↔ DB.
- **Reuse config-tags, bukan tabel `configs`.** Yang di-reuse cuma
  **vocabulary + renderer** (widget names, secret machinery, form
  components) dari [config-tags](../../../docs/reference/config-tags.md).
  Values **ga** masuk tabel `configs` — disimpan per-workflow di kolom
  `env_values`. Beda dari connector (yang pakai `owner="connector:{id}"`
  di tabel `configs`): workflow env nempel ke workflow row, satu blob,
  ga ada join.
- **Kolom terpisah, ga ikut body.** `env_values` di-handle di luar
  `parse.Marshal` body. Ganti config (channel target, retry cap) =
  **ga** masuk version snapshot. Secret ciphertext ga ke-duplikat di 50
  draft history. Config = runtime state, bukan struktur graph.

### Schema di workflow body (`env:` block)

Type: [`workflow.EnvField`](../../../internal/agents/workflow/types.go).

```yaml
env:
  - name: SLACK_CHANNEL
    widget: text                    # widget dari config-tags vocab
    desc: "Where to post notifications"
    default: "#support"

  - name: GITHUB_PAT
    widget: secret                  # encrypted; UI shows ••• when set
    desc: "GitHub PAT for issue creation"
    required: true

  - name: MAX_DAILY_RUNS
    widget: number                  # auto-applied untuk int/float field
    desc: "Daily fire cap"
    default: 100

  - name: ESCALATION_MODE
    widget: dropdown
    options: [pager, slack, email]
    desc: "Where to escalate on failure"
    default: slack

  - name: GUARD_PROMPT_EXTRA
    widget: textarea
    desc: "Custom rules buat AI guard"

  - name: ENABLE_AUTO_TRIAGE
    widget: checkbox                # auto-applied untuk bool field
    desc: "Allow LLM to triage without admin approval"
    default: true

  - name: ALLOWED_SLACK_CHANNELS    # multi-row table (kvlist)
    widget: kvlist
    columns: [id, name]
    desc: "Channel allowlist"

  - name: ALLOWED_USERS             # searchable typeahead from channel
    widget: picker
    source: slack.users             # LookupProvider key
    desc: "Allowed users"
    visible_when: ENABLE_AUTO_TRIAGE:true   # hide kalau auto-triage off

  - name: GITHUB_WEBHOOK_URL
    widget: url
    desc: "Endpoint for GitHub webhook callbacks"

  - name: NOTIFY_EMAIL
    widget: email
    desc: "Where to email failure alerts"
```

### Widget vocabulary (mirror config-tags.md)

| Widget | YAML literal | UI form |
|---|---|---|
| `text` (default) | string | single-line input |
| `textarea` | string | multi-line textarea |
| `secret` | string (encrypted, `wick_enc_`) | password input, "Reveal" button |
| `number` | int/float | number input |
| `checkbox` | bool | toggle |
| `dropdown` | string | select dropdown (needs `options:`) |
| `email` | string | HTML `type="email"` |
| `url` | string | HTML `type="url"` |
| `color` | string `#aabbcc` | color picker |
| `date` | string ISO date | date picker |
| `datetime` | string ISO 8601 | datetime-local picker |
| `kvlist` | JSON array of objects | editable inline table (needs `columns:`) |
| `picker` | JSON array `[{id,name}]` | searchable typeahead chips (needs `source:` registered di [`LookupProvider`](../../agents/channels/slack/lookup.go)) |

Field type derivable: int/float field → widget auto = `number`. bool
field → widget auto = `checkbox`. Override pakai `widget:` explicit.

### Modifiers (mirror config-tags.md)

| Modifier | Effect | YAML key |
|---|---|---|
| Help text | shown below field | `desc:` |
| Default value | seed kalau ga di-set | `default:` |
| Required | block save kalau kosong, validation flag | `required: true` |
| Read-only | set once at boot | `locked: true` |
| Regenerate button | UI button regen (need registered generator) | `regen: true` |
| Hide from form | seeded, akses runtime, ga muncul di UI form | `hidden: true` |
| Conditional visibility | tampil cuma kalau field lain == value | `visible_when: <field>:<value>` |

### Key derivation

Field `name:` auto snake-case ke env key (sama config-tags rule):

| YAML name | env key | reference |
|---|---|---|
| `SLACK_CHANNEL` | `slack_channel` | `{{.Env.SLACK_CHANNEL}}` (case-insensitive) |
| `GitHubPAT` | `git_hub_pat` | `{{.Secret.GITHUB_PAT}}` |
| `APIBaseURL` | `api_base_url` | `{{.Env.APIBaseURL}}` |

Atau eksplisit: `key: legacy_api_key` override default.

### Values di kolom `workflows.env_values`

Satu JSON blob per workflow. Plain literal buat field biasa, `wick_enc_`
ciphertext buat `widget: secret`, JSON-string buat kvlist/picker.
Storage format identik dengan `configs.value` column — same parser bisa
read di Go.

```json
{
  "slack_channel": "#support-prod",
  "github_pat": "wick_enc_aGVsbG8gd29ybGQ=",
  "max_daily_runs": "500",
  "escalation_mode": "pager",
  "guard_prompt_extra": "Reject workflow yang notify ke channel #leadership tanpa approval explicit.",
  "enable_auto_triage": "true",
  "allowed_slack_channels": "[{\"id\":\"C123\",\"name\":\"#support\"},{\"id\":\"C456\",\"name\":\"#support-prod\"}]",
  "allowed_users": "[{\"id\":\"U100\",\"name\":\"Yoga\"}]",
  "github_webhook_url": "https://hooks.example.com/gh",
  "notify_email": "alerts@abc.com"
}
```

Field di luar schema (ada di `env_values` tapi ga di `env:` block) =
warning, ga di-render di form. Schema authoritative dari body.

### Reference dari node

```yaml
- type: channel
  channel: slack
  op: send_dm
  args:
    channel: "{{.Env.SLACK_CHANNEL}}"     # plain field
    text: "..."

- type: http
  method: POST
  url: https://api.github.com/...
  headers:
    Authorization: "Bearer {{.Secret.GITHUB_PAT}}"  # secret, auto-decrypt
```

`{{.Env.<NAME>}}` untuk semua field non-secret; `{{.Secret.<NAME>}}`
hanya untuk `widget: secret`. Engine reject mixing — secret ga bisa
di-render via `.Env.` (prevent accidental log leak). Resolusi di
[`env.ResolveSecrets`](../../agents/workflow/env/env.go), leak guard di
[`env.LeakGuard`](../../agents/workflow/env/env.go).

### UI form — tab Settings

Tab **Settings** di workflow editor Toolbar — sejajar **Editor** |
**Executions** (lihat [`EditorShell.svelte`](../../../fe/agents/workflow/src/lib/components/workflow/EditorShell.svelte)),
**bukan** di bottom panel (bottom = per-run debug output: Logs / JSON /
Validation / Guard / Tests / History). Env = config persisten
per-workflow, butuh full-screen surface sendiri.

**Reuse existing config-tags form renderer** — same widget components,
same auto-save behavior (800ms debounce), same secret reveal/regen
buttons, same kvlist Tab-add-row, same picker debounced lookup.

Save handler:
- `widget: secret` value → encrypt via existing `wick_enc_` helper
  ([`internal/enc`](../../enc/enc.go)) before write
- `widget: kvlist` / `picker` → JSON serialize before write
- Required field kosong → block save, show row error
- `visible_when` field tetep disimpan (just hidden from form)

Validation runtime saat workflow load
([`env.ValidateValues`](../../agents/workflow/env/env.go)):
- Schema diff (field added/removed di `env:` block) → migration prompt
- `required` field kosong → workflow ga jalan, surface "Missing config"
  di UI workflow list (badge merah)

### Storage flow

```
Schema:  workflow body env: block  →  body_draft / body_published
                                       (versioned, ikut snapshot)

Values:  Settings form / MCP        →  workflows.env_values (JSON blob)
                                       (current-only, NOT versioned)

Runtime: env_values + schema        →  env.ResolveSecrets
                                       →  {{.Env.X}} / {{.Secret.X}}
```

### File-era migration

Sebelum DB, values disimpan di file `<id>/env.json` (lihat git history
[`env.UnmarshalFile`](../../agents/workflow/env/env.go)). One-shot
migration saat boot: kalau file ada, baca → tulis kolom `env_values` →
hapus file. Idempotent — file absent setelah boot pertama.

### MCP ops

```
workflow_get_env_schema(id)
  → [{name, type, default, description, required}]   # dari body env: block

workflow_get_env_values(id, reveal_secrets=false)
  → {slack_channel: "#support", github_pat: "wick_enc_..."}
    reveal_secrets=true → require admin token

workflow_set_env_values(id, values)
  → write kolom env_values, secret auto-encrypt server-side
```

AI bisa edit env values lewat MCP — secret encrypt server-side, AI ga
pernah lihat plaintext.

### Secret rotation

UI tombol "Rotate" per secret field — generate new placeholder, mark
old as deprecated. Workflow runs read latest. Old runs di history tetap
audit-able dgn timestamp (ga decrypt-able lagi setelah rotation).

### Default workflows (zero-config)

Workflow tanpa `env:` field = ga butuh config. Tab Settings kosong
(atau tampilin "No config required for this workflow"). Build workflow
sederhana ga dipaksa setup env.

---
