# 23 — MCP AI Guide: Building Workflows via MCP

Panduan praktis untuk AI (Claude, GPT, Gemini) yang membangun workflow lewat MCP
tanpa akses file. Berisi kontrak eksak, gotcha, dan template siap pakai.

---

## TODO

- [ ] Tambah contoh `open_modal` dengan `trigger_id` dari `block_action`
- [ ] Dokumentasikan field `Event.Payload` per event type (message, block_action, dll)
- [ ] Expose `schema` field di `workflow_node_types` / `workflow_trigger_types` response
- [ ] Expose `workflow_channels` dengan full action input/output schema (saat ini return `[]`)
- [ ] Tambah `workflow_integration` yang return non-empty (saat ini return `{}`)

---

## 1. Golden Rule: Param Keys

MCP `params` key = **snake_case dari struct field name**. Contoh:

| Go struct field | MCP param key |
|---|---|
| `ID` | `id` |
| `Triggers` | `triggers` |
| `NodeID` | `node_id` |
| `FromID` | `from_id` |

Trigger / Node JSON body pakai **Go JSON default** (exact field name, PascalCase) karena
di-`json.Unmarshal` langsung. Contoh:

```json
// workflow_set_triggers → params.triggers (string JSON)
[{"Type":"channel","ChannelName":"slack","Event":"message","Target":"C0ABC","MatchEnabled":true,"Match":{"channel_id":["C0ABC"]}}]
```

---

## 2. Event struct

Engine inject `workflow.Event` ke setiap run. Field yang tersedia di template:

```
{{.Event.Type}}       — "channel" | "manual" | "cron" | "webhook"
{{.Event.Subtype}}    — subtype string (jarang dipakai)
{{.Event.Channel}}    — channel ID (Slack: "C0ASUHYCRNU")
{{.Event.At}}         — time.Time
{{.Event.Payload}}    — map[string]any — semua field event asli ada di sini
```

**Penting:** `Event.Thread`, `Event.User`, `Event.Text` TIDAK ADA. Semua ada di
`{{.Event.Payload}}`. Akses via:

```
{{index .Event.Payload "ts"}}          — timestamp pesan (Slack message)
{{index .Event.Payload "user"}}        — user ID
{{index .Event.Payload "text"}}        — teks pesan
{{index .Event.Payload "trigger_id"}} — trigger_id (dari block_action, untuk open_modal)
{{index .Event.Payload "action_id"}}   — action yang diklik
{{index .Event.Payload "value"}}       — value button yang diklik
```

---

## 3. Template RenderCtx

Semua node args / expression / prompt_file di-render dengan:

```
{{.Event}}        — workflow.Event (lihat §2)
{{.Node.<id>}}    — output node upstream (lihat §4)
{{.Env}}          — map[string]string env vars
{{.Secret}}       — map[string]string secrets
{{.Workflow.ID}}  — workflow ID
{{.Run.ID}}       — run ID
```

**Gotcha:** node ID dengan dash (`-`) tidak bisa diakses via `{{.Node.my-node}}` —
Go template parser reject `-`. Pakai underscore atau camelCase: `mynode`, `my_node`.

---

## 4. Node Output Fields

### transform (gotemplate)
```
{{.Node.<id>.result}}   — string hasil render expression
```

### agent
```
{{.Node.<id>.text}}     — last assistant message (string)
```

### channel / send_message
```
{{.Node.<id>.ts}}       — posted message timestamp
{{.Node.<id>.channel}}  — channel ID
```

### channel / open_modal
```
{{.Node.<id>.ok}}       — bool
```

---

## 5. Channel Node: Slack Actions

### send_message

```yaml
- id: sendmsg
  type: channel
  channel: slack
  op: send_message
  args:
    channel: '{{.Event.Channel}}'
    thread_ts: '{{index .Event.Payload "ts"}}'
    text: 'Fallback text (wajib jika blocks kosong)'
    blocks: '<JSON string Block Kit>'   # harus string, bukan object
```

**Gotcha `blocks`:** harus berupa **JSON string**, bukan YAML object. Gunakan node
`transform` untuk build string JSON dulu, lalu `{{.Node.<id>.result}}`.

**Gotcha template dalam blocks string:** tidak bisa escape quote dalam YAML string.
Solusi: build blocks di `transform` node dengan expression yang embed
`{{index .Event.Payload "ts"}}` langsung — tidak perlu escape karena expression
ada di field `expression`, bukan di string bersarang.

Contoh transform → send_message:

```yaml
- id: buildblocks
  type: transform
  engine: gotemplate
  expression: '[{"type":"actions","elements":[{"type":"button","text":{"type":"plain_text","text":"Create Tiket"},"action_id":"create_tiket","value":"{{index .Event.Payload "ts"}}"}]}]'

- id: sendbutton
  type: channel
  channel: slack
  op: send_message
  args:
    channel: '{{.Event.Channel}}'
    thread_ts: '{{index .Event.Payload "ts"}}'
    text: 'Ada pesan baru.'
    blocks: '{{.Node.buildblocks.result}}'
```

