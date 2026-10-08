// Git actions operating on the scm stores. Kept out of components so
// both the sidebar and full layouts share one implementation.

import { get } from "svelte/store";
import * as api from "$lib/api/scm";
import type { FileChange, CompareFile, CompareResult } from "$lib/api/scm";
import { toastOk, toastError } from "@wick-fe/common-stores";
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

// gitErrorText trims git's own multi-line explanation off a server error so
// the toast carries the one line that says what happened.
function gitErrorText(e: unknown): string {
  const s = e instanceof Error ? e.message : String(e);
  const cuts = [s.indexOf(": git "), s.indexOf(": fatal:")].filter((i) => i > 0);
  return cuts.length ? s.slice(0, Math.min(...cuts)) : s.split("\n")[0];
}

export async function stagePaths(paths: string[]): Promise<void> {
  try {
    await api.stage(sid(), repo(), paths);
  } catch (e) {
    toastError("Stage failed", gitErrorText(e));
  }
  await loadStatus();
}

export async function unstagePaths(paths: string[]): Promise<void> {
  try {
    await api.unstage(sid(), repo(), paths);
  } catch (e) {
    toastError("Unstage failed", gitErrorText(e));
  }
  await loadStatus();
}

// Discard is destructive — callers MUST confirm first. untrackedPaths is
// the subset of paths that are untracked (server uses clean vs restore).
// excludePaths makes git stop reporting a path — nothing is deleted, and
// the entry lives in .git/info/exclude, which is not committed, so it is
// one person's decision about their own checkout.
export async function excludePaths(paths: string[]): Promise<void> {
  try {
    await api.exclude(sid(), repo(), paths);
    toastOk("Ignored", `${paths.length === 1 ? paths[0] : `${paths.length} paths`} — added to .git/info/exclude`);
    await loadStatus();
  } catch (e) {
    toastError("Ignore failed", String(e));
  }
}

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

// push/pull take an explicit connector only on the first run in a repo,
// when the picker just asked; afterwards the session remembers the
// choice and these are called with no argument.
// Both return the error text (empty on success) so the caller can react
// to WHY it failed — an auth refusal means the panel should offer the
// credentials it has, rather than leaving a toast and a dead button.
export async function push(connectorID?: string): Promise<string> {
  try {
    await api.push(sid(), repo(), connectorID);
    toastOk("Pushed");
    await loadStatus();
    return "";
  } catch (e) {
    toastError("Push failed", String(e));
    return String(e);
  }
}

// Fetch is the read-only half of pull: it refreshes what the server has
// without merging anything into the working tree, so ahead/behind becomes
// true again without risking a conflict.
export async function fetchRemote(connectorID?: string): Promise<string> {
  try {
    await api.fetchRemote(sid(), repo(), connectorID);
    toastOk("Fetched");
    await loadStatus();
    return "";
  } catch (e) {
    toastError("Fetch failed", String(e));
    return String(e);
  }
}

export async function pull(connectorID?: string): Promise<string> {
  try {
    await api.pull(sid(), repo(), connectorID);
    toastOk("Pulled");
    await loadStatus();
    return "";
  } catch (e) {
    toastError("Pull failed", String(e));
    return String(e);
  }
}

// gitConnectors reports which Git CLI instances this repo can push
// through and which one it already uses.
export async function gitConnectors(): Promise<api.GitConnectorsResponse | null> {
  try {
    return await api.getGitConnectors(sid(), repo());
  } catch (e) {
    toastError("Git connectors", String(e));
    return null;
  }
}

