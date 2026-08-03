# A — Mention Router

Turn `@handle task text` into real work. A mention addressed to an agent already working
in the tree becomes a message; a mention addressed to a role that is not running yet
spawns that role. Both paths already exist as services — this sub-project is the
resolution rule and the wiring that calls them.

## TODO

Depends on Q — a fan-out of four mentions is only sane once the room runs them one at a
time.

- [ ] Roster resolution (`roster.go`): live handles ∪ visible role keys, one lookup.
- [ ] `Router.Route` (`router.go`): resolve, decide mode, dispatch, report.
- [ ] Turn observer: accumulate an agent's turn text, route on `Done`.
- [ ] Wire the human path in `sendAgent`.
- [ ] Wire the agent path in the spawn factory callbacks.
- [ ] Roster block in the task envelope at spawn.
- [ ] `SubAgentsMentionRouter bool` on `GeneralConfig` (key `sub_agents_mention_router`,
      Sub-agents group), read by the router per call via the limits provider pattern.
- [ ] Docs: mention section in `docs/guide/agents/sub-agents.md` + the immutable system prompt.

## Why this and not `delegate`

`delegate` works and stays. The router is a second entry point with a different cost
profile: writing `@log-investigator check the 401s` costs one line, while the equivalent
`delegate` call costs a tool round-trip and a model that remembered to make it. For
fan-out — four investigators on one incident — the difference decides whether the pattern
gets used at all.

`ParseMentions` (`internal/agents/delegation/mention.go`) already exists, is tested against
a false-positive corpus, and has no production caller. This is that caller.

## Resolution

```go
// Target is what an @token resolved to.
type Target struct {
    Kind   TargetKind // TargetAgent | TargetRole | TargetUnknown
    Handle string     // live instance, when Kind == TargetAgent
    Role   string     // profile key, when Kind == TargetRole
}
```

Order matters and is not negotiable:

1. **Live handle in this tree** → `TargetAgent`. Message it via the existing
   `Service.SendMessage`.
2. **Visible role key** (project role shadowing global, same scope rule `list_agents`
   uses) → `TargetRole`. Spawn via `Service.Run`.
3. **Neither** → `TargetUnknown`. The token stays plain text; nothing happens, nothing is
   logged as an error.

An agent that is already working beats spawning a second instance of its role. The
opposite order would let `@code-investigator follow up on that` start a fresh agent with
no memory of the thing it is following up on.

`ParseMentions` is called with the union of both name sets as its roster, so its existing
guards — line-leading only, body required, fenced code skipped — apply unchanged.
Ambiguity cannot occur: handles are allocated from role keys with a numeric suffix
(`reviewer`, `reviewer-2`), and step 1 runs first regardless.

## Mode

- One mention in a text → the role's own default mode (`profile.Mode`, already resolved by
  `NormalizeMode`).
- Two or more mentions in one text → every dispatch is forced to `async` with
  `delivery_sink=session`. Fan-out that blocks is not fan-out; the first sync call would
  hold the leader while the rest queue behind it.
- Fan-out does not mean parallel execution. Q serialises the room: four mentions produce
  one running sub-agent and three queued, in the order they were written. The router only
  decides *what* is dispatched; Q decides *when* each one runs.
- A mention resolving to `TargetAgent` uses `kind=tell`, never `ask`. `ask` blocks the
  sender, and the sender here is a turn that has already ended.

## Wiring

Two call sites, not three.

**Human turn** — `internal/tools/agents/handler.go`, `sendAgent`, beside the existing
`resetHopsForSession` call. Route the text *before* it reaches `SendWithAttachments`, but
send it either way: the leader must see what the human wrote, mentions included.

**Agent turn** — a turn observer hooked into the spawn factory's `OnEvent` and `OnExit`
callbacks (`internal/pkg/api/server.go`, beside `agentsBcast.Publish`). It accumulates
`TextDelta` per `(session, agent)` and routes the accumulated text on `Done`, then clears
the buffer. Sub-agents spawn through the same pool and the same factory, so one observer
covers the leader and every sub-agent; `run.go` is not touched.

The observer is the only place that reads agent output for mentions. Routing from
`run.go`'s inline turn handling as well would dispatch each mention twice.

## Feedback to the author

The original text is never rewritten or stripped — a leader that sees its own words edited
loses the thread. Instead the router appends one line per dispatch to the next thing that
agent receives:

```
dispatched: @log-investigator (running, D-7f3a) · @docs-investigator (queued #1, D-7f3b)
```

The queue position comes straight from Q's response, so an author fanning out four
investigators can see that three of them have not started yet rather than assuming all
four are already reading.

For an unknown token, nothing is appended. Silence is correct: most `@` tokens in agent
output are not mentions, and a "could not resolve @ts-ignore" line trains the model to
avoid a syntax it was never using.

## Roster at spawn

A sub-agent currently learns the roster only when a peer messages it
(`FormatInbound`, `internal/agents/delegation/format.go`). A freshly spawned agent
therefore has no idea anyone else exists and never considers asking. The task envelope
gains a roster block:

```
roster (snapshot at spawn — call list_agents for the current list):
  @main (leader, working) · @code-investigator (code-investigator, working)
spawnable roles: log-investigator, docs-investigator, data-validator
left: 34/40 turns · 10/10 hops
Message a peer with the message op, or open a line with @handle at the start of a line.
```

`FormatInbound`'s comment argues against a spawn-time snapshot because the roster goes
stale. That holds for a snapshot presented as current; it is answered by labelling the
snapshot and naming the op that refreshes it. The rendering helper is shared between the
two call sites so the format cannot drift.

Spawnable roles are listed by key only — descriptions would grow the envelope without
changing the decision, and `list_agents` returns them on demand.

## Config

One key, following the existing pattern exactly: a `SubAgentsMentionRouter bool` field on
`GeneralConfig` (Sub-agents group), which `StructToConfigs` surfaces as
`sub_agents_mention_router` in the settings UI. Default on. Read on every `Route` call the
way `delegationLimitsProvider` re-reads the governor ceilings, so an operator can turn the
behaviour off without a restart. Off means mentions stay plain text — the pre-existing
behaviour exactly.

No per-role opt-out. A role that should not be mentionable is a role that should not be
delegatable, and `Disabled` already covers that.

## Failure handling

Routing is best-effort and never fails the turn that carried the mention. A dispatch that
the governor refuses is reported on the feedback line with the refusal message
(`Refusal.Message` is already written as guidance to a model), so the author learns it hit
a cap rather than watching a silent no-op.

A mention inside a turn whose session has no delegation tree yet is fine: `Run` creates
the root.

## Testing

Table-driven, against the existing fake runner used by `phases_test.go`.

- Resolution precedence: a token matching both a live handle and a role key resolves to
  the agent.
- Unknown token: no dispatch, no error, text unchanged.
- False positives: reuse the `mention_test.go` corpus through the full router, asserting
  zero dispatches.
- Multi-mention: two mentions force async on both, even for a role whose default is sync.
- Feedback line: exact-output test, including the refusal case.
- Observer: `TextDelta`×3 then `Done` routes once with the concatenated text; a second
  `Done` with no deltas routes nothing.
- Roster block: exact-output test, and a test that a spawn with no peers omits the roster
  line rather than printing an empty one.
- Kill switch: `mention_router_enabled=false` dispatches nothing.