### open_modal

```yaml
- id: openmodal
  type: channel
  channel: slack
  op: open_modal
  args:
    trigger_id: '{{index .Event.Payload "trigger_id"}}'
    view:
      type: modal
      title:
        type: plain_text
        text: Judul Modal
      close:
        type: plain_text
        text: Batal
      blocks:
        - type: section
          text:
            type: mrkdwn
            text: '{{.Node.summarize.text}}'
```

**Penting:** `trigger_id` hanya tersedia dari event `block_action`. Harus dipakai
dalam 3 detik setelah event diterima (batas Slack).

---

## 6. Trigger: 1 Workflow 2 Trigger

Engine support `entry_node` per trigger — satu workflow bisa punya 2 jalur masuk:

```yaml
triggers:
  - type: channel
    channel: slack
    event: message
    target: C0ASUHYCRNU
    entry_node: sendbutton        # masuk ke node ini
    match:
      channel_id: ["C0ASUHYCRNU"]
    match_enabled: true

  - type: channel
    channel: slack
    event: block_action
    target: C0ASUHYCRNU
    entry_node: summarize         # masuk ke node ini
    match:
      action_id: ["create_tiket"]
      channel_id: ["C0ASUHYCRNU"]
    match_enabled: true
```

**Graph tidak perlu connect kedua jalur.** Engine resolve entry dari `trigger.entry_node`
langsung — bukan dari `graph.entry`. Dua sub-graph terpisah dalam satu workflow.yaml valid.

**Gotcha `graph.entry`:** tetap wajib diisi salah satu node (untuk fallback manual trigger).
Isi dengan entry node trigger pertama.

---

## 7. Trigger Match Filter

```yaml
match:
  channel_id: ["C0ASUHYCRNU"]     # whitelist channel
  action_id: ["create_tiket"]     # whitelist action (block_action)
  text_contains: "bug"            # substring match pada message text
match_enabled: true               # WAJIB true, default false = no filter
```

---

## 8. Trigger JSON Body (untuk workflow_set_triggers)

Field name = Go struct field (PascalCase, no json tag):

```json
[
  {
    "Type": "channel",
    "ChannelName": "slack",
    "Event": "message",
    "Target": "C0ASUHYCRNU",
    "EntryNode": "buildblocks",
    "Match": {"channel_id": ["C0ASUHYCRNU"]},
    "MatchEnabled": true,
    "DedupTTLSec": 30
  },
  {
    "Type": "channel",
    "ChannelName": "slack",
    "Event": "block_action",
    "Target": "C0ASUHYCRNU",
    "EntryNode": "summarize",
    "Match": {"action_id": ["create_tiket"], "channel_id": ["C0ASUHYCRNU"]},
    "MatchEnabled": true
  }
]
```

---

## 9. Agent Node

```yaml
- id: summarize
  type: agent
  provider: claude
  prompt_file: nodes/summarize.md
```

`prompt_file` di-render sebagai Go template dengan RenderCtx — bisa embed
`{{index .Event.Payload "value"}}` dll. Output: `{{.Node.summarize.text}}`.

**Gotcha simulate:** agent node gagal di `workflow_simulate` jika provider tidak
di-wire ke stdio MCP (`"provider not registered"`). Normal — hanya gagal di simulate,
runtime HTTP server punya provider penuh.

---

## 10. Workflow Build Checklist

```
1. workflow_check_name          — cek nama belum dipakai
2. workflow_create              — scaffold
3. workflow_write_file          — tulis workflow.yaml lengkap
4. workflow_write_file          — tulis nodes/*.md untuk agent
5. workflow_validate            — cek errors/warnings
6. workflow_publish             — promote draft → live
7. workflow_simulate            — dry-run (skip agent node di stdio mode)
8. workflow_list                — verifikasi muncul di list
```

---

## 11. Known Limitations (MCP / Simulate)

| Limitasi | Keterangan |
|---|---|
| `workflow_channels` return `[]` | Slack actions tidak expose schema via MCP |
| `workflow_integration` return `{}` | Integration registry tidak ter-expose di stdio mode |
| `workflow_node_types` schema null | Schema field belum di-populate |
| Agent node gagal di simulate | Provider tidak di-wire ke stdio MCP |
| `blocks` decode error di simulate | `slackgo.Block` interface, tidak bisa unmarshal di simulate |
| Node ID dengan `-` | Go template reject, pakai `_` atau camelCase |
| `.Event.Thread` / `.Event.User` tidak ada | Semua ada di `{{index .Event.Payload "..."}}` |
