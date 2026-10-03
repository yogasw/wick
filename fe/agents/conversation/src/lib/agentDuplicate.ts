import type { AgentItem, AgentWrite } from "./api/team.js";

/* Pure helpers behind "Duplikat agent" in the Team header menu. */

// Mirrors the server's handle rule (api_team.go): ^[a-z0-9][a-z0-9-]{1,30}$.
const HANDLE_MAX = 31;

/** duplicateHandle picks the first free `<handle>-N` (N from 2). A handle
    that already ends in -N counts on from its root, so duplicating
    @ops-2 gives @ops-3 rather than @ops-2-2. The root is cut short when
    the suffix would push the handle past the server's length limit. */
export function duplicateHandle(handle: string, taken: Iterable<string>): string {
  const used = new Set(taken);
  const root = handle.replace(/-\d+$/, "") || handle;
  for (let n = 2; ; n++) {
    const suffix = `-${n}`;
    const head = root.slice(0, HANDLE_MAX - suffix.length).replace(/-+$/, "");
    const next = head + suffix;
    if (!used.has(next)) return next;
  }
}

/** shiftColor turns a #rgb/#rrggbb colour a little around the hue wheel so
    a copy is told apart from its original at a glance. Anything else comes
    back unchanged (the server falls back to its default colour). */
export function shiftColor(color: string, degrees = 24): string {
  const m = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(color.trim());
  if (!m) return color;
  const hex = m[1].length === 3 ? [...m[1]].map((c) => c + c).join("") : m[1];
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  const d = max - min;
  if (d === 0) return `#${hex.toLowerCase()}`; // grey: no hue to turn
  const s = d / (1 - Math.abs(2 * l - 1));
  let h = max === r ? ((g - b) / d) % 6 : max === g ? (b - r) / d + 2 : (r - g) / d + 4;
  h = (((h * 60 + degrees) % 360) + 360) % 360;
  const c = (1 - Math.abs(2 * l - 1)) * s;
  const x = c * (1 - Math.abs(((h / 60) % 2) - 1));
  const o = l - c / 2;
  const [r1, g1, b1] =
    h < 60 ? [c, x, 0] : h < 120 ? [x, c, 0] : h < 180 ? [0, c, x] : h < 240 ? [0, x, c] : h < 300 ? [x, 0, c] : [c, 0, x];
  const to = (v: number) => Math.round((v + o) * 255).toString(16).padStart(2, "0");
  return `#${to(r1)}${to(g1)}${to(b1)}`;
}

/** duplicateBody is the POST body for a copy of `a`. No project_id: the
    server makes a NEW project from the persona fields, so editing the copy
    never changes the original. Persona, look, features and access are
    copied; Captain, disabled, sessions and connections are not. */
export function duplicateBody(a: AgentItem, taken: Iterable<string>): AgentWrite {
  return {
    handle: duplicateHandle(a.handle, taken),
    name: `${a.name} (copy)`,
    tagline: a.tagline ?? "",
    description: a.description,
    system_prompt: a.system_prompt,
    provider: a.provider,
    model: a.model,
    preset: a.preset,
    avatar: { kind: a.avatar?.kind, shape: a.avatar?.shape ?? "circle", color: shiftColor(a.avatar?.color ?? ""), expression: a.avatar?.expression },
    features: a.features,
    allowed_connectors: a.allowed_connectors ?? [],
    include_new_connectors: a.include_new_connectors,
    run_as: a.run_as ?? "caller",
  };
}
