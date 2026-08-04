/* Sidebar liveness indicator.

   Extracted from main.ts so the roll-up rules are testable: the sidebar is
   the only place liveness shows once you navigate away from a chat, and
   getting it wrong is invisible until someone stares at a row that should
   be spinning.

   MIRRORS view.StatusDotClass and view.effectiveStatus in layout.templ —
   the server paints the first frame, this re-renders the same node from
   live events. Change both together. */

export type DotState = {
  /** The conversation's OWN process lifecycle, as the pool reports it. */
  lifecycle: string;
  /** Busiest lifecycle among its sub-agents, "" when none is working. */
  subAgent: string;
};

/* statusDotClass mirrors view.StatusDotClass. */
export function statusDotClass(status: string): string {
  const base = "ml-2 shrink-0 rounded-full";
  switch (status) {
    case "working":
      return `${base} h-3 w-3 border-2 border-green-500 border-t-transparent animate-spin`;
    case "spawning":
      return `${base} h-3 w-3 border-2 border-amber-400 border-t-transparent animate-spin`;
    // Delegated work: teal, not the leader's green — the row is saying a
    // sub-agent is working, which is not the same as this conversation
    // working.
    case "subagent":
      return `${base} h-3 w-3 border-2 border-teal-500 border-t-transparent animate-spin`;
    case "queued":
      return `${base} h-1.5 w-1.5 bg-orange-500 animate-pulse`;
    case "idle":
      return `${base} h-1.5 w-1.5 bg-blue-400`;
    default:
      return `${base} hidden`;
  }
}

/* effectiveStatus mirrors view.effectiveStatus: the leader owns the row
   whenever it is doing something itself, and a working child only takes
   over once the leader has gone quiet.

   "killed" is the pool tearing a finished process down rather than a state
   worth a colour, so it reads as nothing — but it must NOT erase a child's
   marker, since killing the leader is exactly how a session ends up with
   only sub-agents running. */
export function effectiveStatus(state: DotState): string {
  const { lifecycle, subAgent } = state;
  if (lifecycle !== "" && lifecycle !== "killed" && lifecycle !== "idle") {
    return lifecycle;
  }
  if (subAgent === "working" || subAgent === "spawning") {
    return "subagent";
  }
  if (lifecycle === "idle") {
    return "idle";
  }
  return "";
}

/* Per-row state, so a leader event and a sub-agent event can each update
   their own half without clobbering the other. A lifecycle-only event
   would otherwise wipe the sub-agent marker on every idle transition —
   which is the moment the marker matters most. */
export function applyEvent(
  prev: DotState | undefined,
  ev: { lifecycle?: string; sub_agent?: string },
): DotState {
  const base: DotState = prev ?? { lifecycle: "", subAgent: "" };
  // Exactly one half of the state is present per event: the server sets
  // Lifecycle for a leader transition and SubAgent for a delegated one.
  // A sub-agent event carrying no lifecycle key must leave the leader's
  // half alone, and vice versa.
  const hasLifecycle = ev.lifecycle !== undefined;
  const hasSubAgent = ev.sub_agent !== undefined;
  // A child that stopped sends neither key (both empty) — that is the
  // clear signal for the sub-agent half specifically, not for the leader.
  const isSubAgentEvent = hasSubAgent || (!hasLifecycle && !hasSubAgent);
  return {
    lifecycle: hasLifecycle ? (ev.lifecycle ?? "") : base.lifecycle,
    subAgent: isSubAgentEvent ? (ev.sub_agent ?? "") : base.subAgent,
  };
}
