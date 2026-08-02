// Shared status → Tailwind class maps for the rail panels.
//
// Extracted from ProcessPanel so the Sub-agents panel renders identical
// colours for identical states. Two private copies would drift the moment
// one panel gains a state the other does not.
//
// Colour intent follows the design system's status ramps — pos/prog/cau/neg
// — never the green accent, which is reserved for primary actions.

export function lifecycleCls(lc: string): string {
  const map: Record<string, string> = {
    working: "bg-pos-100 text-pos-400",
    idle: "bg-prog-100 text-prog-400",
    spawning: "bg-cau-100 text-cau-400",
    queued: "bg-cau-100 text-cau-400",
    killed: "bg-neg-100 text-neg-400",
    dead: "bg-neg-100 text-neg-400",
    error: "bg-neg-100 text-neg-400",
  };
  return map[lc] ?? "bg-white-300 dark:bg-navy-600 text-black-700";
}

export function lifecycleDotCls(lc: string): string {
  const map: Record<string, string> = {
    working: "bg-pos-400",
    idle: "bg-prog-400",
    spawning: "bg-cau-400",
    queued: "bg-orange-500 animate-pulse",
    killed: "bg-neg-400",
    dead: "bg-neg-400",
    error: "bg-neg-400",
  };
  return map[lc] ?? "bg-white-400 dark:bg-navy-500";
}

// subAgentStatusCls maps a delegation status to a chip class.
//
// Note "done" uses the positive ramp, NOT the green accent: green is the
// primary action colour in this design system and reusing it for success
// makes finished rows compete with buttons for attention.
export function subAgentStatusCls(status: string): string {
  const map: Record<string, string> = {
    queued: "bg-cau-100 text-cau-400",
    running: "bg-pos-100 text-pos-400",
    done: "bg-pos-100 text-pos-400",
    failed: "bg-neg-100 text-neg-400",
    interrupted: "bg-neg-100 text-neg-400",
    stopped_max_turns: "bg-cau-100 text-cau-400",
    stopped_budget: "bg-cau-100 text-cau-400",
  };
  return map[status] ?? "bg-white-300 dark:bg-navy-600 text-black-700";
}

// subAgentStatusLabel renders a status for humans. Every status in
// entity.DelegationStatuses must appear here, or the UI shows a raw
// snake_case token.
export function subAgentStatusLabel(status: string): string {
  const map: Record<string, string> = {
    queued: "Queued",
    running: "Running",
    done: "Done",
    failed: "Failed",
    interrupted: "Stopped",
    stopped_max_turns: "Turn limit",
    stopped_budget: "Budget spent",
  };
  return map[status] ?? status;
}

// isSubAgentWorking reports whether a sub-agent has a process actually
// producing output right now, as opposed to merely being unfinished.
//
// Reads the pool lifecycle first and only falls back to the delegation
// status: a row can sit at status "running" for a while with nothing
// spawned yet (queued behind a slot), and spinning there would promise
// progress that is not happening.
export function isSubAgentWorking(status: string, lifecycle: string): boolean {
  if (lifecycle) return lifecycle === "working" || lifecycle === "spawning";
  return status === "running";
}

// isSubAgentLive reports whether a sub-agent can still be stopped. Both
// queued and running qualify: a queued one is cancelled by dropping it
// from the queue, and hiding its Stop button would leave the user unable
// to cancel work that has not even begun.
export function isSubAgentLive(status: string): boolean {
  return status === "queued" || status === "running";
}
