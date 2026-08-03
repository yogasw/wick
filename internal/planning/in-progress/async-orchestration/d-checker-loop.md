# D — Checker Loop, Production Roles, Brakes

The part that makes an investigation finish. Six roles to do the work, a checker that
decides whether the evidence holds, brakes that stop the loop, and a sanitiser so a client
never receives a stack trace.

Depends on A, B and C.

## TODO

- [ ] Seed seven global roles on boot (insert-if-absent).
- [ ] Intake gate: mechanical validation of every incoming result.
- [ ] Round bookkeeping in Go: when is a round complete, did it add evidence.
- [ ] Checker dispatch + `CheckerDecision` envelope extension.
- [ ] Follow-up dispatch from `followup_tasks`, bounded by iteration count.
- [ ] Brakes: `MaxIterations`, `MaxRuntimeMinutes`, `MinConfidence`, `NoNewEvidenceRounds`.
- [ ] Client-response masking on the way out.
- [ ] Auto-scheduled collect for a stalled async delegation.
- [ ] Docs: the investigation loop, end to end.

## Who supervises

**The leader is the supervisor.** The agent already in the room owns the incident, holds
the conversation with the human, and — once A lands — can fan work out by mention. Adding a
supervisor sub-agent on top of it would put a second orchestrator one level down, arguing
with the first about who is in charge, and would spend a spawn on coordination rather than
work.

One exception, and it gets its own role: a run with **no human in the room** — triggered by
a webhook, a cron, or a channel message nobody is watching. There the leader has no one to
report to and no one to ask, so the seeded `incident-supervisor` role exists with
`CanDelegate = true` and a prompt that says explicitly when to stop and escalate rather
than ask. It is not used when a person is present.

Distribution of work is not a separate mechanism: the supervisor writes its plan into
`incident.next_actions` (C) and dispatches it by mention (A). The plan is therefore
inspectable — a human can read what the supervisor intends before the sub-agents finish,
and edit it.

## Two gates, not one

"Validate the sub-agent's work" is two different jobs and they belong in different places.

**Intake gate — mechanical, in Go, on every result.** Runs before an envelope is ingested
into the incident:

- `Structured` is false → accepted, flagged, and the incident records that this role did not
  report structurally. Not a rejection; B already decided prose is better than nothing.
- An evidence item with an empty `Source` or an empty `Excerpt` → rejected.
- `Confidence` outside the enum → normalised to `unknown`.
- `Findings` present with zero evidence items → flagged as unsupported, and the checker is
  told so.

A rejected item is sent back to the sub-agent as one follow-up turn naming what was missing
("evidence item 2 has no source — quote it or drop the claim"). **Once.** A second failure
drops the item and records the drop. Unbounded re-asking is how a turn budget disappears
into formatting arguments.

No model is called for any of this. Whether a string is empty is not a judgement.

**Judgement gate — the `evidence-checker` role.** Contradiction between sources,
sufficiency, and what is still missing. That is the part that needs reading comprehension,
and it is the only part that gets a spawn.

## The seven roles

Seeded as **global** profiles on boot, insert-if-absent by key. An operator who edits one
keeps their edit across restarts; a deleted one does not come back. Seeding is not
`Locked` — these are starting points, and the first thing a team does with them is tune the
prompts for their own stack.

| Key | Does | Default mode |
|---|---|---|
| `log-investigator` | Query logs, group errors, build a timeline, quote samples. | async |
| `code-investigator` | Map errors to a code path, name a probable cause and its risk. | async |
| `docs-investigator` | Find expected behaviour, runbooks, known issues. | async |
| `data-validator` | Check config, flags, and data anomalies for the affected tenant. | async |
| `evidence-checker` | Compare findings across sources; name contradictions and gaps. | sync |
| `client-response-drafter` | Draft a reply for the customer from confirmed findings only. | sync |
| `incident-supervisor` | Plan, dispatch, decide next actions. Headless runs only. `CanDelegate = true`. | async |

The four investigators default to async because each is a long read against a large
surface; the two that run on already-collected material default to sync because the
supervisor is waiting on their answer to decide what happens next.

Every seeded prompt states the evidence rule explicitly: quote a source and an excerpt, or
report it as a gap. A finding with no excerpt is a guess wearing a finding's clothes.

## Splitting the checker

The source brief puts the whole loop inside the `@evidence-checker` role. Two of its jobs
are bookkeeping and belong in Go:

- **Is the round complete?** — every delegation in this incident's current iteration is in
  a terminal status. A query.
- **Did this round add evidence?** — count of `agent_evidence` rows for this incident
  changed since the round started. A query.

Paying for a spawn to answer either is expensive and occasionally wrong. Go answers both,
then dispatches the checker for the part that needs judgement: do the findings agree, what
contradicts what, and what is still missing.

The checker's answer extends the envelope from B:

```go
type CheckerDecision struct {
    Decision        string     `json:"decision"` // confirmed | need_more_evidence | contradiction | escalate_to_human
    ConfirmedFindings []string `json:"confirmed_findings,omitempty"`
    Contradictions    []string `json:"contradictions,omitempty"`
    MissingEvidence   []string `json:"missing_evidence,omitempty"`
    FollowupTasks     []NextTask `json:"followup_tasks,omitempty"`
}
```

