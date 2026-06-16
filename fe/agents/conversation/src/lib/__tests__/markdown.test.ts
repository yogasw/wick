import { describe, test, expect } from "vitest";
import { esc, linkifyText, renderMarkdown } from "../markdown.js";

describe("esc", () => {
  test("escapes < > & and double quote", () => {
    expect(esc('<script>&"')).toBe("&lt;script&gt;&amp;&quot;");
  });

  test("escapes single quote", () => {
    expect(esc("it's")).toBe("it&#39;s");
  });

  test("leaves plain text untouched", () => {
    expect(esc("hello world")).toBe("hello world");
  });
});

describe("renderMarkdown - XSS safety", () => {
  test("script tag in input is escaped, not executed", () => {
    const html = renderMarkdown("<script>alert(1)</script>");
    expect(html).not.toContain("<script>");
    expect(html).toContain("&lt;script&gt;");
  });

  test("img onerror in input is escaped", () => {
    const html = renderMarkdown('<img onerror="alert(1)" src="x">');
    expect(html).not.toContain("<img");
    expect(html).toContain("&lt;img");
  });
});

describe("renderMarkdown - code block", () => {
  test("renders fenced code block with data-code attribute containing raw code", () => {
    const html = renderMarkdown("```js\nconsole.log('hi');\n```");
    expect(html).toContain("data-copy-code");
    expect(html).toContain("data-code=");
    expect(html).toContain("console.log(&#39;hi&#39;)");
  });

  test("code block has language label", () => {
    const html = renderMarkdown("```python\nprint('hello')\n```");
    expect(html.toLowerCase()).toContain("python");
  });

  test("copy button has no inline onclick", () => {
    const html = renderMarkdown("```\nfoo\n```");
    const copyBtnMatch = html.match(/<button[^>]*data-copy-code[^>]*>/);
    expect(copyBtnMatch).not.toBeNull();
    expect(copyBtnMatch![0]).not.toContain("onclick");
  });
});

describe("renderMarkdown - lists", () => {
  test("renders unordered list", () => {
    const html = renderMarkdown("- item one\n- item two");
    expect(html).toContain("<ul");
    expect(html).toContain("<li");
    expect(html).toContain("item one");
    expect(html).toContain("item two");
  });

  test("renders ordered list", () => {
    const html = renderMarkdown("1. first\n2. second");
    expect(html).toContain("<ol");
    expect(html).toContain("first");
  });
});

describe("renderMarkdown - inline formatting", () => {
  test("renders bold text", () => {
    const html = renderMarkdown("hello **world**");
    expect(html).toContain("<strong>world</strong>");
  });

  test("renders inline code", () => {
    const html = renderMarkdown("use `const` here");
    expect(html).toContain("<code");
    expect(html).toContain("const");
  });

  test("renders external link", () => {
    const html = renderMarkdown("[Qiscus](https://qiscus.com)");
    expect(html).toContain('href="https://qiscus.com"');
    expect(html).toContain("Qiscus");
  });
});

describe("linkifyText", () => {
  test("escapes html and linkifies bare URL", () => {
    const html = linkifyText("see https://example.com for details");
    expect(html).toContain('href="https://example.com"');
    expect(html).toContain("target=\"_blank\"");
  });

  test("escapes dangerous content", () => {
    const html = linkifyText("<script>alert(1)</script>");
    expect(html).not.toContain("<script>");
    expect(html).toContain("&lt;script&gt;");
  });
});
