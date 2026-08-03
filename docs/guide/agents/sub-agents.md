---
outline: deep
---

# Sub-agents

A **sub-agent** is another agent that your agent hands one self-contained task to, waits for, and gets an answer back from. Use it when a piece of work wants a different role — research, code review, image generation — or when the intermediate steps would flood the main conversation.

::: info Source
Delegation core: [`internal/agents/delegation/`](https://github.com/yogasw/wick/blob/master/internal/agents/delegation) — [`run.go`](https://github.com/yogasw/wick/blob/master/internal/agents/delegation/run.go) (spawn + turn counting), [`governor.go`](https://github.com/yogasw/wick/blob/master/internal/agents/delegation/governor.go) (limits), [`interrupt.go`](https://github.com/yogasw/wick/blob/master/internal/agents/delegation/interrupt.go) (stop).
Rows: [`entity.AgentProfile`](https://github.com/yogasw/wick/blob/master/internal/entity/agent_profile.go), [`entity.AgentDelegation`](https://github.com/yogasw/wick/blob/master/internal/entity/agent_delegation.go).
Agent surface: the [`sub-agents` connector](https://github.com/yogasw/wick/blob/master/internal/connectors/sub-agents).
Web UI: [`internal/tools/agents/subagents.go`](https://github.com/yogasw/wick/blob/master/internal/tools/agents/subagents.go).
:::

## Mental model

A **profile** is a reusable role: provider, model, system prompt, tool access, turn budget. A **delegation** is one call against that role. A profile is either global or owned by a project — see [Scope](#scope-global-and-per-project).

The delegating agent (the *leader*) calls the `sub-agents` connector's `delegate` op, which blocks. wick spawns a fresh session for the profile, feeds it the task, waits for it to answer, and returns the final text as the tool's result. The leader then carries on with that answer in hand.

```
leader session
      │  sub-agents.delegate(profile: "researcher", task: "…")
      ▼
isolated child session  ── clean context, same project/cwd
      │  runs its turns
      ▼
final text ──▶ returned to the leader as the tool result
```

The child starts with a **clean context** — it cannot see the leader's conversation. Everything it needs must be in `task`, with any extra background in `context`. That isolation is the point: it keeps the sub-agent cheap and focused, and it keeps the leader's history from leaking into a role that has no business seeing it.

## Scope: global and per-project

A role lives in one of two scopes.

| Scope | Who sees it | Edited from |
|---|---|---|
| **Global** | Every project, and every session with no project | Sidebar → **Sub-agents** |
| **Project** | Only sessions in that project | Project settings → **Sub-agents** |

A project role whose key matches a global one **shadows** it: sessions in
that project get the project's version, and the global role is untouched
everywhere else. That is how a project swaps a role's provider or prompt
without asking anyone to change the shared one.

```
global      researcher (claude)   reviewer (claude)
project     researcher (codex)    db-migrator (claude)

a session in that project sees
            researcher (codex)    reviewer (claude)   db-migrator (claude)
```

Shadowing is created deliberately, through **Override** on an inherited
role — not by happening to reuse a key. Another project's roles are never
visible, whatever they are called.

::: warning Off by default
Sub-agents spawn real processes and spend real tokens, so the feature ships disabled. Turn on **Sub-agents enabled** under Agents settings first.
:::

## Enabling and configuring

Two levels of configuration, deliberately separate:

| Level | What it sets | Where |
|---|---|---|
| **Governor** (system-wide) | Master switch, `max_depth`, per-tree turn budget, `max_parallel`, hard turn ceiling | Agents settings → **Sub-agents** |
| **Profile** (per role) | Provider, model, system prompt, tool tags, default turns, `can_delegate` | Sidebar → **Sub-agents** (global) or Project settings → **Sub-agents** (project) |

Governor values are **ceilings**. A profile can ask for less; it can never raise them. If a profile asks for 200 turns and the ceiling is 50, it gets 50 — clamped, not rejected, so an over-ambitious profile still works rather than failing.

### Governor defaults

| Setting | Default | What it stops |
|---|---|---|
| Sub-agents enabled | `false` | Everything — the emergency stop and the staged-rollout lever |
| Max depth | `3` | A sub-agent delegating to a sub-agent, forever |
| Turn budget per tree | `40` | One conversation quietly burning an unbounded number of turns |
| Max parallel | `1` | More than one sub-agent streaming into a room at once — see [One at a time: the queue](#one-at-a-time-the-queue) |
| Max turns ceiling | `50` | Any single sub-agent running away |
| Max hops | `10` | Two agents messaging each other in a loop between human turns |
| Ask timeout | `10 min` | A blocking `ask` waiting forever for an answer |
| Inbox cap | `20` | A fast agent burying a slow one under messages it will never read |

Turning the master switch off takes effect on the **next delegation** — no restart. Sub-agents already running are left to finish.

## The operations

Delegation is reached through the **`sub-agents` connector**, not through
top-level tools: `wick_get "sub-agents"` to resolve it, then `wick_execute`
per op. That buys it the connector contract — an admin page, tag
visibility, and run history — at the cost of one resolution hop.

Nine ops: `list_agents`, `delegate`, `collect`, `create_agent`, `list_access`, `tasks`, `message`, `reply`, `stop`. The last three are covered in [Talking to other agents](#talking-to-other-agents).

### `list_agents`

Lists the roles this caller may delegate to — `key`, `name`, `description`,
`provider`, and `scope` (`global` or `project`). Call it before `delegate`:
the agent's system prompt tells it to, precisely so it uses a key that
exists rather than guessing one.

### `delegate`

| Input | Required | Notes |
|---|---|---|
| `profile` | yes | Role key from `list_agents` |
| `task` | yes | The complete, self-contained instruction |
| `context` | no | Extra background; not the leader's transcript |
| `max_turns` | no | Clamped to the system ceiling |

Emitting several `delegate` calls in one turn queues them behind whichever sub-agent is already running, up to `max_parallel` concurrently — `1` by default, so they run **one at a time**; see [One at a time: the queue](#one-at-a-time-the-queue). There is no batch op — the queue falls out of multiple calls naturally.

The result always carries a `status`:

| Status | Meaning | What the leader should do |
|---|---|---|
| `done` | Complete answer | Use it |
| `interrupted` | A **human** pressed Stop | Read the note; do *not* silently retry |
| `stopped_max_turns` | Hit its turn cap | Result is partial — use it or ask the user |
| `stopped_budget` | The tree's budget ran out | Summarise with what is already there |
| `failed` | Runtime error | Report it |

### `create_agent`

An agent can define its own roles — and, calling it again with the same
`key`, patch one it already defined. On a first create, `key`,
`description` and `system_prompt` are all required — a role without a
description is invisible to the reasoning that picks it, and one without
a prompt is a generic assistant wearing a role's name. On a patch, any
field left out keeps its current value, so raising a turn budget cannot
accidentally blank out the prompt.

The role is created in **the calling conversation's project**, never
globally. That scoping is what makes the op safe to hand to every user:
a role an agent invents is reachable only from the project it was already
working in, and a key that collides with a global role shadows it there
without touching the shared one. A conversation with no project is
refused rather than silently creating something global.

Creating a **global** role stays admin-only and is done from the
Sub-agents page.

Besides the basics, `create_agent` also takes:

| Input | What it does |
|---|---|
| `allowed_tags` | Comma-separated tag ids narrowing which tools/connectors the role may use. Call `list_access` first to see what you can grant — narrowing only ever *restricts*, it can never hand a role access you do not already have. Empty inherits everything you can reach. |
| `can_delegate` | Lets the role delegate and define roles of its own. Off by default: most roles should do their own work. |
| `allow_take_over` | Lets a human send messages into this role's running sub-agents mid-run (see [Take-over](#take-over)). |
| `mode` | `sync` (default, returns the answer to the caller) or `async` (returns immediately, delivers later). |
| `workspace` | `shared` (default) or `worktree` for a private git worktree. Falls back to shared, with a note, on a project that is not a git repo. |
| `icon` | A single emoji shown beside the role in lists. |
| `max_tokens` | Token budget for one delegation of this role. `0` adds no cap of its own; the per-tree budget still applies. |
| `disabled` | Keeps the role on record but hides it from every roster. A disabled role cannot be delegated to. |
| `locked` | Freezes the role — see [Locking a role](#locking-a-role). One-way from MCP: an agent can lock, never unlock. |
| `allowed_native_tools` | Comma-separated provider-native tool names. **Stored but not enforced today** — nothing forwards it to the spawn, so it does not restrict what the role can call. |
| `strict_mcp` | Meant to drop the host's own MCP servers from the spawn. **Stored but not enforced today** — `WICK_STRICT_MCP` decides this globally, identically for every role. |

Every field the web form has is reachable here, so an agent can define a
role that is actually right rather than one it has to ask a human to
finish. The two marked *not enforced* are the exception worth knowing:
they save and read back, and nothing acts on them yet.

## Locking a role

A role you rely on can be frozen. Tick **Locked** on the role and save:
from then on nothing edits or deletes it — not the web form, and not an
agent calling `create_agent` over MCP. An agent that tries is told the
role is locked and where to unlock it, rather than being left to retry the
same call.

Unlocking is a web-UI action, and only a web-UI action. Untick **Locked**
and save; that save changes nothing else, so editing a locked role is
deliberately two steps. An agent may lock a role it created, but it can
never unlock one — otherwise the lock would guard nothing.

Whoever can edit a role can lock and unlock it: an admin for a global
role, anyone with access to the project for a project role. Locked also
blocks **delete** — without that, a role could be removed and recreated
under the same key with different behaviour, which is the very thing the
lock exists to prevent.

**A stopped sub-agent returns partial work, not an error.** That is deliberate: a tool error reads to a model as "that call failed, try again," which is the exact opposite of what someone who just clicked Stop wanted.

## Watching and stopping

Delegations appear in the **Sub-agents** rail panel on the conversation, one row each: profile, status, task, turns used, and a first look at the result. The tab appears only once a session has delegated, and its badge counts **live** sub-agents — so the badge empties when work finishes while the results stay readable.

Three ways to stop things:

| Action | Where | Effect |
|---|---|---|
| **Stop** | A panel row | That one sub-agent stops; partial work goes back to the leader, which keeps running |
| **Stop all** | Panel header | Every sub-agent under this session stops; the leader keeps running |
| **Kill** | Conversation header | The leader *and* every descendant stop |

Stop works on a **queued** sub-agent too, not just a running one — it is dropped from the queue rather than killed. Without that, the button would appear to do nothing and the work would run anyway.

If a sub-agent happens to finish in the instant between your click and the server handling it, its real result stands and nothing is overwritten.

## Talking to other agents

Delegation hands work down one level and waits. Messaging is the other direction: an agent that is already running can be reached, asked a question, and answered — without re-explaining what it is doing.

Every agent in a conversation has a **handle**: the leader is `@main`, and each sub-agent gets its role key, deduplicated (`reviewer`, `reviewer-2`). Handles address an *instance*, not a role, so a second reviewer is a separate correspondent.

| Op | What it does |
|---|---|
| `message` with `kind=tell` | Delivers and returns immediately. For progress reports and hand-offs. |
| `message` with `kind=ask` | Waits for that agent's answer and returns it. For something the sender cannot continue without. |
| `reply` | Answers a question, using the `message_id` it arrived with. |
| `stop` | Ends another agent's work here; its partial result is kept, not discarded. |

An agent whose process has already exited is **resumed** when a message arrives, with its earlier work intact. If the transcript cannot be recovered, the sender is told plainly that the agent is answering fresh — a confident answer from an agent that has forgotten the question is worse than no answer.

Messages queue per recipient and arrive as **one turn**, not one turn each, so a burst does not cost a model round per line. Each delivery carries the live roster and what is left of the budget:

```
── from @reviewer says ──
2 of 5 files done. auth.go looks wrong.

roster: @main (leader, working) · @reviewer (code-reviewer, working)
left: 12/40 turns left · 3/10 hops left · 660k/1000k tokens left
```

### The hop limit

Two agents can trade short messages cheaply for a very long time, which is why turn and token budgets are not enough on their own. A **hop** is one agent-to-agent message; the default limit is 10 consecutive hops **between human turns**.

When it runs out, sending is refused and the agents are told to summarise and report — nothing is killed, and every agent stays addressable. The counter resets whenever a person sends a message, or when someone clicks **Allow 10 more** in the rail panel. Agents cannot reset it themselves: a leader deep in a loop is exactly the one most convinced it needs more.

Configure it under Settings → Sub-agents: `Max hops`, `Ask timeout` (how long a blocking ask waits before giving up — the question stays in the inbox either way), and `Inbox cap` (how far behind one agent may fall before senders are refused).

## Sub-agent sessions

A sub-agent's session is a **real** session — own transcript, own store — but it is hidden from the conversation list and shown in its parent's rail panel instead. Opening a sub-agent's URL directly redirects to the parent with the panel open on that child, so old links keep working.

It also starts pre-titled from the first line of its `task`, so the rail panel shows something readable instead of a generic placeholder or the sub-agent spending a turn titling a session nobody but its parent will ever open.

## Permissions

Access attaches to the **human** who started the delegation, never to the profile.

```
effective tools = your tags ∩ (profile's allowed tags, if it sets any)
```

A profile can only **narrow** what a sub-agent may reach. Listing a tag you do not hold grants nothing, so an admin cannot build a profile that escalates whoever calls it. A profile with no tag list simply inherits the caller's own access.

Under the hood a sub-agent authenticates to wick's MCP server with a **scoped, short-lived token** carrying that intersected tag set — not the internal token normal spawns use, which maps to an admin principal and would bypass tag filtering entirely. The token is revoked when the delegation ends.

::: warning Strict MCP is not a per-role control yet
A profile's **Strict MCP** and **Allowed native tools** fields are stored
but not read at spawn. Whether a sub-agent sees the host CLI's own MCP
servers is decided globally by the `WICK_STRICT_MCP` environment
variable, the same way for every agent.

Two consequences. A role cannot currently be given — or denied — the
host's MCP servers on its own; and if those servers are configured, every
sub-agent inherits them, since their tools were never brokered by wick
and so never passed the tag filter above.
:::

Other rules:

- **Editing a global profile is admin-only.** It is reachable from every project, so it answers to the whole install.
- **Editing a project profile needs access to that project.** It is confined to sessions in that project, so the project's own membership is the bar.
- **Stopping** is limited to the person who triggered the tree, or an admin.
- A leader may stop **its own** children — never a sibling, its parent, or another user's delegation.

## Async delegation

By default a delegation is **synchronous**: the leader waits. Some work does not deserve that — a research write-up whose reader is a human, not the leader's next step. Pass `mode: "async"` and `delegate` returns immediately with a `delegation_id`.

An async result reaches you through its **delivery sink**:

| Sink | Where the result goes |
|---|---|
| `channel` (default) | Posted back into the conversation that started it |
| `session` | Re-prompts the leader, waking it with the result |
| `none` | Recorded only; visible in the panel and monitor |

The leader can also **pull**: `collect` with a `delegation_id`, or with no arguments to list everything waiting. A delegation still running comes back `pending` rather than blocking.

A delivered result reads `@<handle> finished (<status>) · <elapsed>`, followed by its text — named by handle, not just profile key, so a second reviewer's result is distinguishable from the first. In the web UI it also arrives tagged with a `subagent` source rather than looking like something the user typed, so it's clearly marked as an agent reporting back.

A result is handed over **exactly once**. Collecting the same delegation twice returns it flagged as a repeat, because acting on the same answer twice duplicates whatever the leader did with it.

::: info Async sub-agents are detached
They were fired to run on their own, so killing the leader does **not** stop them. An explicit **Stop all** does.
:::

## Structured results

A sub-agent's answer comes back as prose *and* as typed fields. The prose stays authoritative for people; the fields exist so the agent that delegated can act without re-reading a write-up.

A sub-agent fills them by calling `report_result` before it finishes:

| Field | Meaning |
|---|---|
| `summary` | The answer, in a few sentences. Required. |
| `findings` | Conclusions the agent is prepared to defend. |
| `evidence` | `{kind, source, excerpt}` — quoted material someone else could verify. `kind` is `log`, `code`, `doc`, `data`, or `observation`. |
| `confidence` | `low`, `medium`, or `high`. |
| `needs_followup` | The task is not fully answered. |
| `recommended_next_tasks` | `{role, task, reason}` work worth dispatching next. |

The result carries them alongside the prose:

```json
{
  "delegation_id": "d-7f3a",
  "status": "done",
  "result": "the retry path drops the signature header …",
  "envelope": {
    "summary": "401s come from the retry path dropping the signature header",
    "confidence": "high",
    "evidence": [{"kind": "log", "source": "loki: app=abc", "excerpt": "401 signature_invalid"}],
    "structured": true
  }
}
```

**A forgotten call is not a failure.** If a sub-agent never calls `report_result`, its closing message becomes the `summary`, `confidence` is `unknown`, and `structured` is `false`. Failing the run instead would punish the *leader* for the sub-agent's omission and throw away work already paid for. The rail panel labels these **Unreported** rather than "unknown confidence" — the distinction that matters is whether anyone claimed anything, not how sure they were.

Evidence is capped at 20 items and 4 KB per excerpt. Without a bound, a sub-agent can turn its token budget into a database problem by quoting a whole log file.

## Memory modes

A sub-agent starts with a clean context, so what it knows is exactly what it is told. `memory_mode` makes that an explicit choice instead of an accident of whatever the leader pasted into `context`:

| Mode | What wick adds to the task |
|---|---|
| `no_history` | Nothing. For work whose answer does not depend on what anyone else found. |
| `state_summary` *(default)* | One line per finished sibling: role, status, its summary. |
| `relevant_chunks` | Nothing — your `context` field is the payload. The leader curates. |
| `full_history` | Every sibling's full result. |

Set it per call on `delegate`, or as a role default with `create_agent`. A call always wins over the role.

`context` is appended in every mode, so a leader can always say something of its own.

::: warning full_history is for audit and debugging
It is expensive, it is noisy, and it biases a fresh agent toward the conclusions of the agents before it — which is exactly what you do not want when the point of a second opinion is that it is independent.
:::

## The investigation loop

Put the pieces together and an incident investigates itself: mentions fan work out, the queue runs it one at a time, results come back structured, evidence lands in the incident, and a checker decides whether it holds.

### The seven roles

Installed as global roles on first boot. They are starting points, not fixtures — unlocked, editable, and never overwritten once you have touched them. A role you delete stays deleted.

| Role | Does | Runs |
|---|---|---|
| `log-investigator` | Groups errors, builds a timeline, quotes log lines. | async |
| `code-investigator` | Maps symptoms to a code path and names a probable cause. | async |
| `docs-investigator` | Establishes what the system is *supposed* to do. | async |
| `data-validator` | Checks the tenant's config, flags and data. Read-only. | async |
| `evidence-checker` | Judges whether the evidence supports the findings. | sync |
| `client-response-drafter` | Drafts a customer reply from confirmed findings. Drafts — never sends. | sync |
| `incident-supervisor` | Plans and dispatches when no human is in the room. | async |

The four that read a large surface run in the background. The two that work on already-collected material run synchronously, because the supervisor is waiting on their answer to decide what happens next.

Every investigating prompt ends with the same rule: **quote a source and an excerpt, or report it as a gap.** A finding with no excerpt is a guess, and a supervisor cannot tell the two apart.

### Two gates, not one

"Validate the sub-agent's work" is two different jobs, and they belong in different places.

**The intake gate** runs in Go on every result, before its evidence is stored. It is mechanical — no model is called, because whether a string is empty is not a judgement:

- An evidence item with no source or no excerpt is sent back **once**, naming exactly what was missing. A second failure drops the item and records the drop.
- Findings with no evidence behind them are not rejected — the agent may be right — but the checker is told, so a plausible sentence is not weighed like a verified one.
- A confidence outside the enum becomes `unknown`.

**The judgement gate** is the `evidence-checker` role: contradictions between sources, sufficiency, and what is still missing. That is the part that needs reading comprehension, and it is the only part that pays for a spawn.

Validating only at the checker — as is tempting — lets unsourced claims into the evidence pool, where a model then has to argue them back out.

### The loop

```text
result arrives
  └─ intake gate (Go): missing source/excerpt → one re-ask, then drop
                       otherwise             → evidence stored

round completes (every agent in this round finished)
  └─ did the round add evidence?
        no, twice running  → stop, escalated
        yes                → dispatch evidence-checker
              confirmed          → status confirmed, stop
              contradiction      → record, stop, escalated
              escalate_to_human  → stop, escalated
              need_more_evidence → record what is missing, round++,
                                   dispatch the follow-ups
```

Whether a round is complete and whether it added anything are **queries**, not judgements — Go answers both. Paying for a spawn to answer them is expensive and occasionally wrong.

Every stop writes a reason to the incident. A loop that ends without saying why is indistinguishable from one still running.

A checker that returns no decision, a blank one, or something unrecognised is treated as **escalate to a human**. An unreadable verdict is not a pass — that would be exactly the failure the checker exists to prevent.

### Brakes

| Setting | Default | Stops the investigation when |
|---|---|---|
| Max iterations | 5 | that many checker rounds have run |
| Max runtime | 20 min | wall clock passes it — catches an agent that is slow rather than chatty |
| No-evidence rounds | 2 | that many consecutive rounds add nothing |
| Min confidence | medium | *(gates the customer draft rather than stopping the loop)* |

All four live under **Settings → Agents → Sub-agents** and are re-read per decision, so a change applies to the next round without a restart.

No-evidence rounds is **2**, not 1: an investigation that comes up empty once and lands the next round is the normal case, and stopping at the first blank round throws the run away.

Follow-up dispatches go through the same governor and the same queue as everything else. The loop gets no privileged route around the caps, and a refusal is written to the incident's next actions rather than swallowed into a log.

### Customer drafts are masked, not merely asked

`client-response-drafter` is told not to include logs, stack traces, hostnames or credentials. That is guidance, and guidance is not a control.

Its output passes through a sanitiser on the way out:

- Every secret-marked configuration value is replaced with `***`.
- Absolute paths under the project root are reduced to their filename.
- When anything was masked, the result carries a note telling a human to review before sending.

A prompt injection that persuades the drafter to quote a token therefore produces a **masked** token. The drafter never sends anything either way — it returns a draft.

## The incident record

An investigation that spans several sub-agents needs somewhere to keep what it has established, so "what do we know so far?" is a lookup rather than a leader's recollection.

Each delegation tree can have one **incident**: status, iteration, summary, hypotheses, missing evidence, next actions, and the evidence collected so far.

**It appears by itself.** There is no "open" action. The record is created the first time either of these happens:

- a sub-agent reports evidence, or
- someone calls the `incident` op.

A conversation that does neither leaves no row — most delegation trees are code reviews, not investigations, and a header on all of them would be noise.

### Evidence

Every `evidence` item a sub-agent reports is filed automatically when its delegation finishes, tagged with which agent found it. **Findings are not** — a finding is an interpretation, and merging four agents' interpretations without a check is how a supervisor ends up confidently wrong. The incident stores what was *quoted*.

The same log line found by two investigators is stored **once**. That is a database constraint, not a check-before-insert: two agents finishing at the same moment would both pass a "does this exist?" test and both write.

### The `incident` op

| Action | Does |
|---|---|
| `get` | Returns the record plus its evidence, grouped by kind. A conversation with no incident answers `exists: false` — that is a normal state, not an error. |
| `update` | Patches **only** the fields you pass. Add a hypothesis without restating the summary. |
| `close` | Terminal status plus a `final_summary`, which is required — it is what anyone reading this later gets. |

A closed incident refuses further updates. Reopening is a human action in the UI, the same way role locking works.

Scoping is structural: the caller's session resolves to a tree, and the tree resolves to its incident. There is no way to name someone else's.

### Effect on sub-agents

With an incident present, `state_summary` gains two lines:

```text
current hypotheses: signature mismatch on webhook retry
missing evidence: the signature header from a failing request
```

That is the whole point of the store — an agent spawned in round 3 should not re-derive what rounds 1 and 2 established, nor go hunting for evidence somebody already recorded as missing.

The rail panel shows the same state as a header above the agent list: status, round, evidence count, and — when it stopped — why.

## Mentions

Write `@name` at the start of a line and wick acts on it:

```text
@log-investigator check the 401s on abc.com between 10:00 and 11:00
@docs-investigator find the webhook signature runbook
```

The name resolves in one order, and the order matters:

1. **A handle already working here** → the text is delivered to that agent as a message. It keeps the context of its own work, so there is no need to re-explain anything.
2. **A role** (`list_agents` shows them) → a new sub-agent of that role starts on the task.
3. **Neither** → nothing happens and the text stays as written.

A live agent always wins. Otherwise "@code-investigator follow up on that" would start a fresh agent with no memory of the thing it was asked to follow up on.

Both people and agents can mention. When a person's message carries one, the leader is told so in the same message, before it reads it:

```text
@history-player-a run the bash check

[routed] wick is dispatching @history-player-a for the message above. Do not delegate or message them again for it — that runs the work twice.
```

The conversation shows that line as a small **Routed to @history-player-a** chip under the message rather than as text. Without it the leader sees a bare mention, has no way to know the work already started, and dispatches it a second time.

Whoever wrote the mention gets a short report of what actually started:

```text
dispatched: @log-investigator (running, d-7f3a) · @docs-investigator (queued #1, d-7f3b)
```

Two or more mentions in one message run in the background rather than blocking their author — though they still run [one at a time](#one-at-a-time-the-queue).

### What is not a mention

The scanner is deliberately strict, because the two mistakes cost very different amounts: a missed mention costs one clarifying turn, while a false one spawns an agent and spends tokens because a model happened to write an email address.

A mention must begin its line and be followed by text. These are left alone:

```text
@media (min-width: 40rem) { … }     ← not line-leading intent, no task
mail researcher@abc.com about it    ← an address
`@researcher` in backticks          ← inside code
@researcher                          ← no task
```

Anything inside a fenced code block is skipped entirely.

### Turning it off

**Settings → Agents → Sub-agents → Mention router.** Off means `@name` is plain text again and only `delegate` spawns anything.

## One at a time: the queue

A conversation runs **one sub-agent at a time**. Ask for four at once and the first starts while the other three line up behind it, in the order they were requested.

That is a deliberate default, not a capacity limit. Several sub-agents streaming into one room at once produce output nobody can follow, and they burn the tree's shared turn and token budget in parallel. The cost of running them serially is waiting, which is the cheaper failure.

An async delegation that cannot start yet comes back `queued` with its place in line:

```json
{
  "delegation_id": "d-7f3a",
  "status": "queued",
  "queue_position": 3,
  "note": "Queued behind 2 other sub-agent(s) in this conversation. Carry on with other work; the result is delivered when it finishes."
}
```

`collect` reports a queued delegation as `pending`, exactly as it reports a running one — from the leader's side "not ready yet" is one state.

A **synchronous** call simply waits its turn and then behaves normally. It has no timeout of its own: it lives as long as the call that made it, so if the caller goes away the queued work is cancelled rather than started for nobody.

The rail panel groups by **Working**, **Queued**, then **Finished**, and a queued row shows its position. There is no estimated start time, because there is no honest one to give.

**Stop works before a sub-agent starts.** Cancelling a queued delegation is total: no process is ever spawned, and the row records that it never started rather than looking like an agent that produced nothing.

### A sub-agent that delegates does not deadlock

A sub-agent waiting on a synchronous child of its own is *waiting*, not working, so it releases its place while it waits. Without that, a one-at-a-time room would wedge the first time a sub-agent delegated — the parent holding the only slot until a child that can never start finishes.

A sub-agent that fires an **async** child keeps working and keeps its place; the child queues normally.

### Raising the limit

**Settings → Agents → Sub-agents → Max parallel sub-agents.** Raising it to *n* lets *n* run concurrently in one conversation, at the cost of interleaved output and faster budget burn. The setting is re-read per delegation, so a change applies to the next one without a restart.

## Workspace isolation

Set `workspace: "worktree"` (or make it the profile default) and the sub-agent gets its own git worktree. Use it when several sub-agents edit code at once — in a shared directory they overwrite each other.

On a project that is not a git repository, the delegation **falls back to the shared workspace and says so** in `workspace_note`. It never fails silently, and it never quietly copies your repo.

## Token budget

Turn limits bound how *many* times a sub-agent runs. They do not bound what each run *costs* — one turn that reads a large file can cost more than ten small ones. So spend is capped too:

| Ceiling | Default | Scope |
|---|---|---|
| Max tokens per sub-agent | 200,000 | One delegation |
| Tree token budget | 1,000,000 | Every sub-agent under one root |

Both are enforceable only for providers that **report usage**. A provider that reports nothing yields 0, which is read as *unknown* — never as free — so those runs stay bounded by turn limits alone.

## Squads

A **squad** is a named, fixed line-up: a leader role plus the member roles it may delegate to. Without one, a leader can reach every profile its caller's tags allow — right for ad-hoc work, vague for a recurring job.

A squad only ever narrows. Membership never grants access the calling human lacks: the tag intersection still applies to every member.

## Task boards

A board is work that outlives any one conversation. Tasks are enqueued, claimed by a worker, executed, and completed. Agents drive it with the `tasks` op (`list`, `add`, `claim`, `start`, `complete`, `fail`); humans use the same board through the API.

Two rules make it safe to share:

- **Claims are exclusive.** Two workers polling at once produce exactly one winner — the guard is in the database write, not in an application check that could interleave.
- **Completion can require evidence.** A board's gate mode is `off`, `warning` (allow, but flag a bare claim of success), or `blocking` (refuse it). The gate applies to the tool call *and* to dragging a card into Done, so neither path launders work past the other.

A claim held by a worker that vanished is released back to the queue after 30 minutes; otherwise a crashed worker would pin its task forever.

### Kanban

The same tasks viewed as columns. Columns are **presentation**: each maps to a `stage`, and the state machine reads the stage. Rename or reorder columns freely — automation follows the stage, not the column's name.

## Take-over

Off by default, per role. Turn on **Allow take-over** for a profile and a human can send guidance straight into its running sub-agents.

The reason it is opt-in is honesty, not safety: once a person steers a sub-agent, the answer is no longer that role's unaided work. Steered delegations are flagged, and the leader is told when it collects the result. Stopping a sub-agent is a different act and is always allowed.

## Fleet monitor

`/api/monitor/snapshot` returns every live agent plus per-tree totals — sub-agent count, turns, tokens, a rough cost estimate, and wall-clock. Admins see the whole fleet; everyone else sees only what they triggered.

Read-only, with one deliberate exception: **stopping** is allowed. Halting work is not the same as steering it.

## Process teardown

Killing an agent takes its **descendants** with it. An agent CLI spawns MCP servers and tool subprocesses; stopping only the leader would leave those running.

Teardown asks the process tree to exit, then forces it after a grace window. The escalation is unconditional — a descendant that ignores the polite signal is still reaped. This works the same on Windows (`taskkill /T`) and POSIX (process-group signals); Windows is not a second-class path here.

## Limits worth knowing

- **One delegation is one question — unless someone messages it.** The child returns on its first complete answer. If a message arrives while it works, it keeps going and answers that too, still bounded by its turn cap and the tree's hop limit.
- **Turn caps are enforced by wick**, by counting normalized end-of-turn events and stopping the process — not by a provider flag. Only some CLIs have `--max-turns`; the cap behaves identically on the ones that do not.
- **Cycles are refused.** A profile already active higher in the chain cannot be delegated to again, so `A → B → A` cannot loop.
- **A busy room queues, it does not refuse.** Depth, cycle and budget mean "this must not happen"; a full room means "not yet", so the work waits instead of being thrown away.
- **Budget exhaustion lets running work finish** and refuses only *new* delegations. A manual stop, by contrast, cascades.
