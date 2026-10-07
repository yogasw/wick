import { describe, test, expect } from "vitest";
import { byPath, compareTree, statsByPath, isOpen, visibleFiles } from "$lib/compare-tree";
import type { CompareFile } from "$lib/api/scm";

const f = (path: string, additions = 1, deletions = 1, status = "M"): CompareFile => ({
  path,
  status,
  additions,
  deletions,
});

const sample: CompareFile[] = [
  f("internal/agents/scm/git.go", 10, 2),
  f("internal/agents/scm/compare.go", 120, 0, "A"),
  f("fe/agents/scm/src/lib/tree.ts", 3, 4),
  f("README.md", 1, 1),
  f("assets/logo.png", -1, -1),
];

describe("compareTree", () => {
  test("groups by folder, collapses single-child chains and sorts folders first", () => {
    const tree = compareTree(sample);
    expect(tree.map((n) => n.name)).toEqual([
      // "assets" holds one file, so it stays a folder; the fe chain has a
      // single child the whole way down and collapses into one row.
      "assets",
      "fe/agents/scm/src/lib",
      "internal/agents/scm",
      "README.md",
    ]);
    const scm = tree.find((n) => n.name === "internal/agents/scm")!;
    expect(scm.isDir).toBe(true);
    expect(scm.children?.map((c) => c.name)).toEqual(["compare.go", "git.go"]);
  });

  test("leaf paths stay whole, so the compare file can be found again", () => {
    const tree = compareTree(sample);
    const lib = tree.find((n) => n.name === "fe/agents/scm/src/lib")!;
    expect(lib.children?.[0].path).toBe("fe/agents/scm/src/lib/tree.ts");
  });

  test("survives an empty compare", () => {
    expect(compareTree([])).toEqual([]);
  });
});

describe("statsByPath", () => {
  const files = byPath(sample);
  const stats = statsByPath(compareTree(sample), files);

  test("a folder carries its whole subtree", () => {
    expect(stats.get("internal/agents/scm")).toEqual({
      files: 2,
      additions: 130,
      deletions: 2,
    });
  });

  test("binary files count as files but contribute no lines", () => {
    expect(stats.get("assets")).toEqual({ files: 1, additions: 0, deletions: 0 });
  });

  test("a collapsed chain is keyed by the folder's real path", () => {
    expect(stats.get("fe/agents/scm/src/lib")).toEqual({
      files: 1,
      additions: 3,
      deletions: 4,
    });
  });
});

describe("isOpen", () => {
  test("folders are expanded unless explicitly closed", () => {
    expect(isOpen({}, "internal")).toBe(true);
    expect(isOpen({ internal: true }, "internal")).toBe(true);
    expect(isOpen({ internal: false }, "internal")).toBe(false);
  });
});

describe("visibleFiles", () => {
  const files = byPath(sample);
  const tree = compareTree(sample);

  test("lists every file in draw order when nothing is collapsed", () => {
    expect(visibleFiles(tree, {}, files).map((c) => c.path)).toEqual([
      "assets/logo.png",
      "fe/agents/scm/src/lib/tree.ts",
      "internal/agents/scm/compare.go",
      "internal/agents/scm/git.go",
      "README.md",
    ]);
  });

  test("skips what a collapsed folder hides", () => {
    const visible = visibleFiles(tree, { "internal/agents/scm": false }, files);
    expect(visible.map((c) => c.path)).toEqual([
      "assets/logo.png",
      "fe/agents/scm/src/lib/tree.ts",
      "README.md",
    ]);
  });

  test("a file the compare no longer has is dropped rather than faked", () => {
    expect(visibleFiles(tree, {}, byPath([])).length).toBe(0);
  });
});
