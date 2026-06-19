import { describe, test, expect } from "vitest";
import { buildTree, allFilePaths, allChanges, type TreeNode } from "$lib/tree";
import type { FileChange } from "$lib/api/scm";

function mk(path: string): FileChange {
  return { path, index: " ", work_tree: "M", staged: false, unstaged: true, untracked: false };
}

describe("buildTree", () => {
  test("returns root files sorted alphabetically", () => {
    const tree = buildTree([mk("b.txt"), mk("a.txt")]);
    expect(tree.map((n) => n.name)).toEqual(["a.txt", "b.txt"]);
    expect(tree.every((n) => !n.isDir)).toBe(true);
  });

  test("orders folders before files and sorts each group", () => {
    const tree = buildTree([mk("zoo.txt"), mk("src/a.ts"), mk("lib/b.ts")]);
    expect(tree.map((n) => ({ name: n.name, dir: n.isDir }))).toEqual([
      { name: "lib", dir: true },
      { name: "src", dir: true },
      { name: "zoo.txt", dir: false },
    ]);
  });

  test("collapses a single-child directory chain into one node", () => {
    const tree = buildTree([mk("pkg/a/b/deep.ts"), mk("other.txt")]);
    const dir = tree.find((n) => n.isDir);
    expect(dir?.name).toBe("pkg/a/b");
    expect(dir?.children?.map((c) => c.name)).toEqual(["deep.ts"]);
  });
});

describe("allFilePaths / allChanges", () => {
  const node: TreeNode = {
    name: "src",
    path: "src",
    isDir: true,
    children: [
      { name: "a.ts", path: "src/a.ts", isDir: false, change: mk("src/a.ts") },
      {
        name: "sub",
        path: "src/sub",
        isDir: true,
        children: [{ name: "b.ts", path: "src/sub/b.ts", isDir: false, change: mk("src/sub/b.ts") }],
      },
    ],
  };

  test("allFilePaths walks every file path under a node", () => {
    expect(allFilePaths(node)).toEqual(["src/a.ts", "src/sub/b.ts"]);
  });

  test("allChanges walks every FileChange under a node", () => {
    expect(allChanges(node).map((c) => c.path)).toEqual(["src/a.ts", "src/sub/b.ts"]);
  });

  test("a single file node returns just its own path", () => {
    const file: TreeNode = { name: "x.ts", path: "x.ts", isDir: false, change: mk("x.ts") };
    expect(allFilePaths(file)).toEqual(["x.ts"]);
  });
});
