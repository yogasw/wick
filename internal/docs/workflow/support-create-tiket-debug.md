---

Debug Report: support create tiket

Workflow ID: 24d57030-94f2-420c-915b-2327774345e0

Tanggal: 2026-05-18

Status: Draft saved, NOT published — bugs still open

---

BUGS YANG HARUS DIFIX SEBELUM PUBLISH

BUG KRITIS: submit-ticket kehilangan headers + body — lihat Section 3
Verify match filter blockaction — field actionid tidak ada di match_schema registry
Verify match filter viewsubmission — field callbackid tidak ada di match_schema registry
Verify template path block_action event — {{.Event.TriggerId}}, {{.Event.Message.Text}} belum dikonfirmasi ke wick event schema
Verify template path view_submission event — {{.Event.View.State.Values.*}} belum dikonfirmasi
Publish draft — workflow_simulate masih baca versi lama karena draft belum publish
---

SECTION 1: CARA BERPIKIR WAKTU BUILD

Kenapa 3 trigger terpisah

Slack interactive flow butuh 3 event type berbeda:

message → bot kirim pesan dengan tombol
blockaction → Slack kirim event ketika tombol diklik (triggerid expires 3 detik!)
view_submission → Slack kirim event ketika modal di-submit
Ketiganya tidak bisa disatukan dalam 1 trigger karena event type-nya beda. Setiap trigger punya entry_node sendiri, jadi ada 3 independent flow dalam 1 graph.

Kenapa openmodal harus langsung setelah blockaction tanpa node di antaranya

Slack triggerid expires 3 detik setelah button diklik. Kalau ada LLM call atau processing berat sebelum openmodal, modal gagal dibuka dengan error "expiredtriggerid". Makanya Flow B langsung open-ticket-modal sebagai entrynode dari trigger blockaction, tanpa node tambahan di antaranya.

Kenapa pakai private_metadata di modal

Waktu viewsubmission tiba, event-nya hanya berisi data form dari modal + user yang submit. Context asli seperti channel ID dan threadts tidak ikut terbawa. Solusi: simpan context di field privatemetadata waktu openmodal, lalu baca lagi waktu submit.

Format yang dipakai: {{ .Event.Channel }}|{{ .Event.Message.Ts }}|{{ .Event.User }}

Kenapa body HTTP node harus string, bukan YAML map

YAML schema untuk http node di wick:

headers → map[string]string (harus YAML mapping)
body → string (harus JSON string, bukan YAML map)
Ini tidak konsisten antara headers dan body. Kalau body ditulis sebagai YAML map, parser throw: "cannot unmarshal !!map into string". Kalau headers ditulis sebagai JSON string, parser throw: "cannot unmarshal !!str into map[string]string".

---

SECTION 2: SEMUA TOOL CALLS + PAYLOAD + RESPONSE LENGKAP

CALL 1: wick_search

INPUT:

{ "query": "workflow" }
RESPONSE:

{
  "connectors": [{
    "id": "94c9f2b0-0e43-4c8d-b594-f98f568a09b9",
    "connector": "Workflow",
    "status": "ready",
    "tools": 42 tools total — workflow_workspace, workflow_node_types,
      workflow_trigger_types, workflow_channels, workflow_integration,
      workflow_connectors, workflow_skills, workflow_providers, workflow_list,
      workflow_check_name, workflow_get, workflow_list_files, workflow_read_file,
      workflow_create, workflow_write_file, workflow_add_node, workflow_update_node,
      workflow_connect, workflow_disconnect, workflow_move_node, workflow_set_triggers,
      workflow_toggle, workflow_publish, workflow_discard_draft, workflow_has_draft,
      workflow_validate, workflow_simulate, workflow_test, workflow_test_coverage,
      workflow_record_test, workflow_capture_fixture, workflow_run_now,
      workflow_get_runs, workflow_get_run, workflow_get_run_events, workflow_get_run_log,
      workflow_copy_run_to_editor, workflow_replay_run, workflow_list_test_cases,
      workflow_save_test_case, workflow_delete_test_case, workflow_request_review
  }]
}
---

CALL 2: workflow_workspace

INPUT:

{}
RESPONSE:

