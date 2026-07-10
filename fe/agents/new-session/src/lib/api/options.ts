import { Effect } from "effect";
import { apiGetE } from "@wick-fe/common-api";

export type ProviderOption = {
  type: string;
  name: string;
  version: string;
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
  apiGetE<ProviderOption[] | null>(`${base}/providers/options`).pipe(
    Effect.map((r) => r ?? []),
  );

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

/* scope=new returns only insert-type commands (skills) — panels/views/switch
   don't apply before a session exists. provider (a type like "claude") scopes
   skills to that provider. */
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
