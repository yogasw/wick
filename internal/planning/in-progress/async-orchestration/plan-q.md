# Q — Serial Queue per Room: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The parallel cap enqueues instead of refusing; one sub-agent runs at a time per room by default, the rest wait in a visible FIFO.

**Architecture:** `Admit` keeps refusing depth/cycle/budget but no longer refuses on the parallel cap — `Run` writes a `queued` row instead and either blocks on a slot (sync) or returns a queue position (async). A dispatcher in the delegation package starts queued rows when a terminal status frees a slot; a small sweeper pass is the backstop. A `Blocked` flag keeps a parent waiting on a sync child from holding the only slot.

**Tech Stack:** Go 1.2x, GORM (postgres + sqlite), Svelte 5 runes.

**Spec:** [q-serial-queue.md](q-serial-queue.md)

## Global Constraints

- **Never `git commit`.** The user commits. Every task ends at "tests pass".
- UI copy is English. Samples use `abc.com` / `example.com`.
- Zerolog: `l := log.With().Str("component", "x").Logger()`, never `log.Debug()` directly.
- `safeexec`, never `os/exec`.
- Postgres and sqlite dialects only.
- Run Go tests with `-count=1`.
- No dead knobs: every config key is read in the task that adds it.
- Never edit `*_templ.go` or `dist/` output.

---

## File Structure

**Created:**

| Path | Responsibility |
|---|---|
| `internal/agents/delegation/queue.go` | Slot accounting, queue position, the dispatcher. |
| `internal/agents/delegation/queue_test.go` | Admission counting, FIFO, deadlock, cancel. |

**Modified:**

| Path | Change |
|---|---|
| `internal/entity/agent_delegation.go` | `+ Blocked bool`, `+ ContextText string`. |
| `internal/agents/delegation/governor.go` | Parallel check moves out of `Admit` into the queue; `DefaultMaxParallel` 4 → 1. |
| `internal/agents/delegation/repo.go` | `CountActiveByRoot` excludes blocked; `+ SetBlocked`, `+ OldestQueued`, `+ QueuePosition`, `+ ListQueued`. |
| `internal/agents/delegation/run.go` | Split `Run` into admit/record and `execute`; queued path for both modes. |
| `internal/agents/delegation/interrupt.go` | Interrupting a queued row: terminal, no kill, "never started" note. |
| `internal/agents/delegation/sweeper.go` | `+ DelegationSweeper`: clear stale `Blocked`, poke the dispatcher. |
| `internal/agents/config/general.go` | `SubAgentsMaxParallel` default 4 → 1, desc updated. |
| `internal/connectors/sub-agents/handlers.go` | Queue position surfaces in the delegate response. |
| `internal/tools/agents/subagents.go` | Sub-agent list payload carries `queue_position`. |
| `fe/agents/conversation/src/lib/components/SubAgentPanel.svelte` | working / queued / finished grouping, cancel on queued. |

---

### Task 1: `Blocked` + `ContextText` columns, slot counting

**Files:**
- Modify: `internal/entity/agent_delegation.go`, `internal/agents/delegation/repo.go`
- Test: `internal/agents/delegation/queue_test.go` (create)

**Interfaces:**
- Produces: `entity.AgentDelegation.Blocked bool`, `.ContextText string`;
  `Repo.SetBlocked(ctx, id string, blocked bool) error`;
  `Repo.CountActiveByRoot` now counts `status = running AND NOT blocked`.

- [ ] **Step 1: Write the failing test**

```go
// queue_test.go
package delegation

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// A parent waiting on a synchronous child is not doing work and must not
// hold a slot — otherwise a serial room deadlocks the moment a sub-agent
// delegates.
func TestBlockedRowsDoNotCountAsActive(t *testing.T) {
	r := repoForTest(t) // existing helper in this package's tests
	ctx := context.Background()

	seed := func(id string, blocked bool) {
		if err := r.Create(ctx, &entity.AgentDelegation{
			ID: id, RootID: "root1", Status: entity.DelegationRunning,
			Blocked: blocked,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("d1", false)
	seed("d2", true)

	n, err := r.CountActiveByRoot(ctx, "root1")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("active = %d, want 1 (blocked row excluded)", n)
	}

	if err := r.SetBlocked(ctx, "d2", false); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n, _ = r.CountActiveByRoot(ctx, "root1"); n != 2 {
		t.Fatalf("active after clear = %d, want 2", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agents/delegation/ -run TestBlockedRowsDoNotCountAsActive -count=1`
