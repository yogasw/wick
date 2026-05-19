## 14. Test framework — unit + integration

AI-first principle (§2 #0) mandate: workflow yang dibuat AI dari prompt
**harus testable** sebelum deploy. Test framework punya 2 layer: **unit
test** (single node dgn mock) + **integration test** (full flow dgn
scripted events + mock external).

### Implementation status (snapshot)

| Capability | Status | Where |
|---|---|---|
| `wftest.Runner` — RunAll/RunOne | wired | [`internal/agents/workflow/wftest/runner.go`](../../agents/workflow/wftest/runner.go) |
| Coverage tracking (TotalNodes / HitNodes / Untested) | wired | `RunAllWithCoverage` in `wftest/runner.go` |
| CLI `wick workflow test <id> [--filter X]` | wired | [`cmd/cli/workflow.go`](../../../cmd/cli/workflow.go) |
| Editor "Tests" tab + TestResults panel | wired | [`view/workflow/test_results.templ`](../../tools/agents/view/workflow/test_results.templ) |
| Test case manager UI (CRUD + modal) | wired | [`view/workflow/test_manager.templ`](../../tools/agents/view/workflow/test_manager.templ) |
| Mock interception (provider/connector/channel/HTTP/dataset/shell) | designed, partial | spec below |
| `workflow_test` / `workflow_simulate` MCP ops | designed | §9 |
| `--watch` / `--record` flags | not yet | CLI subcommand only does run-all + --filter |
| 6-layer reliability mocks (run layer 2–5 over mocked provider response) | designed | spec below |

Routes that back the UI live in §10 ("Test framework — runner +
case manager" route block).

### Folder layout

```
__tests__/
  nodes/                          # unit tests — 1 file per node
    classify-intent.test.yaml
    handle-bug.test.yaml
    summarize.test.yaml
  integration/                    # integration tests — full flow
    bug-flow.test.yaml
    feature-flow.test.yaml
    multi-stage-interactive.test.yaml
  fixtures/                       # reusable input data
    sample-events.yaml
    sample-thread-messages.yaml
  mocks/                          # reusable mock responses
    classifier-responses.yaml
    tracker-responses.yaml
```

### Unit test format (per-node, mocked)

```yaml
# __tests__/nodes/classify-intent.test.yaml
node: classify-intent             # node ID di workflow.yaml

cases:
  - name: "bug pattern → verdict bug"
    input:
      Event: { Type: channel, Text: "production error di order checkout" }
      Node: {}                    # upstream outputs (kosong = node pertama)
    mocks:                        # provider response mocked
      provider_response:
        verdict: bug
        confidence: 0.92
        reasoning: "mentions production error"
    expect:
      output.verdict: bug
      output.confidence: ">= 0.5"           # comparison ops: ==, !=, >=, <=, contains, matches
      assertions:
        - { type: case_fired, value: bug }  # asserts edge case selected

  - name: "low confidence → default"
    input:
      Event: { Type: channel, Text: "lorem ipsum" }
    mocks:
      provider_response: { verdict: "unclear", confidence: 0.3 }
    expect:
      output.verdict: "default"             # confidence_threshold defends
      runtime_warnings: ["below confidence threshold"]

  - name: "fuzzy match kicks in"
    input:
      Event: { Type: channel, Text: "got error in widget" }
    mocks:
      provider_response: { verdict: "bug_report", confidence: 0.85 }
    expect:
      output.verdict: bug                   # fuzzy_match: bug_report → bug
      assertions:
        - { type: layer_applied, layer: fuzzy_match }
```

### Integration test format (full flow)

```yaml
# __tests__/integration/bug-flow.test.yaml
name: "bug inquiry → ticket + reply"

trigger:                          # synthetic event yg fire workflow
  type: channel
  event:
    Type: channel
    Text: "production error di order checkout"
    Channel: C123
    Thread: 1234567.890
    User: U999

mocks:                            # mock setiap external call
  nodes:
    - node: classify-intent
      response: { verdict: bug, confidence: 0.9 }
  connectors:
    - module: tracker
      op: create_issue
      args_match: { title: contains "order checkout" }
      response: { number: 42, url: "https://tracker.example.com/issues/42" }
  channels:
    - channel: chat
      op: reply_thread
      capture: true               # capture call, don't actually send
  http:
    - url_pattern: "*.example.com/*"
      method: GET
      response: { status: 200, body: { ok: true } }
  datasets:
    - dataset: handled
      ops: [exists]
      response: { found: false }   # first time → process

assertions:
  - path_taken: [classify-intent, handle-bug, reply-thread]
  - node: handle-bug
    args:
      title: "production error di order checkout"
  - node: reply-thread
    args.text: contains "issues/42"
  - final_status: success
  - duration_ms: < 2000
  - cost_usd: < 0.01
  - mocks_called: [classify-intent.provider, tracker.create_issue, chat.reply_thread]
```

### Mock layer — what gets intercepted

| Type | Real call | Mocked when test mode |
|---|---|---|
| Provider (`classify`, `agent` nodes) | CLI subprocess `claude`/`codex`/`gemini` | Engine bypass, return scripted `provider_response` |
| Connector (`type: connector`) | `Operation.Execute(ctx)` HTTP/DB | Engine bypass, return scripted response from `mocks.connectors[]` |
| Channel (`type: channel`) | `Channel.Send(action, args)` | Capture call (record args), return scripted response |
| HTTP node (`type: http`) | `http.Do(req)` | Match URL pattern + method, return scripted response |
| DB query (`type: db_query`) | `db.Query(...)` | Return scripted rows |
| Dataset (`type: dataset_*`) | Postgres `wick_datasets_rows` | In-memory test DB (default) atau scripted rows kalau explicit mock |
| Shell (`type: shell`) | `exec.Cmd` | Skip exec, return scripted stdout/exit_code |
| Implicit reply-to-source | `Channel.Send` synthetic | Captured like channel mock |

### Mock interception contract (engine impl spec)

**Test mode toggle:**
- Engine spawn dgn `EngineMode = ModeTest` saat called via `workflow_test`,
  `workflow_simulate`, atau CLI `wick workflow test`.
- Normal runs (cron/channel/webhook fire) selalu `ModeProduction`.
- Mode disimpan di `EngineContext` (propagate via ctx.Value), bukan
  per-node flag.

**Interception via Service interface wrapping:**

```go
// internal/agents/workflow/test/mock.go
type MockRegistry struct {
    Provider   map[string]ProviderMock     // node ID → mock response
    Connector  map[string]ConnectorMock    // "module.op" → mock + args match
    Channel    map[string]ChannelMock      // "channel.op" → capture + response
    HTTP       []HTTPMock                  // url pattern + method match
    Dataset    DatasetMockMode             // inmemory | scripted
    Shell      map[string]ShellMock        // node ID → stdout/exit
}

type EngineContext struct {
    Mode    EngineMode                     // ModeProduction | ModeTest
    Mocks   *MockRegistry                  // nil kalau Production
    Captures *CaptureLog                   // record outbound calls for assertion
}
```

**Service wrapping:** engine resolve service via `EngineContext.Mocks`:

```go
func (e *Engine) resolveProvider(ctx EngineContext, nodeID string) Provider {
    if ctx.Mode == ModeTest {
        if mock, ok := ctx.Mocks.Provider[nodeID]; ok {
            return &MockProvider{response: mock}
        }
        return &MockProvider{response: defaultMock}  // fallback: error or default
    }
    return e.providerRegistry.Get(...)             // real provider
}

// Sama pattern untuk Connector, Channel, HTTP, Dataset, Shell.
```

**Mock fallback policy** (no mock declared untuk a node):
- **strict mode** (default): test fail dgn "no mock for node X"
- **permissive mode** (`--allow-unmocked`): use real call (kalau ada
  credentials), atau return zero value (kalau external)

**Capture log** untuk channel/connector outbound:
```go
type CaptureLog struct {
    Channels  []ChannelCapture     // {channel, op, args, ts}
    Connectors []ConnectorCapture
}
// Tersedia di test result, assertion `node: X, args: {...}` baca dari sini
```

**Layer 2-5 di 6-layer reliability TETAP RUN saat mocked provider:**
- Mock provider return raw `provider_response` (schema-valid JSON).
- Engine apply normalize (layer 2), exact match (layer 3), fuzzy_match
  (layer 4), retry (layer 5), confidence threshold (layer 6) ke mock
  output. Sama path kayak real provider.
- This way test verify layer behavior end-to-end with deterministic
  inputs.

**Determinism guarantee:**
- All clock-dependent ops (`{{.Run.StartedAt}}`, `{{.Event.At}}`) use
  fixed time `2026-05-14T10:00:00Z` (configurable per test).
- UUIDs frozen sequence (`test-uuid-1`, `test-uuid-2`, ...).
- Random sampling seeded.
- Result: same test → same output every run, snapshottable.

### Assertion vocab

```yaml
expect:
  output.<field>: <value>              # equality
  output.<field>: ">= <number>"        # numeric comparison
  output.<field>: contains "<text>"    # substring
  output.<field>: matches "/regex/"    # regex
  output.<field>: in [a, b, c]         # set membership
  output.<field>: typeof string        # type check

assertions:
  - { type: case_fired, value: <case> }            # classify/branch picked this case
  - { type: edge_traversed, from: A, to: B }       # specific edge used
  - { type: layer_applied, layer: fuzzy_match }    # 6-layer reliability
  - { type: mock_called, target: <node>.<op> }     # mock was invoked
  - { type: node_skipped, node: <id> }             # on_failure: skip path

path_taken: [<node-id>, ...]                       # exact ordered path through graph
final_status: success | failed
duration_ms: <comparison>
cost_usd: <comparison>
```

### Running tests

```bash
wick workflow test <id>                          # all tests in __tests__/
wick workflow test <id> --filter node:classify   # unit tests filtered
wick workflow test <id> --integration            # only integration/
wick workflow test <id> --watch                  # rerun on file change
wick workflow test <id> --coverage               # which nodes hit
wick workflow test <id> --record <run-id>        # capture real run sebagai fixture
```

UI:
- Per-workflow "Tests" tab — list test results dgn pass/fail/skipped per case
- Click test → preview canvas dgn path_taken highlighted
- Click failing case → see expected vs got diff
- Coverage map → grey nodes = belum di-test

### Mock generation from run history

`wick workflow test <id> --record <run-id>`:
- Take existing JobRun (real or simulated)
- Extract trigger event + per-node outputs + connector responses
- Generate `__tests__/integration/auto-<timestamp>.test.yaml` dgn captured data
- User review + edit + commit

MCP equivalent: `workflow_record_test(id, run_id)`.

### AI-first test workflow

AI compose workflow → AI compose tests untuk verifikasi sebelum
request_review. Pattern:

```
AI prompt: "Buat workflow: ..."
  ↓
AI compose workflow.yaml + edges
  ↓
AI compose __tests__/nodes/*.test.yaml (1 per node)
  ↓
AI compose __tests__/integration/main-flow.test.yaml
  ↓
AI panggil workflow_test(id)
  ↓
Engine return: 5 pass, 1 fail "expected case bug got case other"
  ↓
AI debug — adjust prompt classify-intent, re-test
  ↓
All pass → AI panggil workflow_request_review
```

Tests = AI's verification loop sebelum manusia review. Reduce
back-and-forth admin approval.

### Fixture generation

Tombol "Capture as fixture" di run detail page → ambil event +
per-node output dari run yang baru jalan, simpan ke `__tests__/`. AI
bisa juga pake `workflow_capture_fixture(run_id)` MCP op.

### MCP ops untuk testing

```
workflow_test(id, filter?)           → run tests, return [{case, pass, error?, diff?}]
workflow_record_test(id, run_id)     → generate test YAML dari JobRun
workflow_test_coverage(id)           → {nodes_hit: [...], nodes_uncovered: [...]}
workflow_simulate(id, event, mocks)  → run with synthetic event + inline mocks, no persist
```

---

