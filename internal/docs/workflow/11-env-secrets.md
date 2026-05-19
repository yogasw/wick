## 11. Environment & secrets — workflow config

Workflow butuh config: Slack channel target, GitHub PAT, max retry,
toggle feature, dst. **Schema** declared di `workflow.yaml` (developer
contract, version-controlled). **Values** di file terpisah
`<id>/env.yaml` (UI-managed, secrets encrypted).

**Reuse vocabulary `wick:"..."` config-tag yang sudah ada** di
[docs/reference/config-tags.md](../../docs/reference/config-tags.md).
Same widget + modifier names, same form renderer, same UI behavior.
Beda cuma: untuk Go module schema di struct tag, untuk workflow schema
di YAML — keduanya consume rendering pipeline yang sama.

### Schema di `workflow.yaml`

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
| `secret` | string (encrypted on disk) | password input, "Reveal" button |
| `number` | int/float | number input |
| `checkbox` | bool | toggle |
| `dropdown` | string | select dropdown (needs `options:`) |
| `email` | string | HTML `type="email"` |
| `url` | string | HTML `type="url"` |
| `color` | string `#aabbcc` | color picker |
| `date` | string ISO date | date picker |
| `datetime` | string ISO 8601 | datetime-local picker |
| `kvlist` | JSON array of objects | editable inline table (needs `columns:`) |
| `picker` | JSON array `[{id,name}]` | searchable typeahead chips (needs `source:` registered di [`LookupProvider`](../agents/channels/slack/lookup.go)) |

Field type derivable: int/float field → widget auto = `number`. bool
field → widget auto = `checkbox`. Override pakai `widget:` explicit.

### Modifiers (mirror config-tags.md)

| Modifier | Effect | YAML key |
|---|---|---|
| Help text | shown below field | `desc:` |
| Default value | seed kalau ga di-set | `default:` |
| Required | block save kalau kosong, `c.Missing()` flag | `required: true` |
| Read-only | set once at boot | `locked: true` |
| Regenerate button | UI button regen (need registered generator) | `regen: true` |
| Hide from form | seeded di DB, akses via `c.Cfg()`, ga muncul di UI form | `hidden: true` |
| Conditional visibility | tampil cuma kalau field lain == value | `visible_when: <field>:<value>` |

### Key derivation

Field `name:` auto snake-case ke env key (sama config-tags rule):

| YAML name | env key | reference |
|---|---|---|
| `SLACK_CHANNEL` | `slack_channel` | `{{.Env.SLACK_CHANNEL}}` (case-insensitive) |
| `GitHubPAT` | `git_hub_pat` | `{{.Secret.GITHUB_PAT}}` |
| `APIBaseURL` | `api_base_url` | `{{.Env.APIBaseURL}}` |

Atau eksplisit: `key: legacy_api_key` override default.

### Values di `<id>/env.yaml`

```yaml
# env.yaml — UI-managed, hand-edit OK
# Schema authoritative dari workflow.yaml. Field di luar schema = warning.
SLACK_CHANNEL: "#support-prod"
GITHUB_PAT: wick_enc_aGVsbG8gd29ybGQ=    # encrypted, kelihatan di UI saat Reveal
MAX_DAILY_RUNS: 500
ESCALATION_MODE: pager
GUARD_PROMPT_EXTRA: |
  Reject workflow yang notify ke channel #leadership tanpa approval explicit.
ENABLE_AUTO_TRIAGE: true
ALLOWED_SLACK_CHANNELS:                  # kvlist value = JSON array
  - { id: "C123", name: "#support" }
  - { id: "C456", name: "#support-prod" }
ALLOWED_USERS:                           # picker value = same shape as kvlist=id|name
  - { id: "U100", name: "Yoga" }
GITHUB_WEBHOOK_URL: "https://hooks.example.com/gh"
NOTIFY_EMAIL: "alerts@abc.com"
```

Storage format identical dengan `configs.value` column di config-tags
pattern — JSON array of `{key: value}` objects buat kvlist/picker,
plain literal buat lainnya. Same parser bisa read di Go.

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
di-render via `.Env.` (prevent accidental log leak).

### UI form

Tab "Settings" di workflow editor — **reuse existing config-tags
form renderer**. Same widget components, same auto-save behavior
(800ms debounce after last keystroke), same secret reveal/regen
buttons, same kvlist Tab-add-row behavior, same picker debounced
lookup.

Save handler:
- `widget: secret` value → encrypt via existing `wick_enc_` helper
  before write
- `widget: kvlist` / `picker` → JSON serialize before write
- Required field kosong → block save, show row error
- `visible_when` field tetep seeded (just hidden from form)

Validation runtime saat workflow load:
- Schema diff (field added/removed di workflow.yaml) → migration prompt
- `required` field kosong di env.yaml → workflow ga jalan, surface
  "Missing config" di UI workflow list (badge merah)

### Hand-edit ↔ UI consistency

UI list page baca `env.yaml` saat render. fsnotify push update via SSE
buat editor yang lagi buka. Hand-edit nulis ciphertext langsung
(`wick_enc_...`) tetep valid — UI ga overwrite saat reveal/save kalau
value ga berubah.

### MCP ops

```
workflow_get_env_schema(id)
  → [{name, type, default, description, required}]

workflow_get_env_values(id, reveal_secrets=false)
  → {SLACK_CHANNEL: "#support", GITHUB_PAT: "wick_enc_..."}
    reveal_secrets=true → require admin token

workflow_set_env_values(id, values)
  → atomic write env.yaml, secret auto-encrypt
```

AI bisa edit env values lewat MCP — secret encrypt server-side, AI ga
pernah lihat plaintext.

### Secret rotation

UI tombol "Rotate" per secret field — generate new placeholder, mark
old as deprecated. Workflow runs read latest. Old runs di history tetap
audit-able dgn timestamp (ga decrypt-able lagi setelah rotation).

### Default workflows (zero-config)

Workflow tanpa `env:` field = ga butuh config. UI Settings tab kosong
(atau tampilin "No config required for this workflow"). Build workflow
sederhana ga dipaksa setup env.

---

