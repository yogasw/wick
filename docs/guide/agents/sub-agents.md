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
| Max parallel | `4` | A leader fanning out to dozens of concurrent children |
| Max turns ceiling | `50` | Any single sub-agent running away |

Turning the master switch off takes effect on the **next delegation** — no restart. Sub-agents already running are left to finish.

## The operations

Delegation is reached through the **`sub-agents` connector**, not through
top-level tools: `wick_get "sub-agents"` to resolve it, then `wick_execute`
per op. That buys it the connector contract — an admin page, tag
visibility, and run history — at the cost of one resolution hop.

Five ops: `list_agents`, `delegate`, `collect`, `create_agent`, `tasks`.

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

Emitting several `delegate` calls in one turn runs them **in parallel**, up to `max_parallel`. There is no batch op — parallelism falls out of multiple calls naturally.

The result always carries a `status`:

| Status | Meaning | What the leader should do |
|---|---|---|
| `done` | Complete answer | Use it |
| `interrupted` | A **human** pressed Stop | Read the note; do *not* silently retry |
| `stopped_max_turns` | Hit its turn cap | Result is partial — use it or ask the user |
| `stopped_budget` | The tree's budget ran out | Summarise with what is already there |
| `failed` | Runtime error | Report it |

### `create_agent`

An agent can define its own roles. `key`, `description` and
`system_prompt` are all required — a role without a description is
invisible to the reasoning that picks it, and one without a prompt is a
generic assistant wearing a role's name.

The role is created in **the calling conversation's project**, never
globally. That scoping is what makes the op safe to hand to every user:
a role an agent invents is reachable only from the project it was already
working in, and a key that collides with a global role shadows it there
without touching the shared one. A conversation with no project is
refused rather than silently creating something global.

Creating a **global** role stays admin-only and is done from the
Sub-agents page.

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

A result is handed over **exactly once**. Collecting the same delegation twice returns it flagged as a repeat, because acting on the same answer twice duplicates whatever the leader did with it.

::: info Async sub-agents are detached
They were fired to run on their own, so killing the leader does **not** stop them. An explicit **Stop all** does.
:::

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
- **Budget exhaustion lets running work finish** and refuses only *new* delegations. A manual stop, by contrast, cascades.
