# C — Incident Store: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One lazily-created incident record per delegation tree, an evidence table fed automatically from result envelopes with fingerprint dedup, and an `incident` op for the supervisor.

**Architecture:** Two new entities (`AgentIncident` unique per `root_id`, `AgentEvidence` unique per `(incident_id, fingerprint)`). Creation happens on first write, ingestion runs where delegations finish, and the op gives read/patch/close. B's `MemoryPayload` gains the hypotheses/missing-evidence lines.

**Tech Stack:** Go 1.2x, GORM (postgres + sqlite), Svelte 5.

**Spec:** [c-incident-store.md](c-incident-store.md) — depends on B (plan-b.md) being done.

## Global Constraints

- **Never `git commit`.** The user commits. Every task ends at "tests pass".
- UI copy is English. Samples use `abc.com` / `example.com`.
- Zerolog component-logger pattern. Postgres + sqlite only. Tests `-count=1`. No dead knobs.
- Never edit `*_templ.go` / `dist/`.

---

## File Structure

**Created:**

| Path | Responsibility |
|---|---|
| `internal/entity/agent_incident.go` | `AgentIncident`, `AgentEvidence`, status constants. |
| `internal/agents/delegation/incident.go` | `EnsureForRoot`, ingestion, patch/close logic. |
| `internal/agents/delegation/incident_test.go` | Idempotency, dedup, patch, close. |

**Modified:**

| Path | Change |
|---|---|
| `internal/entity/agent_delegation.go` | `+ IncidentID string`. |
| `internal/pkg/postgres/migrate.go` | Register the two entities (find the list that registers `&entity.AgentDelegation{}` and append). |
| `internal/agents/delegation/run.go` | Ingest envelope evidence in `doneResult` / async finish. |
| `internal/agents/delegation/memorymode.go` | `MemoryPayload` gains incident lines (the seam B left). |
| `internal/connectors/sub-agents/connector.go` | `+ incident` op (Delegation category). |
| `internal/connectors/sub-agents/handlers.go` | `incident` handler. |
| `fe/agents/conversation/src/lib/components/SubAgentPanel.svelte` | Incident section (status, iteration, evidence count). |
| `docs/guide/agents/sub-agents.md` | Incident docs. |

---

### Task 1: Entities + migration + `IncidentID`

**Files:**
- Create: `internal/entity/agent_incident.go`
- Modify: `internal/entity/agent_delegation.go`, `internal/pkg/postgres/migrate.go`
- Test: `internal/agents/delegation/incident_test.go` (create)

**Interfaces:**
- Produces:

```go
// internal/entity/agent_incident.go
package entity

import "time"

const (
	IncidentInvestigating = "investigating"
	IncidentConfirmed     = "confirmed"
	IncidentEscalated     = "escalated"
	IncidentClosed        = "closed"
)

// AgentIncident is what a delegation tree has established so far. One
// per tree, created lazily — a tree that never records evidence and
// never touches the incident op leaves no row.
type AgentIncident struct {
	ID          string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	RootID      string `gorm:"type:varchar(64);not null;uniqueIndex" json:"root_id"`
	ProjectID   string `gorm:"type:varchar(64);not null;default:'';index" json:"project_id"`
	TriggeredBy string `gorm:"type:varchar(128);not null;default:'';index" json:"triggered_by"`

	Status    string `gorm:"type:varchar(32);not null;index" json:"status"`
	Iteration int    `gorm:"not null;default:0" json:"iteration"`
	Title     string `gorm:"type:text;not null;default:''" json:"title"`
	UserIssue string `gorm:"type:text;not null;default:''" json:"user_issue"`
	Summary   string `gorm:"type:text;not null;default:''" json:"summary"`

	// Embedded JSON collections: small, read whole, never queried by
	// element. Evidence is NOT here — it is appended concurrently by
	// many delegations and deduplicated, which is what makes it a table.
	Hypotheses      string `gorm:"type:text;not null;default:'[]'" json:"hypotheses"`
	MissingEvidence string `gorm:"type:text;not null;default:'[]'" json:"missing_evidence"`
	NextActions     string `gorm:"type:text;not null;default:'[]'" json:"next_actions"`
	ClientContext   string `gorm:"type:text;not null;default:'{}'" json:"client_context"`

	FinalSummary string    `gorm:"type:text;not null;default:''" json:"final_summary"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (AgentIncident) TableName() string { return "agent_incidents" }

