## 25. Diagnose + watch — agentic debug loop

**Goal:** AI that built a workflow can shepherd the user through testing it
without making the user copy-paste logs back. Two helpers cover the loop:

- `workflow_get_run_log` UPGRADE — add `diagnose=true` flag → returns error
  classification + suggested fix text + available keys, on top of the existing
  summary. Cheap extension, no new MCP op.
- `workflow_watch` NEW — bounded, filterable read over recent runs. Supports
  long-poll for integration-test flows ("user just triggered, wait for the run
  to land"). Production-safe by design — hard caps and stop-on-first.

Fix application stays out of scope for both. The AI proposes; the user (or AI
in the same conversation) applies via existing `workflow_update_node` /
`workflow_write_file` per the no-auto-commit / no-auto-fix discipline.

### TODO

- [x] doc spec written (this file)
- [x] **Phase 1** — error classifier registry (8 rules) + `diagnose` flag on
      `workflow_get_run_log` → [mcp/diagnose.go](../../agents/workflow/mcp/diagnose.go)
- [x] **Phase 1** — unit tests for each classifier rule + flag round-trip
- [x] **Phase 2** — `workflow_watch` implementation: peek (wait=0), next
      (stop_on_first/expect=1), expect-n, batch (collect-until-timeout) →
      [mcp/watch.go](../../agents/workflow/mcp/watch.go)
- [x] **Phase 2** — multi-dim filter (workflow_id + trigger_id + node_id +
      status + since) backed by sharded index; trigger_id / node_id enrich
      via state.Load only when present
- [x] **Phase 2** — long-poll dispatch via Engine multi-subscriber broker
      ([engine/broker.go](../../agents/workflow/engine/broker.go)); listens
      only on `EventWorkflowCompleted` / `EventWorkflowFailed` for early
      return
- [x] **Phase 2** — Trigger.ID plumbed into RunState.Event by router
      ([trigger/router.go](../../agents/workflow/trigger/router.go))
- [x] Updated [SKILL.md](../../.claude/skills/workflow-node-module/SKILL.md)
      "MCP discovery surface" section

### Why this exists

Sekarang AI yang bikin workflow lewat MCP:

1. ✓ Punya tool buat bikin & validate workflow
2. ✓ Bisa baca state via `workflow_get_run` / `workflow_get_run_log` / `workflow_get_run_events`
3. ✗ Harus parse error string sendiri ("template execute: ... map has no entry
   for key \"channel\"") — gak dapat decision support
4. ✗ Gak ada cara cheap untuk nungguin run baru saat user testing — harus poll
   loop sendiri

Phase 1 fix (3). Phase 2 fix (4). Both keep raw read ops untouched — they
remain the data plane; these helpers add the decision plane on top.

### Phase 1 — `workflow_get_run_log` upgrade

#### New optional input

```go
type getRunLogInput struct {
    ID       string `wick:"required;desc=Workflow ID."`
    RunID    string `wick:"required;desc=Run ID."`
    Diagnose bool   `wick:"desc=When true, attach error classification + suggested fix + available_keys to the response."`
}
```

#### Extended response

Default response shape stays identical — backward-compatible. When `diagnose=true`
**and** the run failed, the response gains a `diagnosis` block:

```json
{
  ... existing fields ...,
  "diagnosis": {
    "error_class": "template_missing_key",
    "failed_node": "send_msg",
    "field": "args.channel",
    "diagnosis": "Template references .Node.trigger.payload.channel but the slack.message payload exposes channel_id (not channel).",
    "available_keys": ["text", "user", "channel_id", "thread", "ts", "is_dm"],
    "suggested_fix": {
      "node_id": "send_msg",
      "field": "args.channel",
      "current": "{{.Node.trigger.payload.channel}}",
      "suggested": "{{.Node.trigger.payload.channel_id}}",
      "confidence": "high",
      "rationale": "channel_id is the closest key in the available set; channel does not exist in this event type's payload."
    },
    "next_actions": [
      "workflow_template_test(suggested, sample_event=slack.message) to verify",
      "workflow_update_node(workflow_id, node_id, patch) to apply"
    ]
  }
}
```

On a successful run, `diagnose=true` returns the same default response with
`diagnosis: { status: \"success\", path_taken: [...] }` so the caller has a
uniform shape to read.

#### Error classifier registry

One registry table, regex-driven, lives at
`internal/agents/workflow/mcp/diagnose.go`. Each entry:

```go
type ErrorRule struct {
    Class    string                   // "template_missing_key", "channel_action_missing", ...
    Pattern  *regexp.Regexp           // matched against state.Error.Message
    Diagnose func(ctx DiagnoseCtx) Diagnosis
}
```

`DiagnoseCtx` carries:

- `RunState` (the failed run snapshot)
- `Workflow` (resolved YAML)
- `Match []string` (regex submatches)
- `Integration *integration.Registry`
- `Connectors *connector.Registry`
- `Engine.Descriptors` (for node-type metadata)

Initial rules (Phase 1 launch set):

| Class | Pattern | Suggestion source |
|---|---|---|
| `template_missing_key` | `map has no entry for key "X"` | Walk RenderCtx at parent path, list sibling keys, propose closest match |
| `template_parse` | `template parse:` / `unexpected "..."` | Surface offending field, propose `arg_modes: <field>: fixed` |
| `channel_action_missing` | `channel action "<ch>.<op>" not registered` | List `Integration.ActionsByChannel(ch)`, suggest closest |
| `connector_op_missing` | `connector op "X" on module "Y"` / `module "X" not registered` | List `Connectors.Module(Y).Operations`, suggest closest |
| `secret_leak_guard` | `secret leak: .Env.X` | Propose swap to `.Secret.X` |
| `provider_skill_missing` | `agent skill "X" not available` | List `provider.ListSkills`, suggest closest |
| `agent_session_invalid` | `session_from references nonexistent node` | List upstream agent / session_init nodes |
| `branch_no_edge_matched` | `branch %s: no edge matched verdict %q` | List edge `case:` labels declared on outgoing edges |

Unknown errors → return `error_class: "unknown"` + raw message + `confidence: "low"` and no suggested_fix. Better to surface "I don't know" than guess.

#### Confidence levels

- `high` — single-key swap, exact substring match, registered name vs typo with
  Levenshtein ≤ 2
- `medium` — multiple candidates within distance, structural inference
  (templating mode toggle, secret-vs-env)
- `low` — heuristic guess only; AI must verify before applying

Per memory: AI gak auto-apply. Even `high` confidence is a suggestion. User
decides.

### Phase 2 — `workflow_watch`

#### Input

```go
type watchInput struct {
    WorkflowID   string `wick:"desc=Optional. Limit to one workflow."`
    TriggerID    string `wick:"key=trigger_id;desc=Optional. Limit to one trigger entry by its workflow.yaml trigger id."`
    NodeID       string `wick:"key=node_id;desc=Optional. Limit to runs that touched (started or finished at) this node id."`
    Status       string `wick:"dropdown=any|success|failed|running;default=any;desc=Filter by run status."`
    Since        string `wick:"desc=RFC3339 timestamp. Default: now. Use \"-1h\" for relative window."`
    Limit        int    `wick:"number;desc=Max results. Default 10, hard cap 50."`
    WaitSeconds  int    `wick:"key=wait_seconds;number;desc=Upper bound for the wait (0..30). 0 = peek-only (non-blocking). >0 = subscribe to the live event stream; return EARLIER than wait_seconds the moment target is met."`
    Expect       int    `wick:"number;desc=Return as soon as N matching runs collected. Default 0 = collect everything until wait_seconds elapses. Set when AI knows it's testing N triggers ('test 2 messages')."`
    StopOnFirst  bool   `wick:"key=stop_on_first;desc=Shortcut for expect=1. Default false."`
}
```

#### Three operating modes (derived from inputs)

| Mode | Trigger | Behavior |
|---|---|---|
| **peek** | `wait_seconds = 0` (default) | Return latest N runs matching filter since `since`. Non-blocking. Used for history scan + prod debug. |
| **next** | `wait_seconds > 0`, `stop_on_first = true` OR `expect = 1` | Subscribe to live event stream. Return on first matching run. `wait_seconds` is the upper bound — server returns the instant the run lands. Used for "test 1 trigger". |
| **expect-n** | `wait_seconds > 0`, `expect = N` | Subscribe + collect. Return as soon as N matching runs land, OR `wait_seconds` elapses. Used for "test N triggers in a row". |
| **batch** | `wait_seconds > 0`, `expect = 0`, `stop_on_first = false` | Subscribe + collect everything matching until `wait_seconds` elapses or `limit` hit. Used for bulk replay / catch-all listen. |

**Critical: server-side early return.** `wait_seconds` is a ceiling, not a sleep.
The handler subscribes to the engine's `OnEvent` hook (already exists for SSE
broadcast) and returns the moment the target collection size is met. AI that
asks `expect=2, wait_seconds=30` typically gets a response in 2–5 seconds when
the user triggers promptly — not the full 30s.

