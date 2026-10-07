import { describe, test, expect } from "vitest";
import {
  buildFileTree,
  compareNodes,
  ext,
  filterFileTree,
  type FileTreeNode,
} from "../file-browser-tree.js";
import type { SessionFileEntry } from "../file-browser-types.js";

const e = (
  path: string,
  isDir = false,
  mtime = 0,
): SessionFileEntry => ({
  path,
  name: path.slice(path.lastIndexOf("/") + 1),
  size: isDir ? 0 : 10,
  isDir,
  mtime,
});

const node = (entry: SessionFileEntry): FileTreeNode => ({ entry, children: [] });

const names = (nodes: FileTreeNode[]) => nodes.map((n) => n.entry.name);

describe("ext", () => {
  test("takes what follows the last dot, lower-cased", () => {
    expect(ext("Main.GO")).toBe("go");
    expect(ext("archive.tar.gz")).toBe("gz");
  });

  test("a dotfile has no extension — the dot starts the name", () => {
    expect(ext(".gitignore")).toBe("");
    expect(ext("README")).toBe("");
  });
});

describe("buildFileTree", () => {
  const entries = [e("src", true), e("src/main.go"), e("README.md"), e("src/util.go")];

  test("hangs entries off their parent and keeps folders first", () => {
    const root = buildFileTree(entries, "name");
    expect(names(root.children)).toEqual(["src", "README.md"]);
    expect(names(root.children[0].children)).toEqual(["main.go", "util.go"]);
  });

  test("drops an entry whose parent folder was never listed", () => {
    // The tree loads a level at a time; a row with nowhere to hang would
    // otherwise render at the root as if it lived there.
    const root = buildFileTree([e("deep/nested/file.ts")], "name");
    expect(root.children).toEqual([]);
  });

  test("recent sorts newest first, within folders-first", () => {
    const root = buildFileTree(
      [e("old.txt", false, 1000), e("new.txt", false, 9000), e("dir", true, 0)],
      "recent",
    );
    expect(names(root.children)).toEqual(["dir", "new.txt", "old.txt"]);
  });

  test("type groups by extension before name", () => {
    const root = buildFileTree([e("b.go"), e("a.ts"), e("c.go")], "type");
    expect(names(root.children)).toEqual(["b.go", "c.go", "a.ts"]);
  });
});

describe("compareNodes", () => {
  test("a folder always precedes a file, whatever the key", () => {
    const dir = node(e("z", true));
    const file = node(e("a.ts"));
    expect(compareNodes(dir, file, "name")).toBeLessThan(0);
    expect(compareNodes(dir, file, "recent")).toBeLessThan(0);
    expect(compareNodes(dir, file, "type")).toBeLessThan(0);
  });

  test("names sort naturally, so file10 follows file9", () => {
    const a = node(e("file9.ts"));
    const b = node(e("file10.ts"));
    expect(compareNodes(a, b, "name")).toBeLessThan(0);
  });
});

describe("filterFileTree", () => {
  const root = buildFileTree(
    [e("src", true), e("src/main.go"), e("docs", true), e("docs/main.md"), e("README.md")],
    "name",
  );

  test("shallow keeps only this level's matches, with their subtree intact", () => {
    const out = filterFileTree(root, "src", false);
    expect(names(out.children)).toEqual(["src"]);
    expect(names(out.children[0].children)).toEqual(["main.go"]);
  });

  test("shallow does not reach into folders that do not match", () => {
    expect(filterFileTree(root, "main", false).children.map((n) => n.entry.name)).toEqual([]);
  });

  test("deep keeps every branch that leads to a match", () => {
    const out = filterFileTree(root, "main", true);
    expect(names(out.children)).toEqual(["docs", "src"]);
    expect(names(out.children[0].children)).toEqual(["main.md"]);
  });

  test("an empty query is the tree untouched", () => {
    expect(filterFileTree(root, "", true)).toBe(root);
  });
});