Reported through `report_result` like any other role, carried in an optional `checker`
field on the envelope. A checker that returns no decision is treated as
`escalate_to_human` — an unparseable verdict is not a pass.

## The loop

```
result arrives
  └─ intake gate (Go)
        evidence missing source/excerpt → one re-ask, then drop
        otherwise                       → ingest into the incident

round completes (Go)
  └─ evidence added this round?
        no, twice in a row  → stop, status escalated
        yes                 → dispatch evidence-checker (sync)
              confirmed          → incident.status = confirmed, stop
              contradiction      → record, stop, status escalated
              escalate_to_human  → stop, status escalated
              need_more_evidence → write missing_evidence + hypotheses to the incident,
                                   dispatch followup_tasks async, iteration++
```

Every stop writes a status and a reason to the incident. A loop that ends without saying
why is indistinguishable from one that is still running.

Follow-up dispatch goes through the same `Service.Run` and the same governor as everything
else — the loop gets no privileged path around the caps.

## Brakes

Added to `Limits` in `governor.go`, each read by code in the task that adds it:

| Knob | `GeneralConfig` field → key | Default | Effect |
|---|---|---|---|
| `MaxIterations` | `SubAgentsMaxIterations` → `sub_agents_max_iterations` | 5 | Checker rounds per incident. On overrun: stop, status escalated. |
| `MaxRuntimeMinutes` | `SubAgentsMaxRuntimeMin` → `sub_agents_max_runtime_min` | 20 | Wall clock from incident creation. On overrun: stop, collect partials, status escalated. |
| `MinConfidence` | `SubAgentsMinConfidence` → `sub_agents_min_confidence` | `medium` | Enum gate on `client-response-drafter`. Below it, the drafter is not dispatched and the supervisor is told why. |
| `NoNewEvidenceRounds` | `SubAgentsNoEvidenceRounds` → `sub_agents_no_evidence_rounds` | 2 | Consecutive empty rounds before stopping. |

All four join the Sub-agents settings group and are re-read per decision through
`delegationLimitsProvider`, like every existing ceiling — operator changes apply to the
next round, no restart. The no-dead-knobs rule holds: each key lands in the same task as
the code that reads it.

`MinConfidence` is an enum because the brief's own §9 uses one; the `0.8` in its §13 is
false precision. `NoNewEvidenceRounds` is 2 rather than the brief's implicit 1: an
investigation that comes up empty once and lands the next round is the normal case, and
stopping at the first empty round throws away the run.

`MaxRuntimeMinutes` is enforced by the delegation sweeper pass Q introduces (the existing
`StaleClaimSweeper` walks board claims, not delegations) rather than a goroutine per
incident.

`max_tool_calls` from the brief is not implemented — see the README for why.

## Client response

`client-response-drafter` is prompted to work only from confirmed findings and to omit
internal detail. That is guidance, and guidance is not a control. The draft passes through
wick's existing masking before it is returned:

- Config values marked `secret` are masked by value.
- The incident's own connection strings, tokens and internal hostnames are masked by value.
- Absolute paths inside the project root are stripped to their basename.

Masking runs on the way out of the op, so a prompt injection that convinces the drafter to
include a token still produces a masked token. A draft that gets masked is reported as such
so a human reviews before sending rather than discovering `***` in front of the customer.

The drafter never sends anything. It returns a draft.

## Auto-scheduled collect

`delivery_sink=session` is the normal path and usually enough. It fails in one case that
matters: the leader has exited and the wake cannot be delivered. Q's delegation sweeper
pass covers this too: an async delegation terminal-and-uncollected for longer than a grace
period gets a one-shot through `internal/agents/schedule` that wakes the leader session
with a collect prompt.

Grace period is one interval of that sweeper, not a new config key. A knob nobody
will ever tune is a knob that should not exist.

## Testing

- Intake gate: an evidence item with no source is re-asked once, then dropped, and the drop
  is recorded on the incident.
- Intake gate: findings with zero evidence reach the checker flagged as unsupported.
- Intake gate calls no model — asserted with a runner that fails the test if it spawns.
- `incident-supervisor` is not dispatched when the trigger carries a human session; it is
  when the trigger is a webhook or cron.
- Round completion: a round with one delegation still running is not complete.
- Empty round twice stops; empty then non-empty continues.
- `MaxIterations` overrun stops with status escalated and a reason on the incident.
- `MaxRuntimeMinutes` overrun collects partial results rather than discarding them.
- `MinConfidence`: a `low` incident does not dispatch the drafter, and the refusal names
  confidence as the reason.
- A checker returning an unparseable or empty decision is treated as escalate.
- Follow-up dispatch is refused by the governor when the tree budget is gone, and the
  refusal reaches the incident rather than being swallowed.
- Masking: a draft containing a seeded secret value comes back masked, and the response
  flags that masking occurred.
- Seeding: a second boot does not overwrite an edited role; a deleted role stays deleted.
- Sweeper schedules exactly one collect for a stalled delegation, not one per pass.
