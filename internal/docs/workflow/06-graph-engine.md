## 6. Graph & engine

### Engine = DAG walker dengan state

Algoritma (edge-first):

```
1. Load workflow.yaml, parse nodes[] + edges[].
2. Validate: 
   - Pick entry from trigger.entry_node OR graph.entry.
   - All edge from/to references exist in nodes[].
   - case: field only on edges from classify/branch source.
   - case: default present for classify/branch (safety net).
   - No cycles (Kahn's topological sort).
3. Build adjacency: source_id → []{target_id, case?}.
4. Init state: current=[entry], completed=[], outputs={}.
5. Persist state to runs/<run-id>/state.json.
6. Loop:
   a. Pick next executable node (all upstream completed atau is_merge_wait_satisfied).
   b. If multiple ready → parallel: spawn N goroutines per branch.
   c. Dispatch by type → execute → capture output.
   d. Validate output_schema kalau declared.
   e. Persist output to nodes/<id>.json + append to events.jsonl.
   f. Resolve next nodes:
      - For classify/branch source: filter edges by case == verdict.
      - For other source: all outgoing edges fire (fan-out).
      - Merge node: wait until all inputs[] completed.
   g. If error: apply on_failure (halt/skip/fallback).
7. No more executable nodes → finalize state. Engine inject implicit
   channel reply node kalau trigger dari channel dan workflow ga punya
   explicit reply (lihat §16).
```

**Fan-out via edges (no `parallel` node needed for simple case):**

```yaml
nodes:
  - { id: A, type: connector, module: store, op: fetch }
  - { id: log, type: dataset_insert, dataset: audit }
  - { id: notify, type: channel, op: send_message }
edges:
  - { from: A, to: log }
  - { from: A, to: notify }   # A → 2 branches paralel
```

Engine deteksi: node A punya 2 outgoing edges, ga ada `case:` → fan-out
spawn 2 goroutines (`log` + `notify` paralel).

**`parallel` node masih useful** untuk explicit fan-out dgn named
branches + named outputs:

```yaml
nodes:
  - id: gather
    type: parallel
    branches: [grafana-fetch, db-fetch, shell-check]
edges:
  - { from: gather, to: grafana-fetch }    # explicit
  - { from: gather, to: db-fetch }
  - { from: gather, to: shell-check }
  - { from: grafana-fetch, to: merge }
  - { from: db-fetch,      to: merge }
  - { from: shell-check,   to: merge }
```

Output `gather.branches.grafana-fetch.<result>` dst — keyed access.

**`merge` node** explicit wait-for-all:

```yaml
nodes:
  - id: merge
    type: merge
    inputs: [grafana-fetch, db-fetch, shell-check]
    strategy: object         # object | array | first | last
```

Engine wait sampai semua `inputs[]` selesai sebelum execute merge node.

### Cycle detection

Static analysis at parse time. Kahn's algorithm — kalau topological sort
ga complete = cycle. Reject save dengan error "cycle detected at
nodes [A, B, C]".

### Parallel exec

`parallel` node spawn N goroutines, semua share parent context. Output
collected dgn map keyed by branch ID. Fail policy:
- `on_failure: halt` (default) — 1 branch fail = cancel sisanya, workflow halt
- `on_failure: skip` — continue, output branch yang fail = `{error: "..."}`
- `on_failure: fallback` — wait all, fallback node jalanin sub-graph

**Per-branch on_failure override** — `parallel` node punya `branches:`
list of node IDs. Tiap branch boleh punya `on_failure` sendiri di node
body — overrides parallel-level policy.

### Merge node readiness (DAG diamond topology)

Merge node `type: merge` punya `inputs: [<node_id>, ...]` — engine wait
sampai SEMUA listed nodes complete sebelum merge execute. Rules:

```
Merge node M with inputs [A, B, C] is "ready" when:
  - All nodes in inputs[] have status: completed | failed | skipped
  - No inflight upstream that hasn't yet reached any of [A, B, C]
  - For each input node, its on_failure outcome is final (not still
    retrying)

Merge fires regardless of which inputs succeeded/failed.
Output composition (`strategy:`):
  - object — { "A": output_A, "B": output_B, "C": output_C }
    Branch ID = input node ID. Failed nodes have output = {error: "..."}.
  - array  — [output_A, output_B, output_C] preserving inputs[] order
  - first  — output dari node yang complete duluan
  - last   — output dari node yang complete terakhir
```

**Merge fail policy** — same `on_failure: halt|skip|fallback` di merge
node body. Default `halt` kalau ANY input failed AND merge ga skip.

**Edge resolution untuk fan-in:**
```yaml
edges:
  - { from: A, to: merge-results }
  - { from: B, to: merge-results }
  - { from: C, to: merge-results }
```
Engine deteksi merge node (by type) → wait-for-all behavior. Non-merge
target dgn multiple incoming edges = engine error at validation (only
merge can fan-in).

### Edge resolution rule (classify/branch source)

Source node `type: classify` atau `type: branch` output verdict string.
Engine filter outgoing edges:

