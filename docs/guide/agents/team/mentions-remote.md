---
outline: deep
---

# Mentions & remote agents

## @mentions and handoff

Type `@handle` in a chat to bring another Team agent in. The teammate's reply comes back into the thread. Group chats (`/tools/agents/team/g/<id>`) hold several agents in one conversation.

Agents reach each other with the built-in **Team** connector:

| Op | What it does |
|---|---|
| `message` | Message a teammate and get its reply |
| `get_task` | Check on a Team task started earlier |

Each agent's settings decide who may hand it a turn (`all`, `captain`, a list, or `off`) and cap the chain of handoffs: the default cap is 4 hops, the most an agent may set is 10.

## Remote agents

**+ New agent › Remote agent** adds an agent that runs elsewhere. It shows in the roster with a badge (*A2A remote*, *Slack remote*, *Plugin remote*), has no local tools, and can be chatted with and `@mentioned` like any teammate.

### A2A

An agent that speaks the A2A protocol. Paste its agent card URL, choose the auth (`none`, `bearer` or `api_key`), **Fetch card**, then **Test**. The card can be refreshed later from Settings.

### Slack

A bot or person you reach in Slack.

| Send to | Behaviour |
|---|---|
| DM | A direct message to a user or bot |
| Channel | Each chat opens a new thread in the channel |
| Thread | Every chat replies in one existing thread (paste the message link; channel and thread fill in) |

**Picking the target:** search the workspace by name instead of typing an id. DM searches users and bots (bots carry a **BOT** badge); Channel and Thread search channels, public and private, archived skipped. Picking fills the id and the display name, which you can still edit. The id field stays under **Enter ID manually**.

The search uses the token of the workspace (and identity) chosen in the form and needs these Slack scopes:

| Target | Scope |
|---|---|
| DM | `users:read` |
| Channel / Thread | `channels:read`, `groups:read` |

Without the scope the form says which one is missing and opens the manual id field. Only the picked id is stored — wick never matches a person by name. Listings are cached for 5 minutes per workspace and a search returns at most 20 matches (`GET /api/team/slack-remote/directory`).

Other options:

- **Post as** the connector's bot, or *As me* through a Slack account you connected yourself.
- **Listen to** only the target, or anyone in the thread.
- **End marker**: each turn asks the other side to end its reply with a marker line; without it the turn ends once the reply has been quiet for the idle time.
- **Always @mention the target**: each message starts with `@target`, so bots that only answer mentions are triggered. Off posts messages as written.
- **Test** posts a real "ping" and waits for the first reply.

### Plugin

A service plugin on this host that offers a remote agent source (capability `remote_source`). See [service-a2a](/plugins/service-a2a).
