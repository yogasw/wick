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
  stale?: boolean;
  error?: string;
};

/** The Default stop's label: names the model default when it is known,
    "Provider default" when it is not (never pretend it is Medium). */
export function effortDefaultLabel(info: EffortInfo): string {
  const known = info.default ? info.options.find((o) => o.id === info.default) : undefined;
  return known ? `Default (${known.label})` : "Provider default";
}

/** Composer config for an agent's effort, or undefined when the model has
    nothing to pick (the picker hides rather than inventing levels).
    The shown value is the session's own pick, else what it inherits, else
    Default; a pick the model no longer offers shows as Default. */
export function effortPickerConfig(
  info: EffortInfo | null | undefined,
  onChange: (value: string) => void,
): EffortPickerConfig | undefined {
  if (!info || !info.configurable || info.options.length === 0) return undefined;
  const options = [{ id: "default", label: effortDefaultLabel(info) }, ...info.options];
  const wanted = info.requested || info.inherited || "default";
  const value = options.some((o) => o.id === wanted) ? wanted : "default";
  return { options, value, defaultValue: info.default ?? "", onChange };
}
