# B — Structured Results + Memory Modes: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sub-agent answers become a typed `ResultEnvelope` filled via a mandatory `report_result` op (with a safe fallback), and what a sub-agent is told at spawn becomes an explicit `memory_mode`.

**Architecture:** One JSON column on `agent_delegations`, one new connector op that writes it, a fallback in `doneResult` that reconstructs an envelope from prose, and a `memory_mode` resolved like `NormalizeMode` that governs what `composeTask` appends. No behaviour change for callers that ignore the new fields.

**Tech Stack:** Go 1.2x, GORM (postgres + sqlite), Svelte 5.

**Spec:** [b-structured-results.md](b-structured-results.md) — independent of Q/A.

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
| `internal/agents/delegation/envelope.go` | `ResultEnvelope`, `Evidence`, `NextTask`, caps, fallback builder. |
| `internal/agents/delegation/envelope_test.go` | Caps, fallback, round-trip. |
| `internal/agents/delegation/memorymode.go` | Mode constants, `NormalizeMemoryMode`, per-mode rendering. |
| `internal/agents/delegation/memorymode_test.go` | Resolution + exact-output rendering. |

**Modified:**

| Path | Change |
|---|---|
| `internal/entity/agent_delegation.go` | `+ ResultJSON string` column. |
| `internal/entity/agent_profile.go` | `+ DefaultMemoryMode string`. |
| `internal/agents/delegation/repo.go` | `+ SaveResultJSON`, envelope read in Get paths. |
| `internal/agents/delegation/run.go` | Fallback in `doneResult`; memory-mode payload in `composeTask` call. |
| `internal/agents/delegation/collect.go` | `CollectResult.Envelope`. |
| `internal/connectors/sub-agents/connector.go` | `+ report_result` op + input struct; `+ memory_mode` on delegate + create_agent inputs. |
| `internal/connectors/sub-agents/handlers.go` | `reportResult` handler; memory_mode validation. |
| `internal/agents/system-prompt/immutable.md` | "Report via report_result" section. |
| `fe/agents/conversation/src/lib/components/SubAgentPanel.svelte` | Envelope rendering (confidence chip, findings list). |
| `docs/guide/agents/sub-agents.md` | Envelope + memory modes docs. |

---

### Task 1: Envelope type, caps, storage

**Files:**
- Create: `internal/agents/delegation/envelope.go`, `envelope_test.go`
- Modify: `internal/entity/agent_delegation.go`, `internal/agents/delegation/repo.go`

**Interfaces:**
- Produces:

```go
const (
	ConfidenceUnknown = "unknown"
	ConfidenceLow     = "low"
	ConfidenceMedium  = "medium"
	ConfidenceHigh    = "high"
)

const (
	maxEvidenceItems  = 20
	maxExcerptBytes   = 4096
)

type Evidence struct {
	Kind    string `json:"kind"`   // log | code | doc | data | observation
	Source  string `json:"source"`
	Excerpt string `json:"excerpt"`
}

type NextTask struct {
	Role   string `json:"role"`
	Task   string `json:"task"`
	Reason string `json:"reason"`
}

type ResultEnvelope struct {
	Summary              string     `json:"summary"`
	Findings             []string   `json:"findings,omitempty"`
	Evidence             []Evidence `json:"evidence,omitempty"`
	Confidence           string     `json:"confidence"`
	NeedsFollowup        bool       `json:"needs_followup,omitempty"`
	RecommendedNextTasks []NextTask `json:"recommended_next_tasks,omitempty"`
	Structured           bool       `json:"structured"`
	// Checker is filled only by the evidence-checker role (sub-project D).
	Checker *CheckerDecision `json:"checker,omitempty"`
}

// CheckerDecision is declared here so the envelope is one type; D wires
// the behaviour.
type CheckerDecision struct {
	Decision          string     `json:"decision"`
	ConfirmedFindings []string   `json:"confirmed_findings,omitempty"`
	Contradictions    []string   `json:"contradictions,omitempty"`
	MissingEvidence   []string   `json:"missing_evidence,omitempty"`
	FollowupTasks     []NextTask `json:"followup_tasks,omitempty"`
}

func (e *ResultEnvelope) Clamp()                      // caps + confidence normalisation
func FallbackEnvelope(finalText string) *ResultEnvelope
func (r *Repo) SaveResultJSON(ctx context.Context, id string, e *ResultEnvelope) error
func EnvelopeOf(d *entity.AgentDelegation) *ResultEnvelope // nil when column empty/garbage
```

- `entity.AgentDelegation` gains:

```go
	// ResultJSON is the structured envelope reported via report_result,
	// or reconstructed from the final text when the op was never called.
	// Prose in Result stays authoritative for humans; this column is for
	// the supervisor.
	ResultJSON string `gorm:"type:text;not null;default:''" json:"result_json,omitempty"`
```

