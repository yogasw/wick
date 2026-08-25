import { Effect } from "effect";
import { apiGetE, apiPostE } from "@wick-fe/common-api";
import type { ProviderOption, ProviderModelOption, ProjectOption } from "../types/agents.js";

export const getProviderOptions = (base: string) =>
  apiGetE<(ProviderOption & { uses_airouter?: boolean })[] | null>(`${base}/providers/options`).pipe(
    Effect.map((r) =>
      (r ?? []).map((p) => ({
        ...p,
        usesAIRouter: p.usesAIRouter ?? p.uses_airouter ?? false,
        models: p.models ?? undefined,
      })),
    ),
  );

// getProviderOptionModels asks the server for one configured provider's LIVE
// model list (wick → vendor API with the stored key; CLI → effective seed).
// The composer calls this lazily when the user drills into a provider so the
// list reflects what the vendor actually serves now, not a build-time seed.
// The server falls back to the curated list on any discovery error, so this
// always resolves to something usable.
export const getProviderOptionModels = (
  base: string,
  type: string,
  name: string,
  opts?: { entry?: string },
) => {
  // `entry` expands ONE live model set by its id (the 4th picker level); the
  // vendor filter stays server-side. Without it the endpoint returns the
  // instance's top-level model choices.
  const q = opts?.entry ? `?entry=${encodeURIComponent(opts.entry)}` : "";
  return apiGetE<{ models?: ProviderModelOption[] | null }>(
    `${base}/providers/options/${encodeURIComponent(type)}/${encodeURIComponent(name)}/models${q}`,
  ).pipe(Effect.map((r) => r.models ?? []));
};

// getPresetOptions lists the configured presets ([{name}]) so the project
// landing / init composer can offer a preset selector (same source the
// new-session page uses).
export const getPresetOptions = (base: string) =>
  apiGetE<{ name: string }[] | null>(`${base}/presets/options`).pipe(
    Effect.map((r) => (r ?? []).map((p) => p.name)),
  );

export const getProjectOptions = (base: string) =>
  apiGetE<
    (ProjectOption & {
      default_provider?: string;
      default_model?: string;
      ticket_enabled?: boolean;
    })[] | null
  >(`${base}/projects/options`).pipe(
    Effect.map((r) =>
      (r ?? []).map((p) => ({
        ...p,
        managed: p.managed ?? false,
        pinned: p.pinned ?? false,
        defaultProvider: p.defaultProvider ?? p.default_provider ?? "",
        // Carried alongside the provider so the landing composer can preselect
        // the exact model the project pins, down to a live-set leaf.
        defaultModel: p.defaultModel ?? p.default_model ?? "",
        ticketEnabled: p.ticketEnabled ?? p.ticket_enabled ?? false,
      })),
    ),
  );

export const switchProvider = (base: string, sessionId: string, provider: string, modelId?: string) =>
  apiPostE<{ status: string; provider?: string }>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/provider`,
    modelId ? { provider, model_id: modelId } : { provider },
  );

export const moveProject = (base: string, sessionId: string, projectId: string | null) =>
  apiPostE<{ status: string; project_id?: string }>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/project`,
    { project_id: projectId },
  );

export const pinProject = (base: string, projectId: string) =>
  apiPostE<{ status: string; pinned: boolean; project_id: string }>(
    `${base}/projects/${encodeURIComponent(projectId)}/pin`,
    {},
  );

export async function createSessionInProject(
  base: string,
  message: string,
  files: File[],
  provider: string,
  projectId: string,
  preset = "",
): Promise<string> {
  const fd = new FormData();
  fd.append("message", message);
  for (const f of files) fd.append("files", f);
  fd.append("provider", provider);
  fd.append("project_id", projectId);
  if (preset) fd.append("preset", preset);
  const res = await fetch(`${base}/`, { method: "POST", body: fd, credentials: "same-origin" });
  if (res.ok || res.redirected) return res.url;
  throw new Error(`create session failed: ${res.status}`);
}
