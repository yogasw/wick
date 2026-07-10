import { Effect } from "effect";
import { apiGetE, apiPostE, apiDeleteE } from "@wick-fe/common-api";
import type { ContextFileEntry, FileContent } from "../types/agents.js";

export const listFiles = (base: string, id: string) =>
  apiGetE<{ cwd: string; files: ContextFileEntry[] }>(`${base}/sessions/${id}/files`).pipe(
    Effect.map((r) => ({ ...r, files: r.files ?? [] })),
  );

/* Backend @-mention search: ranked file paths matching space-separated AND
   terms, over the whole tree (not the list endpoint's client cap). */
export const searchFiles = (base: string, id: string, q: string, limit = 30) =>
  apiGetE<{ files: string[] }>(
    `${base}/sessions/${id}/files/search?q=${encodeURIComponent(q)}&limit=${limit}`,
  ).pipe(Effect.map((r) => r.files ?? []));

/* Project-scoped @-mention search — the session cwd is the project folder, so
   the project-landing composer can browse it before a session exists. */
export const searchProjectFiles = (base: string, projectId: string, q: string, limit = 30) =>
  apiGetE<{ files: string[] }>(
    `${base}/api/projects/${projectId}/files/search?q=${encodeURIComponent(q)}&limit=${limit}`,
  ).pipe(Effect.map((r) => r.files ?? []));

export const readFile = (base: string, id: string, path: string) =>
  apiGetE<FileContent>(`${base}/sessions/${id}/files/read?path=${encodeURIComponent(path)}`);

export const saveFile = (base: string, id: string, path: string, content: string) =>
  apiPostE<{ status: string }>(`${base}/sessions/${id}/files/save`, { path, content });

export const createFile = (base: string, id: string, path: string, isDir: boolean) =>
  apiPostE<{ path: string }>(`${base}/sessions/${id}/files/create`, { path, isDir });

export const deleteFile = (base: string, id: string, path: string) =>
  apiDeleteE<{ status: string }>(`${base}/sessions/${id}/files?path=${encodeURIComponent(path)}`);

export const downloadURL = (base: string, id: string, path: string): string =>
  `${base}/sessions/${id}/files/download?path=${encodeURIComponent(path)}`;