- [ ] **Step 1: Failing tests**

```go
func TestClampCapsEvidence(t *testing.T) {
	e := &ResultEnvelope{Confidence: "HIGH"}
	for i := 0; i < 30; i++ {
		e.Evidence = append(e.Evidence, Evidence{Kind: "log", Source: "s", Excerpt: strings.Repeat("x", 10_000)})
	}
	e.Clamp()
	if len(e.Evidence) != 20 {
		t.Fatalf("items = %d, want 20", len(e.Evidence))
	}
	if len(e.Evidence[0].Excerpt) != 4096 {
		t.Fatalf("excerpt = %d bytes, want 4096", len(e.Evidence[0].Excerpt))
	}
	if e.Confidence != ConfidenceHigh { // case-normalised
		t.Fatalf("confidence = %q", e.Confidence)
	}
}

func TestClampNormalisesUnknownConfidence(t *testing.T) {
	e := &ResultEnvelope{Confidence: "0.8"}
	e.Clamp()
	if e.Confidence != ConfidenceUnknown {
		t.Fatalf("got %q", e.Confidence)
	}
}

func TestFallbackEnvelope(t *testing.T) {
	e := FallbackEnvelope("the webhook signature was stale")
	if e.Structured || e.Confidence != ConfidenceUnknown || e.Summary != "the webhook signature was stale" {
		t.Fatalf("bad fallback: %+v", e)
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	r := repoForTest(t)
	ctx := context.Background()
	seedDelegation(t, r, "d1") // helper exists in package tests
	in := &ResultEnvelope{Summary: "s", Confidence: ConfidenceMedium, Structured: true,
		Evidence: []Evidence{{Kind: "log", Source: "loki: app=abc", Excerpt: "401 signature_invalid"}}}
	if err := r.SaveResultJSON(ctx, "d1", in); err != nil {
		t.Fatalf("save: %v", err)
	}
	row, _ := r.Get(ctx, "d1")
	out := EnvelopeOf(row)
	if out == nil || out.Summary != "s" || out.Evidence[0].Excerpt != "401 signature_invalid" {
		t.Fatalf("round-trip lost data: %+v", out)
	}
}
```

- [ ] **Step 2: Run — FAIL** (compile).
- [ ] **Step 3: Implement.** `Clamp` truncates the slice then each excerpt (bytes, not
      runes — cap is a storage bound, and cutting a rune mid-sequence is acceptable for a
      capped excerpt; note it). Confidence lower-cased then checked against the enum,
      else `unknown`. `EnvelopeOf` returns nil on empty column or unmarshal error —
      garbage in the column must read as "no envelope", never panic.
- [ ] **Step 4: `go test ./internal/agents/delegation/ -run 'TestClamp|TestFallback|TestEnvelopeRoundTrip' -count=1` — PASS.**

---

### Task 2: `report_result` op

**Files:**
- Modify: `internal/connectors/sub-agents/connector.go`, `internal/connectors/sub-agents/handlers.go`
- Test: `internal/connectors/sub-agents/connector_test.go` (enum/description tests will
  catch the new op automatically — extend where they enumerate), handler test beside the
  existing ones

**Interfaces:**
- Consumes: Task 1 types, `Repo.FindByChildSession`, `Repo.SaveResultJSON`.
- Produces: op `report_result` in the Delegation category.

- [ ] **Step 1: Input struct + op registration** (`connector.go`):

```go
type reportResultInput struct {
	Summary    string `wick:"required;textarea;desc=Your finished answer in a few sentences. This is what the delegating agent acts on."`
	Findings   string `wick:"textarea;desc=One finding per line. A finding is a conclusion you are prepared to defend."`
	Evidence   string `wick:"textarea;desc=JSON array of {kind, source, excerpt}. kind: log|code|doc|data|observation. Quote real material — a claim with no excerpt is a guess."`
	Confidence string `wick:"desc=low, medium, or high. How sure you are of the summary overall."`
	NeedsFollowup        bool   `wick:"desc=True when the task is not fully answered and someone should continue."`
	RecommendedNextTasks string `wick:"textarea;desc=JSON array of {role, task, reason} for work you recommend dispatching next."`
}
```

Registered after `collect` in the Delegation category:

```go
	connector.Op("report_result", "Report Your Result",
		"Report your finished work as structured fields so the agent that delegated to you can act on it without re-reading prose. "+
			"Call this ONCE, as the last thing you do before your closing message. "+
			"Evidence must be quoted, not described: a source and an excerpt someone else could verify. "+
			"If you never call this, your closing message is used as the summary with confidence 'unknown'.",
		reportResultInput{}, h.reportResult, wickdocs.Docs{}),
```

