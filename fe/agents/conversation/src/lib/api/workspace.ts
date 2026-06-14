import { apiGetE, apiPostE, apiDeleteE } from "@wick-fe/common-api";
import type { WsInstance, WsBase } from "../types/agents.js";

export const listWorkspace = (base: string, sessionId: string) =>
  apiGetE<{ instances: WsInstance[]; bases: WsBase[] }>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/workspace`,
  );

export const addWorkspace = (base: string, sessionId: string, baseKey: string, label?: string) =>
  apiPostE<WsInstance>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/workspace`,
    label ? { base_key: baseKey, label } : { base_key: baseKey },
  );

export const getWorkspaceInstance = (base: string, sessionId: string, cid: string) =>
  apiGetE<WsInstance>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/workspace/${encodeURIComponent(cid)}`,
  );

export const saveWorkspaceConfig = (
  base: string,
  sessionId: string,
  cid: string,
  values: Record<string, string>,
) =>
  apiPostE<{ status: string }>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/workspace/${encodeURIComponent(cid)}`,
    { values },
  );

export const duplicateWorkspace = (base: string, sessionId: string, cid: string) =>
  apiPostE<WsInstance>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/workspace/${encodeURIComponent(cid)}/duplicate`,
  );

export const renameWorkspace = (base: string, sessionId: string, cid: string, label: string) =>
  apiPostE<{ status: string }>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/workspace/${encodeURIComponent(cid)}/rename`,
    { label },
  );

export const testWorkspace = (
  base: string,
  sessionId: string,
  cid: string,
  config: Record<string, string>,
) =>
  apiPostE<{ ok: boolean; error?: string; no_health_check?: boolean }>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/workspace/${encodeURIComponent(cid)}/test`,
    { config },
  );

export const removeWorkspace = (base: string, sessionId: string, cid: string) =>
  apiDeleteE<{ status: string }>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/workspace/${encodeURIComponent(cid)}`,
  );
