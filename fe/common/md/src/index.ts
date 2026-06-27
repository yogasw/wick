export function esc(s: string): string {
  return String(s).replace(/[&<>"']/g, (c) => {
    return ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" } as Record<string, string>)[c]!;
  });
}

export function linkifyText(s: string): string {
  s = esc(s);
  s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^)]+)\)/g, '<a href="$2" class="underline break-all" target="_blank" rel="noopener">$1</a>');
  s = s.replace(/(^|[\s(])((https?:\/\/)[^\s<>"']+)/g, '$1<a href="$2" class="underline break-all" target="_blank" rel="noopener">$2</a>');
  return s;
}

/* Wrap a TeX fragment in a placeholder the SPA upgrades to rendered math
   (KaTeX). The raw TeX rides in data-math-src (attribute-escaped, decoded
   back by the browser); the visible body is the raw `$…$` so it degrades
   gracefully where no renderer runs. */
function mathSpan(tex: string, display: boolean): string {
  const delim = display ? "$$" : "$";
  return `<span data-math${display ? ' data-math-display=""' : ""} data-math-src="${esc(tex)}">${esc(delim + tex + delim)}</span>`;
}

function inlineMarkdown(s: string): string {
  /* Protect math before escaping: capture the raw TeX (which may contain
     <, >, & that esc would mangle) behind a sentinel, then reinsert as a
     placeholder span after the inline markdown passes. */
  const math: { tex: string; display: boolean }[] = [];
  s = s.replace(/\$\$([^\n$]+?)\$\$/g, (_m, tex) => `\x00M${math.push({ tex, display: true }) - 1}\x00`);
  s = s.replace(/(^|[^\\$])\$(?!\s)([^\n$]+?)(?<!\s)\$(?!\d)/g, (_m, pre, tex) => `${pre}\x00M${math.push({ tex, display: false }) - 1}\x00`);

  s = esc(s);
  s = s.replace(/\*\*\*(.+?)\*\*\*/g, "<strong><em>$1</em></strong>");
  s = s.replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/\*(.+?)\*/g, "<em>$1</em>");
  s = s.replace(/(^|\W)__(\S(?:.*?\S)?)__(?=\W|$)/g, "$1<strong>$2</strong>");
  s = s.replace(/(^|\W)_(\S(?:.*?\S)?)_(?=\W|$)/g, "$1<em>$2</em>");
  s = s.replace(/~~(.+?)~~/g, "<del>$1</del>");
  s = s.replace(/`([^`]+)`/g, '<code class="font-mono text-xs bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 rounded text-black-900 dark:text-white-100">$1</code>');
  s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^)]+)\)/g, '<a href="$2" class="text-green-600 dark:text-green-400 underline" target="_blank" rel="noopener">$1</a>');
  s = s.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_m, label, href) => {
    return `<a href="#" data-chat-path="${href}" class="text-green-600 dark:text-green-400 underline">${label}</a>`;
  });
  s = s.replace(/(^|[\s(])((https?:\/\/)[^\s<>"']+)/g, '$1<a href="$2" class="text-green-600 dark:text-green-400 underline break-all" target="_blank" rel="noopener">$2</a>');

  s = s.replace(/\x00M(\d+)\x00/g, (_m, i) => mathSpan(math[+i].tex, math[+i].display));
  return s;
}

export function renderMarkdown(text: string): string {
  if (!text) return "";
  const lines = text.split("\n");
  const out: string[] = [];
  let inCode = false, codeLang = "", codeLines: string[] = [];
  let inMath = false, mathLines: string[] = [];
  let inSvg = false, svgLines: string[] = [];
  let inList = false, listOl = false;
  let inTable = false, tableHeader = false;
  let listItems: string[] = [];

  function flushList() {
    if (!inList) return;
    const tag = listOl ? "ol" : "ul";
    const cls = listOl
      ? 'class="list-decimal list-inside space-y-0.5 my-1"'
      : 'class="list-disc list-inside space-y-0.5 my-1"';
    out.push(`<${tag} ${cls}>${listItems.join("")}</${tag}>`);
    inList = false; listOl = false; listItems = [];
  }

  function flushTable() {
    if (!inTable) return;
    out.push("</tbody></table></div>");
    inTable = false; tableHeader = false;
  }

  function emitMathBlock(tex: string) {
    out.push(
      `<div class="wick-math my-2 overflow-x-auto" data-math data-math-display="" data-math-src="${esc(tex)}">${esc("$$" + tex + "$$")}</div>`,
    );
  }

  function emitSvgBlock(code: string) {
    out.push(
      `<div class="wick-svg my-2 rounded-lg overflow-hidden bg-white-200 dark:bg-navy-800" data-svg data-svg-src="${esc(code)}">` +
      `<pre class="overflow-x-auto px-4 py-3 text-xs font-mono text-black-900 dark:text-white-100 leading-relaxed"><code>${esc(code)}</code></pre></div>`,
    );
  }

  function emitCodeBlock(lang: string, code: string) {
    /* Mermaid fences become a diagram placeholder; the SPA lazy-loads
       mermaid and swaps the SVG in. The raw source stays as the body so it
       degrades to a plain code block where no renderer runs. */
    if (lang === "mermaid") {
      out.push(
        `<div class="wick-mermaid my-2 rounded-lg overflow-hidden bg-white-200 dark:bg-navy-800" data-mermaid data-mermaid-src="${esc(code)}">` +
        `<pre class="overflow-x-auto px-4 py-3 text-xs font-mono text-black-900 dark:text-white-100 leading-relaxed"><code>${esc(code)}</code></pre></div>`,
      );
      return;
    }
    /* An svg fence becomes a rendered image placeholder; the SPA injects the
       markup as a sanitised inline SVG. The raw source stays as the body so it
       degrades to a plain code block where no renderer runs. */
    if (lang === "svg") {
      out.push(
        `<div class="wick-svg my-2 rounded-lg overflow-hidden bg-white-200 dark:bg-navy-800" data-svg data-svg-src="${esc(code)}">` +
        `<pre class="overflow-x-auto px-4 py-3 text-xs font-mono text-black-900 dark:text-white-100 leading-relaxed"><code>${esc(code)}</code></pre></div>`,
      );
      return;
    }
    /* An html fence becomes a sandboxed live-preview artifact; the SPA swaps
       in an isolated iframe. The raw source stays as the body so it degrades
       to a plain code block where no renderer runs. */
    if (lang === "html") {
      out.push(
        `<div class="wick-html-artifact my-2 rounded-lg overflow-hidden" data-html-artifact data-html-src="${esc(code)}">` +
        `<pre class="overflow-x-auto px-4 py-3 text-xs font-mono text-black-900 dark:text-white-100 bg-white-200 dark:bg-navy-800 leading-relaxed"><code>${esc(code)}</code></pre></div>`,
      );
      return;
    }
    /* An imagecard fence becomes a thumbnail-gallery placeholder; the SPA
       parses the body (one `url | caption` per line) into hotlinked image
       cards with a favicon + domain chip, and a click opens the lightbox
       carousel. The raw urls stay as the body so on a non-rich channel
       (Slack/Telegram) they still degrade to readable links. */
    if (lang === "imagecard") {
      out.push(
        `<div class="wick-imagecard my-2" data-imagecard data-imagecard-src="${esc(code)}">` +
        `<pre class="overflow-x-auto px-4 py-3 text-xs font-mono text-black-900 dark:text-white-100 bg-white-200 dark:bg-navy-800 rounded-lg leading-relaxed"><code>${esc(code)}</code></pre></div>`,
      );
      return;
    }
    const langLabel = lang
      ? `<span class="text-[10px] text-black-600 dark:text-black-700 uppercase tracking-wide">${esc(lang)}</span>`
      : "";
    const copyBtn = `<button type="button" data-copy-code data-code="${code.replace(/"/g, "&quot;")}" class="text-[10px] text-black-500 dark:text-black-600 hover:text-black-700 dark:hover:text-black-400 transition-colors px-1.5 py-0.5 rounded hover:bg-white-400 dark:hover:bg-navy-500">Copy</button>`;
    const codeOpen = lang ? `<code class="language-${esc(lang)}" data-code-lang="${esc(lang)}">` : "<code>";
    out.push(
      `<div class="my-2 rounded-lg overflow-hidden">` +
      `<div class="flex items-center justify-between px-3 py-1 bg-white-300 dark:bg-navy-600">${langLabel}${copyBtn}</div>` +
      `<pre class="overflow-x-auto px-4 py-3 text-xs font-mono text-black-900 dark:text-white-100 bg-white-200 dark:bg-navy-800 leading-relaxed">${codeOpen}${esc(code)}</code></pre></div>`,
    );
  }

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    const fenceMatch = line.match(/^```(\w*)$/);
    if (fenceMatch) {
      if (!inCode) {
        flushList();
        inCode = true; codeLang = fenceMatch[1]; codeLines = [];
      } else {
        emitCodeBlock(codeLang, codeLines.join("\n"));
        inCode = false; codeLang = ""; codeLines = [];
      }
      continue;
    }
    if (inCode) { codeLines.push(line); continue; }

    /* Raw inline SVG (no ```svg``` fence): the model often emits a bare
       <svg>…</svg>. Treat it like an svg block so it renders as an image
       instead of escaped source. Collect from the opening <svg until the
       closing </svg> (may span many lines, or be all on one). */
    if (!inSvg && /^\s*<svg[\s>]/i.test(line)) {
      flushList(); flushTable();
      inSvg = true; svgLines = [];
    }
    if (inSvg) {
      svgLines.push(line);
      if (/<\/svg\s*>/i.test(line)) {
        emitSvgBlock(svgLines.join("\n").trim());
        inSvg = false; svgLines = [];
      }
      continue;
    }

    /* Display-math fence: a line that is exactly "$$" opens/closes a block. */
    if (line.trim() === "$$") {
      if (!inMath) {
        flushList(); flushTable();
        inMath = true; mathLines = [];
      } else {
        emitMathBlock(mathLines.join("\n"));
        inMath = false; mathLines = [];
      }
      continue;
    }
    if (inMath) { mathLines.push(line); continue; }

    /* A line that is solely `$$…$$` is a standalone display equation: emit it
       as a centered block rather than an inline span inside a paragraph. */
    const dispMath = line.match(/^\s*\$\$(.+)\$\$\s*$/);
    if (dispMath && !dispMath[1].includes("$$")) {
      flushList(); flushTable();
      emitMathBlock(dispMath[1].trim());
      continue;
    }

    if (line.trim() === "") { flushList(); flushTable(); out.push('<div class="h-2"></div>'); continue; }

    const h = line.match(/^(#{1,3})\s+(.+)$/);
    if (h) {
      flushList();
      const lvl = h[1].length;
      const cls = lvl === 1
        ? "text-base font-semibold text-black-900 dark:text-white-100 mt-3 mb-1"
        : lvl === 2
          ? "text-sm font-semibold text-black-900 dark:text-white-100 mt-2 mb-1"
          : "text-sm font-medium text-black-800 dark:text-black-600 mt-2 mb-0.5";
      out.push(`<p class="${cls}">${inlineMarkdown(h[2])}</p>`);
      continue;
    }

    const bq = line.match(/^>\s?(.*)$/);
    if (bq) {
      flushList();
      out.push(`<blockquote class="border-l-2 border-green-400 dark:border-green-700 pl-3 my-1 text-black-700 dark:text-black-600 italic">${inlineMarkdown(bq[1])}</blockquote>`);
      continue;
    }

    const ul = line.match(/^[-*+]\s+(.+)$/);
    if (ul) {
      if (inList && listOl) flushList();
      inList = true; listOl = false;
      listItems.push(`<li class="text-sm text-black-900 dark:text-white-100">${inlineMarkdown(ul[1])}</li>`);
      continue;
    }

    const ol = line.match(/^\d+\.\s+(.+)$/);
    if (ol) {
      if (inList && !listOl) flushList();
      inList = true; listOl = true;
      listItems.push(`<li class="text-sm text-black-900 dark:text-white-100">${inlineMarkdown(ol[1])}</li>`);
      continue;
    }

    if (line.trim().startsWith("|")) {
      const trimmed = line.trim();
      if (/^\|[-:\s|]+\|?$/.test(trimmed)) {
        if (inTable && !tableHeader) { out.push("</thead><tbody>"); tableHeader = true; }
        continue;
      }
      const cells = trimmed.replace(/^\||\|$/g, "").split("|");
      if (!inTable) {
        flushList();
        out.push('<div class="overflow-x-auto my-2"><table class="w-full text-xs border-collapse">');
        out.push("<thead><tr>" + cells.map((c) => `<th class="border border-white-300 dark:border-navy-600 px-3 py-1.5 text-left font-semibold text-black-900 dark:text-white-100 bg-white-300 dark:bg-navy-700">${inlineMarkdown(c.trim())}</th>`).join("") + "</tr>");
        inTable = true; tableHeader = false;
      } else {
        out.push("<tr>" + cells.map((c) => `<td class="border border-white-300 dark:border-navy-600 px-3 py-1.5 text-black-900 dark:text-white-100">${inlineMarkdown(c.trim())}</td>`).join("") + "</tr>");
      }
      continue;
    }

    /* Thematic break: a line of only ---, ***, or ___ (3+). Render an
       <hr> instead of leaking the dashes/asterisks into a paragraph. */
    if (/^\s*([-*_])\1{2,}\s*$/.test(line)) {
      flushList();
      flushTable();
      out.push('<hr class="my-3 border-0 border-t border-white-300 dark:border-navy-600"/>');
      continue;
    }

    /* A lone bullet marker (just "*" or "-" with no content) is noise —
       skip it rather than printing a stray asterisk. */
    if (/^\s*[-*+]\s*$/.test(line)) {
      continue;
    }

    flushTable();
    flushList();
    out.push(`<p class="text-sm text-black-900 dark:text-white-100 leading-relaxed">${inlineMarkdown(line)}</p>`);
  }

  flushTable();
  flushList();
  if (inCode && codeLines.length) {
    /* A mermaid fence still open at EOF is mid-stream (no closing ```). Emit a
       partial mermaid block — data-mermaid-partial tells the renderer to paint
       progressively (best-effort parse, keep last good frame) instead of
       flashing raw source / parse errors on every streamed token. */
    if (codeLang === "mermaid") {
      const partial = codeLines.join("\n");
      out.push(
        `<div class="wick-mermaid my-2 rounded-lg overflow-hidden bg-white-200 dark:bg-navy-800" data-mermaid data-mermaid-partial data-mermaid-src="${esc(partial)}">` +
        `<pre class="overflow-x-auto px-4 py-3 text-xs font-mono text-black-900 dark:text-white-100 leading-relaxed"><code>${esc(partial)}</code></pre></div>`,
      );
    } else {
      emitCodeBlock(codeLang, codeLines.join("\n"));
    }
  }
  if (inMath && mathLines.length) {
    emitMathBlock(mathLines.join("\n"));
  }
  /* An SVG still open at EOF is mid-stream (no </svg> yet). Emit it as a
     partial svg block so the SPA renders whatever shapes are already complete
     ("painting" effect) instead of showing raw source. data-svg-partial tells
     the renderer to auto-close + tolerate an unfinished trailing tag. */
  if (inSvg && svgLines.length) {
    const partial = svgLines.join("\n").trim();
    out.push(
      `<div class="wick-svg my-2 rounded-lg overflow-hidden bg-white-200 dark:bg-navy-800" data-svg data-svg-partial data-svg-src="${esc(partial)}">` +
      `<pre class="overflow-x-auto px-4 py-3 text-xs font-mono text-black-900 dark:text-white-100 leading-relaxed"><code>${esc(partial)}</code></pre></div>`,
    );
  }

  return out.join("");
}