{
  "base_dir": "C:\\Users\\Staffinc\\.wick-lab\\agents\\workflows",
  "node_types": [
    "classify", "agent", "channel", "connector", "shell", "http",
    "db_query", "transform", "branch", "parallel", "merge", "end",
    "dataset_get", "dataset_exists", "dataset_query", "dataset_count",
    "dataset_insert", "dataset_upsert", "dataset_delete"
  ],
  "templates": ["empty", "support-triage", "incident-response", "daily-digest"],
  "trigger_types": ["cron", "channel", "webhook", "manual", "schedule_at", "error"]
}
---

CALL 3: workflow_integration — KUNCI UNTUK DEBUG FILTER MATCH

INPUT:

{}
RESPONSE (bagian relevan):

{
  "events": [
    {
      "channel": "slack",
      "event": "message",
      "name": "Slack: New message",
      "match_schema": [
        {
          "Key": "mode",
          "Type": "dropdown",
          "Options": "all|whitelist",
          "Description": "Filter mode: all=fire every message"
        },
        {
          "Key": "channel_id",
          "Type": "picker",
          "Options": "slack.channels",
          "Description": "Only fire from these channels"
        },
        {
          "Key": "user",
          "Type": "picker",
          "Options": "slack.users",
          "Description": "Only from these users"
        },
        {
          "Key": "text_contains",
          "Type": "text",
          "Description": "Case-insensitive substring filter (optional)"
        }
      ]
    },
    {
      "channel": "slack",
      "event": "block_action",
      "name": "Slack: Block action (button/menu)",
      "description": "Fires when user clicks button. Use action_id or callback_id to route. trigger_id expires in 3s.",
      "match_schema": null
    },
    {
      "channel": "slack",
      "event": "view_submission",
      "name": "Slack: Modal submitted",
      "description": "Fires when user clicks Submit on modal. Match by callback_id to route different modal forms.",
      "match_schema": null
    }
  ],
  "actions": [
    {
      "action": "send_message",
      "input_schema": {
        "channel":   "string (required) — Channel ID, DM, or @user",
        "text":      "string — fallback / accessibility",
        "blocks":    "string — Block Kit JSON array, overrides text",
        "thread_ts": "string — post inside this thread",
        "signed":    "boolean — append Sent by wick footer"
      }
    },
    {
      "action": "open_modal",
      "input_schema": {
        "trigger_id": "string (required) — expires 3 seconds",
        "view":       "string (required) — Block Kit modal JSON"
      }
    }
  ]
}
MASALAH KRITIS DI SINI:

message event punya matchschema → field channelid CONFIRMED support
blockaction matchschema = null → field action_id yang dipakai di trigger match TIDAK ADA di registry
viewsubmission matchschema = null → field callback_id yang dipakai TIDAK ADA di registry
Deskripsi blockaction bilang "use actionid or callbackid to route" tapi matchschema null — artinya mungkin engine support tapi tidak expose di registry, atau harus dilakukan via branch node manual
---

CALL 4: wick_get (full connector schema)

INPUT:

{ "id": "94c9f2b0-0e43-4c8d-b594-f98f568a09b9" }
RESPONSE (input_schema per tool yang relevan):

workflow_write_file:
  - id: string (required) — Workflow ID
  - path: string (required) — relative path, contoh: workflow.yaml
  - content: string (required) — full file content, full replace bukan patch

workflow_validate:
  - id: string (required)

workflow_simulate:
  - id: string (required)
  - event: string (required) — synthetic event as JSON

workflow_publish:
  - id: string (required)
  - enable: boolean — default true, set false untuk keep disabled

workflow_set_triggers:
  - id: string (required)
  - triggers: string (required) — JSON array

workflow_update_node:
  - id: string (required)
  - node_id: string (required)
  - patch: string (required) — JSON object, merge patch, existing fields not present = unchanged
---

CALL 5: workflowcheckname

INPUT:

{ "name": "support create tiket" }
RESPONSE:

{ "available": true, "conflict_id": "" }
---

CALL 6: workflow_create

INPUT:

{ "name": "support create tiket", "template": "empty" }
RESPONSE:

{
  "enabled": true,
  "id": "24d57030-94f2-420c-915b-2327774345e0",
  "name": "support create tiket",
  "published": true
}
---

CALL 7: workflow_get (lihat state awal setelah create)

INPUT:

{ "id": "24d57030-94f2-420c-915b-2327774345e0" }
RESPONSE:

