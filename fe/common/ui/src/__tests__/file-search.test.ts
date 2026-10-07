import { describe, test, expect } from "vitest";
import { rankPathHits, scorePath, subsequence, withAncestorDirs } from "../file-search.js";
import type { SessionFileEntry } from "../file-browser-types.js";

const f = (path: string, isDir = false): SessionFileEntry => ({
  path,
  name: path.slice(path.lastIndexOf("/") + 1),
  size: 1,
  isDir,
  mtime: 0,
});

describe("subsequence", () => {
  test("matches characters in order, gaps allowed", () => {
    expect(subsequence("internal/agents/scm/git.go", "scmgit")).toBe(true);
    expect(subsequence("files/server.go", "flsrv")).toBe(true);
  });

  test("order matters", () => {
    expect(subsequence("abc", "cb")).toBe(false);
  });

  test("an empty query matches anything", () => {
    expect(subsequence("abc", "")).toBe(true);
  });
});

describe("scorePath", () => {
  test("an exact file name beats a prefix, which beats a contains", () => {
    const exact = scorePath("a/git.go", "git.go", "git.go");
    const prefix = scorePath("a/git-utils.go", "git-utils.go", "git");
    const contains = scorePath("a/mygit.go", "mygit.go", "git");
    expect(exact).toBeGreaterThan(prefix);
    expect(prefix).toBeGreaterThan(contains);
  });

  test("a name hit beats a hit that is only in the folders", () => {
    const inName = scorePath("x/scm.ts", "scm.ts", "scm");
    const inPath = scorePath("scm/other.ts", "other.ts", "scm");
    expect(inName).toBeGreaterThan(inPath);
  });

  test("a query with a slash is about the path, not the name", () => {
    expect(scorePath("internal/agents/scm/git.go", "git.go", "agents/scm")).toBeGreaterThan(0);
    expect(scorePath("internal/other/git.go", "git.go", "agents/scm")).toBe(0);
  });

  test("shorter paths win ties", () => {
    const shallow = scorePath("git.go", "git.go", "git.go");
    const deep = scorePath("a/b/c/d/git.go", "git.go", "git.go");
    expect(shallow).toBeGreaterThan(deep);
  });

  test("no match and an empty query both score zero", () => {
    expect(scorePath("a/b.ts", "b.ts", "zzz")).toBe(0);
    expect(scorePath("a/b.ts", "b.ts", "  ")).toBe(0);
  });
});

describe("rankPathHits", () => {
  const entries = [
    f("src", true),
    f("src/compare-tree.ts"),
    f("compare.ts"),
    f("docs/comparison.md"),
    f("unrelated.go"),
  ];

  test("best first, folders and non-matches dropped", () => {
    expect(rankPathHits(entries, "compare.ts").map((e) => e.path)).toEqual([
      "compare.ts",
      "src/compare-tree.ts",
    ]);
  });

  test("honours the limit", () => {
    expect(rankPathHits(entries, "compar", 1).length).toBe(1);
  });

  test("an empty query finds nothing rather than everything", () => {
    expect(rankPathHits(entries, "")).toEqual([]);
  });
});

describe("withAncestorDirs", () => {
  test("invents the folders a deep hit needs to hang from", () => {
    const out = withAncestorDirs([f("a/b/c.ts")]);
    expect(out.map((e) => e.path).sort()).toEqual(["a", "a/b", "a/b/c.ts"]);
    const dir = out.find((e) => e.path === "a/b")!;
    expect(dir.isDir).toBe(true);
    expect(dir.name).toBe("b");
  });

  test("never duplicates a folder the search already returned", () => {
    const out = withAncestorDirs([f("a", true), f("a/b.ts")]);
    expect(out.map((e) => e.path)).toEqual(["a", "a/b.ts"]);
  });

  test("a root-level file needs nothing added", () => {
    expect(withAncestorDirs([f("README.md")]).map((e) => e.path)).toEqual(["README.md"]);
  });
});
