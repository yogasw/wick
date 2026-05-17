# Workflow agent node — pool integration

Status: **implemented** (2026-05-16). Awalnya proposed; sekarang
shipped + tests pass. Doc tetep relevan sebagai design contract +
rationale.
Update terakhir: 2026-05-16.

---

## TODO

**Shipped:**
- [x] `workflow/setup/manager.go` — `WithAgentRuntime(pool, subscribe)` setter
- [x] `workflow/nodes/agent.go` — pool routing (queue mandatory, subscribe → send → wait Done)
- [x] `workflow/nodes/session_init.go` — engine-side executor
- [x] `workflow/types.go` — `NodeSessionInit`, `SessionFrom`, `SessionID` fields, `SessionPresetWorkflow{Run,Global,New}` constants
- [x] `pool/pool.go` — `Pool.EnsureSession` public method
- [x] `workflow/setup/providers.go` — drop 5-menit timeout, tambah semaphore
- [x] Canvas palette + codec + inspector lewat plugin arch (lihat [plugin-arch.md](plugin-arch.md))
- [x] sessionID separator `_` (charset-safe) — `DefaultRunSessionID(id, runID)` helper
- [x] Validator allow `_` di Node ID
- [x] Inspector UI: "Reuse" framing (sharing-oriented copy)
- [x] Tests: 15 unit test di `nodes/agent_session_test.go` + `nodes/session_init_test.go`

**Pending / future:**
- [ ] Test: queue mandatory under load — 3 workflow run paralel sesi sama → serialize
- [ ] Test: ctx batal saat nunggu Done → broadcaster unsub, ngak leak
- [ ] `prewarm: true` flag — kirim warmup message biar subprocess hidup sebelum agent pertama (butuh pool.Prewarm API atau warmup-prompt convention)
- [ ] Validator cek `session_from` reachable upstream (saat ini error muncul di runtime)
- [ ] Emit `node_queued` event saat sesi kena `p.queue` (vs langsung spawn)

---

## Latar belakang

Workflow agent node ([internal/agents/workflow/nodes/agent.go](../../agents/workflow/nodes/agent.go))
sekarang bypass agent pool. Akibat:

1. **PC meledak kalau workflow rame.** Tiap call spawn subprocess baru,
   ngak ada cap MaxConcurrent, ngak ada queue.
2. **Sesi ngak muncul di sidebar.** Pool punya `ensureSession` yg
   auto-bikin row di registry → sidebar "RECENT". Skip pool = skip
   sidebar.
