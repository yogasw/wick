# Q — Serial Queue per Room

One sub-agent works at a time in a conversation. Everything else waits in a visible FIFO
queue instead of being refused or running alongside. Async dispatches return a queue
position; the rail panel shows the line.

No dependencies. First — A's fan-out is unusable without it.

## TODO

- [ ] Turn the parallel cap from a refusal into an enqueue.
- [ ] `Blocked` flag so a parent waiting on a child does not hold a slot.
- [ ] FIFO admission per `root_id`, ordered by enqueue time.
- [ ] Queue position in the `delegate` response and in `collect`.
- [ ] Change the `SubAgentsMaxParallel` default from 4 to 1 (the config key
      `sub_agents_max_parallel` already exists and is already read per-delegation).
- [ ] A delegation sweeper pass: clear `Blocked` on terminal rows, start queued rows.
- [ ] Cancel a queued delegation before it starts.
- [ ] Queue rendering in the sub-agent rail panel.
- [ ] Docs: what a `queued` status means for a leader.

## What changes

Today `Admit` returns a `Refusal` with reason `parallel` when a tree already has
`MaxParallel` running delegations. The leader is told to try later, and a fan-out of four
mentions produces one run and three rejections.

After this: the row is written with status `queued` (the constant already exists,
`entity.DelegationQueued`) and a dispatcher starts it when a slot frees. Refusals for
depth, cycle, turn budget and token budget are unchanged — those are "this must not
happen", not "not yet".

Default `MaxParallel` drops from 4 to 1. A room runs one sub-agent at a time unless an
operator raises it. The knob stays because a machine with headroom and a team that wants
throughput is a real case; the default is 1 because the failure mode of four agents
writing at once is confusing output and four times the burn, while the failure mode of
serial is waiting.

## The deadlock, and the rule that avoids it

Serialising a whole tree naively deadlocks the moment a sub-agent delegates: the parent
holds the only slot while it blocks on a child that is queued behind it.

The rule: **a slot is held by an agent that is doing work, not by one that is waiting.**

`agent_delegations` gains `Blocked bool`. `Run` sets it on the *caller's* row when the call
is synchronous and the caller is itself a sub-agent, and clears it when the child returns.
Admission counts rows that are `running AND NOT blocked`.

A parent that fires an async child is not blocked — it carries on working and keeps its
slot, and the child queues normally.

The flag is written through the same guarded-update path as the other status writes, and
cleared in a `defer` so a panic or a cancelled context cannot leave a tree permanently
holding a phantom slot. The delegation sweeper pass clears `Blocked` on any terminal row
as a backstop.

## Admission

FIFO per `root_id`, ordered by `StartedAt` (the enqueue moment for a queued row). No
priority field: priority on a queue this short is a knob that invites tinkering and hides
starvation. The order a leader dispatched things in is the order it expects them back in.

The dispatcher is poked immediately whenever a delegation reaches a terminal status, with
a periodic pass as the backstop for a poke lost to a crash. The existing
`StaleClaimSweeper` sweeps **board claims**, not delegation rows — the backstop is a new
small loop in this package (same shape, same package, different table), not a bolt-on to
the claim sweeper.

Admission re-checks the governor at start time, not only at enqueue time. A tree that
burned its budget while an item sat in the queue must not have that item start; it is
finished as `stopped_budget` with the refusal message as its result, so the leader sees
why rather than watching a queued row vanish.

## What a caller sees

**Async.** Returns immediately, as now, with two added fields:

```json
{
  "delegation_id": "D-7f3a",
  "status": "queued",
  "queue_position": 3,
  "note": "Queued behind 2 others in this conversation. Carry on; the result is delivered when it finishes."
}
```

`collect` on a queued delegation returns `pending: true` exactly as it does for a running
one — from the leader's side "not ready" is one state, and splitting it would make every
caller handle two.

**Sync.** Blocks until its turn, then behaves as today. It inherits the MCP call's context
rather than getting a timeout knob of its own: if the caller goes away, the queued row is
cancelled and marked `interrupted` with a note saying the caller left. A knob nobody can
sensibly set is worse than the context that already exists.

A sync call that would wait behind more than a few items is a design smell, and the note on
the async path says so — but wick does not refuse it. Guessing that a leader's blocking
call is a mistake is how a tool becomes unpredictable.

## Cancel before start

A queued delegation can be cancelled by the existing `stop` op and the existing panel
control. Cancelling one that has not started is cheap and total: the row goes terminal, no
process ever spawns, and no partial result is fabricated. The distinction matters in the
result — `interrupted` with an empty result and a note saying it never started, rather than
an empty result that reads like an agent that produced nothing.

## Panel

The sub-agent rail groups by state: **working** (one), then **queued** in position order,
then finished. A queued row shows role, position, the task's first line, and a cancel
control. No time estimate — an honest one is not available, and a dishonest one is worse
than nothing.

Position is computed server-side and delivered with the existing sub-agent list payload, so
the panel does not derive ordering from timestamps it may have out of order.

## Testing

- Four async dispatches with `MaxParallel=1`: one running, three queued at positions 1–3.
- The running one finishes: position 1 starts within one dispatcher poke, positions
  renumber.
- A sub-agent delegating synchronously does not deadlock: parent `Blocked`, child admitted,
  both complete.
- A sub-agent delegating asynchronously keeps its slot; the child queues.
- `Blocked` is cleared when the child fails, when the child is interrupted, and when the
  parent's context is cancelled.
- Budget exhausted while queued: the item finishes `stopped_budget` with the refusal
  message as its result, and never spawns.
- Cancel a queued item: terminal, no spawn, note says it never started.
- FIFO holds across a mixed sync/async dispatch sequence.
- `MaxParallel=3` admits three and queues the fourth — the knob is real, not decorative.
- A cancelled caller context leaves no row stuck in `queued` or `blocked`.
