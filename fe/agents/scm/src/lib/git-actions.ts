// Git actions operating on the scm stores. Kept out of components so
// both the sidebar and full layouts share one implementation.

import { get } from "svelte/store";
import * as api from "$lib/api/scm";
import type { FileChange } from "$lib/api/scm";
import { toastOk, toastError } from "$lib/stores/toast";
import {
  sessionID,
  activeRepo,
  changes,
  branch,
  loadStatus,
  loadRepos,
  selection,
} from "$lib/stores/scm";

const sid = () => get(sessionID);
const repo = () => get(activeRepo);

export async function stagePaths(paths: string[]): Promise<void> {
  await api.stage(sid(), repo(), paths);
  await loadStatus();
}

export async function unstagePaths(paths: string[]): Promise<void> {
  await api.unstage(sid(), repo(), paths);
  await loadStatus();
}

// Discard is destructive — callers MUST confirm first. untrackedPaths is
// the subset of paths that are untracked (server uses clean vs restore).
export async function discardPaths(paths: string[], untrackedPaths: string[]): Promise<void> {
  try {
    await api.discard(sid(), repo(), paths, untrackedPaths);
    toastOk("Discarded", paths.length === 1 ? paths[0] : `${paths.length} files`);
    await loadStatus();
  } catch (e) {
    toastError("Discard failed", String(e));
  }
}

export async function commit(message: string): Promise<boolean> {
  if (!message.trim()) {
    toastError("Commit", "Message is empty.");
    return false;
  }
  try {
    const r = await api.commit(sid(), repo(), message);
    toastOk("Committed", r.sha);
    await loadStatus();
    return true;
  } catch (e) {
    toastError("Commit failed", String(e));
    return false;
  }
}

export async function push(): Promise<void> {
  try {
    await api.push(sid(), repo());
    toastOk("Pushed");
    await loadStatus();
  } catch (e) {
    toastError("Push failed", String(e));
  }
}

export async function pull(): Promise<void> {
  try {
    await api.pull(sid(), repo());
    toastOk("Pulled");
    await loadStatus();
  } catch (e) {
    toastError("Pull failed", String(e));
  }
}

export async function listBranches(): Promise<{ locals: string[]; remotes: string[] }> {
  try {
    const bl = await api.getBranches(sid(), repo());
    return { locals: bl.branches, remotes: bl.remotes ?? [] };
  } catch (e) {
    toastError("Branches", String(e));
    return { locals: [], remotes: [] };
  }
}

export async function switchBranch(b: string): Promise<void> {
  try {
    await api.switchBranch(sid(), repo(), b);
    await loadStatus();
  } catch (e) {
    toastError("Switch failed", String(e));
  }
}

export async function createBranch(name: string): Promise<void> {
  if (!name.trim()) return;
  try {
    await api.createBranch(sid(), repo(), name, true);
    toastOk("Branch created", name);
    await loadStatus();
  } catch (e) {
    toastError("Create failed", String(e));
  }
}

export async function saveFile(path: string, content: string): Promise<void> {
  try {
    await api.saveFile(sid(), repo(), path, content);
    toastOk("Saved", path);
    await loadStatus();
  } catch (e) {
    toastError("Save failed", String(e));
  }
}

// Compare data for the diff view: original (HEAD/parent) vs modified
// (working/commit), raw — Monaco computes the diff itself. Accurate and
// keeps full context, unlike reconstructing from a unified diff.
export type CompareData = { original: string; modified: string };

// loadCompare fetches the two raw sides for a working-tree change. The
// server picks the sides git-correctly from the staged flag:
//   staged   → HEAD vs index
//   unstaged → index vs working
//   untracked → "" vs working
export async function loadCompare(c: FileChange, staged: boolean): Promise<CompareData> {
  const r = await api.getCompare(sid(), repo(), c.path, staged, c.untracked);
  return { original: r.original, modified: r.modified };
}

// loadCommitCompare fetches both raw sides for a file in a past commit
// (parent vs commit).
export async function loadCommitCompare(sha: string, path: string): Promise<CompareData> {
  const r = await api.getCommitDiff(sid(), repo(), sha, path);
  return { original: r.original, modified: r.modified };
}

export function langFor(path: string): string {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  const map: Record<string, string> = {
    go: "go", ts: "typescript", tsx: "typescript", js: "javascript",
    jsx: "javascript", py: "python", rs: "rust", java: "java",
    json: "json", md: "markdown", yml: "yaml", yaml: "yaml",
    html: "html", css: "css", sh: "shell", sql: "sql",
  };
  return map[ext] ?? "plaintext";
}

export function statusBadge(c: FileChange): string {
  if (c.untracked) return "U";
  if (c.index === "A") return "A";
  if (c.index === "D" || c.work_tree === "D") return "D";
  if (c.work_tree === "M" || c.index === "M") return "M";
  return c.index !== "." ? c.index : c.work_tree;
}

export type { FileChange };
export { selection, loadRepos, loadStatus };