3. **`AgentRequest.SessionID` di-ignore.**
   ([classify.go:111-119](../../agents/workflow/nodes/classify.go#L111-L119))
   build session string tapi `cliProvider.AgentCall` ngak forward ke
   CLI (no `--session-id`/`--resume`). Setting `session: persistent` di
   YAML ngak ngaruh.

Pool sudah punya semua mekanik yg dibutuhin
([pool.go:235-326](../../agents/pool/pool.go#L235-L326) `Pool.Send`):

- auto `ensureSession` → sidebar muncul
- FIFO `p.queue` saat `MaxConcurrent` penuh → `StatusQueued`
- buffer per sesi → message persist ke `conversation.jsonl`
- reuse subprocess kalau sesi masih alive
- `PreemptIdle=true` (default) kick longest-idle subprocess saat queue
  nunggu
- `KillAfterIdle` opsional kill total
- `QueueLen()` / `QueueSnapshot()` / `Active()` snapshot

Channels (Slack, REST) udah pakai pola ini lewat `sendFnFor` closure di
[server.go:507-520](../../pkg/api/server.go#L507-L520). Workflow tinggal
ngikut.

---

## Queue = mandatory

Pool queue **wajib** — ngak ada opt-out. Tanpa queue, paralel workflow
bisa spawn ratusan subprocess sampe leptop mati. Implikasi:

- Semua workflow agent call lewat `pool.SendWithWorkspace`
- Slot penuh → `p.queue` FIFO, sesi nunggu sampe slot bebas
- `PreemptIdle=true` (default) — sesi idle di-kill duluan biar queue
  jalan
- Workflow run yg long-running tetep aman: `IdleTimeout` cuma kill
  subprocess yg ngak terima message, sesi `Working` tetep hidup
- Ctx cancel → cascade ke pool send unblock

Trade-off: latency naik kalau slot full. Acceptable — pool snapshot
ke-expose lewat `Pool.QueueSnapshot()`, operator bisa naikin
`MaxConcurrent` di settings.

---

## Default session

Tiap workflow run dapet sessionID otomatis:

```
wf:<id>:run:<runID>
```

Semua agent node di run yg sama share subprocess. Konsekuensi:

- Node ke-2 reuse subprocess node ke-1 → skip spawn handshake, context
  carry
- Run beda = sesi beda = isolated
- Sidebar muncul row baru per run, label = first user message

Ngak ada knob global di workflow YAML buat ganti default ini. Mau
override? Pakai `session_init` node (lihat di bawah). Alasan: setting
global di YAML/Settings tab ngak testable (perlu fire trigger biar
template `{{event.thread_ts}}` render). Node yg dropping di canvas =
testable via `workflow_simulate` MCP op.

---

## Node baru: `session_init`

Drag dari palette "AI" section. Set `rc.DefaultAgentSessionID` untuk
downstream node + bikin session record di registry.

```yaml
- id: session-init
  type: session_init
  # Pilih salah satu:
  preset: workflow_run                 # workflow_run | workflow_global | new
  # atau:
  session_id: "slack-{{.Event.Payload.thread_ts}}"   # custom, template render runtime
```

**Preset:**

| Preset | sessionID jadi | Inspector label | Cocok buat |
|---|---|---|---|
| `workflow_run` ← default | `wf_<id>_run_<runID>` | Reuse in run | Agents 1 run share subprocess; run baru = fresh |
| `workflow_global` | `wf_<id>` | Reuse across runs | Cross-run state — agent inget conversation lama |
| `new` | UUID baru per call | Fresh each call | Isolated tiap exec |

Separator pakai `_` (bukan `:`) — Windows-safe + lulus
`storage.ValidateSessionID` charset `[A-Za-z0-9._-]`. Helper:
`nodes.DefaultRunSessionID(id, runID)` di
[internal/agents/workflow/nodes/agent.go](../../agents/workflow/nodes/agent.go).

**Custom ID:**
- Template render `rc.RenderCtx()` (event, env, node outputs)
- Cocok buat external anchor — Slack thread_ts (cross-run continuity per
  thread), user ID (1 user 1 sesi), customer ID (1 customer 1 sesi)

**Executor behavior:**
1. Resolve sessionID (template render kalau custom, atau preset pattern)
2. `rc.DefaultAgentSessionID = sessionID`
3. `pool.EnsureSession(sessionID, n.Workspace)` — bikin registry row +
   sidebar entry sekarang
4. Return — ngak send message, ngak spawn subprocess

Spawn subprocess terjadi otomatis saat agent node downstream pertama
nge-send. Pool queue handle scheduling.

**Future enhancement:** `prewarm: true` flag → kirim warmup message biar
subprocess hidup sebelum agent node pertama jalan. Deferred (butuh
pool.Prewarm API atau warmup-prompt convention).

---

## Per-node override di node `agent`

Kalau ngak ada `session:` field → pake `rc.DefaultAgentSessionID`.
Override:

```yaml
# Opsi A — reuse subprocess dari node lain (upstream agent node)
- id: deep-research
  type: agent
  session:
    from: classify-intent              # node ID, validator cek graph reachability

# Opsi B — fresh, isolated
- id: side-task
  type: agent
  session: new
```

**Inspector UI:**

```
Session:
  ● Use default              ← selected default
  ○ Reuse from node: [▾ classify-intent ]
  ○ Fresh (new)
```

Dropdown "Reuse from node" populated dari upstream agent node yg
reachable via DAG walker. Forward-ref + cycle ditolak validator.

---

## Resolver order

```
1. node.session.from set       → resolve sessionID dari node target
2. node.session == "new"       → UUID baru
3. rc.DefaultAgentSessionID    ← di-set oleh upstream session_init (atau engine default)
4. fallback engine             → "wf:<id>:run:<runID>"
```

`session_init` ngak ada di run → engine pake step 4 langsung. Jadi
session_init optional — workflow tanpa session_init tetep jalan dgn
per-run sessionID.

---

## Wiring code

### Manager dapet pool + broadcaster

```go
// internal/agents/workflow/setup/manager.go
type Manager struct {
    // ... existing
    AgentPool  *agentpool.Pool
    AgentBcast *agentstool.Broadcaster
}

func (m *Manager) WithAgentRuntime(p *agentpool.Pool, b *agentstool.Broadcaster) *Manager {
    m.AgentPool = p
    m.AgentBcast = b
    m.Engine.Register(workflow.NodeAgent, nodes.NewAgentExecutor(m.Providers, p, b))
    m.Engine.Register(workflow.NodeSessionInit, nodes.NewSessionInitExecutor(p))
    return m
}
```

server.go panggil setter setelah pool + bcast bikin.

### AgentExecutor pool path

```go
// internal/agents/workflow/nodes/agent.go
func (e *AgentExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
    sessionID, err := resolveSessionID(n, rc)
    if err != nil {
        return workflow.NodeOutput{}, err
    }

    prompt, err := template.Render(n.Prompt, rc.RenderCtx())
    if err != nil {
        return workflow.NodeOutput{}, err
    }

    if e.Pool != nil && e.Bcast != nil && providerUsesPool(n.Provider) {
        return e.runViaPool(ctx, sessionID, prompt, n)
    }
    // Fallback codex/gemini via cliProvider (semaphore'd)
    // ...
}

func (e *AgentExecutor) runViaPool(ctx context.Context, sessionID, prompt string, n workflow.Node) (workflow.NodeOutput, error) {
    evCh, unsub := e.Bcast.Subscribe(sessionID)
    defer unsub()

    if err := e.Pool.SendWithWorkspace(ctx, sessionID, "default", "workflow", "user", prompt, n.Workspace); err != nil {
        return workflow.NodeOutput{}, fmt.Errorf("pool send: %w", err)
    }

    var buf strings.Builder
    toolsUsed := []string{}
    for {
        select {
        case <-ctx.Done():
            return workflow.NodeOutput{}, ctx.Err()
        case ev, ok := <-evCh:
            if !ok {
                return workflow.NodeOutput{}, fmt.Errorf("event channel closed before done")
            }
            switch ev.Type {
            case "text_delta":
                buf.WriteString(ev.Data)
            case "tool_use":
                toolsUsed = append(toolsUsed, ev.Data)
            case "error":
                return workflow.NodeOutput{}, fmt.Errorf("agent error: %s", ev.Data)
            case "done":
                text := strings.TrimSpace(buf.String())
                return workflow.NodeOutput{
                    Result: text,
                    Fields: map[string]any{
                        "text":       text,
                        "tools_used": toolsUsed,
                        "session_id": sessionID,
                    },
                }, nil
            }
        }
    }
}
```

### SessionInitExecutor

```go
// internal/agents/workflow/nodes/session_init.go
type SessionInitExecutor struct {
    Pool *agentpool.Pool
}

func (e *SessionInitExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
    sessionID, err := resolveSessionInitID(n, rc)
    if err != nil {
        return workflow.NodeOutput{}, err
    }
    rc.DefaultAgentSessionID = sessionID
    if e.Pool != nil {
        // Bikin registry row + sidebar entry sekarang (no spawn)
        if err := e.Pool.EnsureSession(ctx, sessionID, "workflow", n.Workspace); err != nil {
            return workflow.NodeOutput{}, fmt.Errorf("ensure session: %w", err)
        }
    }
    return workflow.NodeOutput{
        Result: sessionID,
        Fields: map[string]any{"session_id": sessionID},
    }, nil
}

func resolveSessionInitID(n workflow.Node, rc *workflow.RunContext) (string, error) {
    if n.SessionID != "" {
        return template.Render(n.SessionID, rc.RenderCtx())
    }
    switch n.SessionPreset {
    case "", workflow.SessionPresetWorkflowRun:
        return "wf:" + rc.Workflow.ID + ":run:" + rc.RunID, nil
    case workflow.SessionPresetWorkflowGlobal:
        return "wf:" + rc.Workflow.ID, nil
    case workflow.SessionPresetNew:
        return "wf:adhoc:" + uuid.NewString(), nil
    }
    return "", fmt.Errorf("unknown session preset %q", n.SessionPreset)
}
```

### Pool.EnsureSession (new public method)

Pool sekarang `ensureSession` private. Expose public buat session_init:

```go
// internal/agents/pool/pool.go
func (p *Pool) EnsureSession(ctx context.Context, sessionID, source, workspace string) error {
    return p.ensureSession(ctx, sessionID, source, workspace)
}
```

Idempotent — kalau session udah ada, no-op.

---

## Constants & types

```go
// internal/agents/workflow/types.go
const (
    NodeSessionInit = NodeType("session_init")

    SessionPresetWorkflowRun    = "workflow_run"
    SessionPresetWorkflowGlobal = "workflow_global"
    SessionPresetNew            = "new"
)

type Node struct {
    // ... existing
    // session_init
    SessionPreset string `yaml:"preset,omitempty"`
    SessionID     string `yaml:"id,omitempty"`     // template OK

    // agent override
    Session struct {
        From string `yaml:"from,omitempty"`        // node ID upstream
        Mode string `yaml:"mode,omitempty"`        // "new" | ""
    } `yaml:"session,omitempty"`
}
```

`RunContext` tambah field:

```go
type RunContext struct {
    // ... existing
    DefaultAgentSessionID string
}
```

---

## Timeout strategy

Workflow agent node bisa lama — expected, bukan bug.

- ❌ Drop hardcoded 5-menit di `cliProvider.AgentCall`
- ✅ Layer timeout cascade:
  1. **Node-level** — `n.TimeoutSec` (per-node, user explicit)
  2. **Workflow-level** — `w.MaxDurationSec` (default 10 menit, engine
     wrap di [engine.go:212-217](../../agents/workflow/engine/engine.go#L212-L217))
  3. **`ctx.Done()` cascade** — engine cancel → pool send unblock →
     event loop break

Sesi yg nunggu di `p.queue` ngak punya timeout sendiri — nunggu sampe
slot bebas atau ctx batal. `IdleTimeout` (default 120s) cuma kill
subprocess `Idle`, sesi `Working` aman. `KillAfterIdle=0` (default) →
ngak ada hard kill. Sesi ke-kill saat idle → next message auto-respawn
via `--resume <CLI session id>`.

---

## Observability

Free dari pool:
- Sidebar entry — `ensureSession` bikin row, label = first user message
- `conversation.jsonl` — semua turn persist
- `agents.json` — CLI session ID untuk resume
- `meta.json` Status: `Queued` / `Working` / `Idle`

Workflow-side event lewat engine `emit`:
- `node_started` — sebelum `Pool.Send`
- `node_completed` — setelah `Done`, latency_ms = total termasuk wait
  queue

**Nice-to-have (deferred):**
- Emit `node_queued` saat `Pool.Send` return tanpa langsung spawn
- Pool snapshot di workflow run detail page (depth waiter, active sesi)

---

## Non-claude provider

Pool factory saat ini `ClaudeFactory` only. Codex/gemini ngak punya
pool path. Mitigation: semaphore global di `cliProvider.AgentCall`.

```go
// internal/agents/workflow/setup/providers.go
var nonClaudeSem chan struct{}

func (p *cliProvider) AgentCall(ctx context.Context, req provider.AgentRequest) (provider.AgentResult, error) {
    select {
    case nonClaudeSem <- struct{}{}:
        defer func() { <-nonClaudeSem }()
    case <-ctx.Done():
        return provider.AgentResult{}, ctx.Err()
    }
    // ...
}
```

Size = `MaxConcurrent`. Skip queue/sidebar (deferred sampai pool
multi-factory).

---

## Migration

Backward-compat YAML lama:

```yaml
session: root         # legacy → drop dari schema (was per-run, now default)
session: persistent   # legacy → drop (use session_init dgn preset workflow_global)
session: new          # → session: new (kept)
```

Workflow lama dgn `session: root`/`persistent` → validator warn + auto-
treat as default. Editor saat publish offer "Convert to session_init
node?" buat user yg mau explicit.

Default behavior berubah: dulu `""` = fresh per call, sekarang `""` =
per-run shared. Workflow yg butuh isolation absolut antar node → set
explicit `session: new`.

---

## Open questions

- `AgentName` hardcoded `"default"` di pool.Send. Kalau workflow mau
  pakai agent name beda (mis "researcher"), butuh expose `agent_name`
  field? Pool key = `sessionKey(sessionID, agentName)` → beda agentName
  = beda subprocess di sesi yg sama. Defer sampai use case konkret.
- Multi-tool-call streaming: kumpulin semua `tool_use` event di
  `tools_used` (cuma nama). Cukup? Atau perlu nested per-tool result?
- Skills allowlist — validate di executor sebelum send tetep relevan?
  Konsisten lebih baik validate.
- `session_init` di branch — kalau 2 cabang punya `session_init`
  berbeda, downstream branch yg merge dapat sessionID mana?
  Last-execution-wins via `rc.DefaultAgentSessionID` mutation. Aman?
  Atau warn validator?