// AgentEvidence is one verifiable excerpt, deduplicated per incident by
// fingerprint so two investigators finding the same log line produce one
// row.
type AgentEvidence struct {
	ID           string    `gorm:"primaryKey;type:varchar(64)" json:"id"`
	IncidentID   string    `gorm:"type:varchar(64);not null;index:idx_evidence_dedup,unique" json:"incident_id"`
	Fingerprint  string    `gorm:"type:varchar(64);not null;index:idx_evidence_dedup,unique" json:"fingerprint"`
	DelegationID string    `gorm:"type:varchar(64);not null;index" json:"delegation_id"`
	Role         string    `gorm:"type:varchar(128);not null;default:''" json:"role"`
	Kind         string    `gorm:"type:varchar(32);not null" json:"kind"`
	Source       string    `gorm:"type:text;not null;default:''" json:"source"`
	Excerpt      string    `gorm:"type:text;not null;default:''" json:"excerpt"`
	CreatedAt    time.Time `json:"created_at"`
}

func (AgentEvidence) TableName() string { return "agent_evidence" }
```

- `entity.AgentDelegation` gains (after `HopCount`/`Blocked`):

```go
	// IncidentID links this delegation to its tree's incident, when one
	// exists. This column is the source brief's whole agent_tasks table.
	IncidentID string `gorm:"type:varchar(64);not null;default:'';index" json:"incident_id,omitempty"`
```

- [ ] **Step 1:** Write a compile-level failing test — `TestIncidentTableNames` asserting
      the two `TableName()`s, plus sqlite AutoMigrate of both entities succeeds and the
      dedup unique index rejects a duplicate `(incident_id, fingerprint)` insert.

```go
func TestEvidenceDedupIndexHolds(t *testing.T) {
	db := sqliteForTest(t) // follow migrate_test.go's helper
	if err := db.AutoMigrate(&entity.AgentIncident{}, &entity.AgentEvidence{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	e := entity.AgentEvidence{ID: "e1", IncidentID: "i1", Fingerprint: "f1", Kind: "log"}
	if err := db.Create(&e).Error; err != nil {
		t.Fatalf("first: %v", err)
	}
	dup := entity.AgentEvidence{ID: "e2", IncidentID: "i1", Fingerprint: "f1", Kind: "log"}
	if err := db.Create(&dup).Error; err == nil {
		t.Fatal("duplicate fingerprint accepted; want unique-index violation")
	}
}
```

- [ ] **Step 2: FAIL** (entities undefined). **Step 3:** create the file, add the column,
      register both entities in `migrate.go` next to `&entity.AgentDelegation{}`.
- [ ] **Step 4: `go test ./internal/agents/delegation/ ./internal/pkg/postgres/ -count=1` — PASS.**

---

### Task 2: `EnsureForRoot` — lazy, idempotent, concurrent-safe

**Files:**
- Create: `internal/agents/delegation/incident.go`
- Test: `internal/agents/delegation/incident_test.go`

**Interfaces:**
- Consumes: `Repo.DB()`, root row via `Repo.Get`.
- Produces:

```go
// EnsureForRoot returns the tree's incident, creating it on first call.
// Idempotent under concurrency: the unique index on root_id decides the
// winner; the loser re-reads. There is deliberately no "open" action
// anywhere — existence follows from having something to record.
func (s *Service) EnsureForRoot(ctx context.Context, rootID string) (*entity.AgentIncident, error)
func (s *Service) IncidentForRoot(ctx context.Context, rootID string) (*entity.AgentIncident, error) // nil,nil when none
```

- [ ] **Step 1: Failing tests**

```go
func TestEnsureForRootIsIdempotentUnderConcurrency(t *testing.T) {
	svc := serviceForTest(t, DefaultLimits())
	seedRootDelegation(t, svc.Repo, "root1")
	var wg sync.WaitGroup
	ids := make([]string, 8)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			inc, err := svc.EnsureForRoot(context.Background(), "root1")
			if err != nil {
				t.Errorf("ensure: %v", err)
				return
			}
			ids[i] = inc.ID
		}(i)
	}
	wg.Wait()
	for _, id := range ids[1:] {
		if id != ids[0] {
			t.Fatalf("two incidents created: %s vs %s", ids[0], id)
		}
	}
}

