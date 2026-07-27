import { Effect } from "effect";
import { apiGetE } from "@wick-fe/common-api";
import type { ModelCaps } from "@wick-fe/common-ui";

export type ProviderModelOption = {
  id: string;
  label: string;
  default: boolean;
  desc?: string;
  live?: boolean;
  caps?: ModelCaps;
};

export type ProviderOption = {
  type: string;
  name: string;
  version: string;
  usesAIRouter?: boolean;
  // Selectable models for a multi-model provider (wick). >1 entry makes the
  // composer show a nested model picker (the "›" arrow). Absent for
  // single-model / CLI providers.
  models?: ProviderModelOption[];
  // Capability-chip prefs (wick-scoped, ride on the wick option row). Read by
  // the composer to decide whether/how to render capability chips.
  show_capabilities?: boolean;
  capability_display_mode?: string;
};

export type PresetOption = {
  name: string;
};

export type ProjectOption = {
  id: string;
  name: string;
  path: string;
  managed: boolean;
  default_provider: string;
  default_preset: string;
};

export const getProviderOptions = (base: string) =>
  apiGetE<(ProviderOption & { uses_airouter?: boolean })[] | null>(`${base}/providers/options`).pipe(
    Effect.map((r) =>
      (r ?? []).map((p) => ({ ...p, usesAIRouter: p.usesAIRouter ?? p.uses_airouter ?? false })),
    ),
  );

// getProviderOptionModels asks the server for one configured provider's LIVE
// model list (wick → vendor API with the stored key; CLI → effective seed).
// The composer calls this lazily when the user drills into a provider so the
// list reflects what the vendor actually serves now, not a build-time seed.
// `entry` expands ONE live model set by its id (the 4th picker level); the
// vendor filter stays server-side. Without it the endpoint returns the
// instance's top-level model choices. Mirrors the conversation composer's
// loader so the new-session picker can expand live sets identically (before
// this it had no loader wired, so live-set rows showed "No extra models").
export const getProviderOptionModels = (
  base: string,
  type: string,
  name: string,
  opts?: { entry?: string },
) => {
  const q = opts?.entry ? `?entry=${encodeURIComponent(opts.entry)}` : "";
  return apiGetE<{ models?: ProviderModelOption[] | null }>(
    `${base}/providers/options/${encodeURIComponent(type)}/${encodeURIComponent(name)}/models${q}`,
  ).pipe(Effect.map((r) => r.models ?? []));
};

export const getPresetOptions = (base: string) =>
  apiGetE<PresetOption[] | null>(`${base}/presets/options`).pipe(
    Effect.map((r) => r ?? []),
  );

export const getProjectOptions = (base: string) =>
  apiGetE<ProjectOption[] | null>(`${base}/projects/options`).pipe(
    Effect.map((r) =>
      (r ?? []).map((p) => ({
        ...p,
        managed: p.managed ?? false,
        default_provider: p.default_provider ?? "",
        default_preset: p.default_preset ?? "",
      })),
    ),
  );

/* Project-scoped @-mention file search — the session cwd IS the project folder,
   so the new-session composer can browse it before the session exists. */
export const searchProjectFiles = (base: string, projectId: string, q: string, limit = 30) =>
  apiGetE<{ files: string[] }>(
    `${base}/api/projects/${projectId}/files/search?q=${encodeURIComponent(q)}&limit=${limit}`,
  ).pipe(Effect.map((r) => r.files ?? []));

export type ComposerApiCommand = {
  id: string;
  label: string;
  hint?: string;
  category?: string;
  action?: string;
  insert?: string;
};

/* scope=new returns the switch actions (/provider, /project) + insert-type
   skills — panels/views/send actions need a live session and are dropped.
   provider (a type like "claude") scopes skills to that provider. */
export const listComposerCommands = (base: string, scope = "new", provider = "") => {
  const p = new URLSearchParams({ scope });
  if (provider) p.set("provider", provider);
  return apiGetE<{ commands: ComposerApiCommand[] }>(`${base}/api/composer/commands?${p.toString()}`).pipe(
    Effect.map((r) => r.commands ?? []),
  );
};

export async function createSession(
  base: string,
  message: string,
  files: File[],
  provider: string,
  preset: string,
  projectId: string,
): Promise<string> {
  const fd = new FormData();
  fd.append("message", message);
  for (const f of files) fd.append("files", f);
  fd.append("provider", provider);
  fd.append("preset", preset);
  fd.append("project_id", projectId);
  const res = await fetch(`${base}/`, { method: "POST", body: fd, credentials: "same-origin" });
  if (res.ok || res.redirected) return res.url;
  const body = await res.text().catch(() => "");
  throw new Error(body || `create session failed: ${res.status}`);
}
