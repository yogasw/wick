/* Tree shaping for the file browser: build it from a flat listing, order it,
   and narrow it to a filter. Pure functions, lifted out of the panel they
   used to live in so both callers — the session rail and the Source panel's
   Files tab — share one set of rules, and so the rules can be tested without
   mounting anything. */

import type { SessionFileEntry } from "./file-browser-types.js";

export type FileTreeNode = {
  entry: SessionFileEntry;
  children: FileTreeNode[];
};

export type SortKey = "name" | "recent" | "type";

/** The extension, lowercased, or "" for a dotfile or a name without one. */
export function ext(name: string): string {
  const i = name.lastIndexOf(".");
  return i <= 0 ? "" : name.slice(i + 1).toLowerCase();
}

/* Folders always come before files — the one ordering rule every file
   manager shares — then the chosen key decides within each group. Name is
   the tie-break everywhere, so "recent" and "type" stay stable instead of
   reshuffling equal entries on every rebuild. */
export function compareNodes(a: FileTreeNode, b: FileTreeNode, key: SortKey): number {
  if (a.entry.isDir !== b.entry.isDir) return a.entry.isDir ? -1 : 1;
  const byName = a.entry.name.localeCompare(b.entry.name, undefined, {
    numeric: true,
    sensitivity: "base",
  });
  if (key === "recent") return (b.entry.mtime ?? 0) - (a.entry.mtime ?? 0) || byName;
  if (key === "type" && !a.entry.isDir) {
    const byExt = ext(a.entry.name).localeCompare(ext(b.entry.name));
    if (byExt !== 0) return byExt;
  }
  return byName;
}

/** Sort a node's children, and theirs, in place. */
export function sortTree(node: FileTreeNode, key: SortKey): void {
  node.children.sort((a, b) => compareNodes(a, b, key));
  for (const c of node.children) sortTree(c, key);
}

/* Entries arrive flat, each with a path relative to the root being shown, and
   only for the levels someone has opened. An entry whose parent has not been
   loaded is dropped rather than re-parented to the root: showing it at the top
   would claim a file lives somewhere it does not. */
export function buildFileTree(entries: SessionFileEntry[], key: SortKey): FileTreeNode {
  const root: FileTreeNode = {
    entry: { path: "", name: "", isDir: true, size: 0, mtime: 0 },
    children: [],
  };
  const byPath: Record<string, FileTreeNode> = { "": root };
  for (const e of entries) {
    byPath[e.path] = { entry: e, children: [] };
  }
  for (const p of Object.keys(byPath)) {
    if (p === "") continue;
    const parent = p.indexOf("/") === -1 ? "" : p.slice(0, p.lastIndexOf("/"));
    if (byPath[parent]) byPath[parent].children.push(byPath[p]);
  }
  sortTree(root, key);
  return root;
}

function nameMatches(node: FileTreeNode, q: string): boolean {
  return node.entry.name.toLowerCase().includes(q);
}

/* Shallow (default): each level is filtered by its OWN names, like the filter
   box in a file manager — a folder that matches keeps its whole subtree so you
   can browse into it, one that does not is simply gone. Recursive: keep any
   branch that leads to a match, so a hit deep in an opened tree still shows
   with its ancestors around it. */
export function filterFileTree(node: FileTreeNode, q: string, recursive: boolean): FileTreeNode {
  if (!q) return node;
  const kept: FileTreeNode[] = [];
  for (const c of node.children) {
    if (nameMatches(c, q)) {
      kept.push(c);
      continue;
    }
    if (!recursive || !c.entry.isDir) continue;
    const sub = filterFileTree(c, q, true);
    if (sub.children.length > 0) kept.push({ entry: c.entry, children: sub.children });
  }
  return { entry: node.entry, children: kept };
}