{
  "ID": "24d57030-94f2-420c-915b-2327774345e0",
  "Name": "support create tiket",
  "Enabled": true,
  "Triggers": [{
    "Type": "manual",
    "EntryNode": "start",
    "Label": "Run"
  }],
  "Graph": {
    "Entry": "start",
    "Nodes": [{
      "ID": "start",
      "Type": "end",
      "Result": "ok"
    }],
    "Edges": []
  }
}
Ini graph kosong dari empty template — 1 node "start" dengan type end.

---

CALL 8–12: workflowwritefile — 5 kali percobaan

PERCOBAAN 1 — GAGAL:

ERROR: yaml: unmarshal errors:
  line 2:  cannot unmarshal !!str `1` into int
  line 35: cannot unmarshal !!map into []workflow.Node
Penyebab: version: "1" harus int bukan string. nodes: pakai format map (send-button-message:) harus array (- id: send-button-message).

PERCOBAAN 2 — GAGAL:

ERROR: yaml: line 35: cannot unmarshal !!map into []workflow.Node
Penyebab: nodes masih map format, belum semua difix.

PERCOBAAN 3 — GAGAL:

ERROR: yaml: line 160: cannot unmarshal !!map into string
Penyebab: body: ditulis sebagai YAML map (key-value), harus string.

PERCOBAAN 4 — GAGAL:

ERROR: yaml: line 157: cannot unmarshal !!str `{"Conte...` into map[string]string
Penyebab: headers: ditulis sebagai JSON string, harus YAML map.

PERCOBAAN 5 — SUKSES:

INPUT (content diringkas):

{
  "id": "24d57030-94f2-420c-915b-2327774345e0",
  "path": "workflow.yaml",
  "content": "name: support create tiket\nversion: 1\nenabled: false\n\ntriggers:\n  - type: channel\n    channel: slack\n    event: message\n    match:\n      channel_id: C0ASUHYCRNU\n    entry_node: send-button-message\n  - type: channel\n    channel: slack\n    event: block_action\n    match:\n      action_id: create_ticket_btn\n    entry_node: open-ticket-modal\n  - type: channel\n    channel: slack\n    event: view_submission\n    match:\n      callback_id: create_ticket_modal\n    entry_node: submit-ticket\n\ngraph:\n  entry: send-button-message\n  nodes:\n    - id: send-button-message\n      type: channel\n      channel: slack\n      op: send_message\n      args:\n        channel: '{{.Event.Channel}}'\n        thread_ts: '{{.Event.Ts}}'\n        text: Ada pesan baru...\n        blocks: |\n          [...block kit json...]\n    - id: end-a\n      type: end\n      result: button message posted\n    - id: open-ticket-modal\n      type: channel\n      channel: slack\n      op: open_modal\n      args:\n        trigger_id: '{{.Event.TriggerId}}'\n        view: |\n          {...modal json...}\n    - id: end-b\n      type: end\n      result: modal opened\n    - id: submit-ticket\n      type: http\n      method: POST\n      url: https://webhook.example.com/ticket\n      headers:\n        Content-Type: application/json\n      body: |\n        {...json body with templates...}\n    - id: end-c\n      type: end\n      result: ticket submitted\n  edges:\n    - from: send-button-message\n      to: end-a\n    - from: open-ticket-modal\n      to: end-b\n    - from: submit-ticket\n      to: end-c"
}
RESPONSE:

{ "draft": true, "message": "Saved to draft. Call workflow_publish to make it live.", "ok": true }
---

CALL 13: workflow_validate

INPUT:

{ "id": "24d57030-94f2-420c-915b-2327774345e0" }
RESPONSE:

{ "ok": true }
Tidak ada errors, tidak ada warnings.

---

CALL 14: workflow_simulate — HASILNYA MISLEADING

INPUT:

{
  "id": "24d57030-94f2-420c-915b-2327774345e0",
  "event": "{\"Type\":\"channel\",\"Channel\":\"C0ASUHYCRNU\",\"Text\":\"bug di fitur login\",\"Ts\":\"1716000000.123456\",\"User\":\"U0123456\"}"
}
RESPONSE:

