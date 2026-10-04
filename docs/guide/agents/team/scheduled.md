---
outline: deep
---

# Scheduled

**⋯ › Scheduled** lists the schedules aimed at an agent. They use the ordinary schedule store and runner — there is no second scheduler (see [Scheduled Messages](/guide/agents/scheduled-messages)).

- **Destination:** the agent's chat, Telegram or a Slack channel.
- **Actions:** edit, pause, resume, **Run now**, delete.
- **History:** each schedule keeps its runs; finished schedules stay in the drawer for 7 days.
- The Captain can manage other agents' schedules through `agents.schedule`.

API: `/api/team/agents/{id}/scheduled` (list/create), `…/{sid}` (edit/delete), `…/{sid}/pause|resume|run`, `…/{sid}/runs`.