#### Response — bare minimum (deliberately)

Watch carries ONLY the index fields needed to decide whether to drill in.
Status comes free from the sharded index; nothing else is loaded. AI walks the
list, picks the runs that matter, and pulls full state via the existing
`workflow_get_run_log(..., diagnose=true)` per chosen ID.

```json
{
  "runs": [
    {
      "run_id": "abc...",
      "workflow_id": "wf1",
      "status": "failed",
      "started_at": "...",
      "ended_at": "..."
    }
  ],
  "checked_until": "2026-05-19T10:32:17Z",
  "truncated": false
}
```

This keeps watch cheap even in prod where a single window may surface 50
runs — none of which are loaded until AI explicitly picks one. AI policy:
sample 1–2 representative IDs (newest + 1 failure if any), not bulk-process
the entire list.

#### Empty result is informative

`runs: []` is a valid + meaningful answer. Use case (integration test): user
triggered something that the workflow's match filter should reject. An empty
watch result confirms the filter held. If a run leaks through, AI loads its
log via `workflow_get_run_log(diagnose=true)` and explains the mismatch.

#### Typical flows

**Trigger validation (one happy path at a time):**

```
User: "kirim message 'BC' di #A"
AI: workflow_watch(workflow_id=wf, wait_seconds=15, stop_on_first=true)
    ← AI is BLOCKED in this call; server is subscribed to the event stream
    [user kirim message]
    [run lands → server fires return within ~1s]
    → 1 run id → AI loads workflow_get_run_log(diagnose=true) → confirms path
User: "sekarang button ABC"
AI: workflow_watch(workflow_id=wf, since=<last>, wait_seconds=15, stop_on_first=true)
    ← blocked again
    [user clicks button → run lands → return]
```

