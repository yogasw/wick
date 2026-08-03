# C — Incident Store

Somewhere to put what an investigation has established, so the answer to "what do we know
so far?" is a query rather than a leader's recollection. One incident per delegation tree,
created the moment there is something to record.

Depends on B (the evidence shape). Prerequisite for D.

## TODO

- [ ] `entity.AgentIncident` + `entity.AgentEvidence`, registered in `migrate.go`.
- [ ] `IncidentID` column on `agent_delegations`.
- [ ] Lazy creation: first evidence write or first `incident` op call.
- [ ] Evidence ingestion from `ResultEnvelope`, with dedup.
- [ ] `incident` op — `get` / `update` / `close`.
- [ ] `state_summary` memory mode carries hypotheses + missing evidence (the seam left in B).
- [ ] Incident panel in the sub-agent rail.

## Schema

```go
type AgentIncident struct {
    ID     string `gorm:"primaryKey;type:varchar(64)"`
    RootID string `gorm:"type:varchar(64);not null;uniqueIndex"` // one per tree
    ProjectID string `gorm:"type:varchar(64);not null;default:'';index"`
    TriggeredBy string `gorm:"type:varchar(128);not null;default:'';index"`

    Status    string `gorm:"type:varchar(32);not null;index"` // investigating | confirmed | escalated | closed
    Iteration int    `gorm:"not null;default:0"`
    Title     string `gorm:"type:text;not null;default:''"`
    UserIssue string `gorm:"type:text;not null;default:''"`
    Summary   string `gorm:"type:text;not null;default:''"`

    // Embedded collections. Small, always read whole, never queried by
    // element — the storage rule this project already follows.
    Hypotheses       string `gorm:"type:text;not null;default:'[]'"` // []Hypothesis
    MissingEvidence  string `gorm:"type:text;not null;default:'[]'"` // []string
    NextActions      string `gorm:"type:text;not null;default:'[]'"` // []string
    ClientContext    string `gorm:"type:text;not null;default:'{}'"` // app id, name, environment

    FinalSummary string `gorm:"type:text;not null;default:''"`
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

```go
type AgentEvidence struct {
    ID           string `gorm:"primaryKey;type:varchar(64)"`
    IncidentID   string `gorm:"type:varchar(64);not null;index"`
    DelegationID string `gorm:"type:varchar(64);not null;index"` // who found it
    Role         string `gorm:"type:varchar(128);not null;default:''"`
    Kind         string `gorm:"type:varchar(32);not null"`
    Source       string `gorm:"type:text;not null;default:''"`
    Excerpt      string `gorm:"type:text;not null;default:''"`
    // Fingerprint is sha256(kind|source|excerpt), unique per incident.
    Fingerprint  string `gorm:"type:varchar(64);not null"`
    CreatedAt    time.Time
}
```

`entity.AgentEvidence` is the stored row; `delegation.Evidence` from B is the wire shape a
sub-agent reports. They are deliberately separate types — the stored row carries
provenance (incident, delegation, role, fingerprint) that a sub-agent has no business
setting.

Evidence is a table, not an embedded JSON column, because it is appended by many
delegations concurrently and deduplicated across them — the two properties that make an
embedded collection wrong. Everything else about the incident is read whole and stays
embedded.

Composite unique index on `(incident_id, fingerprint)`. Dedup is a database constraint
rather than a read-then-write, so two investigators finishing at the same moment cannot
both insert the same log line.

`agent_delegations` gains `IncidentID` — the link the source brief drew as a separate
`agent_tasks` table. The delegation row already carries status, timestamps, error and
once-only collection.

## Lazy creation

No `open` action. The row is created by whichever of these happens first:

- evidence ingestion has something to write, or
- `incident` op is called with `update` or `get`.

Both go through one `EnsureForRoot(ctx, rootID)` that is idempotent under concurrency (the
unique index on `root_id` decides; a duplicate-key error means someone else won, and it
re-reads). A tree that never produces evidence and never touches the op leaves no row.

The alternatives were both worse. Auto-creating at spawn puts an incident row behind every
delegation tree in the product, most of which are code reviews. An explicit `open` puts a
step in front of the model that it will sometimes skip, and every downstream write then has
to handle "no incident".

## Ingestion

When a delegation finishes with an envelope, its `Evidence` items are written to the
incident, tagged with the delegation id and role. A conflict on the fingerprint is a
successful no-op — the same log line found by two investigators is one piece of evidence,
and the second finder is not an error.

`envelope.Findings` are not auto-ingested. A finding is an interpretation, and merging
interpretations from four agents without a checker is how a supervisor ends up confidently
wrong. D's checker promotes findings; C only stores what was quoted.

## The `incident` op

Actions:

- **get** — the incident for this conversation's tree: status, iteration, summary,
  hypotheses, missing evidence, next actions, and the evidence list grouped by kind.
- **update** — patch semantics on summary, hypotheses, missing evidence, next actions,
  client context, status. Absent fields are untouched, so a supervisor can add a hypothesis
  without restating the rest.
- **close** — terminal status plus `final_summary`. A closed incident refuses further
  `update`; reopening is a human action in the UI, matching how role locking already works.

Scoped like everything else in the connector: the caller's session resolves to a root, the
root resolves to the incident. There is no way to name someone else's incident.

## Effect on B

`state_summary` gains two lines when an incident exists:

```
current hypotheses: signature mismatch on webhook retry (medium)
missing evidence: signature header from a failing request
```

That is the whole point of the store — a sub-agent spawned in iteration 3 should not
re-derive what iterations 1 and 2 established.

## Testing

- `EnsureForRoot` twice concurrently yields one row.
- Ingest the same evidence item from two delegations: one row, no error.
- `update` patch semantics: setting only `summary` leaves hypotheses intact.
- `close` then `update` is refused with a message.
- A tree with no evidence and no op call has no incident row.
- `get` from a session outside the tree resolves to that session's own tree, never the
  first one found.
- Evidence excerpt cap from B still applies at the store boundary.
- `state_summary` rendering with and without an incident, exact output.
