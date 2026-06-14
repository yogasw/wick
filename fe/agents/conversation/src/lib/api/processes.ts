import { Effect } from "effect";
import { apiGetE, apiPostE } from "@wick-fe/common-api";
import type { ProcessInfo } from "../types/agents.js";

export const getProcesses = (base: string, id: string) =>
  apiGetE<ProcessInfo[]>(`${base}/sessions/${encodeURIComponent(id)}/processes`).pipe(
    Effect.map((r) => r ?? []),
  );

export const killProcess = (base: string, sessionId: string) =>
  apiPostE<{ status: string }>(`${base}/sessions/${encodeURIComponent(sessionId)}/kill`, {});

export const dequeueProcess = (base: string, sessionId: string) =>
  apiPostE<{ status: string }>(`${base}/sessions/${encodeURIComponent(sessionId)}/dequeue`, {});