**Multi-trigger in one shot (faster path):**

```
User: "aku mau test message + button sekaligus"
AI: workflow_watch(workflow_id=wf, wait_seconds=20, expect=2)
    ← AI blocked, server collects matching runs
    [user kirim message → server holds at count=1]
    [user click button → server holds at count=2 → returns immediately]
    → [run_id_msg, run_id_button] in ~3s, not 20s
```

**Negative case (filter should reject):**

```
User: "aku coba kirim message random"
AI: workflow_watch(workflow_id=wf, since=<last>, wait_seconds=10)
    → runs=[] → AI reports "filter held, no run fired"
```

**Filter leak detected:**

```
User: "harusnya cuma kena di channel A, tapi aku coba dari channel B"
AI: workflow_watch(workflow_id=wf, since=<last>, wait_seconds=10)
    → 1 run → unexpected. AI loads workflow_get_run_log(run_id, diagnose=true)
              → trigger.channel_id=B reached classify. Filter is missing
                channel whitelist. Suggest patch.
```

**Prod debug (large history, sample-first):**

```
User: "create button bug di prod, last hour"
AI: workflow_watch(workflow_id=wf, status=failed, since=-1h, limit=20)
    → 12 run IDs
AI: workflow_get_run_log(<first failed run>, diagnose=true)
    → error_class = template_missing_key on node "create_btn"
AI: "12 failures, sampled 1: missing key. Probably same root cause.
     Lihat 1-2 lagi buat konfirmasi atau langsung patch?"
```

#### Production safety

- **Hard cap** `limit = 50` regardless of input value (server enforces).
- **Wait cap** `wait_seconds = 30` (server enforces).
- **Index-only reads** — pulls from sharded run index
  (`runs/index/<date>-<seq>.jsonl`), never loads per-run dirs. Constant-time
  for large history; safe to call in prod.
