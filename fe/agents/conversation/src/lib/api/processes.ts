import { Effect } from "effect";
import { apiGetE, apiPostE } from "@wick-fe/common-api";
import type { ProcessInfo } from "../types/agents.js";

// liveProcesses drops the idle-fallback rows (kind === "idle"): those exist
// only to carry the session's provider/agent name for the composer toolbar
// when the pool has no process at all. They are not running processes, so
// they must not be counted in the "Process N" rail badge nor rendered as a
// (misleading "dead") process card. A row with no kind is treated as a
// process for backward compat with older payloads.
export const liveProcesses = (procs: ProcessInfo[]): ProcessInfo[] =>
  procs.filter((p) => p.kind !== "idle");

export const getProcesses = (base: string, id: string) =>
  apiGetE<ProcessInfo[]>(`${base}/sessions/${encodeURIComponent(id)}/processes`).pipe(
    Effect.map((r) => r ?? []),
  );

export const killProcess = (base: string, sessionId: string) =>
  apiPostE<{ status: string }>(`${base}/sessions/${encodeURIComponent(sessionId)}/kill`, {});

export const dequeueProcess = (base: string, sessionId: string) =>
  apiPostE<{ status: string }>(`${base}/sessions/${encodeURIComponent(sessionId)}/dequeue`, {});
