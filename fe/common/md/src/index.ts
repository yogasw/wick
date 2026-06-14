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

function inlineMarkdown(s: string): string {
  s = esc(s);
  s = s.replace(/\*\*\*(.+?)\*\*\*/g, "<strong><em>$1</em></strong>");
  s = s.replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/\*(.+?)\*/g, "<em>$1</em>");
  s = s.replace(/(^|\W)__(\S(?:.*?\S)?)__(?=\W|$)/g, "$1<strong>$2</strong>");
  s = s.replace(/(^|\W)_(\S(?:.*?\S)?)_(?=\W|$)/g, "$1<em>$2</em>");
  s = s.replace(/`([^`]+)`/g, '<code class="font-mono text-xs bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 rounded text-black-900 dark:text-white-100">$1</code>');
  s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^)]+)\)/g, '<a href="$2" class="text-green-600 dark:text-green-400 underline" target="_blank" rel="noopener">$1</a>');
  s = s.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_m, label, href) => {
    return `<a href="#" data-chat-path="${href}" class="text-green-600 dark:text-green-400 underline">${label}</a>`;
  });
  s = s.replace(/(^|[\s(])((https?:\/\/)[^\s<>"']+)/g, '$1<a href="$2" class="text-green-600 dark:text-green-400 underline break-all" target="_blank" rel="noopener">$2</a>');
  return s;
}

export function renderMarkdown(text: string): string {
  if (!text) return "";
  const lines = text.split("\n");
  const out: string[] = [];
  let inCode = false, codeLang = "", codeLines: string[] = [];
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

  function emitCodeBlock(lang: string, code: string) {
    const langLabel = lang
      ? `<span class="text-[10px] text-black-600 dark:text-black-700 uppercase tracking-wide">${esc(lang)}</span>`
      : "";
    const copyBtn = `<button type="button" data-copy-code data-code="${code.replace(/"/g, "&quot;")}" class="text-[10px] text-black-500 dark:text-black-600 hover:text-black-700 dark:hover:text-black-400 transition-colors px-1.5 py-0.5 rounded hover:bg-white-400 dark:hover:bg-navy-500">Copy</button>`;
    out.push(
      `<div class="my-2 rounded-lg overflow-hidden border border-white-300 dark:border-navy-600">` +
      `<div class="flex items-center justify-between px-3 py-1 bg-white-300 dark:bg-navy-600">${langLabel}${copyBtn}</div>` +
      `<pre class="overflow-x-auto px-4 py-3 text-xs font-mono text-black-900 dark:text-white-100 bg-white-200 dark:bg-navy-800 leading-relaxed"><code>${esc(code)}</code></pre></div>`
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

    flushTable();
    flushList();
    out.push(`<p class="text-sm text-black-900 dark:text-white-100 leading-relaxed">${inlineMarkdown(line)}</p>`);
  }

  flushTable();
  flushList();
  if (inCode && codeLines.length) {
    emitCodeBlock(codeLang, codeLines.join("\n"));
  }

  return out.join("");
}
