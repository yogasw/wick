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

  test("svg fence becomes a rendered-image placeholder with raw source", () => {
    const html = renderMarkdown('```svg\n<svg viewBox="0 0 10 10"><rect width="10" height="10"/></svg>\n```');
    expect(html).toContain("data-svg");
    expect(html).toContain("data-svg-src=");
    /* degrades to the raw source as a fallback */
    expect(html).toContain("rect");
    /* an svg block is not a highlightable code block */
    expect(html).not.toContain("data-code-lang");
  });

  test("bare <svg>…</svg> (no fence) becomes a rendered-image placeholder", () => {
    const html = renderMarkdown('intro\n<svg viewBox="0 0 10 10"><rect width="10" height="10"/></svg>\nafter');
    expect(html).toContain("data-svg");
    expect(html).toContain("data-svg-src=");
    /* surrounding prose still renders */
    expect(html).toContain("intro");
    expect(html).toContain("after");
  });

  test("single-line bare <svg> is detected", () => {
    const html = renderMarkdown('<svg><circle r="5"/></svg>');
    expect(html).toContain("data-svg");
    expect(html).toContain("circle");
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

  test("htmlfile fence references a file by path, not inlined markup", () => {
    const html = renderMarkdown("```htmlfile\ndashboards/report.html\n```");
    expect(html).toContain("data-html-artifact");
    /* it carries the path, NOT the document source */
    expect(html).toContain('data-html-path="dashboards/report.html"');
    expect(html).not.toContain("data-html-src=");
    /* degrades to the raw path as a readable fallback on non-rich channels */
    expect(html).toContain("dashboards/report.html");
    /* not a highlightable code block */
    expect(html).not.toContain("data-code-lang");
  });

  test("imagecard fence becomes an image-card placeholder with raw source", () => {
    const html = renderMarkdown(
      "```imagecard\nhttps://abc.com/a.jpg | Caption A\nhttps://abc.net/b.png\n```",
    );
    expect(html).toContain("data-imagecard");
    expect(html).toContain("data-imagecard-src=");
    /* both urls ride in the source so the SPA can build the cards */
    expect(html).toContain("abc.com/a.jpg");
    expect(html).toContain("abc.net/b.png");
    /* degrades to the raw urls as a fallback (non-rich channels) */
    expect(html).toContain("Caption A");
    /* an image-card block is not a highlightable code block */
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

  test("a standalone $$…$$ line renders as a centered block, not inline in a paragraph", () => {
    const html = renderMarkdown("$$A=\\begin{pmatrix} a & b \\\\ c & d \\end{pmatrix} \\Rightarrow \\det(A)=ad-bc$$");
    expect(html).toContain("data-math-display");
    /* the matrix `&` survives (attribute-escaped, decoded by the browser) */
    expect(html).toContain("\\begin{pmatrix} a &amp; b");
    /* emitted as a block, not wrapped in a text paragraph */
    expect(html).not.toContain('<p class="text-sm');
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
