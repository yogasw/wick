import { describe, expect, test } from "vitest";
import { questionHtml, THREAD_CODE_CLASS } from "../questionText.js";

describe("questionHtml", () => {
  test("backticks become mono code, the rest is escaped", () => {
    expect(questionHtml("Diff `v2.14.0` & <b>x</b>?")).toBe('Diff <code class="font-mono text-[11.5px]">v2.14.0</code> &amp; &lt;b&gt;x&lt;/b&gt;?');
  });
  test("codeClass styles the code spans (s20a N1: the thread's chip)", () => {
    expect(questionHtml("got `401`", THREAD_CODE_CLASS)).toBe(`got <code class="${THREAD_CODE_CLASS}">401</code>`);
    expect(THREAD_CODE_CLASS).toContain("rounded-sm border");
    expect(THREAD_CODE_CLASS).toContain("dark:bg-navy-800");
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
