import { describe, test, expect } from "vitest";
import { esc, linkifyText, renderMarkdown } from "../index.js";

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

describe("renderMarkdown — rich blocks", () => {
  test("mermaid fence becomes a diagram placeholder with raw source + fallback", () => {
    const html = renderMarkdown("```mermaid\nflowchart TD\n  A-->B\n```");
    expect(html).toContain("data-mermaid");
    expect(html).toContain('data-mermaid-src="flowchart TD');
    /* degrades to the raw code as a fallback */
    expect(html).toContain("flowchart TD");
    /* a mermaid block is not a highlightable code block */
    expect(html).not.toContain("data-code-lang");
  });

  test("html fence becomes a sandboxed artifact placeholder", () => {
    const html = renderMarkdown('```html\n<button onclick="x()">Hi</button>\n```');
    expect(html).toContain("data-html-artifact");
    expect(html).toContain("data-html-src=");
    /* degrades to the raw source as a fallback */
    expect(html).toContain("Hi");
    /* an html artifact is not a highlightable code block */
    expect(html).not.toContain("data-code-lang");
  });

  test("non-mermaid code fence carries its language for highlighting", () => {
    const html = renderMarkdown("```js\nconst x = 1;\n```");
    expect(html).toContain('data-code-lang="js"');
    expect(html).toContain('class="language-js"');
    expect(html).toContain("const x = 1;");
  });

  test("display-math fence becomes a math placeholder", () => {
    const html = renderMarkdown("$$\n\\frac{a}{b}\n$$");
    expect(html).toContain("data-math");
    expect(html).toContain("data-math-display");
    expect(html).toContain('data-math-src="\\frac{a}{b}"');
  });

  test("inline math becomes a math span carrying the raw tex", () => {
    const html = renderMarkdown("the value $a^2 + b^2$ is fixed");
    expect(html).toContain('data-math-src="a^2 + b^2"');
    expect(html).not.toContain("data-math-display");
  });

  test("does not treat a price like $5 and $10 as inline math", () => {
    const html = renderMarkdown("it costs $5 and $10 total");
    expect(html).not.toContain("data-math");
  });

  test("renders strikethrough", () => {
    const html = renderMarkdown("~~gone~~");
    expect(html).toContain("<del>gone</del>");
  });
});
