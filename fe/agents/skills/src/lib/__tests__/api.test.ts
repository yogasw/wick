import { describe, it, expect, vi, beforeEach } from "vitest";
import { listSkills, getSkill, getSkillFile } from "../api.js";

beforeEach(() => {
  vi.resetAllMocks();
});

describe("listSkills - null normalization", () => {
  it("normalizes null dirs to empty array", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ dirs: null, skills: [] }),
    }));
    const r = await listSkills();
    expect(r.dirs).toEqual([]);
  });

  it("normalizes null skills to empty array", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ dirs: [], skills: null }),
    }));
    const r = await listSkills();
    expect(r.skills).toEqual([]);
  });

  it("normalizes null in_dirs on skill items", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        dirs: ["~/.claude/skills"],
        skills: [{ name: "foo", is_dir: false, in_dirs: null, missing_dirs: null }],
      }),
    }));
    const r = await listSkills();
    expect(r.skills[0].in_dirs).toEqual([]);
    expect(r.skills[0].missing_dirs).toEqual([]);
  });

  it("passes through valid data unchanged", async () => {
    const payload = {
      dirs: ["~/.claude/skills", "~/.agents/skills"],
      skills: [{ name: "brainstorming", is_dir: true, in_dirs: ["~/.claude/skills"], missing_dirs: ["~/.agents/skills"] }],
    };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => payload,
    }));
    const r = await listSkills();
    expect(r.dirs).toEqual(payload.dirs);
    expect(r.skills[0].name).toBe("brainstorming");
    expect(r.skills[0].in_dirs).toEqual(["~/.claude/skills"]);
  });
});

describe("getSkill", () => {
  it("normalizes missing entries to empty array", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ name: "foo", is_dir: true, in_dirs: ["~/.claude/skills"], entries: null }),
    }));
    const r = await getSkill("foo");
    expect(r.entries).toEqual([]);
  });

  it("returns file content for non-dir skill", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ name: "foo.md", is_dir: false, content: "# Hello", source_path: "/path/foo.md", in_dirs: ["~/.claude/skills"] }),
    }));
    const r = await getSkill("foo.md");
    expect(r.content).toBe("# Hello");
    expect(r.is_dir).toBe(false);
  });
});

describe("getSkillFile", () => {
  it("normalizes null in_dirs to empty array", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ name: "folder/file.md", content: "content", source_path: "/p", in_dirs: null }),
    }));
    const r = await getSkillFile("folder", "file.md");
    expect(r.in_dirs).toEqual([]);
  });
});
