import { apiGetE, apiPostE, apiDeleteE } from "@wick-fe/common-api";
import type { ContextFileEntry, FileContent } from "../types/agents.js";

export const listFiles = (base: string, id: string) =>
  apiGetE<{ cwd: string; files: ContextFileEntry[] }>(`${base}/sessions/${id}/files`);

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
