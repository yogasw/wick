import { describe, expect, test } from "vitest";
import { frontmatterRows, hitKey, hitScope, hitTitle, pageScope, rawObservations, snippetHTML } from "../wiki.js";
import type { Page } from "../types.js";

const page = (body: string, extra: Partial<Page> = {}): Page => ({
  path: "sessions/abc.md",
  workspace: "default",
  project: "proj",
  body,
  ...extra,
});

describe("search hits", () => {
  test("the key carries the scope, because a path is only unique inside a project", () => {
    const a = hitKey({ workspace: "default", project: "proj", path: "sessions/x.md" });
    const b = hitKey({ workspace: "default", project: "proj2", path: "sessions/x.md" });
    expect(a).not.toBe(b);
  });

  test("a hit with no title falls back to its path, not to 'Untitled'", () => {
    expect(hitTitle({ path: "notes/foo.md" })).toBe("notes/foo.md");
    expect(hitTitle({ path: "notes/foo.md", title: "  " })).toBe("notes/foo.md");
    expect(hitTitle({ path: "notes/foo.md", title: "Foo" })).toBe("Foo");
  });

  test("every hit says which project it came from", () => {
    expect(hitScope({ path: "p", workspace: "default", project: "proj" })).toBe("default/proj");
    expect(hitScope({ path: "p" })).toBe("scope unknown");
  });
});

describe("snippetHTML", () => {
  test("keeps the backend's <mark> and escapes everything else", () => {
    expect(snippetHTML("a <mark>hit</mark> b")).toBe("a <mark>hit</mark> b");
  });

  test("a page body is agent-written text, so injected markup is escaped", () => {
    const out = snippetHTML('<img src=x onerror="alert(1)"> <mark>ok</mark>');
    expect(out).not.toContain("<img");
    expect(out).toContain("&lt;img");
    expect(out).toContain("<mark>ok</mark>");
  });

  test("an absent snippet is empty, not 'undefined'", () => {
    expect(snippetHTML(undefined)).toBe("");
  });
});

describe("rawObservations", () => {
  const body = [
    "# Title",
    "",
    "## Session metadata",
    "",
    "- **session_id:** `abc`",
    "",
    "## Raw observations",
    "",
    "- `session-start` @ 2026-09-24T15:26:56Z — session-start",
    "- `user-prompt` @ 2026-09-24T15:27:14Z — Ingat: srv-melati-07",
    "",
    "_Synthesised by ai-memory (M3, no-LLM heuristic)._",
  ].join("\n");

  test("reads the section the consolidator wrote into the page", () => {
    const got = rawObservations(page(body));
    expect(got.present).toBe(true);
    if (!got.present) return;
    expect(got.lines).toHaveLength(2);
    expect(got.lines[1]).toContain("srv-melati-07");
  });

  test("stops at the next heading, so the footer is not an observation", () => {
    const withNext = body.replace(
      "_Synthesised by ai-memory (M3, no-LLM heuristic)._",
      "## Tool calls\n\n- `tool non-file`: 2",
    );
    const got = rawObservations(page(withNext));
    expect(got.present).toBe(true);
    if (!got.present) return;
    expect(got.lines).toHaveLength(2);
    expect(got.lines.join(" ")).not.toContain("tool non-file");
  });

  // The honest half: a page with no section must say so rather than render an
  // empty list, which would read as "this session did nothing".
  test("a page without the section explains why, and does not claim zero events", () => {
    const got = rawObservations(page("# A concept page\n\nSome prose."));
    expect(got.present).toBe(false);
    if (got.present) return;
    expect(got.reason).toMatch(/no raw-observation section/i);
  });

  test("no page open is its own answer", () => {
    expect(rawObservations(null)).toEqual({ present: false, reason: "No page open." });
  });
});

describe("page chrome", () => {
  test("pageScope labels the store slot", () => {
    expect(pageScope(page("x"))).toBe("default/proj");
    expect(pageScope(null)).toBe("");
  });

  test("frontmatter keeps nested values as JSON instead of dropping them", () => {
    const rows = frontmatterRows(
      page("x", { frontmatter: { agent: "claude-code", sources: [{ author: "claude-code" }] } }),
    );
    expect(rows).toContainEqual({ key: "agent", value: "claude-code" });
    expect(rows.find((r) => r.key === "sources")?.value).toContain("claude-code");
  });

  test("no frontmatter is no rows", () => {
    expect(frontmatterRows(page("x"))).toEqual([]);
    expect(frontmatterRows(null)).toEqual([]);
  });
});
