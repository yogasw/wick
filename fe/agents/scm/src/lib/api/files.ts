// Session files API — browse and edit the files in a session's working
// directory. Separate from api/scm.ts because these endpoints are not git:
// they predate the Source panel and back the conversation shell's Files
// panel. The Files tab reuses them rather than growing a git-side twin, and
// scopes every path to the selected repo with $lib/files-root.
//
// Every path here is relative to the SESSION cwd, never to the repo.

import { apiGet, apiPost, apiDelete } from "@wick-fe/common-api";

const BASE = "/tools/agents";

export type FileEntry = {
  /** Session-relative, forward slashes. */
  path: string;
  name: string;
  /** Bytes; 0 for directories. */
  size: number;
  isDir: boolean;
  /** Unix ms. */
  mtime: number;
};

export type DirListing = {
  /** Absolute session working directory — the root all paths hang off. */
  cwd: string;
  path: string;
  files: FileEntry[];
  /** One pathological folder hit the server's per-directory cap. */
  truncated?: boolean;
};

// content is absent for a binary or oversized file: the server refuses to
// ship bytes the editor cannot show, and the client offers a download.
export type FileContent = {
  path: string;
  size: number;
  binary?: boolean;
  tooBig?: boolean;
  content?: string;
  mtime?: number;
};

const s = (id: string) => `${BASE}/sessions/${encodeURIComponent(id)}/files`;
const q = (v: string) => encodeURIComponent(v);

// One directory's immediate children. Depth costs a request and the tree is
// never preloaded — a session holding dozens of clones has hundreds of
// thousands of files below its top level.
export const listDir = (id: string, path = "") =>
  apiGet<DirListing>(`${s(id)}${path ? `?path=${q(path)}` : ""}`).then((r) => ({
    ...r,
    files: r.files ?? [],
  }));

// Whole-tree name search (directories included). Results are session-wide,
// so a repo-scoped caller has to drop what falls outside its root.
export const searchFiles = (id: string, query: string, limit = 300) =>
  apiGet<{ files: FileEntry[]; truncated?: boolean }>(
    `${s(id)}/search?q=${q(query)}&limit=${limit}`,
  ).then((r) => ({ files: r.files ?? [], truncated: r.truncated === true }));

export const readFile = (id: string, path: string) =>
  apiGet<FileContent>(`${s(id)}/read?path=${q(path)}`);

export const saveFile = (id: string, path: string, content: string) =>
  apiPost<{ ok: boolean; path: string; size: number; mtime: number }>(`${s(id)}/save`, {
    path,
    content,
  });

// Creates an empty file or a directory; missing parents are created, and an
// existing target is a 409 rather than a silent overwrite.
export const createEntry = (id: string, path: string, isDir: boolean) =>
  apiPost<{ ok: boolean; path: string; isDir: boolean }>(`${s(id)}/create`, { path, isDir });

// Recursive for directories — callers confirm first.
export const deleteEntry = (id: string, path: string) =>
  apiDelete<{ ok: boolean }>(`${s(id)}?path=${q(path)}`);

// A plain href, not a fetch: the browser's own download handles the
// attachment headers and streams whatever size the file is.
export const downloadURL = (id: string, path: string): string =>
  `${s(id)}/download?path=${q(path)}`;