- [ ] **Step 2: Failing handler tests**

```go
func TestReportResultRefusesALeader(t *testing.T) {
	// caller session has no delegation row → error mentioning "only a
	// sub-agent can report a result".
}

func TestReportResultWritesTheEnvelopeAndLastCallWins(t *testing.T) {
	// child session with a running row: call twice with different
	// summaries; EnvelopeOf(row).Summary is the second, Structured true.
}

func TestReportResultRejectsMalformedEvidenceJSON(t *testing.T) {
	// evidence "not json" → error naming the field and the expected
	// shape; row untouched.
}
```

- [ ] **Step 3: Implement `handlers.go`:**

```go
func (h *handlers) reportResult(c *connector.Ctx) (any, error) {
	row, err := h.deps.svc().Repo.FindByChildSession(c.Context(), c.SessionID())
	if err != nil || row == nil {
		return nil, errors.New("only a sub-agent can report a result; this session has no delegation")
	}
	env := &delegation.ResultEnvelope{
		Summary:    strings.TrimSpace(c.Input("summary")),
		Confidence: strings.TrimSpace(c.Input("confidence")),
		NeedsFollowup: c.InputBool("needs_followup"),
		Structured: true,
	}
	if raw := strings.TrimSpace(c.Input("evidence")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &env.Evidence); err != nil {
			return nil, errors.New("evidence must be a JSON array of {kind, source, excerpt}")
		}
	}
	if raw := strings.TrimSpace(c.Input("recommended_next_tasks")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &env.RecommendedNextTasks); err != nil {
			return nil, errors.New("recommended_next_tasks must be a JSON array of {role, task, reason}")
		}
	}
	for _, line := range strings.Split(c.Input("findings"), "\n") {
		if f := strings.TrimSpace(line); f != "" {
			env.Findings = append(env.Findings, f)
		}
	}
	env.Clamp()
	if err := h.deps.svc().Repo.SaveResultJSON(c.Context(), row.ID, env); err != nil {
		return nil, fmt.Errorf("report_result: %w", err)
	}
	return map[string]any{"recorded": true, "note": "Recorded. Finish with a short closing message; do not repeat the full report as prose."}, nil
}
```

(`c.InputBool` — check `pkg/connector` for the actual bool accessor name and use that.)

- [ ] **Step 4: `go test ./internal/connectors/sub-agents/ -count=1` — PASS**, including
      the existing every-op-described test now covering `report_result`.

---

### Task 3: Fallback + surface in results

**Files:**
- Modify: `internal/agents/delegation/run.go` (`doneResult`, line ~706), `collect.go`
- Test: `internal/agents/delegation/envelope_test.go`

**Interfaces:**
- Produces: `Result.Envelope *ResultEnvelope` json `envelope,omitempty`;
  `CollectResult.Envelope *ResultEnvelope`; wake text includes summary/confidence line.

- [ ] **Step 1: Failing tests**

```go
func TestDoneResultFallsBackToProseEnvelope(t *testing.T) {
	// fake-runner run that never calls report_result → Result.Envelope
	// has Structured=false, Summary == final text; row.ResultJSON persisted.
}

func TestDoneResultKeepsAReportedEnvelope(t *testing.T) {
	// seed ResultJSON via SaveResultJSON mid-run → Result.Envelope has
	// Structured=true and the reported summary, NOT the final text.
}

func TestCollectCarriesEnvelope(t *testing.T) { /* async done row → CollectResult.Envelope non-nil */ }
```

- [ ] **Step 2: FAIL.** **Step 3:** in `doneResult`, after the fresh re-read: if
      `EnvelopeOf(fresh) == nil`, build `FallbackEnvelope(out)`, persist with
      `SaveResultJSON` (best-effort, Warn on failure), attach to `res.Envelope`; else
      attach the read one. `Collect`/`CollectPending` attach via `EnvelopeOf`. The session
      wake (`deliver`, run.go:476 → `DeliverToSession`) prepends one line when an envelope
      exists: `fmt.Sprintf("[%s confidence] %s (%d evidence items — collect %s for detail)", env.Confidence, env.Summary, len(env.Evidence), row.ID)`.
- [ ] **Step 4: Package PASS.**

---

### Task 4: Memory modes

**Files:**
- Create: `internal/agents/delegation/memorymode.go`, `memorymode_test.go`
- Modify: `internal/entity/agent_profile.go`, `internal/agents/delegation/run.go`,
  `internal/connectors/sub-agents/connector.go` (delegate + create_agent inputs),
  `internal/connectors/sub-agents/handlers.go` (validation + pass-through)

**Interfaces:**
- Produces:

