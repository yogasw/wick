import { describe, expect, it } from "vitest";
import {
  STAGED,
  WORKTREE,
  filterFiles,
  fuzzyScore,
  presetPair,
  rangePair,
  refShapeError,
  sideLabel,
} from "../compare-picker";

describe("refShapeError", () => {
  it("accepts branches, tags, shas, relative refs and the working sides", () => {
    for (const ok of ["main", "origin/main", "v1.2.0", "a1b2c3d", "HEAD~3", "abc123^", WORKTREE, STAGED]) {
      expect(refShapeError(ok), ok).toBe("");
    }
  });

  it("refuses options, ranges, pathspecs and whitespace", () => {
    for (const bad of ["", "  ", "-p", "--output=/x", "main..dev", "main...dev", "HEAD:README.md", "a b", "x*"]) {
      expect(refShapeError(bad), bad).not.toBe("");
    }
  });
});

describe("presetPair", () => {
  it("compares the default branch with HEAD by merge base", () => {
    expect(presetPair("trunk-head", "origin/master")).toEqual({ base: "origin/master", head: "HEAD", threeDot: true });
  });

  it("compares the default branch with the working tree", () => {
    expect(presetPair("trunk-worktree", "main")).toEqual({ base: "main", head: WORKTREE, threeDot: true });
  });

  it("needs a trunk for the trunk presets", () => {
    expect(presetPair("trunk-head", "")).toBeNull();
  });

  it("expands Last N commits to HEAD~N..HEAD", () => {
    expect(presetPair("last-n", "", 5)).toEqual({ base: "HEAD~5", head: "HEAD", threeDot: false });
    expect(presetPair("last-n", "", 0)).toBeNull();
    expect(presetPair("last-n", "", NaN)).toBeNull();
  });
});

describe("rangePair", () => {
  it("runs from the older commit's parent to the newer, in either click order", () => {
    const newer = { sha: "bbb", index: 1 };
    const older = { sha: "aaa", index: 4 };
    const want = { base: "aaa^", head: "bbb", threeDot: false };
    expect(rangePair(newer, older)).toEqual(want);
    expect(rangePair(older, newer)).toEqual(want);
  });
});

describe("file search", () => {
  const files = [
    { path: "internal/agents/scm/compare.go" },
    { path: "fe/agents/scm/src/lib/components/CompareModal.svelte" },
    { path: "README.md" },
    { path: "docs/new.md", orig_path: "docs/old-readme.md" },
  ];

  it("is case-insensitive substring first", () => {
    expect(filterFiles(files, "COMPARE").map((f) => f.path)).toEqual([
      "internal/agents/scm/compare.go",
      "fe/agents/scm/src/lib/components/CompareModal.svelte",
    ]);
  });

  it("falls back to an in-order fuzzy match", () => {
    expect(filterFiles(files, "cmpmdl").map((f) => f.path)).toEqual([
      "fe/agents/scm/src/lib/components/CompareModal.svelte",
    ]);
  });

  it("matches a rename by its old name", () => {
    expect(filterFiles(files, "old-readme").map((f) => f.path)).toEqual(["docs/new.md"]);
  });

  it("returns everything for an empty query and nothing for a miss", () => {
    expect(filterFiles(files, " ")).toBe(files);
    expect(filterFiles(files, "zzz")).toEqual([]);
  });

  it("ranks a file-name hit above a folder hit", () => {
    expect(fuzzyScore("scm/readme.md", "readme")).toBeLessThan(fuzzyScore("readme/x.go", "readme"));
  });
});

describe("sideLabel", () => {
  it("names the working sides", () => {
    expect(sideLabel(WORKTREE)).toBe("Working tree (uncommitted)");
    expect(sideLabel(STAGED)).toBe("Staged changes");
    expect(sideLabel("main")).toBe("main");
  });
});
