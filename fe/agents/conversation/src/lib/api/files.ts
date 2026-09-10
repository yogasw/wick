import { Effect } from "effect";
import { apiGetE, apiPostE, apiDeleteE } from "@wick-fe/common-api";
import type { ContextFileEntry, FileContent } from "../types/agents.js";

/* One directory's immediate children — the session cwd when path is empty.
   Depth costs a request; it is never preloaded. A session holding 55 clones
   has ~400 entries at the top level and hundreds of thousands below it, so
   fetching the whole tree up front is both slow and, past the server's cap,
   silently incomplete. */
export const listFiles = (base: string, id: string, path = "") =>
  apiGetE<{ cwd: string; path: string; files: ContextFileEntry[]; truncated?: boolean }>(
    `${base}/sessions/${id}/files${path ? `?path=${encodeURIComponent(path)}` : ""}`,
  ).pipe(Effect.map((r) => ({ ...r, files: r.files ?? [] })));

/* Whole-tree name search for the file panel's "Subfolders" mode. Unlike
   searchMentionPaths (the @-mention path) this returns DIRECTORIES too, plus the
   ancestors of every hit so the caller can attach them to its tree. */
export const searchTree = (base: string, id: string, q: string, limit = 300) =>
  apiGetE<{ files: ContextFileEntry[]; truncated?: boolean }>(
    `${base}/sessions/${id}/files/search?q=${encodeURIComponent(q)}&limit=${limit}`,
  ).pipe(Effect.map((r) => ({ files: r.files ?? [], truncated: r.truncated === true })));

/* Backend @-mention search: ranked file paths matching space-separated AND
   terms, over the whole tree (not the list endpoint's client cap). */
export const searchMentionPaths = (base: string, id: string, q: string, limit = 30) =>
  apiGetE<{ files: string[] }>(
    `${base}/sessions/${id}/files/mentions?q=${encodeURIComponent(q)}&limit=${limit}`,
  ).pipe(Effect.map((r) => r.files ?? []));

/* Project-scoped @-mention search — the session cwd is the project folder, so
   the project-landing composer can browse it before a session exists. */
export const searchProjectMentionPaths = (base: string, projectId: string, q: string, limit = 30) =>
  apiGetE<{ files: string[] }>(
    `${base}/api/projects/${projectId}/files/mentions?q=${encodeURIComponent(q)}&limit=${limit}`,
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
