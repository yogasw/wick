## 3. Use cases (canonical examples)

Validasi desain dgn 5 contoh konkret + generic identifiers (abc.com,
example.com). Workflow yang ga muat ke salah satu pattern ini = scope
creep.

### Use case 1: Inbound inquiry triage (single trigger, branch routing)

Pattern paling umum — channel message masuk, klasifikasi AI, route ke
handler.

```
trigger: channel chat, event=message, target="#inbox", mention_bot=true
  ↓
classify (AI): "bug" / "question" / "feature" / "other"
  ├─ bug      → connector tracker.create_issue → channel reply "tracked: <url>"
  ├─ question → agent (skills: doc-search) → channel reply <answer>
  ├─ feature  → connector airtable.append_row → channel reply "noted"
  └─ other    → agent (bounce text) → channel reply
```

Tree-shape. Single trigger entry → 4 leaves.

### Use case 2: Multi-source incident response (parallel + merge)

Pattern fan-out → gabung → process. Cocok buat alert response.

```
trigger: webhook /hooks/alerts
  ↓
parallel (3 branches):
  ├─ connector grafana.fetch_dashboard
  ├─ shell run "df -h" on target host
  └─ connector log_store.query_errors (last 5min)
  ↓ merge (wait-for-all)
agent: "Analyze: metrics + disk + logs. Identify root cause."
  ↓
classify: "needs_human" / "self_heal"
  ├─ needs_human → connector pager.escalate → channel #oncall send_message
  └─ self_heal   → shell run runbook.sh → channel #oncall send_message (result)
```

DAG (parallel + merge). Edge-first model:
- Trigger entry → `parallel-fetch` node
- `parallel-fetch` fan-out via 3 edges to grafana/shell/db_query
- All 3 → `merge-results` node
- Linear chain to `analyze` → `classify-severity` → branch

### Use case 3: Daily metric digest (cron + parallel)

```
trigger: cron "0 8 * * *" timezone=UTC
  ↓
parallel (3 sources):
  ├─ connector github.list_recent_issues (24h)
  ├─ connector status_page.list_incidents (24h)
  └─ connector customer_db.query_active_count
  ↓ merge
agent: "Format digest markdown dari 3 sumber"
  ↓
channel chat.send_message #leadership <digest>
  └─ parallel-fan-out:
       → dataset_insert digest_archive (audit trail)
```

### Use case 4: Multi-stage interactive flow (3 trigger, 1 workflow)

Pattern stateful tanpa pause/resume — 3 trigger ke entry node terpisah
di workflow yang sama.

```
WORKFLOW ticket-flow:

triggers:
  1. event=message  → entry: post-button     (inquiry datang)
  2. event=action   → entry: fetch-thread    (user click button)
  3. event=submission → entry: create-ticket (user submit modal)

graph:
  STAGE 1 (run #1):
    post-button (connector chat.post_with_button)
      [run ends — wait next trigger via Slack interaction event]

  STAGE 2 (run #2, fired by action event):
    fetch-thread (connector chat.get_thread_messages)
      → summarize (agent skills=[validator])
      → show-modal (connector chat.open_modal)
      [run ends — wait submit]

  STAGE 3 (run #3, fired by submission event):
    create-ticket (connector helpdesk.create)
      → confirm (connector chat.send_message)
```

Context dipropagasi via Slack `metadata` di button/modal payload,
retrieved via `{{.Event.Payload.metadata.*}}` di stage berikutnya.
3 independent runs di JobRun history (traceable per stage).

### Use case 5: Nested classification (deep tree)

```
trigger: channel chat, event=message, target="#support"
  ↓
classify-1: "question" / "statement"
  └─ question →
       classify-2: "product-A" / "product-B" / "other"
       └─ product-A →
            classify-3: "how-to" / "bug-report"
            ├─ how-to     → agent skills=[product-A-docs] → reply
            └─ bug-report → connector tracker.create + reply
       ├─ product-B → agent skills=[product-B-docs] → reply
       └─ other     → end silent
  └─ statement → end silent
```

Pure tree, depth 3 classify. Bukti tree cukup buat 80% AI orchestration.

### Edge-first vs embed comparison (use case 1 in YAML)

```yaml
# EDGE-FIRST (n8n-style, what wick uses)
graph:
  entry: classify-intent
  nodes:
    - { id: classify-intent, type: classify, prompt: "..." }
    - { id: handle-bug,      type: connector, module: tracker, op: create_issue }
    - { id: handle-question, type: agent, skills: [doc-search] }
    - { id: handle-feature,  type: connector, module: airtable, op: append_row }
    - { id: handle-other,    type: agent, prompt: "friendly bounce" }
    - { id: reply,           type: channel, channel: chat, op: reply_thread }
  edges:
    - { from: classify-intent, case: bug,      to: handle-bug }
    - { from: classify-intent, case: question, to: handle-question }
    - { from: classify-intent, case: feature,  to: handle-feature }
    - { from: classify-intent, case: default,  to: handle-other }
    - { from: handle-bug,      to: reply }
    - { from: handle-question, to: reply }
    - { from: handle-feature,  to: reply }
    - { from: handle-other,    to: reply }
```

Add new handler = `workflow_add_node` + 1 `workflow_connect` MCP call.
Reroute = swap edge target. Atomic operations.

---

