## 13. State persistence + resume

### Lifecycle state file

```
runs/
  index/                       # sharded run summary index
    2026-05-15-01.jsonl        # one row per run, max ~100 rows per shard
    2026-05-15-02.jsonl
  <run-id>/                    # runID = plain UUID, listing not chronological
    state.json                 # current snapshot
    events.jsonl               # append-only log
    nodes/
      <id>.json                # output cache per node (large objects)
```

### Sharded run index (Runs panel pagination)

The Runs panel in the editor needs "newest 100 runs" answers in
constant time even after 100k+ historical runs accumulate. Scanning
`runs/<id>/state.json` for every entry doesn't scale.

Solution: append a one-line summary to a sharded JSONL file every
time a run completes. Each shard is bounded at ~100 rows so any
"page N" query reads exactly one ~10KB file regardless of total
history size.

**Shard naming:** `YYYY-MM-DD-NN.jsonl`. Per-day grouping with a
seq suffix when the day's first shard fills up. Alphabetical
descending listing = chronological newest-first natural — no need
to read state.json to sort.

**Row shape** (kept lean so 100 rows = ~10KB):
```json
{"id":"<uuid>","status":"success","at":"2026-05-15T17:32:22Z","end":"2026-05-15T17:32:33Z","ms":11000}
```

**Generic module:** [`internal/shardedlog`](../shardedlog/) is a
reusable `Store[T any]` (generics-based). Workflow runs use it via
[`state.IndexAppend`](../agents/workflow/state/index.go) +
`IndexList(id, page, pageSize)`. Future features (agent sessions,
log mirrors, …) can reuse the same store.

**Migration:** old runs (saved before the index existed) won't be
in any shard — the Runs panel only shows indexed entries. They
remain accessible via direct URL `/runs/<id>`. Acceptable trade-off
for the scale win.

### `state.json` schema

```json
{
  "id": "01938...",
  "workflow_id": "0193e2b4-...",
  "workflow_version": 3,
  "trigger": {
    "type": "channel",
    "event": {"text": "...", "user": "U123", ...}
  },
  "status": "running",
  "started_at": "2026-05-14T10:00:01Z",
  "updated_at": "2026-05-14T10:00:03Z",
  "current": "handle-bug",
  "completed": ["classify-intent"],
  "outputs": {
    "classify-intent": {"verdict": "bug"}
  },
  "error": null,
  "cost_usd": 0.0008,
  "tokens": 245
}
```

### Write pattern

Atomic write (`tmp+rename`) setelah tiap node selesai. Pre-execution
write update `current=<node>`, `status=running`. Post-execution write
move ke `completed[]`, `outputs[id]=...`. events.jsonl append per
sub-step.

### Structured event logs (zerolog mirror)

Every engine event hits zerolog with stable correlation fields so
operators can reconstruct any run from the wick log file without
touching `runs/`:

```json
{
  "level": "info",
  "component": "wf",
  "wf_id": "0193e2b4-...",
  "wf_run_id": "<uuid>",
  "request_id": "<from HTTP middleware>",
  "wf_node": "agent",
  "wf_event": "node_completed",
  "data": {
    "latency_ms": 12843,
    "output": { ... },              // truncated at 4KB
    "verdict": "..."
  },
  "time": "2026-05-15T17:32:22+07:00",
  "message": "workflow event"
}
```

Grep targets:
- `wf_run_id=<id>` — every event for one specific run
- `request_id=<id>` — HTTP request → engine run correlation
- `wf_id=<id>` — every event for one workflow (folder name)

Engine carries `request_id` across the queue boundary via
`Event.Payload["request_id"]` because the queue-worker goroutine's
own ctx is the server's bootstrap ctx, not the originating HTTP
request — see `Engine.Run` in
[`engine.go`](../agents/workflow/engine/engine.go).

### Resume

Worker startup atau crash recovery:
1. Scan `<BaseDir>/workflows/*/runs/*/state.json`.
2. Filter `status=running`.
3. Per row: hitung "stale" (now - updated_at > 2*max_duration_sec) →
   mark failed dgn reason "stuck".
4. Sisanya: replay dari `state.current`. Re-execute current node
   (assumes idempotent — author's responsibility).

UI tombol "Resume" di run detail buat run yang `status=paused` (manual
pause atau infrastructure-level pause).

### Skip persistence

Workflow ringan (cron tick yang cepet, ga butuh resume) bisa flag
`persistent: false` di YAML → engine ga tulis state per-node, cuma
final result. Trade-off: ga bisa resume, tapi lebih cepet + ga ngotorin
disk.

### Cleanup

Workflow-level config `run_retention_days: 30`. Daily cleanup job
(reuse `connector-runs-purge` pattern) hapus folder `runs/*` yang
lebih lama. Per-workflow override possible.

---