{
  "run_id": "983ba546-f81a-4e1d-bd45-120655df48d5",
  "status": "success",
  "entry": "start",
  "completed": ["start"],
  "outputs": { "start": { "result": "ok" } }
}
MASALAH: entry adalah "start" bukan "send-button-message". completed hanya ["start"]. Ini graph lama dari empty template, bukan graph yang baru ditulis. Root cause: workflow_simulate baca versi published, bukan draft. Draft belum di-publish saat simulate dijalankan. Harusnya publish dulu sebelum simulate.

---

SECTION 3: BUG KRITIS — submit-ticket kehilangan body + headers

Yang ditulis ke YAML waktu percobaan 5:

- id: submit-ticket
  type: http
  method: POST
  url: "https://webhook.example.com/ticket"
  headers:
    Content-Type: application/json
  body: |
    {
      "user_id": "{{.Event.User.Id}}",
      "title": "{{.Event.View.State.Values.title_block.ticket_title.Value | js}}",
      "description": "{{.Event.View.State.Values.description_block.ticket_description.Value | js}}",
      "priority": "{{.Event.View.State.Values.priority_block.ticket_priority.SelectedOption.Value}}",
      "metadata": "{{.Event.View.PrivateMetadata}}",
      "submitted_at": "{{now | date \"2006-01-02T15:04:05Z07:00\"}}"
    }
Yang muncul di dashboard (dari YAML yang user paste):

- id: submit-ticket
  type: http
  method: POST
  url: https://webhook.example.com/ticket
  (headers HILANG)
  (body HILANG)
Kemungkinan penyebab:

