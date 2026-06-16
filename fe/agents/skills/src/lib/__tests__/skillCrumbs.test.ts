import { describe, it, expect, vi } from "vitest";
import { buildSkillFileCrumbs } from "../skillCrumbs.js";

describe("buildSkillFileCrumbs", () => {
  it("returns Skills + folder + all path segments for a/b/c.md", () => {
    const nav = vi.fn();
    const items = buildSkillFileCrumbs("git", "a/b/c.md", nav);
    expect(items.map((i) => i.label)).toEqual(["Skills", "git", "a", "b", "c.md"]);
  });

  it("intermediate items have onClick", () => {
    const nav = vi.fn();
    const items = buildSkillFileCrumbs("git", "a/b/c.md", nav);
    expect(typeof items[0].onClick).toBe("function");
    expect(typeof items[1].onClick).toBe("function");
    expect(typeof items[2].onClick).toBe("function");
    expect(typeof items[3].onClick).toBe("function");
  });

  it("last item has no onClick", () => {
    const nav = vi.fn();
    const items = buildSkillFileCrumbs("git", "a/b/c.md", nav);
    expect(items[4].onClick).toBeUndefined();
  });

  it("clicking Skills navigates to /", () => {
    const nav = vi.fn();
    const items = buildSkillFileCrumbs("git", "a/b/c.md", nav);
    items[0].onClick!();
    expect(nav).toHaveBeenCalledWith("/");
  });

  it("clicking folder navigates to /skills/{folder}", () => {
    const nav = vi.fn();
    const items = buildSkillFileCrumbs("git", "a/b/c.md", nav);
    items[1].onClick!();
    expect(nav).toHaveBeenCalledWith("/skills/git");
  });

  it("clicking segment 'b' navigates to /skills/git/files/a/b", () => {
    const nav = vi.fn();
    const items = buildSkillFileCrumbs("git", "a/b/c.md", nav);
    items[3].onClick!();
    expect(nav).toHaveBeenCalledWith("/skills/git/files/a/b");
  });

  it("clicking first path segment 'a' navigates to /skills/git/files/a", () => {
    const nav = vi.fn();
    const items = buildSkillFileCrumbs("git", "a/b/c.md", nav);
    items[2].onClick!();
    expect(nav).toHaveBeenCalledWith("/skills/git/files/a");
  });

  it("encodes segments with spaces in folder", () => {
    const nav = vi.fn();
    const items = buildSkillFileCrumbs("my skill", "a.md", nav);
    items[1].onClick!();
    expect(nav).toHaveBeenCalledWith("/skills/my%20skill");
  });

  it("encodes segments with spaces in file path", () => {
    const nav = vi.fn();
    const items = buildSkillFileCrumbs("git", "my dir/a.md", nav);
    items[2].onClick!();
    expect(nav).toHaveBeenCalledWith("/skills/git/files/my%20dir");
  });

  it("single-segment file has no onClick on last item", () => {
    const nav = vi.fn();
    const items = buildSkillFileCrumbs("git", "readme.md", nav);
    expect(items.map((i) => i.label)).toEqual(["Skills", "git", "readme.md"]);
    expect(items[2].onClick).toBeUndefined();
  });

  it("long segments have truncate: true", () => {
    const nav = vi.fn();
    const longSeg = "a".repeat(40);
    const items = buildSkillFileCrumbs("git", `${longSeg}/b.md`, nav);
    const longItem = items.find((i) => i.label === longSeg);
    expect(longItem?.truncate).toBe(true);
  });

  it("short segments do not have truncate: true", () => {
    const nav = vi.fn();
    const items = buildSkillFileCrumbs("git", "a/b.md", nav);
    const aItem = items.find((i) => i.label === "a");
    expect(aItem?.truncate).toBeUndefined();
  });
});
