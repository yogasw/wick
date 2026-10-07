---
outline: deep
---

# Access & sharing

## Access checklist

A Team agent only reaches what its **Settings › Access** checklist allows: connectors the agent's creator can use, plus platform and native tools. The server enforces the checklist — the agent's `tools/list` is filtered, so a persona cannot talk its way past it. Anything not ticked is denied.

The Captain cannot grant access on its own: `agents.set_access` only **proposes** a change, which shows up as an approval card in the chat. Accept applies it; Decline leaves access as it was. Changes are recorded in the agent's access history (`GET /api/team/agents/{id}/access-history`).

## Sharing an agent

An owner can share an agent with another wick user from the agent's menu.

- The recipient gets **chat only**: the agent appears in their roster with a shared badge, and they can chat with it and `@mention` it.
- The agent keeps running with the **owner's** connector access. There is no copy — an edit, a disable or a delete reaches the recipient at once; unsharing removes it from their roster.
- The recipient cannot open the agent's menu or drawers, and the owner's rail (sessions, notes, files) is hidden from them.
- **Cannot be shared:** the Captain ("The Captain runs your Team and cannot be shared.") and a remote agent whose Usage is set to *Only me*.

API: `GET/POST /api/team/agents/{id}/shares`, `DELETE /api/team/agents/{id}/shares/{uid}`.
