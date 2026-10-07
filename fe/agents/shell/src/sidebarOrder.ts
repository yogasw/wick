/* Sidebar ordering and "last update" ages.

   The server renders the first frame already sorted (running first, then
   last use) with an age on every row; this module is what keeps it true
   while the page stays open — ages tick, and a session that starts
   working moves up without a reload.

   MIRRORS view.RelativeAge / view.IsRunningStatus in
   internal/tools/agents/view/sidebar_time.go and orderSidebarIDs in
   internal/tools/agents/sidebar_order.go. Change both together. */

/* relativeAge mirrors view.RelativeAge: "now", "3m", "2h", "5d". */
export function relativeAge(lastMs: number, nowMs: number): string {
  if (!(lastMs > 0)) {
    return "";
  }
  const d = nowMs - lastMs;
  if (d < 60_000) {
    return "now";
  }
  if (d < 3_600_000) {
    return `${Math.floor(d / 60_000)}m`;
  }
  if (d < 86_400_000) {
    return `${Math.floor(d / 3_600_000)}h`;
  }
  return `${Math.floor(d / 86_400_000)}d`;
}

/* isRunningStatus mirrors view.IsRunningStatus. Idle is a warm process
   with nothing to do, so it is not "running". */
export function isRunningStatus(status: string): boolean {
  return status === "working" || status === "spawning" || status === "subagent" || status === "queued";
}

export type RowKey = { id: string; running: boolean; lastActive: number };

/* sortRows mirrors orderSidebarIDs: running first, then newest first,
   then by id — the same total order the server renders and pages the
   ticket rail by, so a live re-sort never reshuffles equal-time rows.
   A re-sort with nothing changed moves nothing. */
export function sortRows<T extends RowKey>(rows: T[]): T[] {
  return rows
    .map((r, i) => ({ r, i }))
    .sort((a, b) => {
      if (a.r.running !== b.r.running) {
        return a.r.running ? -1 : 1;
      }
      if (a.r.lastActive !== b.r.lastActive) {
        return b.r.lastActive - a.r.lastActive;
      }
      if (a.r.id !== b.r.id) {
        return a.r.id < b.r.id ? -1 : 1;
      }
      return a.i - b.i;
    })
    .map((x) => x.r);
}

/* touchesActivity decides whether a live transition counts as "this
   session was just used". Events carry no timestamp, so the arrival time
   stands in for it — but only when something actually started or stopped.
   The stream replays every warm process on connect, and an idle one that
   last spoke an hour ago must not jump to "now" just because the page
   loaded. */
export function touchesActivity(prevStatus: string | undefined, nextStatus: string): boolean {
  if (isRunningStatus(nextStatus)) {
    return true;
  }
  return prevStatus !== undefined && isRunningStatus(prevStatus);
}
