import { Effect } from "effect";
import { apiGetE, apiPostE, APIError } from "@wick-fe/common-api";
import type { AgentMessageItem, SubAgentItem } from "../types/agents.js";

// liveSubAgents keeps only the sub-agents that are still working. Finished
// ones must NOT raise the rail badge — otherwise the badge sticks at "3"
// long after every sub-agent has returned. The tab itself is driven by the
// TOTAL count instead, so completed results stay readable.
export const liveSubAgents = (subs: SubAgentItem[]): SubAgentItem[] =>
  subs.filter((s) => s.status === "queued" || s.status === "running");

export const getSubAgents = (base: string, id: string) =>
  apiGetE<{ subagents: SubAgentItem[] }>(
    `${base}/api/sessions/${encodeURIComponent(id)}/subagents`,
  ).pipe(Effect.map((r) => r?.subagents ?? []));

// interruptSubAgent stops one sub-agent.
//
// A 409 means the sub-agent completed a moment before the click landed —
// its real result stands. That is a normal outcome of a race the user
// cannot avoid, so it is mapped to a value rather than surfaced as an
// error toast.
export const interruptSubAgent = (base: string, delegationId: string) =>
  apiPostE<{ outcome: string }>(
    `${base}/api/delegations/${encodeURIComponent(delegationId)}/interrupt`,
    {},
  ).pipe(
    Effect.catchAll((e) =>
      e instanceof APIError && e.status === 409
        ? Effect.succeed({ outcome: "already_done" })
        : Effect.fail(e),
    ),
  );

export const interruptAllSubAgents = (base: string, sessionId: string) =>
  apiPostE<{ stopped: number }>(
    `${base}/api/sessions/${encodeURIComponent(sessionId)}/subagents/interrupt-all`,
    {},
  );

// getMessages loads the agent-to-agent thread for this session's tree,
// with the hop budget that governs whether more may be sent.
export const getMessages = (base: string, id: string) =>
  apiGetE<{ messages: AgentMessageItem[]; hops_left: number }>(
    `${base}/api/sessions/${encodeURIComponent(id)}/messages`,
  ).pipe(Effect.map((r) => ({ messages: r?.messages ?? [], hopsLeft: r?.hops_left ?? 0 })));

// bumpHops refills the agent-to-agent message budget. Human-only: there
// is no matching agent-facing op, so a looping leader cannot lift its own
// limit.
export const bumpHops = (base: string, id: string) =>
  apiPostE<{ ok: boolean }>(`${base}/api/sessions/${encodeURIComponent(id)}/hops/reset`, {});