func TestIncidentForRootIsNilWhenNothingRecorded(t *testing.T) {
	// fresh root, no ensure call → nil, nil. The lazy contract.
}
```

- [ ] **Step 2: FAIL.** **Step 3:** implement — try `First` by root_id; on not-found,
      insert `{ID: uuid, RootID: rootID, Status: IncidentInvestigating, ProjectID/TriggeredBy
      copied from the root delegation row}`; on a duplicate-key error, re-read and return
      that. Copy the duplicate-key detection idiom from wherever the codebase already
      handles it (grep `ErrDuplicatedKey` — GORM's translated error — and match; sqlite
      and postgres must both pass).
- [ ] **Step 4: PASS** (run the concurrency test with `-race`).

---

### Task 3: Evidence ingestion from envelopes

**Files:**
- Modify: `internal/agents/delegation/incident.go`, `internal/agents/delegation/run.go`
- Test: `internal/agents/delegation/incident_test.go`

**Interfaces:**
- Consumes: `ResultEnvelope` (B), `EnsureForRoot` (Task 2).
- Produces:

```go
// IngestEvidence writes an envelope's evidence to the tree's incident.
// Creates the incident lazily; a conflict on the fingerprint is a
// successful no-op. Returns how many rows were actually new.
func (s *Service) IngestEvidence(ctx context.Context, row *entity.AgentDelegation, env *ResultEnvelope) (int, error)

func evidenceFingerprint(e Evidence) string // sha256 hex of kind|source|excerpt
```

- [ ] **Step 1: Failing tests**

```go
func TestIngestSameEvidenceFromTwoDelegationsOnce(t *testing.T) {
	// two done rows in one root, identical Evidence item in both
	// envelopes → first ingest returns 1, second returns 0, table has
	// one row, no error either time.
}

func TestIngestWithNoEvidenceCreatesNoIncident(t *testing.T) {
	// envelope with findings but zero evidence → IncidentForRoot nil.
	// Findings are interpretations; C stores only what was quoted.
}

func TestIngestStampsDelegationIncidentID(t *testing.T) {
	// after ingest, the delegation row's IncidentID is set.
}
```

- [ ] **Step 2: FAIL.** **Step 3:** implement. Skip entirely when
      `env == nil || len(env.Evidence) == 0`. Insert each item with
      `ON CONFLICT DO NOTHING` semantics — in GORM use
      `clause.OnConflict{DoNothing: true}` (works on both dialects) and count
      `RowsAffected`. Stamp `IncidentID` on the delegation row afterwards. Call site:
      `doneResult` (run.go:706) after the envelope is resolved, and the same for the async
      terminal path if it does not flow through `doneResult` — trace `finish` callers and
      cover both; the test seeds one sync and one async completion.
- [ ] **Step 4: Package PASS.**

---

### Task 4: The `incident` op

**Files:**
- Modify: `internal/connectors/sub-agents/connector.go`, `handlers.go`
- Test: `internal/connectors/sub-agents/` handler tests

**Interfaces:**
- Produces: op `incident` in the Delegation category; patch semantics on update; terminal
  close.

`connector.go` input:

```go
type incidentInput struct {
	Action string `wick:"required;desc=get | update | close."`
	Title  string `wick:"desc=Short incident title, for update."`
	UserIssue string `wick:"textarea;desc=The user-reported problem, for update."`
	Summary   string `wick:"textarea;desc=Current best understanding, for update."`
	Status    string `wick:"desc=investigating | confirmed | escalated, for update."`
	Hypotheses      string `wick:"textarea;desc=JSON array of strings replacing the hypothesis list, for update. Omit to leave unchanged."`
	MissingEvidence string `wick:"textarea;desc=JSON array of strings replacing the missing-evidence list, for update. Omit to leave unchanged."`
	NextActions     string `wick:"textarea;desc=JSON array of strings replacing the next-actions list, for update. Omit to leave unchanged."`
	ClientContext   string `wick:"textarea;desc=JSON object (app id, client name, environment), for update. Omit to leave unchanged."`
	FinalSummary    string `wick:"textarea;desc=Closing summary, required for close."`
}
```

Op description:

```go
	connector.Op("incident", "Work the Incident Record",
		"Read or update this conversation's incident record — the durable state of an investigation. "+
			"get returns status, iteration, summary, hypotheses, missing evidence, next actions, and evidence grouped by kind. "+
			"update patches only the fields you pass; absent fields are untouched. "+
			"close sets a terminal status with a final summary; a closed incident refuses further updates (a human can reopen it in the UI). "+
			"The record is created automatically the first time there is something to store — there is no open action.",
		incidentInput{}, h.incident, wickdocs.Docs{}),
