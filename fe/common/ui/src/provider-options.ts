import { normalizeProviderKey, type ProviderListItem } from "@wick-fe/common-api";
import type { ComposerSelectOption } from "./composer-types.js";

/**
 * buildProviderOptions turns the server's provider instances into picker
 * options.
 *
 * Each option's value is the "type/name" key the caller stores; the label
 * drops the redundant name for a canonical instance (claude/claude →
 * Claude). When `current` names an instance the server no longer offers —
 * deleted, renamed, or simply unhealthy — it is appended as an
 * "(unavailable)" option so opening the form cannot silently move a saved
 * value onto some other provider.
 *
 * `current` may carry a pinned model as "type/name::modelID"; only the
 * instance half is compared, and a bare type is normalized first so a role
 * stored before instances existed still matches a real option.
 */
export function buildProviderOptions(
  list: ProviderListItem[],
  current: string,
): ComposerSelectOption[] {
  const opts: ComposerSelectOption[] = list.map((p) => ({
    value: `${p.type}/${p.name}`,
    label:
      p.name === p.type ? p.type.charAt(0).toUpperCase() + p.type.slice(1) : `${p.type}/${p.name}`,
    models: (p.models ?? []).map((m) => ({
      id: m.id,
      label: m.label,
      default: m.default,
      desc: m.desc,
    })),
  }));
  const bare = (current ?? "").split("::")[0];
  const key = bare ? normalizeProviderKey(bare) : "";
  if (key && !opts.some((o) => o.value === key)) {
    opts.push({ value: key, label: `${key} (unavailable)`, models: [] });
  }
  return opts;
}
