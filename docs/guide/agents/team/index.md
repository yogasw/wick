---
outline: deep
---

# Team

**Team** is the full-screen app at `/tools/agents/team` for persistent agents. Each Team agent has its own name, `@handle`, persona, avatar, chat history and access checklist, and stays in the roster until you delete it.

| | Team agent | [Sub-agent](/guide/agents/sub-agents) | [Channels](/guide/agents/channels) |
|---|---|---|---|
| Lifetime | Persistent, listed in the roster | Temporary worker started for one task | A transport, not an agent |
| Identity | Name, `@handle`, persona, avatar | A role preset | None |
| Access | Its own checklist, enforced by the server | Inherits from the session that started it | Whatever the session behind it has |

## Layout

- **Roster** (left): your agents and group chats, with search, a typing indicator and unread counts. Remote and shared agents carry a badge.
- **Chat** (center): the same conversation view as an agent session, so the rail (files, notes, todos…) works the same way. Remote agents hide the local-tool rail tabs — they run on their own host.
- **⋯ menu**: opens the drawers — **Settings** (persona, access, features, session, avatar), **Connections** and **Scheduled**.

## Captain

The **Captain** (`@captain`) is created for you the first time you open Team. It is your main agent and can manage the rest of the Team through the built-in **Team agents** connector:

| Op | What it does |
|---|---|
| `list` | List your Team agents |
| `create` | Create an agent |
| `update_persona` | Edit an agent's persona |
| `set_access` | **Propose** an access change — it never changes access itself; you accept or decline it on an approval card in the chat |
| `schedule` | Manage an agent's schedules |

The Captain cannot be shared.

## Creating an agent

**+ New agent** offers two kinds:

- **Wick agent** — runs on this wick host with a provider, a project and the access you grant. See [Access & sharing](./access-sharing).
- **Remote agent** — an agent that runs somewhere else and is reached from the roster: A2A, Slack or a service plugin. See [Mentions & remote agents](./mentions-remote).

## Persona and system prompt

A Team agent's persona lives in its project, so switching an agent's project switches its persona. When a Team session spawns, the system prompt is assembled from static to dynamic:

1. wick's immutable rules, then the Team rules shared by every Team agent (`immutable_team.md`);
2. a generated **Who you are** block: name, tagline, `@handle`, role, whether it is the Captain, and the Team roster;
3. **Your access** — a summary of the connectors and tools it has (enforcement stays on the server);
4. the preset, the operator's Team prompt, then the agent's own persona.

Sections a Team session does not need are gated: *Session title* is dropped, *Delegating* appears only when Sub-agents is on, *Scheduling* only when Scheduled is on.

## Team settings

**Settings** in the Team app holds options that apply to all your agents:

| Setting | Default | What it does |
|---|---|---|
| Team instructions | empty | Markdown added to the system prompt of every agent in your Team |
| Open Team when I open Agents | **off** | When on, the bare `/tools/agents` landing redirects to Team |
| Idle animations | on | Idle avatars fidget now and then |

## Pages in this section

- [Access & sharing](./access-sharing)
- [Mentions & remote agents](./mentions-remote)
- [Connections (Slack, Telegram, A2A, REST)](./connections)
- [Scheduled](./scheduled)