```

- [ ] **Step 1: Failing tests**

```go
func TestIncidentUpdatePatchesOnlyGivenFields(t *testing.T) {
	// ensure incident, update hypotheses only → summary untouched.
}
func TestIncidentCloseThenUpdateRefused(t *testing.T) {
	// close with final summary; update → error "incident is closed".
}
func TestIncidentGetScopesToCallersTree(t *testing.T) {
	// two roots with incidents; caller in tree A gets A, never B.
}
func TestIncidentGetWithNoIncidentSaysSo(t *testing.T) {
	// no row → {exists:false, note:"No incident recorded for this conversation yet."}
	// not an error — "nothing yet" is a normal state.
}
```

- [ ] **Step 2: FAIL.** **Step 3:** handler resolves root via the same
      caller-resolution `delegate` uses (`resolveCaller` + `FindByChildSession` /
      `rootForSession` equivalent in `handlers.go` — reuse, don't duplicate), then `get` →
      `IncidentForRoot` + evidence via a new `Repo.ListEvidence(ctx, incidentID)`;
      `update` → `EnsureForRoot` then a map-based `Updates` with only present fields (JSON
      fields validated with `json.Valid` first, error names the field); `close` →
      guarded update `WHERE status <> 'closed'`, refusing when `FinalSummary` empty.
- [ ] **Step 4: `go test ./internal/connectors/sub-agents/ -count=1` — PASS.**

---

### Task 5: `state_summary` carries the incident (B's seam)

**Files:**
- Modify: `internal/agents/delegation/memorymode.go`, `run.go` (pass incident into the payload)
- Test: `internal/agents/delegation/memorymode_test.go`

**Interfaces:**
- Produces: `MemoryPayload(mode string, siblings []entity.AgentDelegation, inc *entity.AgentIncident) string`
  — signature change; A/B call sites updated.

- [ ] **Step 1: Failing test**

```go
func TestStateSummaryCarriesIncidentLines(t *testing.T) {
	inc := &entity.AgentIncident{
		Hypotheses:      `["signature mismatch on webhook retry"]`,
		MissingEvidence: `["signature header from a failing request"]`,
	}
	got := MemoryPayload(MemorySummary, nil, inc)
	// asserts exactly:
	// current hypotheses: signature mismatch on webhook retry
	// missing evidence: signature header from a failing request
}

func TestStateSummaryWithoutIncidentUnchanged(t *testing.T) {
	// nil incident → byte-identical to the B-era output (no empty headers).
}
```

- [ ] **Step 2: FAIL** (signature). **Step 3:** implement; `Run` fetches
      `IncidentForRoot` only when the mode is `state_summary` or `full_history` — no
      query for modes that will not use it. Malformed JSON in the columns renders
      nothing rather than erroring: the payload is best-effort context.
- [ ] **Step 4: Package PASS.**

---

### Task 6: Panel + docs

**Files:**
- Modify: `fe/agents/conversation/src/lib/components/SubAgentPanel.svelte`,
  `docs/guide/agents/sub-agents.md`
- Test: component test

- [ ] **Step 1:** Extend the sub-agent list payload (`internal/tools/agents/subagents.go`)
      with the tree's incident summary block (`incident: {status, iteration, summary,
      evidence_count} | null`). Failing handler test first, matching Task 7 of plan-q's
      pattern.
- [ ] **Step 2:** Panel: when `incident` is non-null, a compact header above the agent
      list — status chip (investigating→prog, confirmed→pos, escalated→cau, closed→neutral;
      named tokens, `dark:` variants), iteration counter, evidence count. Component test:
      renders for non-null, absent for null.
- [ ] **Step 3:** Docs: what the incident record is, when it appears, the op's three
      actions, and that evidence dedup is automatic. Full Go + vitest suites — PASS.

## Self-review notes

- Spec "no open action" → EnsureForRoot only; op has get/update/close. ✔
- Spec "findings are not auto-ingested" → Task 3 test `NoEvidenceCreatesNoIncident` +
  ingestion reads only `env.Evidence`. ✔
- Spec "dedup is a DB constraint" → composite unique index, `OnConflict DoNothing`. ✔
- Spec "closed refuses update, human reopens in UI" → guarded close; reopen endpoint is
  NOT in this plan (spec says UI reopen matches role-lock precedent — that is D-adjacent
  polish; flagged as deliberately out of scope here, add a line to docs saying closed is
  final for agents). ✔
- B seam honoured with one signature change, both call sites listed. ✔