```
1. Find outgoing edges where edge.case == output.verdict
2. If found → fire those edges (1 or more, fan-out allowed even with case)
3. If not found → find edges where edge.case == "default"
4. If still not found → halt with error "verdict not in cases"
```

`case: default` WAJIB ada untuk classify/branch — validator reject save
kalau missing.

**Note:** untuk classify dgn `output_cases: [...]` declared, validator
also check that every value in `output_cases[]` has corresponding edge
(plus `default`) — else warn at save.

### Error propagation di parallel

Per-branch error handling — propagate via parallel/merge node policy:

```
parallel: [A, B, C]
  ├─ A succeeded
  ├─ B failed → apply B.on_failure
  └─ C succeeded
  
on_failure di parallel node body:
  - halt: signal cancellation ke A, C goroutines. Parallel node fail.
  - skip: continue dgn B output = {error: ...}. Parallel completes.
  - fallback: wait A, C done. Apply fallback node ID.

Then downstream merge (kalau ada) receives partial results sesuai
strategy.
```

### Engine struct sketch

```go
// internal/agents/workflow/engine.go
type Engine struct {
    layout  config.Layout
    pool    *pool.Pool             // agent
    skills  skill.Registry         // skill nodes
    db      *sql.DB                // db_query nodes
    audit   AuditWriter
}

type Run struct {
    ID         string                   // UUIDv7
    WorkflowID string                   // workflow.id
    StartedAt  time.Time
    Event      Event                    // trigger event
    State      RunState
}

type RunState struct {
    Status      string                   // queued | running | paused | success | failed
    Current     string                   // current node ID
    Completed   []string                 // node IDs done
    Outputs     map[string]any           // node_id → output
    Error       *NodeError               // last error if any
    UpdatedAt   time.Time
}

func (e *Engine) Run(ctx context.Context, w Workflow, evt Event) (Run, error)
func (e *Engine) Resume(ctx context.Context, runID string) (Run, error)
func (e *Engine) Pause(runID string) error
func (e *Engine) Cancel(runID string) error
```

### State persistence

`runs/<run-id>/state.json` ditulis atomic (tmp+rename) setelah tiap
node selesai. Crash mid-node = next start baca state, current node
re-execute dari awal (idempotent assumption — node author's
responsibility).

`events.jsonl` append-only:
```
{"ts":"2026-05-14T08:00:01Z","event":"node_started","node":"classify-intent"}
{"ts":"2026-05-14T08:00:03Z","event":"node_completed","node":"classify-intent","output":{"verdict":"bug"}}
{"ts":"2026-05-14T08:00:03Z","event":"node_started","node":"create-ticket"}
{"ts":"2026-05-14T08:00:05Z","event":"node_failed","node":"create-ticket","error":"linear API 503"}
{"ts":"2026-05-14T08:00:05Z","event":"workflow_failed"}
```

Render to UI timeline. Resume reads events to find last `node_completed`,
state.current = next of that.

### Event emission: state + SSE in lockstep

Engine `emit()` is the single funnel for every run event:

```
emit(id, runID, ev):
  1. stamp ev.TS = Now() once (if not already set)
  2. StateStore.AppendEvent → events.jsonl row
  3. zerolog Info (wf_id, wf_run_id, wf_event, request_id, data)
  4. OnEvent hook → SSE broadcaster (wf:<id> session)
```

`workflow_started` follows the same shape — TS stamped before
AppendEvent, then `OnEvent(startEv)` fires with the same `ev.TS`.

**`ev.TS` invariant:** every emit path (per-node + workflow start)
stamps the timestamp on the in-memory `RunEvent` before
AppendEvent, so the row written to `events.jsonl` and the payload
broadcast via `OnEvent` share the same ts down to the nanosecond.

This invariant is load-bearing for the editor's live-run UI:
`POST /workflows/edit/<id>/run` returns immediately after the
run is enqueued, so the FE's EventSource subscribe races with the
worker. The FE closes the window by also fetching
`/runs/<id>/state` (backfill) and dedup'ing live SSE against
backfill via the tuple `(ts|event|node|case)`. See §10 "Live run
stream" for the FE side. Don't break the invariant — if a future
emission path generates its own timestamp (e.g. `time.Now()` inside
the broadcast hook), dedup silently degrades and the run timeline
double-paints.

### Test mode

`__tests__/<node-id>.json`:
```json
{
  "input": {
    "Event": {"Type": "channel", "Text": "ada bug di chat widget"},
    "Node": {}
  },
  "expected_output": {
    "verdict": "bug"
  }
}
```

`workflow_test(id)` MCP op (atau "Test" button UI) → engine jalankan
dgn fixture sebagai input, compare output ke `expected_output`. Mode:
- **Per-node**: run 1 node dgn fixture, check output.
- **Full flow**: run dari entry, kalau ada fixture per-node pakai itu,
  kalau ga ada panggil real (atau mock LLM dgn fixture).

Hasil: per-node ✓/✗ + diff kalau mismatch. Color-coded di canvas.

---