// rememberGitConnector stores the repo's choice. An empty id clears it,
// which makes the next push ask again.
export async function rememberGitConnector(connectorID: string): Promise<boolean> {
  try {
    await api.setGitConnector(sid(), repo(), connectorID);
    return true;
  } catch (e) {
    toastError("Git connector", String(e));
    return false;
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

export async function createBranch(name: string, from = ""): Promise<void> {
  if (!name.trim()) return;
  try {
    await api.createBranch(sid(), repo(), name, true, from);
    toastOk("Branch created", from ? `${name} from ${from}` : name);
    await loadStatus();
  } catch (e) {
    toastError("Create failed", String(e));
  }
}

export async function renameBranch(from: string, to: string): Promise<void> {
  if (!to.trim() || to === from) return;
  try {
    await api.renameBranch(sid(), repo(), from, to);
    toastOk("Branch renamed", `${from} → ${to}`);
    await loadStatus();
  } catch (e) {
    toastError("Rename failed", String(e));
  }
}

// Deleting is the one branch action that can lose work, so it asks twice: the
// first attempt is git's own safe delete, and only its "not fully merged"
// refusal escalates to the forced form — with the branch's tip in the prompt,
// because that sha is what makes the deletion recoverable.
export async function deleteBranch(name: string, tip = ""): Promise<void> {
  try {
    await api.deleteBranch(sid(), repo(), name, false);
    toastOk("Branch deleted", name);
    await loadStatus();
    return;
  } catch (e) {
    const msg = String(e);
    if (!/not fully merged|not merged/i.test(msg)) {
      toastError("Delete failed", msg);
      return;
    }
    const where = tip ? ` Its tip is ${tip} — restore with: git branch ${name} ${tip}` : "";
    if (!confirm(`${name} is not fully merged. Delete it anyway?${where}`)) return;
  }
  try {
    await api.deleteBranch(sid(), repo(), name, true);
    toastOk("Branch deleted (forced)", name);
    await loadStatus();
  } catch (e) {
    toastError("Delete failed", String(e));
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

// ---------------------------------------------------------------------------
// Branch compare + rollback. Same repo/session resolution as everything else
// here, so the compare overlay never has to know which repo is selected.

// compareRefs answers the whole header at once: the changed files, their ±
// counts and how far apart the two refs are. null means the request failed
// and the user already saw why.
export async function compareRefs(
  base: string,
  head: string,
  threeDot: boolean,
): Promise<CompareResult | null> {
  try {
    return await api.compareRefs(sid(), repo(), base, head, threeDot);
  } catch (e) {
    toastError("Compare failed", String(e));
    return null;
  }
}

// loadRefCompare fetches the two raw sides of one compared file. orig_path
// and three_dot are passed through because both change which left side git
// reads — without them the diff contradicts the list it was opened from.
export async function loadRefCompare(
  base: string,
  head: string,
  f: CompareFile,
  threeDot: boolean,
): Promise<CompareData> {
  const r = await api.refCompare(sid(), repo(), base, head, f.path, threeDot, f.orig_path ?? "");
  return { original: r.original, modified: r.modified };
}

// restoreFromRef takes paths back to how they look at ref. Destructive to
// whatever is in the working tree for those paths — callers confirm first.
export async function restoreFromRef(ref: string, paths: string[]): Promise<boolean> {
  try {
    await api.restorePaths(sid(), repo(), ref, paths);
    toastOk("Restored", `${paths.length === 1 ? paths[0] : `${paths.length} files`} from ${ref}`);
    await loadStatus();
    return true;
  } catch (e) {
    toastError("Restore failed", String(e));
    return false;
  }
}

// revertCommit applies a commit's inverse. On conflict git stops half way
// and leaves the tree mid-revert: say so, because the next thing the user
// does depends on knowing the repo is in that state. Nothing here aborts
// it — that would throw away a resolution already in progress.
export async function revertCommit(sha: string): Promise<boolean> {
  try {
    await api.revertCommit(sid(), repo(), sha);
    toastOk("Reverted", sha);
    await loadStatus();
    return true;
  } catch (e) {
    const msg = String(e);
    if (/conflict/i.test(msg)) {
      toastError(
        "Revert conflicted",
        `${msg} — the repo is left mid-revert. Resolve the files and commit, or run git revert --abort.`,
      );
    } else {
      toastError("Revert failed", msg);
    }
    await loadStatus();
    return false;
  }
}

// resetCurrentTo moves the checked-out branch to ref. hard throws away
// uncommitted work, so callers mark that one destructive in the confirm.
export async function resetCurrentTo(
  ref: string,
  mode: "soft" | "mixed" | "hard",
): Promise<boolean> {
  try {
    await api.resetTo(sid(), repo(), ref, mode);
    toastOk("Branch reset", `${mode} → ${ref}`);
    await loadStatus();
    return true;
  } catch (e) {
    toastError("Reset failed", String(e));
    return false;
  }
}

export type { CompareFile, CompareResult };

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
