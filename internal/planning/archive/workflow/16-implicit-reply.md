## 16. Implicit reply-to-source

Workflow ga punya dedicated `notify:` field. **Notification = action node**
— user/AI compose explicit channel/connector node di graph untuk handle
success/failure/intermediate updates. Lebih transparan, visible di
canvas, ga ada hidden behavior.

Pattern explicit:

```yaml
graph:
  entry: process
  nodes:
    - id: process
      type: agent
      ...
      on_failure: fallback
      fallback: notify-fail        # node ID — used kalau on_failure: fallback

    - id: notify-success
      type: channel
      channel: slack
      op: send_message
      args:
        channel: "{{.Env.LEADERSHIP_CHANNEL}}"
        text: "✓ done: {{.Run.final_result}}"

    - id: notify-fail
      type: channel
      channel: slack
      op: send_message
      args:
        channel: "{{.Env.ONCALL_CHANNEL}}"
        text: "✗ FAIL: {{.Run.error}}"
```

**Tetep ada satu engine convenience: implicit reply-to-source.**

Kalau trigger dari channel (`type: channel`), dan workflow **ga punya**
explicit `type: channel` node yang reply ke event source thread —
engine inject synthetic node di akhir flow (plus synthetic edge dari
node terakhir):

```yaml
# Synthetic, auto-injected (NOT di workflow.yaml)
- type: channel
  channel: <event.channel>
  op: reply_thread
  args:
    channel: "{{.Event.Payload.channel_id}}"
    thread:  "{{.Event.Payload.thread}}"
    text:    "{{.Run.final_result}}"
```

Override: set `reply_source: false` di trigger spec (default `true`).
Atau user inject explicit `type: channel` reply node — engine detect
itu dan skip synthetic.

Untuk skenario complex (post ke #leadership + reply ke source +
email admin), user tulis 3 action nodes terpisah. Engine ga magic-merge.

---