Field name salah — wick http node mungkin pakai nama field lain, bukan "headers" atau "body". Perlu cek workflownodetypes untuk http node detail schema.
YAML literal block dengan Go template di dalamnya di-strip parser — body: | diikuti JSON yang berisi {{...}} mungkin tidak di-handle dengan benar oleh wick YAML parser.
Escaped quote corrupt di MCP transport — template {{now | date \"2006-01-02T15:04:05Z07:00\"}} punya backslash escape. Waktu dikirim via MCP sebagai JSON string yang sudah di-escape, bisa menyebabkan silent parse failure untuk seluruh node.
Cara fix: Jalankan workflownodetypes tanpa filter, cari schema untuk http node secara lengkap. Lihat apakah ada field bodytemplate, bodyraw, atau field lain. Lalu gunakan workflowupdatenode dengan patch berisi field yang benar.

---

SECTION 4: TEMPLATE PATHS YANG PERLU DIVERIFIKASI

Flow A — message event — kemungkinan besar benar karena ada di match_schema docs:

{{.Event.Channel}} → channel ID
{{.Event.Ts}} → message timestamp
{{.Event.User}} → user ID
{{.Event.Text}} → message text
Flow B — block_action event — BELUM VERIFIED terhadap wick event schema:

{{.Event.TriggerId}} → trigger_id dari Slack payload. Bisa jadi .Event.TriggerID atau .Event.Trigger.Id
{{.Event.Channel}} → channel.id. Bisa jadi flat atau nested
{{.Event.Message.Ts}} → message.ts. Bisa jadi .Event.MessageTs (flat) atau .Event.Message.Ts (nested)
{{.Event.Message.Text}} → message.text. Sama, bisa flat atau nested
{{.Event.User}} → user.id dari block_action payload. Di Slack API ini user.id bukan hanya user
Flow C — view_submission event — BELUM VERIFIED, risiko tinggi salah:

{{.Event.User.Id}} → bisa jadi .Event.UserId (flat)
{{.Event.View.State.Values.titleblock.tickettitle.Value}} → deep path, sangat tergantung bagaimana wick map Slack view.state.values ke Event struct
{{.Event.View.State.Values.priorityblock.ticketpriority.SelectedOption.Value}} → untuk dropdown
{{.Event.View.PrivateMetadata}} → bisa jadi .Event.View.PrivateMetadata atau .Event.PrivateMetadata
---

SECTION 5: CARA DEBUG STEP BY STEP

Step 1 — Fix submit-ticket, cek exact field name dulu:

Jalankan workflownodetypes dan cari schema http node. Kalau field name beda, gunakan workflowupdatenode dengan patch JSON.

Step 2 — Verifikasi match filter blockaction dan viewsubmission:

Setelah publish, fire real Slack event dengan klik button. Kalau trigger blockaction tidak fire, berarti match.actionid tidak support. Fix: hapus match dari trigger block_action dan tambah branch node setelah open-ticket-modal untuk filter berdasarkan {{.Event.ActionId}}.

Step 3 — Publish draft dalam kondisi disabled:

workflow_publish dengan enable: false supaya triggers terdaftar tapi tidak aktif dulu.

Step 4 — Fire real event Flow A lalu inspect raw payload:

Kirim pesan ke channel C0ASUHYCRNU. Jalankan workflowgetruns untuk dapat runid. Jalankan workflowgetrunevents dengan run_id tersebut. Output tampilkan raw Slack event JSON yang diterima wick — gunakan field names dari sana untuk verifikasi semua template path.

Step 5 — Test Flow B dan C dengan cara sama:

Klik button, inspect run events untuk blockaction payload. Submit modal, inspect run events untuk viewsubmission payload.

---

SECTION 6: RINGKASAN SCHEMA WICK YANG DITEMUKAN SESI INI

version field: harus int, BUKAN string. Kalau version: "1" → parser error.

graph.nodes: harus array dengan format "- id: nama-node". BUKAN map dengan format "nama-node:".

headers di http node: harus YAML map (key: value format). BUKAN JSON string.

body di http node: harus string. YAML literal block (|) atau quoted string. BUKAN YAML map. Tapi ada bug — body yang ditulis dengan literal block hilang di dashboard, belum diketahui penyebab pastinya.

blocks di send_message args: harus string berisi JSON array.

view di open_modal args: harus string berisi JSON object.

trigger.entry_node: wajib ada di setiap trigger. Kalau tidak ada, trigger tidak connect ke graph.

match.channelid: CONFIRMED support — ada di matchschema registry untuk message event.

match.actionid untuk blockaction: TIDAK VERIFIED — tidak ada di matchschema registry. Deskripsi event bilang "use actionid to route" tapi tidak ada di match_schema.

match.callbackid untuk viewsubmission: TIDAK VERIFIED — sama seperti action_id, tidak ada di registry.

workflowsimulate baca published version bukan draft: harus publish dulu sebelum simulate berguna. Kalau simulate langsung setelah writefile, akan jalankan graph lama.

workflow_validate dengan ok: true tidak berarti template paths benar: validate hanya cek cycle detect + schema check + guard dry-run. Tidak verify apakah {{.Event.Message.Text}} benar-benar ada di event payload.

---
---

SECTION 7: SEMUA EXPRESSION TIAP INPUT — TAHU ATAU TIDAK + KENAPA DIPILIH

---

FLOW A — send-button-message (trigger: message event)

args.channel = "{{.Event.Channel}}"

Status: TAHU — confident

Kenapa: Field Channel ada di matchschema message event di registry wick. Docs workflowintegration tulis "channel_id" sebagai filter key, dan di simulate event shape docs tertulis "Channel":"C123". Konsisten.

Alternatif yang tidak dipakai: tidak ada.

args.thread_ts = "{{.Event.Ts}}"

Status: TAHU — confident

Kenapa: Di simulate event shape docs tertulis "Ts":"1234567.890" untuk message event. Slack message timestamp memang field Ts. Dipakai sebagai thread_ts supaya reply masuk di thread pesan asli bukan channel.

Alternatif yang tidak dipakai: tidak ada.

args.text = string biasa

Status: TAHU — ini fallback plain text, tidak ada template.

Kenapa: Slack pakai text sebagai fallback kalau blocks gagal render (notifikasi push, screen reader). Tidak perlu dynamic.

args.blocks — {{.Event.Text | js}}

Status: TAHU bahwa js filter ada di sprig/Go template. TIDAK TAHU apakah wick expose js filter.

Kenapa pakai js: Text dari user bisa mengandung karakter yang break JSON string — kutip, backslash, newline. js filter escape karakter-karakter itu untuk safe embed di dalam JSON string. Kalau tidak pakai js, ada risiko block JSON invalid kalau user kirim pesan dengan tanda kutip.

Risiko: Kalau wick tidak support js filter, template akan error saat runtime. Filter alternatif yang mungkin support: html, urlquery. Tapi html escape & menjadi &amp; yang salah untuk JSON. Tidak ada filter 100% safe untuk JSON di luar js.

args.blocks — {{.Event.User}} dan {{.Event.Channel}} di dalam blocks JSON

Status: TAHU untuk channel. TIDAK TAHU PASTI untuk user.

Kenapa: Channel confident dari match_schema. User diasumsikan flat string ID seperti "U0123456" berdasarkan simulate event shape docs. Kalau User adalah struct (bukan string), template akan render "{...}" bukan "U0123456" dan format Slack "<@{...}>" akan salah.

args.blocks — button value = "{{.Event.Ts}}"

Status: TAHU

Kenapa: Simpan message Ts di value button supaya waktu blockaction event tiba, bisa tahu thread mana yang diklik. Meski blockaction event juga bawa message context, value ini jadi backup. Format string langsung karena Slack button value harus string.

---

FLOW B — open-ticket-modal (trigger: block_action event)

args.trigger_id = "{{.Event.TriggerId}}"

Status: TIDAK TAHU PASTI — ini asumsi

Kenapa pakai TriggerId: Slack blockaction payload punya field "triggerid" di top level. Wick biasanya PascalCase field names di Event struct. Jadi "trigger_id" → "TriggerId". Tapi belum diverifikasi dari real run log.

Alternatif yang mungkin benar: {{.Event.TriggerID}} (ID kapital semua), {{.Event.Trigger.Id}} (nested), {{.Event.Raw.trigger_id}} kalau wick expose raw payload.

Kenapa tidak pakai alternatif: Tidak ada docs yang confirm naming convention wick untuk block_action event fields. Pilih TriggerId karena paling standar PascalCase.

Risiko: Kalau salah, triggerid empty string → Slack return error "invalidtrigger_id" dan modal tidak buka.

view.private_metadata = "{{.Event.Channel}}|{{.Event.Message.Ts}}|{{.Event.User}}"

Status: TIDAK TAHU PASTI untuk .Event.Message.Ts dan .Event.Message.Text

Kenapa pakai nested .Event.Message: Slack block_action payload struktur-nya nested — ada object "message" yang berisi "ts" dan "text". Wick mungkin map ini ke Event.Message.Ts dan Event.Message.Text. Tapi mungkin juga flat seperti Event.MessageTs.

Kenapa simpan di privatemetadata: viewsubmission event tidak bawa balik channel atau thread context. Satu-satunya cara pass context dari Flow B ke Flow C adalah lewat private_metadata.

Alternatif yang tidak dipakai: Simpan di dataset wick (datasetinsert di Flow B, datasetget di Flow C). Tidak dipakai karena lebih kompleks dan butuh primary key setup. Private_metadata lebih sederhana untuk case ini.

view blocks — {{.Event.Message.Text | js}}

Status: TIDAK TAHU PASTI sama seperti di atas

Kenapa: Untuk pre-fill summary di modal header section supaya user lihat context thread asli. js filter sama alasannya seperti Flow A.

view input initial_value — {{.Event.Message.Text | trunc 100 | js}}

Status: TIDAK TAHU PASTI untuk trunc filter di wick

Kenapa pakai trunc: Title field idealnya singkat, max 100 karakter. trunc adalah sprig filter standard. Tapi belum dikonfirmasi wick include sprig.

Kenapa angka 100: Batas UI yang wajar untuk ticket title. Slack modal input tidak ada built-in maxLength untuk initial_value, jadi dibatasi manual.

Alternatif yang tidak dipakai: Tidak pakai trunc dan biarkan user edit manual. Dipilih trunc karena UX lebih baik kalau field sudah pre-fill reasonable.

Risiko: Kalau trunc tidak support, template error. Bisa dihapus saja kalau masalah.

view dropdown options — hardcoded high/medium/low

Status: TAHU — ini static, tidak ada template

Kenapa: Priority options memang seharusnya fixed enum, tidak dynamic dari event.

view context — {{.Event.Channel}} dan {{.Event.Message.Ts}}

Status: TIDAK TAHU PASTI untuk Message.Ts seperti sudah dijelaskan

---

FLOW C — submit-ticket (trigger: view_submission event)

body.user_id = "{{.Event.User.Id}}"

Status: TIDAK TAHU — ini asumsi nested

Kenapa pakai .Event.User.Id: Slack view_submission payload punya "user": {"id": "U...", "name": "..."}. Diasumsikan wick map ini ke nested struct Event.User.Id. Tapi bisa jadi flat Event.UserId.

Alternatif: {{.Event.UserId}}, {{.Event.User}}, {{.Event.UserID}}

Kenapa tidak pakai alternatif: Tidak ada docs. Pilih .User.Id karena paling deskriptif.

Risiko tinggi: Kalau salah, user_id di payload webhook kosong atau error.

body.title = "{{.Event.View.State.Values.titleblock.tickettitle.Value}}"

Status: TIDAK TAHU — sangat spekulatif

Kenapa path ini: Slack view.state.values struktur-nya: view → state → values → {blockid} → {actionid} → {type-specific fields}. Untuk plaintextinput, field value-nya adalah "value". Jadi full path di Slack API: view.state.values.titleblock.tickettitle.value. Di wick PascalCase → View.State.Values.titleblock (blockid tetap lowercase karena itu user-defined string).tickettitle (actionid juga user-defined).Value (PascalCase karena ini wick struct field).

Risiko sangat tinggi: Ini path paling dalam dan paling spekulatif. Mungkin wick tidak nested sedalam ini dan expose values sebagai flat map atau raw JSON string.

Alternatif yang mungkin: {{.Event.Values.titleblock.tickettitle.value}}, {{.Event.Raw.view.state.values.titleblock.tickettitle.value}}, {{index .Event.View.State.Values "titleblock" "tickettitle" "value"}}

body.priority = "{{.Event.View.State.Values.priorityblock.ticketpriority.SelectedOption.Value}}"

Status: TIDAK TAHU — paling spekulatif dari semua expression

Kenapa: Slack staticselect mengembalikan selectedoption object yang berisi text dan value. Jadi harus akses .SelectedOption.Value bukan langsung .Value. Tapi wick mungkin flatten ini.

Alternatif: {{.Event.View.State.Values.priorityblock.ticketpriority.Value}}, mungkin wick sudah flatten selected_option.value ke .Value langsung.

body.metadata = "{{.Event.View.PrivateMetadata}}"

Status: SEDANG — lebih confident dari fields lain

Kenapa: private_metadata di Slack view payload adalah top-level field di view object, bukan nested. Wick path yang masuk akal: Event.View.PrivateMetadata.

Alternatif: {{.Event.PrivateMetadata}} kalau wick flatten view fields ke Event langsung.

body.submitted_at = "{{now | date \"2006-01-02T15:04:05Z07:00\"}}"

Status: TIDAK TAHU apakah now dan date function available di wick

Kenapa pakai now: Standard sprig function untuk current time. date adalah sprig format function dengan Go time format string.

Kenapa format "2006-01-02T15:04:05Z07:00": Ini ISO 8601 / RFC 3339 format. Go pakai reference time "Mon Jan 2 15:04:05 MST 2006" sebagai format template.

Risiko: Backslash escape di dalam YAML string yang dikirim via MCP JSON bisa corrupt. Ini salah satu suspect penyebab body hilang di dashboard. Alternatif yang lebih aman: hapus submitted_at dari body dan tambah di webhook receiver saja, atau pakai format tanpa karakter yang perlu escape.

---

RINGKASAN CONFIDENCE LEVEL

Expression yang CONFIDENT (verified dari docs atau registry):

.Event.Channel untuk message event
.Event.Ts untuk message event
.Event.User untuk message event (flat string)
.Event.Text untuk message event
match.channel_id filter untuk trigger message
Expression yang SEDANG (logis tapi belum verified):

.Event.TriggerId untuk block_action
.Event.View.PrivateMetadata untuk view_submission
js filter availability di wick
Expression yang TIDAK TAHU / SPEKULATIF:

.Event.Message.Ts dan .Event.Message.Text untuk block_action (flat vs nested tidak tahu)
.Event.User.Id untuk view_submission (flat vs nested tidak tahu)
.Event.View.State.Values.{blockid}.{actionid}.Value (path sangat dalam)
.Event.View.State.Values.{blockid}.{actionid}.SelectedOption.Value (paling spekulatif)
trunc filter availability
now dan date function availability
match.actionid filter untuk blockaction (tidak ada di registry)
match.callbackid filter untuk viewsubmission (tidak ada di registry)
Cara paling cepat untuk verify semua: publish draft, fire 1 real Slack event per flow, jalankan workflowgetrun_events, lihat raw event JSON yang diterima wick. Semua field names langsung terlihat dari sana.

---

## SECTION 8: ROOT CAUSE — AI MCP pakai field Event yang salah

**Ditemukan: 2026-05-18** — review kode engine vs YAML yang digenerate AI.

### Bug 1: `.Event.X` shorthand tidak ada

`workflow.Event` struct (internal/agents/workflow/types.go) hanya punya:

```
Type, Subtype, Channel, At, Payload map[string]any
```

Semua field lain (User, Text, Ts, TriggerId, Message.Ts, dll) **tidak ada**. Semua ada di `{{index .Event.Payload "key"}}`.

| Draft YAML (salah) | Benar |
|---|---|
| `{{.Event.Text}}` | `{{index .Event.Payload "text"}}` |
| `{{.Event.User}}` | `{{index .Event.Payload "user"}}` |
| `{{.Event.Ts}}` | `{{index .Event.Payload "ts"}}` |
| `{{.Event.Channel}}` | `{{index .Event.Payload "channel_id"}}` |
| `{{.Event.TriggerId}}` | `{{index .Event.Payload "trigger_id"}}` |
| `{{.Event.Message.Ts}}` | `{{index .Event.Payload "value"}}` (button value carry ts) |
| `{{.Event.Message.Text}}` | tidak tersedia di block_action — simpan di button value atau dataset |

### Bug 2: `arg_modes` tidak di-set AI

Semua field expression harus punya `arg_modes: expression`. Tanpa itu:
- Runtime: OK (default = expression, template di-render)
- Inspector UI: toggle tampil **Fixed** padahal isi adalah template (`initialMode = modes[key] || 'fixed'` di editor.js:371)

### Bug 3: trigger `match` value harus array

```yaml
# salah
match:
  channel_id: C0ASUHYCRNU

# benar
match:
  channel_id: ["C0ASUHYCRNU"]
```

Dan `match_enabled: true` wajib ada — default false = no filter.

### Fix YAML nodes (pakai Payload path yang benar)

**send-button-message:**
```yaml
args:
  channel: '{{index .Event.Payload "channel_id"}}'
  thread_ts: '{{index .Event.Payload "ts"}}'
  text: 'Ada pesan baru di channel support — mau buat tiket?'
  blocks: |
    [{"type":"actions","elements":[{"type":"button","text":{"type":"plain_text","text":"Buat Tiket"},"action_id":"create_ticket_btn","style":"primary","value":"{{index .Event.Payload \"ts\"}}"}]}]
arg_modes:
  channel: expression
  thread_ts: expression
  text: fixed
  blocks: expression
```

**open-ticket-modal:**
```yaml
args:
  trigger_id: '{{index .Event.Payload "trigger_id"}}'
  view:
    type: modal
    callback_id: create_ticket_modal
    title: {type: plain_text, text: ":ticket: Create Ticket", emoji: true}
    submit: {type: plain_text, text: Submit}
    close: {type: plain_text, text: Cancel}
    blocks: [...]
arg_modes:
  trigger_id: expression
  view: fixed
```

**private_metadata problem:** `view` di-render sebagai `fixed` → template di dalam `private_metadata` tidak di-render. Untuk pass channel+ts ke Flow C, simpan di button `value` sebagai JSON, atau gunakan dataset node.

**submit-ticket — akses form values:**
```yaml
# view_submission Payload["values"] = map[block_id][action_id] → {"type":..., "value":...}
body: |
  {
    "title": "{{index (index (index .Event.Payload \"values\") \"title_block\") \"ticket_title\" | js}}"
  }
```

### Action item untuk 23-mcp-ai-guide.md §13

Tambahkan ke Known Limitations:

| Limitasi | Keterangan |
|---|---|
| AI pakai `.Event.User` dll | Field tidak ada. Semua payload di `{{index .Event.Payload "key"}}` |
| AI tidak set `arg_modes` | Default expression di runtime aman, tapi inspector UI tampil Fixed → confusing |
| `match` value harus array | `channel_id: ["C"]` bukan `channel_id: C` |
| `match_enabled` default false | Tanpa `match_enabled: true` trigger fire semua event |
| `view: fixed` block template | Template di dalam view (private_metadata dll) tidak di-render — gunakan dataset atau button value untuk pass context |

---