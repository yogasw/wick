import { describe, test, expect } from "vitest";
import {
  normalizeRel, repoRoot, sessionPath, toRepoRel, joinRel, parentRel,
  ancestorRels, breadcrumbs, rootLabel, baseName, isWithin, sortEntries,
} from "$lib/files-root";

describe("normalizeRel", () => {
  test("strips edge and duplicate slashes and dot segments", () => {
    expect(normalizeRel("/a//b/./c/")).toBe("a/b/c");
    expect(normalizeRel("")).toBe("");
    expect(normalizeRel(".")).toBe("");
  });

  test("normalizes windows separators", () => {
    expect(normalizeRel("src\\lib\\main.ts")).toBe("src/lib/main.ts");
  });

  test("resolves .. against the path and never climbs above the root", () => {
    expect(normalizeRel("a/../b")).toBe("b");
    expect(normalizeRel("../../etc/passwd")).toBe("etc/passwd");
    expect(normalizeRel("a/b/../../..")).toBe("");
  });
});

describe("repoRoot", () => {
  test('"." and "" both mean no prefix', () => {
    expect(repoRoot(".")).toBe("");
    expect(repoRoot("")).toBe("");
  });
  test("a nested repo keeps its folder path", () => {
    expect(repoRoot("clients/wick/")).toBe("clients/wick");
  });
});

describe("sessionPath", () => {
  test("prefixes the repo folder", () => {
    expect(sessionPath("wick", "fe/agents/scm")).toBe("wick/fe/agents/scm");
  });
  test("the repo root itself is the repo folder", () => {
    expect(sessionPath("wick", "")).toBe("wick");
  });
  test('"." leaves the path untouched — the cwd IS the repo', () => {
    expect(sessionPath(".", "fe/main.ts")).toBe("fe/main.ts");
    // The cwd's own listing is the empty path, which the API reads as root.
    expect(sessionPath(".", "")).toBe("");
  });
});

describe("toRepoRel", () => {
  test("strips the repo prefix", () => {
    expect(toRepoRel("wick", "wick/fe/main.ts")).toBe("fe/main.ts");
    expect(toRepoRel("wick", "wick")).toBe("");
    expect(toRepoRel(".", "fe/main.ts")).toBe("fe/main.ts");
  });

  test("a path outside the repo is rejected, not clipped", () => {
    // What a listing for the previous repo looks like once the user has
    // switched: it must be dropped, never attached to the new tree.
    expect(toRepoRel("wick", "other/fe/main.ts")).toBeNull();
    // A sibling whose name merely starts the same is still outside.
    expect(toRepoRel("wick", "wick-fe/main.ts")).toBeNull();
  });

  test("round-trips with sessionPath", () => {
    for (const repo of [".", "wick", "a/b"]) {
      for (const rel of ["", "x.ts", "deep/nested/x.ts"]) {
        expect(toRepoRel(repo, sessionPath(repo, rel))).toBe(rel);
      }
    }
  });
});

describe("joinRel / parentRel / baseName", () => {
  test("joins a name onto a folder, including a nested name", () => {
    expect(joinRel("src", "main.ts")).toBe("src/main.ts");
    expect(joinRel("", "main.ts")).toBe("main.ts");
    expect(joinRel("src", "lib/util.ts")).toBe("src/lib/util.ts");
  });
  test("walks up, stopping at the root", () => {
    expect(parentRel("a/b/c.ts")).toBe("a/b");
    expect(parentRel("c.ts")).toBe("");
    expect(parentRel("")).toBe("");
  });
  test("baseName is the last segment", () => {
    expect(baseName("a/b/c.ts")).toBe("c.ts");
    expect(baseName("c.ts")).toBe("c.ts");
  });
});

describe("ancestorRels", () => {
  test("lists the folders to expand, root first", () => {
    expect(ancestorRels("a/b/c.ts")).toEqual(["", "a", "a/b"]);
    expect(ancestorRels("c.ts")).toEqual([""]);
    expect(ancestorRels("")).toEqual([""]);
  });
});

describe("breadcrumbs", () => {
  test("always starts at the repo and names each folder on the way down", () => {
    expect(breadcrumbs("wick", "fe/agents")).toEqual([
      { name: "wick", path: "" },
      { name: "fe", path: "fe" },
      { name: "agents", path: "fe/agents" },
    ]);
  });
  test("the cwd repo is labelled rather than left blank", () => {
    expect(breadcrumbs(".", "")).toEqual([{ name: "cwd", path: "" }]);
    expect(rootLabel("clients/wick")).toBe("wick");
  });
});

describe("isWithin", () => {
  test("a folder contains itself and its descendants only", () => {
    expect(isWithin("src", "src")).toBe(true);
    expect(isWithin("src", "src/lib/a.ts")).toBe(true);
    expect(isWithin("src", "srclib/a.ts")).toBe(false);
    expect(isWithin("src", "lib/a.ts")).toBe(false);
  });
  test("the root contains everything", () => {
    expect(isWithin("", "anything/at/all")).toBe(true);
  });
});

describe("sortEntries", () => {
  test("folders first, then files, each alphabetical", () => {
    const xs = [
      { name: "z.ts", isDir: false },
      { name: "src", isDir: true },
      { name: "a.ts", isDir: false },
      { name: "lib", isDir: true },
    ];
    expect(sortEntries(xs).map((e) => e.name)).toEqual(["lib", "src", "a.ts", "z.ts"]);
    // Non-destructive: the caller's array keeps the server's order.
    expect(xs[0].name).toBe("z.ts");
  });
});
