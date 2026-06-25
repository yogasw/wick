# wick_execute — batch calls (multi-op, timeout, partial response)

> Let one `wick_execute` MCP call run several connector ops in one round-trip. Each call is isolated: an error or timeout in one does NOT sink the others — the batch always returns a per-call result array (partial on timeout). Backward compatible: the existing single `{tool_id, params}` shape still works.

## TODO (urut)

- [ ] **T1** — Schema: add optional `calls: [{tool_id, params, session_id?}]` to `wick_execute` input (keep `tool_id`+`params` for single). Add optional `timeout_ms` (per-call) + `max_parallel`.
- [ ] **T2** — Handler `WickExecute`: detect batch vs single. Single path unchanged.
- [ ] **T3** — Batch exec: run calls concurrently (bounded by `max_parallel`), each with its OWN `context.WithTimeout(timeout_ms)`. ctx cancel → connector aborts (http.NewRequestWithContext already wired).
- [ ] **T4** — Per-call result: `{index, tool_id, ok, result?|error?, timed_out?, duration_ms}`. Collect all, return array even if some failed/timed out (partial response).
- [ ] **T5** — Access check per-call (IsVisibleTo / session instance) — same as single, per entry.
- [ ] **T6** — Cap batch size (e.g. ≤ 20) + clamp timeout_ms to ≤ sseExecuteTimeout. Whole-batch deadline = SSE 5min still applies as backstop.
- [ ] **T7** — Tests: batch ok, one fails (others ok), one times out (partial + timed_out flag), size cap, single still works.
- [ ] **T8** — Update tool description + MCP instructions.

---

## Shape

Input (additive — `calls` OR `tool_id`+`params`):
```json
{
  "calls": [
    {"tool_id": "conn:abc/send", "params": {"channel":"C1","text":"hi"}},
    {"tool_id": "conn:abc/send", "params": {"channel":"C2","text":"yo"}, "session_id": "sw_..."}
  ],
  "timeout_ms": 30000,     // per-call; clamp ≤ sseExecuteTimeout (5min)
  "max_parallel": 4         // default min(4, len)
}
```

Output (batch → array; partial on timeout):
```json
{
  "results": [
    {"index":0, "tool_id":"conn:abc/send", "ok":true,  "result":{...}, "duration_ms":210},
    {"index":1, "tool_id":"conn:abc/send", "ok":false, "error":"...", "timed_out":true, "duration_ms":30000}
  ],
  "ok_count":1, "error_count":1, "timed_out_count":1
}
```
- Batch overall `isError=false` even if some calls failed — caller inspects per-call `ok`. (A fully-empty/invalid batch → isError=true.)
- Single shape (no `calls`) returns the SAME response as today — zero behaviour change.

## Timeout / kill / partial (the user's ask)

- **Per-call timeout**: each call gets `ctx, cancel := context.WithTimeout(reqCtx, timeout_ms)`. `svc.Execute(ctx,…)` → `connector.NewCtx(ctx,…)` → connector's `http.NewRequestWithContext` aborts the upstream the moment the deadline fires ([service.go:1187](../../../connectors/service.go#L1187)). No goroutine leak — that's the existing contract.
- **Kill = ctx cancel**: a timed-out call's ctx is cancelled → in-flight HTTP request unwinds. We mark it `timed_out:true, ok:false` and move on. We do NOT wait for it.
- **Partial response**: `parallel`-style fan-out collects every call's outcome into a slice; a slow/cancelled call resolves to a timeout result, never blocks the others. Batch returns as soon as all calls have settled (success, error, or timeout) — bounded by the longest single `timeout_ms`, NOT the sum.
- **Backstop**: SSE's existing `sseExecuteTimeout` (5min, [sse.go:41](../../../mcp/sse.go#L41)) caps the whole batch. `timeout_ms` is clamped ≤ that so a per-call value can't outlive the transport.

## Concurrency (DECIDED — final)

- **Concurrency fixed server-side at 5** (`batchConcurrency`). NOT exposed to the caller — `max_parallel` was dropped. Bounding it in the server protects memory/sockets regardless of batch size.
- **Input up to 100 calls** (`maxBatchCalls`); they queue through the 5-wide semaphore.
- **Timeout per call**: caller sets `timeout_ms`; default **3 min** (`defaultBatchTimeout`), clamped to **5 min** max (`maxBatchTimeout`).
- **Always run all** — a failed/timed-out call never stops siblings. The result array makes outcomes obvious: ok → result, fail → error + reason, timeout → `timed_out:true`. No "stop on first error" mode.
- Order preserved in `results` via `index` (write into pre-sized slice by position, not append).
- Each call independent: own access check, own ctx, own Execute. One panic/err can't take down siblings (recover per-goroutine).

## Risk / edges
- **Access**: check `IsVisibleTo` / session-instance PER call — a batch can't smuggle an unauthorized tool_id.
- **Encrypted fields**: `wick_enc_` resolution is per-Execute already; batch inherits it per call. No change.
- **Size cap**: hard ≤ 100 calls (reject over), 5 run concurrently. Bounds resource use without rejecting reasonable batches.
- **Mixed session_id**: each call carries its own optional `session_id` (sw_ instances). Don't assume one session for the whole batch.
- **Whole-batch cancel**: if the MCP client disconnects, `reqCtx` cancels → all per-call ctxs cancel → everything unwinds. Return what settled.

## Files
- `internal/mcp/handlers/tools.go` — extend `wick_execute` InputSchema (T1, T8).
- `internal/mcp/handlers/connectors.go` — `WickExecute` branch single vs batch; add `executeBatch` (T2–T6).
- `internal/mcp/handlers/connectors_batch_test.go` (new) — T7.
- `internal/mcp/instructions.go` — note batch usage (T8).
