// Build a folder tree from a flat list of FileChange entries, VSCode
// style: directories collapse single-child chains ("a/b/c" → one node
// "a/b/c" when each level has one child) and sort folders-first.

import type { FileChange } from "$lib/api/scm";

export type TreeNode = {
  // Display name (segment, or collapsed "a/b").
  name: string;
  // Full path from repo root (folders end without trailing slash).
  path: string;
  isDir: boolean;
  // Dirs: child nodes. Files: the underlying change.
  children?: TreeNode[];
  change?: FileChange;
};

type RawDir = {
  name: string;
  path: string;
  dirs: Map<string, RawDir>;
  files: { name: string; change: FileChange }[];
};

function newDir(name: string, path: string): RawDir {
  return { name, path, dirs: new Map(), files: [] };
}

// buildTree turns changes into a collapsed, sorted tree.
export function buildTree(changes: FileChange[]): TreeNode[] {
  const root = newDir("", "");
  for (const ch of changes) {
    const segs = ch.path.split("/");
    let cur = root;
    for (let i = 0; i < segs.length - 1; i++) {
      const seg = segs[i];
      const p = cur.path ? cur.path + "/" + seg : seg;
      let next = cur.dirs.get(seg);
      if (!next) {
        next = newDir(seg, p);
        cur.dirs.set(seg, next);
      }
      cur = next;
    }
    cur.files.push({ name: segs[segs.length - 1], change: ch });
  }
  return finalize(root).children ?? [];
}

// finalize converts a RawDir into TreeNodes, collapsing single-child dir
// chains and sorting (dirs first, then alpha).
function finalize(dir: RawDir): TreeNode {
  const childDirs = [...dir.dirs.values()].map(finalize);
  const childFiles: TreeNode[] = dir.files.map((f) => ({
    name: f.name,
    path: f.change.path,
    isDir: false,
    change: f.change,
  }));

  childDirs.sort((a, b) => a.name.localeCompare(b.name));
  childFiles.sort((a, b) => a.name.localeCompare(b.name));

  const node: TreeNode = {
    name: dir.name,
    path: dir.path,
    isDir: true,
    children: [...childDirs, ...childFiles],
  };

  // Collapse: a dir with exactly one child dir and no files becomes
  // "parent/child" (VSCode compact folders).
  if (node.children!.length === 1 && node.children![0].isDir && childFiles.length === 0) {
    const only = node.children![0];
    return {
      name: node.name ? node.name + "/" + only.name : only.name,
      path: only.path,
      isDir: true,
      children: only.children,
    };
  }
  return node;
}

// allFilePaths returns every file path under a node (for folder-scope
// stage/unstage/discard).
export function allFilePaths(node: TreeNode): string[] {
  if (!node.isDir) return node.change ? [node.change.path] : [];
  const out: string[] = [];
  for (const c of node.children ?? []) out.push(...allFilePaths(c));
  return out;
}

// allChanges returns every FileChange under a node.
export function allChanges(node: TreeNode): FileChange[] {
  if (!node.isDir) return node.change ? [node.change] : [];
  const out: FileChange[] = [];
  for (const c of node.children ?? []) out.push(...allChanges(c));
  return out;
}
