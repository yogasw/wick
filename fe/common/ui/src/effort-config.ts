import type { EffortPickerConfig } from "./effort-picker.mjs";

/** Wire shape of GET/POST …/effort (provider.EffortInfo). */
export type EffortInfo = {
  configurable: boolean;
  options: { id: string; label: string }[];
  default?: string;
  source?: string;
  requested: string;
  inherited?: string;
  effective?: string;
  clamped?: boolean;
  stale?: boolean;
  error?: string;
};

/** The Default stop's label: names the model default when it is known,
    "Provider default" when it is not (never pretend it is Medium). */
export function effortDefaultLabel(info: EffortInfo): string {
  const known = info.default ? info.options.find((o) => o.id === info.default) : undefined;
  return known ? `Default (${known.label})` : "Provider default";
}

/** Shared effort scale, lowest first (mirrors provider.effortRank). Ids
    off the scale (auto, vendor variants) have no nearest level. */
const EFFORT_RANK: Record<string, number> = {
  none: 0, off: 0, minimal: 1, low: 2, medium: 3, high: 4, xhigh: 5, max: 6,
};

/** The offered level closest to wanted: the highest not above it, else the
    lowest above it. "" when wanted or every option is off the scale. */
export function nearestEffort(options: readonly { id: string }[], wanted: string): string {
  const want = EFFORT_RANK[wanted];
  if (want === undefined) return "";
  let below = "", above = "";
  let belowRank = -1, aboveRank = Infinity;
  for (const o of options) {
    const r = EFFORT_RANK[o.id];
    if (r === undefined) continue;
    if (r <= want && r > belowRank) [below, belowRank] = [o.id, r];
    if (r > want && r < aboveRank) [above, aboveRank] = [o.id, r];
  }
  return below || above;
}

const UNAVAILABLE = "This model has no effort setting";

/** Composer config for an agent's effort, or undefined when the model has
    nothing to pick (a form hides the field rather than inventing levels;
    with showUnavailable the composer keeps a disabled gauge instead, so
    its buttons do not shift between models).
    The shown value is the session's own pick, else what it inherits, else
    Default. A pick the model does not offer shows as the nearest level it
    does (the pick itself is kept, so a model offering it again gets it
    back); one off the scale shows as Default. */
export function effortPickerConfig(
  info: EffortInfo | null | undefined,
  onChange: (value: string) => void,
  opts: { showUnavailable?: boolean } = {},
): EffortPickerConfig | undefined {
  if (!info) return undefined;
  if (!info.configurable || info.options.length === 0) {
    if (!opts.showUnavailable || info.error) return undefined;
    return { options: [{ id: "default", label: "Default" }], value: "default", onChange, disabledReason: UNAVAILABLE };
  }
  const options = [{ id: "default", label: effortDefaultLabel(info) }, ...info.options];
  const wanted = info.requested || info.inherited || "default";
  const value = options.some((o) => o.id === wanted) ? wanted : nearestEffort(info.options, wanted) || "default";
  return { options, value, defaultValue: info.default ?? "", onChange };
}

/** What to tell the person when their pick runs at another level on this
    model ("" when it runs as picked). */
export function effortClampMessage(info: EffortInfo | null | undefined, wanted?: string): string {
  if (!info || !info.configurable) return "";
  const pick = wanted ?? (info.requested || info.inherited || "");
  if (!pick || pick === "default" || info.options.some((o) => o.id === pick)) return "";
  const near = nearestEffort(info.options, pick);
  if (!near) return "";
  const label = (id: string) => info.options.find((o) => o.id === id)?.label ?? EFFORT_LABELS[id] ?? id;
  return `${label(pick)} isn't available on this model — using ${label(near)}`;
}

const EFFORT_LABELS: Record<string, string> = {
  none: "None", off: "Off", minimal: "Minimal", low: "Low", medium: "Medium",
  high: "High", xhigh: "Extra High", max: "Max", auto: "Auto",
};
