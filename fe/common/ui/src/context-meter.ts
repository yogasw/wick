/* context-meter.ts — how a context-window reading is READ OUT.
 *
 * Two surfaces show the same reading and must agree on what it means:
 * the composer's Context window panel, and the sub-agent inspector. The
 * panel draws it big, with a sparkline and a Compact button; the
 * inspector has one strip of a modal header. What they share is not the
 * layout — it is the decision of WHICH of three readings this is, and
 * that decision is the part that is easy to get wrong.
 *
 * The rule the server sets (SessionContextDTO.Window): a window of 0
 * means the CLI never reported a limit. There is no denominator, so
 * there is no percentage, and inventing one would be a lie told in a
 * place people trust. A level with no window is shown as a token count;
 * no level at all is shown as nothing read yet — never as 0%, which
 * reads as an empty window rather than an unknown one. */

import { compactTokens } from "./usageReport.js";

/** How full the window is, as a band rather than a number — the colour
 *  the bar and the figure take. */
export type ContextTone = "green" | "amber" | "red";

/** Which of the three readings this is.
 *  - `ring`   a window size is known, so a percentage is meaningful
 *  - `tokens` a level but no denominator: the count, and no percentage
 *  - `none`   nothing has been read yet */
export type ContextMeterKind = "ring" | "tokens" | "none";

/** The shape of a reading, kept structural so this module does not
 *  depend on either SPA's copy of the DTO. */
export type ContextReading = {
  used?: number;
  window?: number;
  /** The server's own percentage. Preferred when present — it is the
   *  source of truth for the figure — but recomputed when absent. */
  pct?: number;
};

export type ContextMeterView = {
  kind: ContextMeterKind;
  used: number;
  window: number;
  /** 0 whenever `kind` is not "ring". Read it only through `kind`. */
  pct: number;
  tone: ContextTone;
  /** The whole reading in one line, ready to render. */
  text: string;
  /** Why it reads that way, when it is not the ordinary case. Empty for
   *  a plain ring — the number already says everything. */
  note: string;
};

function clampPct(n: number): number {
  if (!Number.isFinite(n) || n <= 0) return 0;
  return Math.min(100, n);
}

/** contextTone bands a percentage. 75 and 90 are the same thresholds the
 *  Context window panel has always used; they live here now so the two
 *  surfaces cannot drift into disagreeing about what "nearly full" is. */
export function contextTone(pct: number): ContextTone {
  return pct >= 90 ? "red" : pct >= 75 ? "amber" : "green";
}

/** contextBarClass is the fill colour for a tone. Status ramp, not the
 *  green accent: this is a warning channel, not a brand one. */
export function contextBarClass(tone: ContextTone): string {
  return tone === "red" ? "bg-neg-400" : tone === "amber" ? "bg-cau-400" : "bg-pos-400";
}

/** contextTextClass is the same tone applied to the figure itself, with
 *  a dark-mode counterpart so it survives both themes. */
export function contextTextClass(tone: ContextTone): string {
  return tone === "red"
    ? "text-neg-400"
    : tone === "amber"
      ? "text-cau-400"
      : "text-black-800 dark:text-black-600";
}

/** contextMeter turns a raw reading into the one line that describes it,
 *  and says which of the three cases it is so a caller can decide
 *  whether a bar belongs on screen at all. */
export function contextMeter(reading: ContextReading | null | undefined): ContextMeterView {
  const used = Math.max(0, reading?.used ?? 0);
  const window = Math.max(0, reading?.window ?? 0);

  if (used <= 0) {
    return {
      kind: "none",
      used: 0,
      window,
      pct: 0,
      tone: "green",
      text: "no reading yet",
      note: "Nothing has run here yet, so no context window has been reported.",
    };
  }

  if (window <= 0) {
    return {
      kind: "tokens",
      used,
      window: 0,
      pct: 0,
      tone: "green",
      text: `${compactTokens(used)} in context`,
      note: "This provider doesn't report a window size, so there's no percentage to show.",
    };
  }

  const pct = clampPct(reading?.pct ?? (used / window) * 100);
  return {
    kind: "ring",
    used,
    window,
    pct,
    tone: contextTone(pct),
    text: `${compactTokens(used)} / ${compactTokens(window)} · ${Math.round(pct)}%`,
    note: "",
  };
}

/** budgetText renders a used/cap pair the way every budget in the panel
 *  is written. A cap of 0 is uncapped, not zero-allowed, so it prints a
 *  bare count rather than "4/0". */
export function budgetText(used: number, max: number, noun: string): string {
  const u = Math.max(0, Number.isFinite(used) ? used : 0);
  const m = Math.max(0, Number.isFinite(max) ? max : 0);
  return m > 0 ? `${u}/${m} ${noun}` : `${u} ${noun}`;
}

/** tokenBudgetText is budgetText for token counts, which are compacted
 *  and which have a third case: a provider that reported no usage at
 *  all. 0 there means "never told us", so saying so beats printing a
 *  zero that reads as "this run was free". */
export function tokenBudgetText(used: number, max: number): string {
  const u = Math.max(0, Number.isFinite(used) ? used : 0);
  const m = Math.max(0, Number.isFinite(max) ? max : 0);
  if (u <= 0 && m <= 0) return "not reported";
  if (m <= 0) return `${compactTokens(u)} tokens`;
  return `${compactTokens(u)} / ${compactTokens(m)} tokens`;
}