```go
const (
	MemoryNone     = "no_history"
	MemorySummary  = "state_summary"
	MemoryChunks   = "relevant_chunks"
	MemoryFull     = "full_history"
)

func ValidMemoryMode(m string) bool                       // "" allowed
func NormalizeMemoryMode(requested, profileDefault string) string // default state_summary
// MemoryPayload renders what wick adds to the task envelope for a mode.
// Siblings are this tree's completed delegations.
func MemoryPayload(mode string, siblings []entity.AgentDelegation) string
```

- `entity.AgentProfile` gains `DefaultMemoryMode string
  gorm:"type:varchar(32);not null;default:''"`.
- `Request` gains `MemoryMode string`; `delegateInput` gains
  `MemoryMode string wick:"desc=What this sub-agent is told beyond the task: no_history (nothing), state_summary (default; one line per finished sibling), relevant_chunks (your context field verbatim, curated by you), full_history (every sibling's full result — audit/debug only, expensive and biasing)."`;
  `createAgentInput` gains the same field described as the role default.

- [ ] **Step 1: Failing tests**

```go
func TestNormalizeMemoryMode(t *testing.T) {
	cases := []struct{ req, def, want string }{
		{"", "", MemorySummary},
		{"no_history", "full_history", MemoryNone},
		{"", "relevant_chunks", MemoryChunks},
	}
	// ...table assert
}

func TestMemoryPayloadStateSummary(t *testing.T) {
	sibs := []entity.AgentDelegation{
		doneRow("log-investigator", `{"summary":"401s spike at 10:02","confidence":"high","structured":true}`),
		doneRow("docs-investigator", ""), // no envelope → falls back to first line of Result
	}
	got := MemoryPayload(MemorySummary, sibs)
	want := "What this conversation's other agents have established (snapshot):\n" +
		"- log-investigator (done): 401s spike at 10:02\n" +
		"- docs-investigator (done): <first line of its result>\n"
	// exact-output assert with a concrete Result seeded in doneRow
}

func TestMemoryPayloadNoHistoryIsEmpty(t *testing.T)    {}
func TestMemoryPayloadFullHistoryCarriesResults(t *testing.T) {}
func TestUnknownMemoryModeRejectedAtOpBoundary(t *testing.T) {
	// handler test: delegate with memory_mode "vibes" → error listing the
	// four valid values; no row created.
}
```

- [ ] **Step 2: FAIL.** **Step 3:** implement; in `Run`, resolve
      `mem := NormalizeMemoryMode(req.MemoryMode, profile.DefaultMemoryMode)`, fetch
      siblings via `ListByRoot` filtered terminal when `mem` needs them, and extend the
      `composeTask` call: task + memory payload + context. `relevant_chunks` adds nothing
      beyond context (context IS the payload); `no_history` suppresses the payload but
      NEVER the context field. `create_agent` handler stores the default after
      `ValidMemoryMode` check; `list_agents` output gains `memory_mode` so a leader can
      see it.
- [ ] **Step 4: `go test ./internal/agents/delegation/ ./internal/connectors/sub-agents/ -count=1` — PASS.**

---

### Task 5: Immutable prompt + panel + docs

**Files:**
- Modify: `internal/agents/system-prompt/immutable.md`,
  `fe/agents/conversation/src/lib/components/SubAgentPanel.svelte`,
  `docs/guide/agents/sub-agents.md`
- Test: existing prompt-load test; component test

- [ ] **Step 1:** Immutable prompt, in the sub-agent section: *When you work as a
      sub-agent, finish by calling `report_result` with your summary, findings, quoted
      evidence, and confidence — then close with a short message. Prose alone is recorded
      with confidence "unknown".*
- [ ] **Step 2:** Panel: a finished row with an envelope shows a confidence chip
      (`unknown`→neutral, `low`→cau ramp, `medium`→prog, `high`→pos — named tokens, dark
      variants) and an expandable findings/evidence list. `structured: false` rows show
      the chip as `unreported`. Component test: chip text per confidence + the
      unreported case.
- [ ] **Step 3:** Docs: the envelope fields, the fallback rule, the four memory modes with
      the same trade-off wording as the op description. `npx vitest run` + full Go suite —
      PASS.

## Self-review notes

- Spec caps (20 items / 4 KB) → Task 1 constants + tests. ✔
- Spec "refused for a leader" → Task 2 test. ✔
- Spec "last call wins" → Task 2 test. ✔
- Spec "existing callers unchanged" → Task 3 test asserts result/status fields untouched;
  envelope is additive. ✔
- Spec seam for C ("state_summary later carries hypotheses") → `MemoryPayload` is the one
  function C extends; said in its doc comment. ✔
- `CheckerDecision` declared here (D fills it) — matches spec's cross-reference. ✔
