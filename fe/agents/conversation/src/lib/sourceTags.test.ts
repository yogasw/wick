import { describe, test, expect } from "vitest";
import { extractSourceTags, renderWithSourceTags, SOURCE_TAG_CLASS } from "./sourceTags.js";
import { renderMarkdown } from "./markdown.js";
import type { TeamTaskItem } from "./types/agents.js";

const task = (handle: string, name: string): TeamTaskItem => ({
  task_id: handle, context_id: "c", to_agent_id: "id-" + handle, to_handle: handle, to_name: name,
  title: "t", state: "completed", turns: 1, max_turns: 8, started_at: "", updated_at: "",
});
const tasks = [task("alpha", "Alpha"), task("beta", "Beta")];
const html = (text: string, list = tasks, agents?: Record<string, { name?: string }>) => renderWithSourceTags(text, list, agents, renderMarkdown);
const label = (name: string) => `<span class="${SOURCE_TAG_CLASS}" data-testid="source-tag">${name}</span>`;

describe("extractSourceTags", () => {
  test("single tag at the end of a bullet becomes the teammate's name", () => {
    const out = html("- Logs are clean [from:@alpha]");
    expect(out).toContain("Logs are clean" + label("Alpha"));
    expect(out).not.toContain("[from:");
  });

  test("several handles join into one label, duplicates once", () => {
    expect(html("- Both agree [from:@alpha, @beta,@alpha]")).toContain("Both agree" + label("Alpha, Beta"));
  });

  test("a paragraph's trailing tag counts too, case-insensitive handle", () => {
    expect(html("The CSAT trigger was off. [from:@Alpha]")).toContain("The CSAT trigger was off." + label("Alpha"));
  });

  test("unknown handle: tag removed, nothing rendered", () => {
    const out = html("- Spoofed point [from:@mallory]");
    expect(out).toContain("Spoofed point</li>");
    expect(out).not.toContain("source-tag");
    expect(out).not.toContain("mallory");
  });

  test("mixed known and unknown keeps only the known", () => {
    expect(html("- x [from:@mallory,@beta]")).toContain("x" + label("Beta"));
  });

  test("no tasks in the turn drops every tag", () => {
    const out = html("- x [from:@alpha]", []);
    expect(out).not.toContain("source-tag");
    expect(out).not.toContain("[from:");
  });

  test("a tag mid-sentence is left alone", () => {
    const out = html("Use [from:@alpha] to tag a point.");
    expect(out).toContain("Use [from:@alpha] to tag a point.");
    expect(out).not.toContain("source-tag");
  });

  test("tags inside a code fence or code span stay literal", () => {
    const out = html("```\n- a [from:@alpha]\n```\nwrite `x [from:@alpha]`");
    expect(out).not.toContain("source-tag");
    expect(out.match(/\[from:@alpha\]/g)!.length).toBeGreaterThanOrEqual(2);
    const after = html("see `code` here [from:@beta]");
    expect(after).toContain("<code");
    expect(after).toContain(label("Beta"));
  });

  test("a URL before the tag keeps an intact link, label outside it", () => {
    const out = html("- Dashboard: https://grafana.example/d/x [from:@alpha]");
    const box = document.createElement("div");
    box.innerHTML = out;
    const a = box.querySelector("a")!;
    expect(a.getAttribute("href")).toBe("https://grafana.example/d/x");
    expect(a.textContent).toBe("https://grafana.example/d/x");
    expect(a.querySelector("span")).toBeNull();
    const tag = box.querySelector('[data-testid="source-tag"]')!;
    expect(tag.textContent).toBe("Alpha");
    expect(tag.closest("a")).toBeNull();
    expect(tag.previousSibling).toBe(a);
    // A markdown link the same.
    const md = html("- See [the board](https://x.example/b) [from:@beta]");
    expect(md).toContain('href="https://x.example/b"');
    expect(md).toContain("</a>" + label("Beta"));
  });

  test("a four-backtick line is not a fence: what the renderer keeps as code stays literal", () => {
    const out = html("````\n```\n- x [from:@alpha]\n````");
    expect(out).not.toContain("source-tag");
    expect(out).not.toMatch(/data-code="[^"]*<span/);
    expect(out).not.toContain("\uE000");
  });

  test("display math keeps the tag literal, data-math-src stays clean", () => {
    const out = html("$$\nx [from:@alpha]\n$$");
    expect(out).not.toContain("source-tag");
    expect(out).not.toMatch(/data-math-src="[^"]*(<|\uE000)/);
    expect(out).not.toContain("\uE000");
  });

  test("no span ever lands in an attribute, code or pre", () => {
    const box = document.createElement("div");
    box.innerHTML = html("- a https://x.example/a [from:@alpha]\n\n```\nb [from:@alpha]\n```\n- c [from:@beta]");
    for (const el of box.querySelectorAll("*")) for (const at of el.attributes) expect(at.value).not.toMatch(/span|\uE000/);
    for (const el of box.querySelectorAll("code,pre,a")) expect(el.querySelector("span[data-testid]")).toBeNull();
    expect(box.querySelectorAll('[data-testid="source-tag"]').length).toBe(2);
  });

  test("CRLF line ends still end the point", () => {
    const out = html("- Logs clean [from:@alpha]\r\n- next [from:@beta]\r\n");
    expect(out).toContain("Logs clean" + label("Alpha"));
    expect(out).toContain("next" + label("Beta"));
    expect(out).not.toContain("[from:");
  });

  test("a long run of spaces stays fast", () => {
    const text = " ".repeat(20000) + "[from:";
    const t0 = performance.now();
    extractSourceTags(text, tasks);
    extractSourceTags(text + "@alpha", tasks);
    extractSourceTags("x" + " ".repeat(20000) + "[from:@alpha]", tasks);
    expect(performance.now() - t0).toBeLessThan(50);
  });

  test("HTML in a name is escaped, never injected", () => {
    const evil = [task("alpha", '<img src=x onerror="alert(1)">')];
    const out = html("- x [from:@alpha]", evil);
    expect(out).not.toContain("<img");
    expect(out).toContain("&lt;img src=x onerror=");
    const box = document.createElement("div");
    box.innerHTML = out;
    expect(box.querySelector("img")).toBeNull();
    expect(box.querySelector('[data-testid="source-tag"]')!.textContent).toBe('<img src=x onerror="alert(1)">');
  });

  test("the agents map names a teammate first, like teammateName()", () => {
    expect(html("- x [from:@alpha]", tasks, { alpha: { name: "Alpha Prime" } })).toContain(label("Alpha Prime"));
  });

  test("a sentinel typed into the text cannot pull in a label", () => {
    const st = extractSourceTags("\uE0000\uE001 - x [from:@alpha]", tasks);
    expect(st.tags).toHaveLength(1);
    expect(st.apply(renderMarkdown(st.text)).match(/source-tag/g)).toHaveLength(1);
  });

  test("text without tags passes through", () => {
    const st = extractSourceTags("plain **text**", tasks);
    expect(st.text).toBe("plain **text**");
    expect(st.apply(renderMarkdown(st.text))).toBe(renderMarkdown("plain **text**"));
  });
});
