/* Connections drawer: one tab per way to reach the agent. The tab strip
   shows ✓ on the ones that are live and ⚠ on the ones that need a look,
   from the same data as each card's own badge. */

export type ConnTab = "slack" | "telegram" | "a2a" | "rest";
/** on = the card's badge is green; warn = set up but not working (or the
    status could not be read); off = not set up. */
export type ConnState = "on" | "warn" | "off";

export const CONN_TABS: { key: ConnTab; label: string }[] = [
  { key: "slack", label: "Slack" },
  { key: "telegram", label: "Telegram" },
  { key: "a2a", label: "A2A" },
  { key: "rest", label: "REST" },
];

/** connTabOf resolves a `conn=` value; unknown or empty → null (pick the default). */
export function connTabOf(t: string | null | undefined): ConnTab | null {
  return CONN_TABS.some((c) => c.key === t) ? (t as ConnTab) : null;
}

/** defaultConnTab: the first tab that is connected, else Slack. */
export function defaultConnTab(states: Partial<Record<ConnTab, ConnState>>): ConnTab {
  return CONN_TABS.find((c) => states[c.key] === "on")?.key ?? "slack";
}

/** connState maps a card's status to its tab mark: `set` = connected or
    enabled, `healthy` = what turns the badge green, `failed` = the card
    shows an error. */
export function connState(set: boolean, healthy: boolean, failed = false): ConnState {
  if (set && healthy && !failed) return "on";
  if (set || failed) return "warn";
  return "off";
}

export const CONN_MARK: Record<ConnState, string> = { on: "✓", warn: "⚠", off: "" };
export const CONN_MARK_LABEL: Record<ConnState, string> = { on: "connected", warn: "needs attention", off: "not connected" };
