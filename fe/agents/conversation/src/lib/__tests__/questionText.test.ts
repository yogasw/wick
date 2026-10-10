import { describe, expect, test } from "vitest";
import { questionHtml } from "../questionText.js";

describe("questionHtml", () => {
  test("backticks become mono code, the rest is escaped", () => {
    expect(questionHtml("Diff `v2.14.0` & <b>x</b>?")).toBe('Diff <code class="font-mono text-[11.5px]">v2.14.0</code> &amp; &lt;b&gt;x&lt;/b&gt;?');
  });
  test("markup never survives, in code or out", () => {
    const html = questionHtml('<img src=x onerror="alert(1)"> `<script>alert(1)</script>`');
    expect(html).not.toMatch(/<img|<script/);
    expect(html).toContain("&lt;script&gt;");
  });
  test("links render; a code span is not linkified", () => {
    expect(questionHtml("see https://example.com/x")).toContain('<a href="https://example.com/x"');
    expect(questionHtml("run `https://example.com/x`")).not.toContain("<a ");
    expect(questionHtml("[docs](javascript:alert(1))")).not.toContain("<a ");
  });
  test("empty and unclosed backticks stay text", () => {
    expect(questionHtml(undefined)).toBe("");
    expect(questionHtml("a ` b")).toBe("a ` b");
  });
});
