# D — Checker Loop, Production Roles, Brakes: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Seven seeded roles, a mechanical intake gate, a Go-driven checker loop that dispatches the `evidence-checker` role and acts on its decision, four config-backed brakes, masked client drafts, and an auto-scheduled collect for stalled async work.

**Architecture:** Go owns bookkeeping (round completion, evidence deltas, iteration/runtime caps); the model owns judgement (contradiction, sufficiency). The loop lives in the delegation package and is driven from the same place envelopes are ingested. Everything dispatches through `Service.Run` under the existing governor.

**Tech Stack:** Go 1.2x, GORM (postgres + sqlite).

**Spec:** [d-checker-loop.md](d-checker-loop.md) — depends on A (plan-a.md), B (plan-b.md), C (plan-c.md).

## Global Constraints

- **Never `git commit`.** The user commits. Every task ends at "tests pass".
- UI copy is English. Samples use `abc.com` / `example.com`.
- Zerolog component-logger pattern. Postgres + sqlite only. Tests `-count=1`. No dead knobs.
- `safeexec`, never `os/exec`.

---

## File Structure

**Created:**

| Path | Responsibility |
|---|---|
| `internal/agents/delegation/seedroles.go` | The seven role definitions + insert-if-absent seeding. |
| `internal/agents/delegation/seedroles_test.go` | Idempotency, no-overwrite, prompt content assertions. |
| `internal/agents/delegation/intake.go` | Mechanical gate on arriving envelopes. |
| `internal/agents/delegation/intake_test.go` | Reject/re-ask/drop/flag matrix. |
| `internal/agents/delegation/checkerloop.go` | Round bookkeeping + loop driver. |
| `internal/agents/delegation/checkerloop_test.go` | Round completion, dry-stop, decisions, brakes. |
| `internal/agents/delegation/sanitize.go` | Client-draft masking. |
| `internal/agents/delegation/sanitize_test.go` | Secret/hostname/path masking. |

**Modified:**

| Path | Change |
|---|---|
| `internal/agents/config/general.go` | 4 new Sub-agents keys. |
| `internal/pkg/api/delegation_limits.go` | Read the 4 keys. |
| `internal/agents/delegation/governor.go` | `Limits` + defaults for the 4 brakes. |
| `internal/agents/delegation/incident.go` | Iteration bump, round markers. |
| `internal/agents/delegation/run.go` | Intake gate + loop hook where envelopes are ingested. |
| `internal/agents/delegation/sweeper.go` | Runtime-cap enforcement + stalled-collect scheduling in the `DelegationSweeper` pass (from Q). |
| `internal/pkg/api/server.go` | Seed roles at boot; wire the schedule store into the sweeper. |
| `docs/guide/agents/sub-agents.md` | The investigation loop end-to-end. |

---

### Task 1: The four brakes (config → Limits)

**Files:**
- Modify: `internal/agents/config/general.go`, `internal/pkg/api/delegation_limits.go`,
  `internal/agents/delegation/governor.go`
- Test: extend `governor_test.go` + the provider tests in `internal/pkg/api/`

**Interfaces:**
- Produces on `Limits`:

```go
	// MaxIterations caps checker rounds per incident.
	MaxIterations int
	// MaxRuntimeMinutes caps wall clock from incident creation.
	MaxRuntimeMinutes int
	// MinConfidence gates the client-response drafter: unknown|low|medium|high.
	MinConfidence string
	// NoNewEvidenceRounds is how many consecutive evidence-free rounds
	// stop the loop. 2, not 1: investigations routinely come up empty
	// once and land the next round.
	NoNewEvidenceRounds int
```

Defaults in `DefaultLimits()`: `5, 20, ConfidenceMedium, 2`. `normalize()` clauses for all
four (non-positive int → default; confidence outside the enum → `ConfidenceMedium`).

`general.go`, Sub-agents group:

```go
	SubAgentsMaxIterations    int    `wick:"number;group=Sub-agents;desc=Checker rounds one investigation may run before it stops and escalates. Default: 5."`
	SubAgentsMaxRuntimeMin    int    `wick:"number;group=Sub-agents;desc=Minutes one investigation may run wall-clock before it stops, keeps partial results, and escalates. Default: 20."`
	SubAgentsMinConfidence    string `wick:"select;group=Sub-agents;options=low,medium,high;desc=Minimum checker confidence before a client-facing draft may be produced. Default: medium."`
	SubAgentsNoEvidenceRounds int    `wick:"number;group=Sub-agents;desc=Consecutive rounds with no new evidence before an investigation stops. Default: 2."`
```

(Verify the `select`/`options` tag syntax against an existing select field in the config —
grep `wick:"select` — and match whatever the real grammar is; if none exists, plain string
+ validation in the provider.)

