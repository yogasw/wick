import { Effect } from "effect";
import { apiGetE, apiPostE, apiDeleteE } from "@wick-fe/common-api";
import type { WsInstance, WsBase, WsTombstone } from "../types/agents.js";

const normalizeInstance = (i: WsInstance): WsInstance => ({ ...i, fields: i.fields ?? [] });

export const listWorkspace = (base: string, sessionId: string) =>
  apiGetE<{ instances: WsInstance[]; bases: WsBase[]; deleted?: WsTombstone[] }>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/workspace`,
  ).pipe(
    Effect.map((r) => ({
      instances: (r.instances ?? []).map(normalizeInstance),
      bases: r.bases ?? [],
      deleted: r.deleted ?? [],
    })),
  );

export const addWorkspace = (base: string, sessionId: string, baseKey: string, label?: string) =>
  apiPostE<WsInstance>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/workspace`,
    label ? { base_key: baseKey, label } : { base_key: baseKey },
  ).pipe(Effect.map(normalizeInstance));

export const getWorkspaceInstance = (base: string, sessionId: string, cid: string) =>
  apiGetE<WsInstance>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/workspace/${encodeURIComponent(cid)}`,
  ).pipe(Effect.map(normalizeInstance));

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
  ).pipe(Effect.map(normalizeInstance));

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
