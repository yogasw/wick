# Async Sub-Agent Orchestration

Five sub-projects that turn wick's existing delegation machinery into a mention-driven
orchestration layer: `@role task` dispatches work, a room runs one sub-agent at a time,
results come back structured, state lives in a store rather than in model memory, and a
checker loop decides when the investigation is done.

Source brief: `async-mcp-subagent-orchestration-plan.md` (external). This directory is
the wick-grounded version of it.

## TODO

All five are implemented and their tests pass. Nothing here is committed — that is the
user's call.

- [x] **Q — Serial queue per room** — spec `q-serial-queue.md`, plan `plan-q.md`.
- [x] **A — Mention router** — spec `a-mention-router.md`, plan `plan-a.md`.
- [x] **B — Structured results + memory modes** — spec `b-structured-results.md`, plan `plan-b.md`.
- [x] **C — Incident store** — spec `c-incident-store.md`, plan `plan-c.md`.
- [x] **D — Checker loop, roles, brakes** — spec `d-checker-loop.md`, plan `plan-d.md`.

### Not done, and why

- **`max_tool_calls`** — dropped by design, see departure 4 below. Its own sub-project if a
  real case ever needs it.
- **Reopening a closed incident** — an agent cannot, by design. The human path is a UI
  action that does not exist yet, so today a closed incident is closed for good.
- **`allowed_native_tools` / `strict_mcp`** — still stored and still unenforced, unchanged
  by this work. The `create_agent` field descriptions say so plainly.

### Deviations found while implementing

Recorded here because each contradicts what its plan said:

- **A wires two call sites, not three.** Sub-agents spawn through the same pool as the
  leader, so one turn observer on the factory callbacks covers both. A second hook inside
  `run.go` would have dispatched every mention twice.
- **A's dispatch report skips the leader.** It cannot message itself, and its rail panel
  already shows every dispatch — waking it to repeat that would spend a tree turn on
  nothing. Sub-agents, which have no panel, still get the line.
- **D's role seeding needs a marker, not insert-if-absent.** "Fill in what is missing" and
  "a deleted role stays deleted" cannot both hold: a deleted role IS missing. A
  first-run marker in configs (`sub_agents_seeded_roles_v1`) is what makes deletion stick.
- **D's round bookkeeping excludes blocked rows**, for the same reason Q's slot count does.
  Counting a parent parked on its own child means a round with any nesting never completes.

Plans C and D were written before Q/A/B landed: where a plan names a signature from an
earlier sub-project, the landed code is authoritative, not the plan's guess.

## What already exists

The transport and the brakes landed on `feat/agent-mention` and earlier. Do not rebuild
them.

| Capability | Where |
|---|---|
| sync/async modes, profile default | `internal/agents/delegation/mode.go` |
| session binding (`ParentSessionID`, `ChildSessionID`, `RootID`, `Depth`) | `internal/entity/agent_delegation.go` |
| async delivery to the leader session | `internal/agents/delegation/run.go` — `deliver()` |
| pending response ("do not block") | `internal/agents/delegation/collect.go` |
| once-only result handover | `collect.go` — `MarkCollected` |
| governor: depth, per-tree turn budget, parallel cap, per-delegation and per-tree token caps, hop cap, kill switch | `internal/agents/delegation/governor.go` |
| agent-to-agent messaging: `message`/`ask`/`reply`/`stop`, FIFO mailbox, auto-reply | `internal/agents/delegation/mailbox.go` |
| mention scanner (`ParseMentions`) — was written and tested with no production caller; A is that caller | `internal/agents/delegation/mention.go` |
| role CRUD over MCP, role locking | `internal/connectors/sub-agents/` |
| shared task boards | `internal/agents/delegation/board.go` |

## How this maps to the source brief

