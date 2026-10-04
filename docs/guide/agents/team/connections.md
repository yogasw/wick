---
outline: deep
---

# Connections

**⋯ › Connections** lists every way to reach an agent from outside, one tab each: **Slack**, **Telegram**, **A2A**, **REST**. A tab shows ✓ when that connection is live and ⚠ when it is set up but needs a look.

## Slack

Connect the agent to its own Slack app (generated manifest, health check), or to a shared app in Instant mode where several personas share one app. See [Channels](/guide/agents/channels) for the Slack setup itself.

## Telegram

Paste a BotFather token; the agent answers through that bot. Disabling the agent stops the bot; enabling it starts it again.

## A2A (server)

Expose the agent to other systems over A2A:

| | Path |
|---|---|
| Agent card | `GET /integrations/a2a/{agent_id}/.well-known/agent-card.json` |
| Messages | `POST /integrations/a2a/{agent_id}` |

Enabling creates an API key that is shown once. **Rotate** issues a new one, **Revoke** turns access off, **Test** checks the endpoint.

## REST

The OpenAI-compatible [REST channel](/guide/agents/channels) runs a turn as a Team agent when the request sets `"model": "agent:<handle>"`. The REST tab turns that on or off per agent.
