import { Effect } from "effect";
import { apiGetE, apiPostE, apiDeleteE } from "@wick-fe/common-api";
import type { TraceFileStat } from "@wick-fe/common-ui";
import type { SessionFileEntry, FileContent } from "../types/agents.js";

/* One directory's immediate children — the session cwd when path is empty.
   Depth costs a request; it is never preloaded. A session holding 55 clones
   has ~400 entries at the top level and hundreds of thousands below it, so
   fetching the whole tree up front is both slow and, past the server's cap,
   silently incomplete. */
export const listFiles = (base: string, id: string, path = "") =>
  apiGetE<{ cwd: string; path: string; files: SessionFileEntry[]; truncated?: boolean }>(
    `${base}/sessions/${id}/files${path ? `?path=${encodeURIComponent(path)}` : ""}`,
  ).pipe(Effect.map((r) => ({ ...r, files: r.files ?? [] })));

/* Whole-tree name search for the file panel's "Subfolders" mode. Unlike
   searchMentionPaths (the @-mention path) this returns DIRECTORIES too, plus the
   ancestors of every hit so the caller can attach them to its tree. */
export const searchTree = (base: string, id: string, q: string, limit = 300) =>
  apiGetE<{ files: SessionFileEntry[]; truncated?: boolean }>(
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

/* Files a trace named, checked lazily: every stat asked for in the same tick
   goes out as one request, and each path is asked once per session. */
export type TraceFiles = {
  stat: (path: string) => Promise<TraceFileStat>;
  load: (rel: string) => Promise<Blob>;
};

export function makeTraceFiles(base: string, id: string, fetchImpl: typeof fetch = (...a) => fetch(...a)): TraceFiles {
  const cache = new Map<string, Promise<TraceFileStat>>();
  let pending: { path: string; resolve: (s: TraceFileStat) => void }[] = [];

  async function flush(batch: typeof pending): Promise<void> {
    const q = batch.map((b) => `path=${encodeURIComponent(b.path)}`).join("&");
    let files: (TraceFileStat & { path: string })[] = [];
    try {
      const res = await fetchImpl(`${base}/sessions/${id}/files/stat?${q}`, { credentials: "same-origin" });
      if (res.ok) files = ((await res.json()) as { files?: typeof files }).files ?? [];
    } catch {
      // Unreachable reads as "could not check", never as deleted.
    }
    for (const b of batch) {
      const hit = files.find((f) => f.path === b.path);
      if (!hit) cache.delete(b.path);
      b.resolve(hit ?? { status: "unknown" });
    }
  }

  return {
    stat(path) {
      let p = cache.get(path);
      if (!p) {
        p = new Promise<TraceFileStat>((resolve) => {
          if (!pending.length) queueMicrotask(() => { const b = pending; pending = []; void flush(b); });
          pending.push({ path, resolve });
        });
        cache.set(path, p);
      }
      return p;
    },
    async load(rel) {
      const res = await fetchImpl(`${base}/sessions/${id}/files/raw?path=${encodeURIComponent(rel)}`, { credentials: "same-origin" });
      if (!res.ok) throw new Error(`file ${rel}: ${res.status}`);
      return res.blob();
    },
  };
}
