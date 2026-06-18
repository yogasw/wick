import { Effect } from "effect";
import { apiGetE, apiPostE } from "@wick-fe/common-api";
import type { ProviderOption, ProjectOption } from "../types/agents.js";

export const getProviderOptions = (base: string) =>
  apiGetE<ProviderOption[] | null>(`${base}/providers/options`).pipe(
    Effect.map((r) => r ?? []),
  );

export const getProjectOptions = (base: string) =>
  apiGetE<ProjectOption[] | null>(`${base}/projects/options`).pipe(
    Effect.map((r) => (r ?? []).map((p) => ({ ...p, managed: p.managed ?? false, pinned: p.pinned ?? false }))),
  );

export const switchProvider = (base: string, sessionId: string, provider: string) =>
  apiPostE<{ status: string; provider?: string; redirect?: string }>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/provider`,
    { provider },
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
): Promise<string> {
  const fd = new FormData();
  fd.append("message", message);
  for (const f of files) fd.append("files", f);
  fd.append("provider", provider);
  fd.append("project_id", projectId);
  const res = await fetch(`${base}/`, { method: "POST", body: fd, credentials: "same-origin" });
  if (res.ok || res.redirected) return res.url;
  throw new Error(`create session failed: ${res.status}`);
}