`delegation_limits.go` reads all four with `intOr` / string-with-enum-check.

- [ ] **Step 1: Failing tests** — provider test per key (absent → default, row → value,
      garbage confidence → medium), normalize test for zero values.
- [ ] **Step 2: FAIL. Step 3: implement. Step 4:**
      `go test ./internal/agents/delegation/ ./internal/pkg/api/ -count=1` — PASS.

---

### Task 2: Seed the seven roles

**Files:**
- Create: `internal/agents/delegation/seedroles.go`, `seedroles_test.go`
- Modify: `internal/pkg/api/server.go` (call at boot, after migrate, before serving)

**Interfaces:**
- Produces: `SeedProductionRoles(ctx context.Context, repo *Repo) error` — global profiles
  (empty `ProjectID`), insert-if-absent by key, never updates an existing row.

Role table (each entry sets Key, Name, Icon, Description, SystemPrompt, DefaultMode,
CanDelegate, DefaultMemoryMode):

| Key | Mode | CanDelegate | Prompt core (full prose in code, 10–20 lines each) |
|---|---|---|---|
| `log-investigator` | async | false | Query logs, group errors, build a timeline. Every claim carries a quoted excerpt + source query. Report via report_result; kind=log. |
| `code-investigator` | async | false | Map errors to code paths; name probable cause and blast radius. Evidence = file:line + quoted snippet; kind=code. |
| `docs-investigator` | async | false | Find expected behaviour, runbooks, known issues. Evidence = doc title/URL + quoted passage; kind=doc. |
| `data-validator` | async | false | Check tenant config, flags, data anomalies. Evidence = query + result rows; kind=data. Never modify anything. |
| `evidence-checker` | sync | false | Compare findings across sources. Output the checker block: decision, confirmed findings, contradictions, missing evidence, followup tasks. Judge only what the evidence supports. |
| `client-response-drafter` | sync | false | Draft a customer-facing reply from confirmed findings only. No logs, no stack traces, no internal names. You draft; you never send. |
| `incident-supervisor` | async | **true** | Headless runs only. Plan into incident next_actions, dispatch by mention, decide stop/escalate. Prefer stopping over guessing — nobody is present to correct you. |

Every prompt ends with the shared evidence rule sentence: *"Quote a source and an excerpt,
or report it as a gap — a finding with no excerpt is a guess."*

- [ ] **Step 1: Failing tests**

```go
func TestSeedIsInsertIfAbsent(t *testing.T) {
	r := repoForTest(t)
	ctx := context.Background()
	if err := SeedProductionRoles(ctx, r); err != nil { t.Fatal(err) }
	// operator edits one
	p, _ := r.GetProfileExact(ctx, "", "log-investigator")
	p.SystemPrompt = "tuned"
	_ = r.SaveProfile(ctx, p)
	// second boot
	if err := SeedProductionRoles(ctx, r); err != nil { t.Fatal(err) }
	p2, _ := r.GetProfileExact(ctx, "", "log-investigator")
	if p2.SystemPrompt != "tuned" {
		t.Fatal("seed overwrote an operator edit")
	}
}

func TestSeedDoesNotResurrectADeletedRole(t *testing.T) {
	// seed, delete client-response-drafter, seed again → still gone.
	// Requires a tombstone: seeding checks a configs-table marker
	// "agents/seeded_roles_v1" and only inserts on the FIRST run —
	// simplest honest way to honour "deleted stays deleted".
}

func TestSeededRolesCarryTheEvidenceRule(t *testing.T) {
	// every seeded prompt contains the shared sentence; checker is sync;
	// only incident-supervisor has CanDelegate.
}
```

- [ ] **Step 2: FAIL. Step 3:** implement with the first-run marker (write
      `seeded_roles_v1 = "true"` through the configs service after a successful seed; skip
      entirely when present). **Step 4: PASS.**

---

### Task 3: Intake gate

**Files:**
- Create: `internal/agents/delegation/intake.go`, `intake_test.go`
- Modify: `internal/agents/delegation/run.go` (gate runs before `IngestEvidence`)

**Interfaces:**
- Consumes: `ResultEnvelope` (B), `Service.SendMessage` (re-ask), incident (C).
- Produces:

```go
// IntakeVerdict is what the mechanical gate decided about one envelope.
type IntakeVerdict struct {
	Accepted    []Evidence // items that passed
	Dropped     []Evidence // items that failed twice
	ReAsked     bool       // one follow-up was sent this time
	Unsupported bool       // findings present with zero accepted evidence
}

// GateEnvelope validates mechanically — empty source/excerpt, enum
// confidence. It calls NO model: whether a string is empty is not a
// judgement. One re-ask per delegation, ever; the second failure drops
// the item and records the drop on the incident.
func (s *Service) GateEnvelope(ctx context.Context, row *entity.AgentDelegation, env *ResultEnvelope) IntakeVerdict
```

