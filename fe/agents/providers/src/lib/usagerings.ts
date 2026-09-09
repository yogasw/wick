/* Geometry + window selection for the two nested usage rings shown on
   each provider card: inner = the rolling 5-hour session window, outer
   = the rolling 7-day window. Kept out of the component so the maths is
   unit-testable without rendering. */

import { fmtResetsIn, usageLabel, type UsageWindow } from "./logintty.js";

/* connectionKey is the join key between a connection row and the card it
   belongs to — the same {type, name} pair the list keys cards on.

   It lives here rather than in api.ts because ProvidersList reads it
   during render: a component test that auto-mocks the api module would
   stub it to undefined, collapsing every card onto one lookup key. */
export function connectionKey(type: string, name: string): string {
  return `${type}/${name}`;
}

export type RingWindows = {
  inner: UsageWindow | null; // five_hour
  outer: UsageWindow | null; // seven_day (or a model-specific weekly)
};

/* pickWindows chooses which two of the API's windows the rings show.

   The weekly slot prefers the plain `seven_day` window and falls back to
   a model-specific one (`seven_day_opus` / `seven_day_fable`) so an
   account that only reports the model-scoped limit still gets an outer
   ring. */
export function pickWindows(windows: UsageWindow[]): RingWindows {
  const byKey = new Map(windows.map((w) => [w.key, w]));
  const weekly =
    byKey.get("seven_day") ??
    byKey.get("seven_day_opus") ??
    byKey.get("seven_day_fable") ??
    null;
  return { inner: byKey.get("five_hour") ?? null, outer: weekly };
}

/* ringDash converts a utilization percentage into the stroke-dasharray
   length for a circle of radius r. Out-of-range input is clamped rather
   than trusted — the value comes from an upstream API. */
export function ringDash(utilization: number, r: number): { dash: number; circumference: number } {
  const circumference = 2 * Math.PI * r;
  const pct = Math.min(100, Math.max(0, Number.isFinite(utilization) ? utilization : 0));
  return { dash: (pct / 100) * circumference, circumference };
}

/* ringColor maps utilization onto the status ramp, matching the usage
   bars on the provider detail page. */
export function ringColor(utilization: number): string {
  if (utilization >= 90) return "text-neg-400";
  if (utilization >= 70) return "text-cau-400";
  return "text-link-400";
}

/* resetHint renders "when does this window reset" for the compact card.

   `short` is the inline chip text (3h / 25m / 4d) and `full` is the
   spelled-out sentence used as the tooltip and the accessible name —
   the chip alone is ambiguous next to the window's own length, where
   "5h 42%" already contains a duration.

   Both are empty when the API gave no usable timestamp (missing, past,
   unparseable), so a caller can drop the element entirely rather than
   render a stray separator. */
export function resetHint(
  window: UsageWindow | null,
  nowMs: number,
): { short: string; full: string } {
  if (!window) return { short: "", full: "" };
  const short = fmtResetsIn(window.resetsAt, nowMs);
  if (!short) return { short: "", full: "" };
  return { short, full: `${usageLabel(window.key)} resets in ${short}` };
}
