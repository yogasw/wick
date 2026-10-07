// Folder view for a branch compare. The tree itself is the Changes tree —
// buildTree already collapses single-child chains VSCode-style and sorts
// folders first, and a compare with 159 files wants exactly that. What a
// compare adds is per-row numbers, so this module is the adapter: it feeds
// CompareFile into buildTree, totals each folder's subtree, and flattens
// back to the rows actually on screen for the keyboard walk.

import type { CompareFile, FileChange } from "$lib/api/scm";
import type { TreeNode } from "$lib/tree";
import { buildTree } from "$lib/tree";

/** Folder rows show what is under them, so a collapsed folder still says
 *  how much it hides. Binary files count toward `files` but contribute no
 *  lines — git counted none. */
export type TreeStat = { files: number; additions: number; deletions: number };

/** byPath indexes the compare by path — the tree carries paths, the rows
 *  need the status letter and counts that go with them. */
export function byPath(files: CompareFile[]): Map<string, CompareFile> {
  const m = new Map<string, CompareFile>();
  for (const f of files) m.set(f.path, f);
  return m;
}

/** compareTree builds the Changes tree out of compare rows. buildTree only
 *  ever reads `path` and `dir` off a change, so the compare is adapted to
 *  its input rather than the tree being forked — one implementation of
 *  "how do paths become folders" for the whole panel. */
export function compareTree(files: CompareFile[]): TreeNode[] {
  return buildTree(files.map((f) => ({ path: f.path }) as FileChange));
}

/** statsByPath totals every folder's subtree, keyed by folder path. One
 *  walk for the whole tree: a folder row asking for its own total would
 *  re-walk its children on every render. */
export function statsByPath(
  nodes: TreeNode[],
  files: Map<string, CompareFile>,
): Map<string, TreeStat> {
  const out = new Map<string, TreeStat>();
  for (const n of nodes) walkStats(n, files, out);
  return out;
}

function walkStats(
  node: TreeNode,
  files: Map<string, CompareFile>,
  out: Map<string, TreeStat>,
): TreeStat {
  if (!node.isDir) {
    const f = files.get(node.path);
    if (!f) return { files: 0, additions: 0, deletions: 0 };
    return {
      files: 1,
      // -1 is git's "binary" on both counts; it is not a line total and
      // must not be summed into one.
      additions: Math.max(f.additions, 0),
      deletions: Math.max(f.deletions, 0),
    };
  }
  const total: TreeStat = { files: 0, additions: 0, deletions: 0 };
  for (const c of node.children ?? []) {
    const s = walkStats(c, files, out);
    total.files += s.files;
    total.additions += s.additions;
    total.deletions += s.deletions;
  }
  out.set(node.path, total);
  return total;
}

/** isOpen mirrors the Changes tree's rule: a folder is expanded unless it
 *  was explicitly closed. Exported so the renderer and the keyboard walk
 *  cannot drift apart on what "open" means. */
export function isOpen(expanded: Record<string, boolean>, path: string): boolean {
  return expanded[path] !== false;
}

/** visibleFiles lists the compare files in the order the tree draws them,
 *  skipping what sits inside a collapsed folder. ↑/↓ walk THIS, not the
 *  flat list: stepping into a folder the user closed would move the diff
 *  to a row that is not on screen. */
export function visibleFiles(
  nodes: TreeNode[],
  expanded: Record<string, boolean>,
  files: Map<string, CompareFile>,
): CompareFile[] {
  const out: CompareFile[] = [];
  const walk = (node: TreeNode) => {
    if (!node.isDir) {
      const f = files.get(node.path);
      if (f) out.push(f);
      return;
    }
    if (!isOpen(expanded, node.path)) return;
    for (const c of node.children ?? []) walk(c);
  };
  for (const n of nodes) walk(n);
  return out;
}