Re-ask bookkeeping: one new column
`entity.AgentDelegation.IntakeReasked bool gorm:"not null;default:false"`.

- [ ] **Step 1: Failing tests**

```go
func TestGateRejectsSourcelessEvidenceOnceThenDrops(t *testing.T) {
	// envelope with one bad item (no source): first gate → ReAsked true,
	// a message to the child's handle exists containing "has no source",
	// IntakeReasked set. Second gate (still bad) → item in Dropped,
	// incident's next_actions or a note records the drop, no new message.
}

func TestGateFlagsFindingsWithoutEvidence(t *testing.T) {
	// findings non-empty, evidence empty → Unsupported true, nothing dropped.
}

func TestGateCallsNoModel(t *testing.T) {
	// service wired with a runner whose StartAgent fails the test if
	// called; gate a bad envelope; runner untouched. The re-ask goes
	// through SendMessage (mailbox), not a spawn.
}

func TestGateAcceptsStructuredFalseButFlags(t *testing.T) {
	// fallback envelope → accepted, verdict notes unstructured; B decided
	// prose beats nothing.
}
```

- [ ] **Step 2: FAIL. Step 3:** implement; wire into the ingestion call site from plan-c
      Task 3 — gate first, ingest `verdict.Accepted` only. **Step 4: PASS with `-race`.**

---

### Task 4: Round bookkeeping + checker dispatch

**Files:**
- Create: `internal/agents/delegation/checkerloop.go`, `checkerloop_test.go`
- Modify: `internal/agents/delegation/incident.go` (round markers), `run.go` (hook)

**Interfaces:**
- Produces:

```go
// RoundState is the Go-side answer to "should the checker run".
type RoundState struct {
	Complete    bool // every delegation in this iteration is terminal
	NewEvidence int  // evidence rows added since the round started
	DryRounds   int  // consecutive completed rounds with zero new evidence
}

func (s *Service) roundState(ctx context.Context, inc *entity.AgentIncident) (RoundState, error)

// OnDelegationFinished is the loop's single entry point, called where
// envelopes finish ingestion. It is a no-op when the tree has no
// incident, when the finished role IS the checker (its result is handled
// by decideNext), or when the round is not yet complete.
func (s *Service) OnDelegationFinished(ctx context.Context, row *entity.AgentDelegation)

func (s *Service) dispatchChecker(ctx context.Context, inc *entity.AgentIncident) // sync Run of evidence-checker with a state_summary task
```

Round membership: a delegation belongs to iteration N via a new column
`entity.AgentDelegation.Iteration int gorm:"not null;default:0"`, stamped from the
incident's current `Iteration` at Create. Evidence delta: store
`EvidenceAtRoundStart int` on the incident (new column, same migration touch) and compare
`COUNT(agent_evidence)`.

- [ ] **Step 1: Failing tests**

```go
func TestRoundIncompleteWhileOneStillRuns(t *testing.T) {}
func TestCompletedRoundWithNewEvidenceDispatchesChecker(t *testing.T) {
	// two investigator rows finish with evidence → exactly one checker
	// Run (fake runner records profile keys), task text contains the
	// incident summary + evidence grouped by kind.
}
func TestTwoDryRoundsStopWithEscalated(t *testing.T) {
	// rounds finishing with zero accepted evidence twice → no checker
	// dispatch, incident.Status = escalated, a reason string on the row.
}
func TestCheckerNotDispatchedForItsOwnCompletion(t *testing.T) {}
```

- [ ] **Step 2: FAIL. Step 3: implement. Step 4: PASS.**

---

### Task 5: Acting on the decision

**Files:**
- Modify: `internal/agents/delegation/checkerloop.go`
- Test: `checkerloop_test.go`

