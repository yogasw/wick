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
    Effect.map((r) => (r ?? []).map((p) => ({ ...p, managed: p.managed ?? false }))),
  );

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
