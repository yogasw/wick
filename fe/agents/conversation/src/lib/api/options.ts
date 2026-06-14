import { Effect } from "effect";
import { apiGetE, apiPostE } from "@wick-fe/common-api";
import type { ProviderOption, ProjectOption } from "../types/agents.js";

export const getProviderOptions = (base: string) =>
  apiGetE<ProviderOption[] | null>(`${base}/providers/options`).pipe(
    Effect.map((r) => r ?? []),
  );

export const getProjectOptions = (base: string) =>
  apiGetE<ProjectOption[] | null>(`${base}/projects/options`).pipe(
    Effect.map((r) => r ?? []),
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
