# B — Structured Results and Memory Modes

A sub-agent's answer is a block of prose today. A supervisor that has to act on it — merge
evidence, decide whether to ask for more, gate a client reply on confidence — has to parse
that prose with a model. This sub-project makes the answer a typed record, and makes what
a sub-agent is *told* an explicit choice instead of whatever the leader happened to paste.

Independent of A. Prerequisite for C and D.

## TODO

- [ ] `ResultEnvelope` type + `agent_delegations.result_json` column.
- [ ] `report_result` op in the Delegation category.
- [ ] Fallback: promote final text to `summary` when the op was never called.
- [ ] Surface the envelope through `Result`, `CollectResult`, and the session wake.
- [ ] `memory_mode` on `delegate` and on `create_agent` (as the role default).
- [ ] Envelope rendering in the sub-agent rail panel.
- [ ] Docs + immutable system-prompt line telling sub-agents to call `report_result`.

## The envelope

```go
// ResultEnvelope is a sub-agent's answer in a shape a supervisor can act
// on without re-reading prose.
type ResultEnvelope struct {
    Summary              string     `json:"summary"`
    Findings             []string   `json:"findings,omitempty"`
    Evidence             []Evidence `json:"evidence,omitempty"`
    Confidence           string     `json:"confidence"` // unknown | low | medium | high
    NeedsFollowup        bool       `json:"needs_followup,omitempty"`
    RecommendedNextTasks []NextTask `json:"recommended_next_tasks,omitempty"`
    // Structured is false when this envelope was reconstructed from the
    // final text because report_result was never called.
    Structured bool `json:"structured"`
}

type Evidence struct {
    Kind   string `json:"kind"`   // log | code | doc | data | observation
    Source string `json:"source"` // file:line, query, URL, table
    Excerpt string `json:"excerpt"`
}

type NextTask struct {
    Role   string `json:"role"`
    Task   string `json:"task"`
    Reason string `json:"reason"`
}
```

D extends this struct with one optional `Checker *CheckerDecision` field for the
`evidence-checker` role. Nothing in B reads it; it is named here so the struct is not a
surprise later.

Stored as JSON in one new column, `agent_delegations.result_json`. `Result` (the prose)
stays and stays authoritative for humans — the panel and the transcript keep rendering it.
Nothing that reads `Result` today changes behaviour.

`Evidence.Excerpt` is capped server-side (4 KB per item, 20 items) before the write. An
uncapped excerpt list is how a sub-agent turns a token cap into a database problem.

## How the envelope gets filled: `report_result`

A new op in the existing `Delegation` category:

> **report_result** — Report your finished work as structured fields so the agent that
> delegated to you can act on it without re-reading your prose. Call this once, as the last
> thing you do. Evidence must be quoted, not described: a source and an excerpt someone
> else could verify.

Input fields map to the envelope one for one. The handler resolves the caller's delegation
row via `FindByChildSession(c.SessionID())` — the same lookup `delegate` already uses to
inherit a root — and writes `result_json`. Calling it twice overwrites; the last call
before the turn ends wins.

Refused with a plain message when the caller is not a sub-agent. A leader has no
delegation row to write to, and silently accepting the call would leave a model believing
it reported something.

**Fallback.** A model that finishes without calling `report_result` still produces a
result: `doneResult` builds an envelope with `Summary` = the final text, `Confidence` =
`unknown`, `Structured` = false. The consequence is visible rather than silent — a
supervisor reading `structured: false` knows the fields it wants were never asserted.
Failing the run instead would punish the leader for the sub-agent's omission and throw away
work that is already paid for.

Envelope-producing behaviour is stated once in the immutable system prompt, so every role
inherits it without every role's author remembering to write it.

## Delivery

- `delegation.Result` and `CollectResult` gain `Envelope *ResultEnvelope`.
- The MCP response for `delegate` (sync) and `collect` carries it. The prose stays in
  `result`, so an existing caller sees no change.
- The `delivery_sink=session` wake message renders the envelope compactly — summary,
  findings, confidence, and the count of evidence items with a pointer to `collect` for the
  full list. Waking a leader with 20 excerpts spends its context on data it may not need.

## Memory modes

`delegate` gains `memory_mode`; `create_agent` gains the same field as the role default.
Resolution mirrors `NormalizeMode`: explicit request, then role default, then
`state_summary`.

| Mode | What reaches the sub-agent |
|---|---|
| `no_history` | The task, nothing else. |
| `state_summary` *(default)* | The task, plus one line per completed delegation in this tree: role, status, `envelope.Summary`. |
| `relevant_chunks` | The task, plus the caller's `context` field verbatim. The leader curates; wick does not guess. |
| `full_history` | The task, plus the full `Result` of every completed delegation in this tree. |

`context` remains a free-text field and is appended in every mode — a leader always gets to
say something. The mode governs what *wick* adds on top.

`full_history` is documented as audit-and-debug only, in the op description the model
reads, with the reason stated: it is expensive, it is noisy, and it biases a fresh agent
toward the conclusions of the agents before it.

Sibling summaries are read at spawn time and are a snapshot; the roster block from A already
establishes that spawn-time context is a snapshot, and the same wording is reused.

Once C lands, `state_summary` additionally carries the incident's current hypotheses and
missing-evidence list. That is a change to one function in C, noted here so the seam is
obvious.

## Testing

- Envelope round-trip through sqlite: write, read, field-for-field equality.
- `report_result` from a leader session is refused with a message naming the reason.
- Two `report_result` calls: the second wins.
- Fallback: a run that never calls the op yields `Structured=false`, `Confidence=unknown`,
  `Summary` = final text.
- Caps: 30 evidence items in, 20 stored; a 10 KB excerpt is truncated to 4 KB.
- `NormalizeMemoryMode`: request beats role default beats `state_summary`; an unknown
  string is rejected at the op boundary, not silently defaulted.
- Each memory mode's rendered envelope, exact output, with two completed siblings in the
  tree.
- An existing `delegate` caller that ignores `envelope` still receives unchanged `result`
  and `status`.