| Brief § | Sub-project | Note |
|---|---|---|
| 13 `max_parallel_agents` | Q | exists as a refusal; becomes a queue, default 1 |
| 3 Mention Router | A | new |
| 4 session_id on every task | — | already exists |
| 5 mention → profile mapping | A | resolved dynamically, not from a static map |
| 6 sync/async modes | — | already exists |
| 7 incident state | C | new |
| 7 `agent_tasks` table | C | folded into `agent_delegations` |
| 8 memory modes | B | new |
| 9 structured MCP response | B | new, forced through a `report_result` op |
| 10 result handler | B + C | dedup already exists |
| 11 checker loop | D | split: bookkeeping in Go, judgement in the model |
| 12 production roles | D | seven, incl. `incident-supervisor` for headless runs |
| 12 `@incident-supervisor` | D | the leader IS the supervisor when a human is present |
| 13 backpressure | D | five of seven knobs exist; two added; one dropped |
| 14 scheduled collect fallback | D | `collect` exists; auto-scheduling is new |
| 16 Phases 1–8 | A–D | P2→A, P4→exists, P3+P5→C, P1+P6+P7+P8→D |

## Deliberate departures from the brief

1. **Mention mapping is dynamic.** The brief pins `@log-investigator` to a profile in a
   static JSON map. That map drifts the moment someone adds a role. Resolution reads the
   role registry instead, so creating a role makes it mentionable with no extra config.

2. **No `agent_tasks` table.** The brief's task schema — status, timestamps, error,
   `is_result_processed` — is already `agent_delegations`, which additionally carries the
   governor, the cycle guard, interrupt and take-over, all under test. A second table
   would be a second source of truth to keep in sync. An `IncidentID` column on the
   delegation row does the linking.

3. **The envelope is produced by an explicit op.** The brief specifies a structured
   response shape without saying who builds it. Sub-agents are CLI agents emitting free
   text, so the shape has to be requested: `report_result`, with a fallback that promotes
   the final text to `summary` when a model forgets to call it.

4. **`max_tool_calls` is dropped.** wick counts turns off the normalized `Done` event, not
   tool calls; counting the latter needs per-provider event parsing for claude, codex and
   gemini separately. The turn cap already bounds the same runaway. Revisit as its own
   sub-project if a real case needs it.

5. **The checker is split.** "Are all investigators finished?" and "did this round add
   evidence?" are queries, not reasoning — Go decides *when* the checker runs. The model
   decides only *whether the evidence holds together*, which is the part that needs
   judgement.

6. **Confidence is an enum, not a float.** The brief uses `confidence: "high"` in §9 and
   `min_confidence_to_respond: 0.8` in §13. Enum wins; a decimal from an LLM is false
   precision. The gate is `>= medium` by default.

7. **The parallel cap queues instead of refusing, and defaults to 1.** The brief allows
   three agents at once. In practice a room where several sub-agents talk at the same time
   is unreadable, and a refusal on the fourth mention of a fan-out throws the work away.
   One at a time, everything else in a visible FIFO line, cap still raisable. Detail in Q.

## Additions the brief does not cover

- **Roster at spawn.** A sub-agent currently learns who else exists only when someone
  messages it, so a fresh agent never considers calling a peer. A snapshot of the roster,
  the spawnable role keys and the remaining budget rides in the task envelope. Detail in A.
- **Client-response sanitising is a code path, not a prompt.** "No raw logs or secrets" is
  an instruction, and instructions are not controls. The draft is masked on the way out
  against every secret-marked config value, and absolute paths under the project root are
  reduced to filenames. Detail in D.
- **An intake gate before the judgement gate.** The brief validates only at the checker,
  which means unsourced claims reach the evidence pool and a model has to argue them out
  later. A mechanical check on arrival — every evidence item has a source and an excerpt —
  costs nothing and keeps the pool clean. Detail in D.
- **Loop-until-dry.** The brief stops on the first iteration that adds no evidence. Two
  consecutive empty iterations is the stop condition here; investigations routinely come
  up empty once and land the next round.

## Conventions

- UI copy is English. Samples use `abc.com` / `example.com`.
- Never edit `*_templ.go`; edit `.templ` and regenerate.
- Zerolog: `l := log.With().Str("component", "x").Logger()`.
- `safeexec`, never `os/exec`.
- Postgres and sqlite dialects only.
- Go tests run with `-count=1`.
- No dead knobs: every config key is read by code in the same task that adds it.
- The user commits. Plans end at "tests pass".