Expected: FAIL — `unknown field Blocked` (compile error).

- [ ] **Step 3: Implement**

`entity/agent_delegation.go`, after `HopCount`:

```go
	// Blocked marks a running delegation whose agent is WAITING on a
	// synchronous child rather than working. A blocked row does not hold
	// a parallel slot — without this, a serial room deadlocks the moment
	// a sub-agent delegates: the parent owns the only slot while its
	// child queues behind it.
	Blocked bool `gorm:"not null;default:false" json:"blocked"`
	// ContextText preserves the caller's context field so a queued
	// delegation can be started later exactly as it was requested.
	ContextText string `gorm:"type:text;not null;default:''" json:"-"`
```

`repo.go` — change the WHERE in `CountActiveByRoot` and add `SetBlocked`:

```go
// CountActiveByRoot counts delegations holding a parallel slot: running
// and not blocked. Queued rows and parents waiting on a sync child do
// not hold slots.
func (r *Repo) CountActiveByRoot(ctx context.Context, rootID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("root_id = ? AND status = ? AND blocked = ?",
			rootID, entity.DelegationRunning, false).
		Count(&n).Error
	return n, err
}

// SetBlocked flips the waiting flag. Guarded to running rows so a
// terminal row can never be resurrected into the slot count.
func (r *Repo) SetBlocked(ctx context.Context, id string, blocked bool) error {
	return r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("id = ? AND status = ?", id, entity.DelegationRunning).
		Update("blocked", blocked).Error
}
```

