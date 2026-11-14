---
name: wick-agents
description: Use when the user asks how wick agents work — sessions, projects, presets, providers, channels (Slack/Telegram/web), the pool and its queue, scheduled messages, or where agent state lives on disk. Covers the mental model behind sessions and projects, how to point an agent at a folder, and how to reach the same agent from several channels.
---

# Using wick agents

An **agent** in wick is a CLI coding assistant (Claude Code, Codex, Gemini) or the in-process `wick` provider, run as a subprocess against a folder, reachable from several channels at once. Everything it does happens inside a **session**.

For Codex, **Providers → instance → Configuration → sandbox_mode** selects `read-only`, `workspace-write`, or `danger-full-access` for the next turn. An unset mode retains the existing `danger-full-access` default. Changing it requires the existing sandbox permission and does not change tool approval policy.

## The mental model

Four things, in order of how often you touch them:

| | What it is |
|---|---|
| **Session** | One conversation. Auto-created on the first message in a Slack thread, a Telegram chat, or a fresh web conversation — never pre-allocated. |
| **Project** | One folder plus its defaults. The folder is the `cwd` the agent subprocess runs in. Several sessions can share one project. |
| **Preset** | Reusable agent instructions — one `agent.md` file. The built-in `default` preset is the fallback when a session has no project; it cannot be deleted, only edited. |
| **Provider** | Which CLI runs, and under which credentials. Two `claude/...` profiles with different tokens can coexist alongside codex and gemini. |

The folder is the agent's world. Whatever is in the project folder — files you put there, files the agent writes via Bash — is what it can see.

## Projects: managed vs custom

- **Managed** — wick creates an empty folder under `~/.<app>/agents/projects/<id>/files/` and deletes it when the project is deleted.
- **Custom** — you point the project at an existing path anywhere on disk. Wick does **not** delete it on project delete.

Use custom when the agent should work on a real repo you already have; managed when you want a scratch space wick owns.

## Channels

The same agent is reachable from Slack, Telegram, and the web UI. Each thread, chat, or web conversation becomes its own session, so context does not bleed between them.

The web UI is always on. Slack and Telegram need a bot token and access-control config under the Channels page, where you also set the default project new sessions land in.

## Team agents: whose access

An agent from the Team page inherits its OWNER's connector access, narrowed by its checklist — the checklist only offers what the owner sees in `wick_list`, and saving anything outside it is refused. Its "run as" setting picks the identity: `caller` (default) runs a turn as the person who sent it, falling back to the owner for schedules, bots and workflows; `owner` always runs as the owner, so anyone who can message the agent uses the owner's access within the checklist.

### By role

Settings › Access › "Which connectors?" has three modes: All, Selected, By role. Switching asks for a confirmation because it saves at once. In By role the agent's picked connectors (the same list as Selected) are its ceiling: a music bot reaches its music connector, not the owner's whole catalog, for everyone including the owner. Within that list, who is chatting decides what they may borrow of the owner's connectors:

- **owner**: the agent's owner, everything. A rule can make another person owner too (a co-owner): they borrow every connector of the agent, but only the real owner changes roles.
- **guest**: anyone no rule names, plus bots and unknown callers (a rule can also make a person guest). Nothing of their own; only the connectors the lend table lends to `guest` by name, e.g. `"lend": {"<huddle connector id>": {"guest": "all"}}`. Guest has no default level.
- Roles in between are the owner's own (default: `trusted` rank 70 borrows all, `member` rank 40 borrows read with writes waiting for approval). Rules map a wick user id, or "anyone the agent is shared with", to a role; first match wins.
- `lend` lists, per connector, what each role (and `guest`) borrows (`off` / `read` / `all` / pick ops). A connector not in `lend` is never borrowed; people keep their own access to it.
- A write that needs approval is refused for now with a message saying so: the approval flow is not built yet.

The Settings screen only SHOWS roles (who is who, the connector × role table, a preview as one person, history). They are changed by chatting:

- **Inside the agent itself** (every Team agent has these built-in tools, whatever its connectors): `wick_roles` answers "what is my role here" for whoever is chatting (`your_role`) and, for the owner, returns the whole document; `wick_roles_set` saves it (doc JSON + `base_rev` + reason). The owner asks in their own chat with that agent ("make Person A an owner here", "guests can use Huddle FM"). Any other sender, a teammate, a schedule or a forwarded message is refused, so a guest cannot promote themselves, and a non-owner sees only their own role.
- **From the Captain**: the Team agents connector ops `roles_get` / `roles_set` do the same for any agent of the owner's Team.

Steps: read (`wick_roles`), edit the WHOLE document, save with the `rev` you read (a stale `base_rev` is refused). People are named by wick user id in rules. Every save is a new revision; undo by saving an old revision's doc again. Turning By role on is still the owner's switch in Settings › Access.

## The pool

A pool caps how many agent subprocesses run at once and FIFO-queues the rest. Idle sessions are killed and later resumed transparently, so a long-lived conversation does not hold a process hostage.

What this means practically: a queued session is not stuck, it is waiting. Check the Overview page (active / max / queue) before concluding something has hung.

## Scheduled messages

`wick_schedule_message` puts work on the clock without the workflow engine — the agent schedules it over MCP, or a human does from the UI; the Scheduled page is a cross-session monitor (Message/Watch badge, pause / resume / cancel / run now, a watch's run history).

Two types: `message` wakes the agent on every fire ("check back later", "every Monday write the report"); `watch` polls a condition in wick with connector / check / bash steps and wakes the agent once when it is met (pipeline, build, deploy, status). Details, rules and copy-ready examples: the `wick-schedules` skill. Reach for a workflow when the need is a multi-step pipeline.

## Where the pages are

Everything lives under `/tools/agents`:

| Page | What you do there |
|---|---|
| **Overview** | Pool stats, running list, recent sessions. |
| **Sessions** | Open a session; tabs for Conversation, Commands (gate audit), Approvals, Raw. |
| **Projects** | Create / delete projects. |
| **Presets** | Edit reusable agent instructions. |
| **Providers** | Per-instance status: binary path, version, env vars, Rescan. |
| **Skills** | Browse, preview, sync, delete skill files across provider dirs. |
| **Channels** | Slack + Telegram config. |
| **Scheduled** | Monitor scheduled messages. |

## Storage

All agent state is on disk under `~/.<app>/agents/` — sessions, projects, presets, workflows. There is no DB migration to worry about: backup is `tar`, and a restart re-scans the tree.

`<app>` is whatever the binary's name resolves to, so a dev build named `wick-lab` keeps its state in `~/.wick-lab/` and never collides with a release build's `~/.wick/`.

## Skills

Skills are folders holding a `SKILL.md`, discovered from each provider's skill directory (`~/.claude/skills`, `~/.codex/skills`, `~/.gemini/skills`, `~/.<app>/skills`, and the shared `~/.agents/skills`). The Skills page syncs them across those directories.

The `wick` provider loads them differently from the CLIs: because it runs in-process, it injects a compact catalog — name, description, and the `SKILL.md` path — into its system prompt, then reads a skill's full body on demand when it actually engages one.
