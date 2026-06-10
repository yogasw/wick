## 19. Failure & timeout

- **Validation gagal** — Service.Create return err. UI/MCP munculin
  error msg dengan path field.
- **Pool penuh** — `pool.RunOnce` queue-in.
- **Node fail** — apply `on_failure`:
  - `halt` (default) — flow stop, status=failed.
  - `skip` — output set `{error: ...}`, lanjut ke `next`.
  - `fallback` — jump ke `fallback` node ID.
- **Timeout per node** — `context.WithTimeout(ctx, node.TimeoutSec)`.
  Kill node, apply `on_failure`.
- **Timeout workflow** — `MaxDurationSec` total. Kill running node +
  cancel pending.
- **Worker crash mid-run** — state.json ada `current=X`. Reaper tandain
  Failed kalau `now - updated_at > 2 * max_duration_sec`. Atau Resume
  by manual button.
- **Concurrent fire (same workflow)** — FIFO queue.
- **Duplicate event** — dedup LRU + file fallback.
- **Render error** — template ke field gak ada → node fail dgn jelas.
- **Cycle detected** — parse-time error, ga sampe runtime.
- **DB query fail** — node fail dgn error. retry policy applies.
- **External API down** (http/skill) — retry policy applies, abis itu
  apply on_failure.

---

