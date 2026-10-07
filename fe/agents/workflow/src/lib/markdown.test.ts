import { describe, it, expect } from "vitest";
import { mdPlainText, renderMarkdown } from "./markdown";

describe("mdPlainText", () => {
  it("strips markdown syntax into one line", () => {
    expect(mdPlainText("## Fetch user\n- **why**: dedup `id`\n[docs](https://x.y)")).toBe("Fetch user why: dedup id docs");
  });
  it("is empty for blank input", () => {
    expect(mdPlainText("  \n ")).toBe("");
  });
});

describe("renderMarkdown sanitising", () => {
  it("escapes raw html", () => {
    const html = renderMarkdown('<img src=x onerror="alert(1)"> **ok**');
    expect(html).not.toContain("<img");
    expect(html).toContain("<strong");
  });
});