**Interfaces:**
- Consumes: `CheckerDecision` (declared in B's envelope), brakes (Task 1).
- Produces: `func (s *Service) decideNext(ctx context.Context, inc *entity.AgentIncident, d *CheckerDecision)`.

Decision table (each writes status + reason to the incident; every stop is explained):

| Decision | Action |
|---|---|
| `confirmed` | `Status = confirmed`, write `ConfirmedFindings` into `Summary`, stop. |
| `contradiction` | Record contradictions in `NextActions`, `Status = escalated`, stop. |
| `escalate_to_human` | `Status = escalated`, stop. |
| `need_more_evidence` | Write `MissingEvidence` + merge hypotheses; `Iteration++`; snapshot `EvidenceAtRoundStart`; dispatch each `FollowupTasks` entry async (`SinkSession`) through `Run` — governor refusals land on the incident, not swallowed. |
| nil / unparseable / empty decision | Treated as `escalate_to_human`. An unreadable verdict is not a pass. |

Brakes checked in `decideNext` before dispatching follow-ups: `Iteration >= MaxIterations`
→ escalated with reason `"iteration cap"`.

- [ ] **Step 1: Failing tests** — one per row of the table, plus:

```go
func TestNilCheckerDecisionEscalates(t *testing.T) {}
func TestIterationCapStopsFollowups(t *testing.T) {}
func TestFollowupRefusalLandsOnIncident(t *testing.T) {
	// exhaust tree budget first; need_more_evidence with one followup →
	// no spawn, incident NextActions contains the refusal message.
}
```

- [ ] **Step 2: FAIL. Step 3: implement. Step 4: PASS.**

---

### Task 6: Runtime cap + stalled-collect in the sweeper

**Files:**
- Modify: `internal/agents/delegation/sweeper.go` (the `DelegationSweeper` from plan-q Task 4)
- Test: `checkerloop_test.go`

**Interfaces:**
- Consumes: `internal/agents/schedule` store (grep its Create/one-shot API and use it as
  the session-schedule handler in `internal/tools/agents/session_schedule_handler.go`
  does — copy the invocation shape from there).
- Produces: two additional sweeper passes:
  1. incidents `investigating` older than `MaxRuntimeMinutes` → status escalated, reason
     `"runtime cap"`, running delegations interrupted via the existing `Interrupt` (their
     partials are kept by design).
  2. async delegations terminal-and-uncollected for > one sweep interval whose leader
     session has no live agent → ONE one-shot schedule that wakes the leader with
     `"Collect delegation <id> — its result arrived while you were away."`. A marker
     column `CollectNudged bool` on the delegation prevents a nudge per pass.

- [ ] **Step 1: Failing tests** — runtime-cap pass (old incident → escalated + partials
      kept), nudge-once (two sweeps, one schedule row), no-nudge-when-leader-alive.
- [ ] **Step 2: FAIL. Step 3: implement. Step 4: PASS.**

---

### Task 7: Client-draft masking

**Files:**
- Create: `internal/agents/delegation/sanitize.go`, `sanitize_test.go`
- Modify: `internal/agents/delegation/run.go` — applied to the result of a
  `client-response-drafter` delegation before it is returned/delivered

**Interfaces:**
- Produces:

```go
// SanitizeClientDraft masks values that must never reach a customer,
// regardless of what the prompt said: prompt injection that convinces
// the drafter to include a token still produces a masked token.
// Returns the masked draft and whether anything was masked.
func SanitizeClientDraft(draft string, secrets []string, projectRoot string) (string, bool)
```

Masking rules, in order: each non-empty `secrets` value → `***`; absolute paths under
`projectRoot` → basename; when anything masked, the caller appends
`"Note: sensitive values were masked in this draft — review before sending."` to the
result note. Secrets source: the same set the connector masking layer holds — grep
`c.Mask(` / the `secret` tag plumbing in `pkg/connector` and reuse its value registry;
if that registry is not reachable from the delegation package, take `[]string` from a
provider func wired in server.go (read spec: control, not prompt).

- [ ] **Step 1: Failing tests** — a draft containing a seeded secret comes back with
      `***` and masked=true; a clean draft comes back untouched and masked=false; a path
      `D:\code\work\abc\internal\x.go` under root reduces to `x.go`.
- [ ] **Step 2: FAIL. Step 3: implement (plain `strings.ReplaceAll` per secret — value
      masking, no regex on secrets; path rule via `filepath` handling both separators).
      Hook: in `doneResult`/deliver where `row.ProfileKey == "client-response-drafter"`.
      **Step 4: PASS.**

---

### Task 8: Docs

**Files:**
- Modify: `docs/guide/agents/sub-agents.md`

- [ ] The loop end-to-end with a worked example (webhook 401 investigation at `abc.com`):
      mention fan-out → queue → envelopes → incident → checker → follow-up → confirmed →
      masked client draft. The four settings with defaults and what each stop status
      means. The headless `incident-supervisor` and when it exists. English throughout.

## Self-review notes

- Spec "checker split" → roundState/OnDelegationFinished in Go; only dispatchChecker
  spawns. ✔
- Spec "unparseable verdict escalates" → Task 5 nil-decision test. ✔
- Spec "one re-ask then drop" → `IntakeReasked` column + tests. ✔
- Spec "seed does not resurrect deleted" → first-run marker; honest implementation of an
  otherwise contradictory requirement (insert-if-absent alone would resurrect). ✔
- Spec "masking is code not prompt" → Task 7, wired to the drafter's results
  unconditionally. ✔
- New columns introduced in D (`Iteration`, `EvidenceAtRoundStart`, `IntakeReasked`,
  `CollectNudged`) all ride the existing AutoMigrate registration — no separate migration
  step needed; noted so nobody writes one.