- **No body parsing in watch** — watch deliberately returns only the index
  fields (run_id, workflow_id, status, started_at, ended_at). Run state +
  events + outputs are loaded ONLY when AI subsequently calls
  `workflow_get_run_log` on a chosen ID. Keeps the watch op cheap regardless
  of how many runs match.
- **No streaming push over MCP** — long-poll is the closest primitive that
  composes with stdio/JSON-RPC. SSE / websocket out of scope.
- **`since` parsing** accepts both RFC3339 absolute and relative (`-15m`,
  `-1h`, `-1d`). Relative is clamped to `-24h` upper bound to keep scans cheap.
- **AI policy: sample, don't bulk-load.** When watch returns 10+ IDs, AI
  picks 1–2 representative runs (newest + one failure) rather than calling
  get_run_log on every entry. Prevents log-storm on prod debugging.

#### Filter semantics

Filters are AND-ed. An empty filter matches everything (subject to limit + since).

- `workflow_id` — sharded index already partitions by workflow, cheap to scope.
- `trigger_id` — matched against `RunState.Event.TriggerID` (need to ensure
  Engine populates this when dispatching — Phase 2 prereq).
- `node_id` — matched against `RunState.Completed ∪ Failed ∪ Skipped`. Use case:
  "show me failed runs that touched my new node".
- `status` — `RunState.Status`.

#### Backend dispatch — subscribe, don't sleep

When `wait_seconds > 0` the handler:

1. **Peek phase** — query the sharded index for matching runs since `since`.
   If `len(initial) >= target` (where target is `expect` or `1` for
   stop_on_first or `limit` for batch), return immediately. No subscription.

2. **Subscribe phase** — register a per-call fan-out channel on the engine's
   existing `OnEvent` hook (already drives SSE). The hook fires once per
   state Save. Handler matches incoming RunState against the filter,
   pushes to channel.

3. **Select loop** —
   ```go
   for {
       select {
       case rs := <-ch:
           if matchesFilter(rs, in) {
               collected = append(collected, summarize(rs))
               if len(collected) >= target {
                   return collected, nil   // ← early return, NOT a sleep
               }
           }
       case <-time.After(wait):
           return collected, nil
       case <-ctx.Done():
           return collected, ctx.Err()
       }
   }
   ```

4. **Unsubscribe on return** — defer cleanup so abandoned long-poll requests
   don't leak channels. Engine already has unicast subscriber accounting from
   the SSE wire-up.

`OnEvent` fires on every node event, not every run completion. Watch listens
specifically for `EventWorkflowCompleted` / `EventWorkflowFailed` to avoid
spamming the channel mid-run.

### Open questions

1. ~~Fan-out broker for `OnEvent`~~ — RESOLVED. Engine now has
   `Subscribe(bufferSize) → *Subscription` backed by a non-blocking
   fan-out broker. Legacy `OnEvent` slot still fires first for SSE; new
   subscribers register through the broker. Slow consumers get dropped
   after 5ms to keep the engine event loop unblocked.
2. ~~Trigger ID surfacing on RunState~~ — RESOLVED. Router copies the
   event per matching trigger and stamps `evt.TriggerID = tr.ID` before
   enqueue. `RunState.Event.TriggerID` is now populated for every
   router-dispatched run; manual / RunNow paths still leave it empty.
3. **Streaming vs poll for prod** — production users with high run volume may
   want incremental yield instead of full collect-then-return. Defer to a
   v2 spec if a real user hits the limit.
4. **Diagnosis cache** — diagnose adalah pure function over RunState. If the
   same run gets diagnosed repeatedly (UI hover, repeated AI call), cache the
   result by RunID. Optional — measure first.

### Cross-ref

- [§9 MCP surface](09-mcp-surface.md) — existing get_run / get_run_log / get_run_events
- [§24 describe contract](24-describe-contract.md) — wickdocs.Docs.Examples
  carry input/output samples the classifier compares against
- [`mcp/template_check.go`](../../agents/workflow/mcp/template_check.go) —
  reuse `availableKeysAt` + `bestMatch` helpers for the template_missing_key rule
- [`mcp/validate_rich.go`](../../agents/workflow/mcp/validate_rich.go) —
  reuse the `extractQuoted` / `bestMatchAmong` plumbing for did-you-mean hints