Check the previous `CountActiveByRoot` body first: if it also counted `queued` (it may —
read `repo.go:369`), keep queued OUT of the new count and note it in the commit message;
queued rows must not consume slots.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/agents/delegation/ -count=1`
Expected: PASS (including existing suite — if an existing governor test asserted queued
rows count as active, update that test's expectation and say so).

---

### Task 2: Queue repo primitives

**Files:**
- Modify: `internal/agents/delegation/repo.go`
- Test: `internal/agents/delegation/queue_test.go`

**Interfaces:**
- Produces: `Repo.OldestQueued(ctx, rootID string) (*entity.AgentDelegation, error)` (nil, nil when empty);
  `Repo.QueuePosition(ctx, rootID, id string) (int, error)` (1-based; 0 = not queued);
  `Repo.ListQueued(ctx, rootID string) ([]entity.AgentDelegation, error)` (FIFO order).

- [ ] **Step 1: Write the failing test**

```go
func TestQueueIsFIFOByStartedAt(t *testing.T) {
	r := repoForTest(t)
	ctx := context.Background()
	base := time.Now().UTC()
	for i, id := range []string{"q1", "q2", "q3"} {
		if err := r.Create(ctx, &entity.AgentDelegation{
			ID: id, RootID: "root1", Status: entity.DelegationQueued,
			StartedAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	head, err := r.OldestQueued(ctx, "root1")
	if err != nil || head == nil || head.ID != "q1" {
		t.Fatalf("head = %v, %v; want q1", head, err)
	}
	if pos, _ := r.QueuePosition(ctx, "root1", "q3"); pos != 3 {
		t.Fatalf("q3 position = %d, want 3", pos)
	}
	if pos, _ := r.QueuePosition(ctx, "root1", "missing"); pos != 0 {
		t.Fatalf("missing position = %d, want 0", pos)
	}
	rows, _ := r.ListQueued(ctx, "root1")
	if len(rows) != 3 || rows[0].ID != "q1" || rows[2].ID != "q3" {
		t.Fatalf("ListQueued order wrong: %v", rows)
	}
}
```

- [ ] **Step 2: Run test — expect FAIL** (`undefined: OldestQueued`).

- [ ] **Step 3: Implement in `repo.go`**

```go
// OldestQueued returns the next delegation in line for a tree, or nil.
// FIFO by StartedAt — the order the leader dispatched is the order it
// expects things back in; there is deliberately no priority column.
func (r *Repo) OldestQueued(ctx context.Context, rootID string) (*entity.AgentDelegation, error) {
	var d entity.AgentDelegation
	err := r.db.WithContext(ctx).
		Where("root_id = ? AND status = ?", rootID, entity.DelegationQueued).
		Order("started_at ASC").First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// QueuePosition is 1-based; 0 means the row is not queued.
func (r *Repo) QueuePosition(ctx context.Context, rootID, id string) (int, error) {
	row, err := r.Get(ctx, id)
	if err != nil || row.Status != entity.DelegationQueued {
		return 0, err
	}
	var ahead int64
	err = r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("root_id = ? AND status = ? AND started_at < ?",
			rootID, entity.DelegationQueued, row.StartedAt).
		Count(&ahead).Error
	return int(ahead) + 1, err
}

// ListQueued returns a tree's waiting line in FIFO order.
func (r *Repo) ListQueued(ctx context.Context, rootID string) ([]entity.AgentDelegation, error) {
	var rows []entity.AgentDelegation
	err := r.db.WithContext(ctx).
		Where("root_id = ? AND status = ?", rootID, entity.DelegationQueued).
		Order("started_at ASC").Find(&rows).Error
	return rows, err
}
```

- [ ] **Step 4: Run tests** — `go test ./internal/agents/delegation/ -count=1` — PASS.

---

### Task 3: Split `Run` — parallel refusal becomes enqueue

**Files:**
- Modify: `internal/agents/delegation/run.go`, `internal/agents/delegation/governor.go`
- Test: `internal/agents/delegation/queue_test.go`

**Interfaces:**
- Consumes: Task 1–2 repo methods.
- Produces: `Run` splits into row construction and `execute(ctx, row, profile) (*Result, error)`;
  `Result.QueuePosition int` json `queue_position,omitempty`;
  governor `Admit` no longer checks the parallel cap; new `Service.hasSlot(ctx, rootID) bool`.

This is the surgical task. `Run` (run.go:255) currently: admit → build row → Create →
spawn → stream. Refactor shape, behaviour preserved for the un-queued path:

1. `Admit` (governor.go:271) drops the `RefusedParallel` branch. Depth, cycle, turn and
   token budget refusals stay exactly as they are. Keep the `RefusedParallel` constant —
   the panel maps reasons and other refusal text still references it.
2. `Run` builds and `Create`s the row as today, but with `Status: entity.DelegationQueued`
   and `ContextText: req.Context` always. Then:
   - slot free (`hasSlot`) → `MarkRunning` → `execute` (the current body from spawn
     onward, extracted verbatim).
   - no slot, `mode == async` → return immediately:
     ```go
     pos, _ := s.Repo.QueuePosition(ctx, rootID, id)
     return &Result{
         DelegationID: id, Profile: profile.Key,
         Status: entity.DelegationQueued, Mode: ModeAsync, QueuePosition: pos,
         Note: fmt.Sprintf("Queued behind %d other(s) in this conversation. Carry on; the result is delivered when it finishes.", pos-1),
     }, nil
     ```
   - no slot, `mode == sync` → `s.waitForSlot(ctx, rootID, id)` then `MarkRunning` +
     `execute`. On ctx cancellation while waiting: `finish(..., DelegationQueued,
     DelegationInterrupted, "", "caller left before this delegation started", 0)` and
     return the interrupt-shaped Result with note `"Cancelled before it started — the
     caller went away while this was queued."`.
3. `hasSlot` and `waitForSlot` live in `queue.go`:

```go
// pokes fans "a slot may have freed" to waiters, keyed by root.
// Closed-over map on Service; see Task 4 for the dispatcher that also
// listens.
func (s *Service) hasSlot(ctx context.Context, rootID string) bool {
	n, err := s.Repo.CountActiveByRoot(ctx, rootID)
	if err != nil {
		return false // fail closed: an unknown count must not overspawn
	}
	return int(n) < s.limits().MaxParallel
}

// waitForSlot blocks until this row is at the head of the queue AND a
// slot is free. Wakes on dispatcher pokes; re-checks on a coarse ticker
// as the lost-poke backstop.
func (s *Service) waitForSlot(ctx context.Context, rootID, id string) error {
	ch := s.subscribeSlot(rootID)
	defer s.unsubscribeSlot(rootID, ch)
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		head, err := s.Repo.OldestQueued(ctx, rootID)
		if err == nil && head != nil && head.ID == id && s.hasSlot(ctx, rootID) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		case <-tick.C:
		}
	}
}
```

4. **The `Blocked` handshake.** In `Run`, before the slot decision, when the caller is
   itself a sub-agent and the call is sync, mark the parent's row blocked and restore it
   on the way out:

```go
	if mode == ModeSync {
		if parentRow, perr := s.Repo.FindByChildSession(ctx, req.ParentSessionID); perr == nil && parentRow != nil {
			if err := s.Repo.SetBlocked(ctx, parentRow.ID, true); err == nil {
				defer func() {
					if cerr := s.Repo.SetBlocked(context.WithoutCancel(ctx), parentRow.ID, false); cerr != nil {
						log.Warn().Err(cerr).Str("delegation", parentRow.ID).
							Msg("delegation: unblock failed; sweeper will clear it")
					}
				}()
			}
		}
	}
```

`context.WithoutCancel` on the clear: a cancelled caller must still release the slot.

- [ ] **Step 1: Write the failing tests**

```go
// Fan-out with MaxParallel=1: one runs, the rest queue in order.
func TestParallelOverflowQueuesInsteadOfRefusing(t *testing.T) {
	svc := serviceForTest(t, Limits{MaxParallel: 1}) // fake-runner helper, see phases_test.go
	first := delegateAsync(t, svc, "researcher", "task one")
	if first.Status != entity.DelegationRunning && first.Status != entity.DelegationDone {
		t.Fatalf("first status = %s", first.Status)
	}
	second := delegateAsync(t, svc, "researcher", "task two")
	if second.Status != entity.DelegationQueued {
		t.Fatalf("second status = %s, want queued", second.Status)
	}
	if second.QueuePosition != 1 {
		t.Fatalf("position = %d, want 1", second.QueuePosition)
	}
}

// A sub-agent delegating synchronously must not deadlock the serial room.
func TestSyncChildOfSubAgentDoesNotDeadlock(t *testing.T) {
	svc := serviceForTest(t, Limits{MaxParallel: 1, MaxDepth: 3})
	// fake runner's agent, mid-run, issues a nested sync Run — the test
	// harness in phases_test.go drives this; assert both finish within
	// the test timeout and neither is stopped_budget/failed.
}
```

(Adapt the two helpers to whatever `phases_test.go` already provides — `serviceForTest`
and the fake runner exist there under other names; reuse, don't duplicate.)

- [ ] **Step 2: Run — expect FAIL** (second delegate returns a Refusal error today).
- [ ] **Step 3: Implement as described above.**
- [ ] **Step 4: `go test ./internal/agents/delegation/ -count=1` — full package PASS.**
      Existing tests asserting `RefusedParallel` from `Admit` move their assertion to the
      queue behaviour (status queued, not error).

---

### Task 4: Dispatcher — start queued work when a slot frees

**Files:**
- Create: `internal/agents/delegation/queue.go` (dispatcher half), `queue_test.go`
- Modify: `internal/agents/delegation/run.go` (`finish` pokes), `internal/agents/delegation/sweeper.go`

**Interfaces:**
- Produces: `Service.pokeSlot(rootID string)`; `Service.startNextQueued(ctx, rootID string)`;
  `DelegationSweeper` with `NewDelegationSweeper(svc *Service, every time.Duration)`,
  `.Start()`, `.Stop()`.

- [ ] **Step 1: Failing test**

```go
func TestQueuedAsyncStartsWhenSlotFrees(t *testing.T) {
	svc := serviceForTest(t, Limits{MaxParallel: 1})
	blockFirst := fakeRunnerHold() // fake runner parks the first agent until released
	first := delegateAsync(t, svc, "researcher", "hold")
	second := delegateAsync(t, svc, "researcher", "next")
	if second.Status != entity.DelegationQueued {
		t.Fatalf("precondition: second should queue")
	}
	blockFirst.Release()
	waitFor(t, 5*time.Second, func() bool {
		row, _ := svc.Repo.Get(context.Background(), second.DelegationID)
		return row.Status != entity.DelegationQueued
	})
}

// Budget gone while waiting: the queued row finishes stopped_budget with
// the refusal message as its result, and never spawns.
func TestQueuedRowRecheckedAgainstGovernorAtStart(t *testing.T) {
	// seed a queued row, exhaust the root turn budget, poke, assert
	// stopped_budget + non-empty result text + fake runner never called.
}
```

- [ ] **Step 2: Run — FAIL** (`undefined: pokeSlot`).
- [ ] **Step 3: Implement**

`finish` (run.go:722) gains one line after a successful terminal write:
`s.pokeSlot(row.RootID)`.

`queue.go` dispatcher:

```go
// startNextQueued admits and executes the head of a tree's queue.
// Re-admission is deliberate: a tree that burned its budget while an
// item waited must fail that item visibly, not run it.
func (s *Service) startNextQueued(ctx context.Context, rootID string) {
	head, err := s.Repo.OldestQueued(ctx, rootID)
	if err != nil || head == nil {
		return
	}
	if !s.hasSlot(ctx, rootID) {
		return
	}
	profile, err := s.Repo.GetProfileScoped(ctx, head.ProjectID, head.ProfileKey)
	if err != nil {
		s.finish(ctx, head, entity.DelegationQueued, entity.DelegationFailed, "", "profile vanished while queued: "+err.Error(), 0)
		return
	}
	if err := s.Repo.Admit(ctx, s.limits(), profile, head.RootID, head.Depth, ancestorsOf(head)); err != nil {
		var refusal *Refusal
		if errors.As(err, &refusal) {
			s.finish(ctx, head, entity.DelegationQueued, refusal.TerminalStatus(), refusal.Message, "", 0)
			return
		}
		return // transient DB error: leave it queued, next poke retries
	}
	if err := s.Repo.MarkRunning(ctx, head.ID); err != nil {
		return
	}
	go s.executeQueued(head, profile) // background: nobody is blocking on it
}
```

`executeQueued` reconstructs the child spec from the row (`Task`, `ContextText`,
`MaxTurns`, `MaxTokens`, `Workspace`, `DeliverySink` are all on it) and calls the same
`execute` extracted in Task 3, then `deliver`s exactly as the async path in `Run` does
today (run.go:451). Only async rows can be started by the dispatcher — a sync row's caller
is blocked inside `waitForSlot` and starts itself; `startNextQueued` must skip a head row
whose `Mode == ModeSync` and instead just `pokeSlot` so the waiter wakes.

`sweeper.go` — new type alongside `StaleClaimSweeper` (which sweeps board claims, not
delegations):

```go
// DelegationSweeper is the queue's backstop: it clears Blocked flags
// stranded by a crash and re-pokes queues whose poke was lost. The poke
// path is the primary mechanism; this loop only has to be eventually
// right, so a coarse interval is fine.
type DelegationSweeper struct { /* svc, every, stop — same shape as StaleClaimSweeper */ }
```

Its pass: `UPDATE agent_delegations SET blocked=false WHERE blocked AND status <> 'running'`
(add `Repo.ClearStaleBlocked(ctx) (int64, error)`), then for each distinct rootID with
queued rows (`Repo.RootsWithQueued(ctx) ([]string, error)`) call `startNextQueued`.
Wire it in `internal/pkg/api/server.go` where `StaleClaimSweeper` is started — same
lifecycle, interval 15s.

- [ ] **Step 4: `go test ./internal/agents/delegation/ -count=1` — PASS.**

---

### Task 5: Interrupt a queued row + collect parity

**Files:**
- Modify: `internal/agents/delegation/interrupt.go`, `internal/agents/delegation/collect.go`
- Test: `internal/agents/delegation/queue_test.go`

**Interfaces:**
- Consumes: existing `Service.Interrupt`, `CollectResult.Pending`.
- Produces: interrupting a queued row → terminal `interrupted`, empty result, note
  `"Cancelled while queued — it never started."`; `Collect` on a queued row → `Pending: true`.

- [ ] **Step 1: Failing tests**

```go
func TestInterruptQueuedRowNeverSpawns(t *testing.T) {
	// seed queued row; svc.Interrupt(...); assert status interrupted,
	// result "", note says it never started, fake runner never called,
	// and a subsequent poke does not start it.
}

func TestCollectOnQueuedRowIsPending(t *testing.T) {
	// seed queued row; Collect; assert Pending true and the existing
	// "still running" note — callers must not need a third state.
}
```

- [ ] **Step 2: Run — FAIL** (Interrupt today assumes a live process; read `interrupt.go`
      first and note the exact failure).
- [ ] **Step 3: Implement.** In `Interrupt`, before the kill path: if the row's status is
      `queued`, `FinishGuarded(ctx, id, DelegationQueued, DelegationInterrupted, "",
      "cancelled while queued", 0)` and return the standard interrupt result with the
      never-started note. `Collect` needs no change if it keys on
      `IsTerminalDelegationStatus` (it does — verify, and the test proves it).
- [ ] **Step 4: Package tests PASS.**

---

### Task 6: Default to serial + config surface

**Files:**
- Modify: `internal/agents/config/general.go`, `internal/agents/delegation/governor.go`
- Test: existing `governor_test.go` expectations

- [ ] **Step 1:** `governor.go`: `DefaultMaxParallel = 1`. Comment: serial is the default
      because four agents interleaving in one room is unreadable and 4× the burn; the knob
      stays for operators who want throughput.
- [ ] **Step 2:** `general.go`: `SubAgentsMaxParallel: 1` in `DefaultGeneralConfig`, and the
      field desc becomes: `desc=Max sub-agents running concurrently within one conversation.
      1 (default) runs them one at a time in a visible queue; raise it for parallel
      throughput at the cost of interleaved output.`
- [ ] **Step 3:** Fix any test asserting the old default (`governor_test.go`,
      `delegation_limits.go` tests). Run `go test ./internal/agents/... ./internal/pkg/api/... -count=1` — PASS.

---

### Task 7: Queue in the API payloads

**Files:**
- Modify: `internal/connectors/sub-agents/handlers.go` (delegate response already carries
  `Result` verbatim — nothing to do if Task 3's `QueuePosition` field is set; verify),
  `internal/tools/agents/subagents.go`
- Test: `internal/tools/agents/sessions_test.go` pattern

**Interfaces:**
- Produces: the sub-agent list endpoint's per-row JSON gains `queue_position` (0 when not
  queued), computed server-side via `Repo.QueuePosition`.

- [ ] **Step 1: Failing test** — list endpoint returns rows where a queued row carries its
      1-based position and a running row carries 0.
- [ ] **Step 2–4:** Wire `QueuePosition` into the row-building loop in `subagents.go`
      (find the handler that feeds the rail panel — it builds from `ListByParent` /
      `ListByRoot`), run, PASS.

---

### Task 8: Panel — working / queued / finished

**Files:**
- Modify: `fe/agents/conversation/src/lib/components/SubAgentPanel.svelte`
- Test: `fe/agents/conversation/src/lib/components/SubAgentPanel.test.ts` (vitest, follow
  the existing component-test pattern in that directory)

- [ ] **Step 1: Failing test** — given rows `[{status:'running'}, {status:'queued',
      queue_position:1}, {status:'done'}]` the component renders three groups in order
      **Working**, **Queued**, **Finished**; the queued row shows `#1`, its task's first
      line, and a Cancel button; no time estimate anywhere.
- [ ] **Step 2: Implement.** Grouping is `$derived` from the rows prop (Svelte 5 runes —
      `{@const}` only under `{#if}`/`{#each}`, hoist to `$derived` otherwise). Cancel
      posts to the existing interrupt endpoint — same call the running-row Stop uses.
      English copy: `Working`, `Queued`, `Finished`, `Cancel`, `Queued #{n}`.
- [ ] **Step 3: `npx vitest run` in `fe/agents/conversation` — PASS.** Then
      `npm run build` and commit the regenerated `dist/` only if the repo convention
      tracks it (it does — `internal/tools/agents/dist` exists; rebuild, don't hand-edit).

---

### Task 9: Docs

**Files:**
- Modify: `docs/guide/agents/sub-agents.md`

- [ ] Describe: rooms run one sub-agent at a time by default; the queue is visible in the
      rail; `queued` status + `queue_position` in delegate/collect responses; what Cancel
      does to a queued row; the `sub_agents_max_parallel` setting and the trade-off of
      raising it. English, generic sample names.

## Self-review notes

- Spec's "sync inherits the MCP call's context" → Task 3 waitForSlot ctx.Done path. ✔
- Spec's "admission re-checks the governor at start" → Task 4 `startNextQueued`. ✔
- Spec's "no priority field" → none added anywhere. ✔
- `RefusedParallel` constant retained but unreferenced by Admit — governor_test cleanup in
  Task 3 Step 4 must delete or repurpose its test, not skip it.
