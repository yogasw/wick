// Skills page interactions — row navigation, kebab delete, markdown preview.
(function () {
  // ── Markdown renderer (copy of agents.js renderMarkdown) ──────────────
  function esc(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  function inlineMarkdown(s) {
    s = esc(s);
    s = s.replace(/\*\*\*(.+?)\*\*\*/g, '<strong><em>$1</em></strong>');
    s = s.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
    s = s.replace(/\*(.+?)\*/g, '<em>$1</em>');
    // Underscore emphasis — only at word boundaries so identifiers like
    // mcp_claude_ai_Slack don't get eaten as italic.
    s = s.replace(/(^|\W)__(\S(?:.*?\S)?)__(?=\W|$)/g, '$1<strong>$2</strong>');
    s = s.replace(/(^|\W)_(\S(?:.*?\S)?)_(?=\W|$)/g, '$1<em>$2</em>');
    s = s.replace(/`([^`]+)`/g, '<code class="font-mono text-xs bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 rounded text-black-900 dark:text-white-100">$1</code>');
    s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^\)]+)\)/g, '<a href="$2" class="text-green-600 dark:text-green-400 underline" target="_blank" rel="noopener">$1</a>');
    s = s.replace(/(^|[\s(])((https?:\/\/)[^\s<>"']+)/g, '$1<a href="$2" class="text-green-600 dark:text-green-400 underline break-all" target="_blank" rel="noopener">$2</a>');
    return s;
  }

  function renderMarkdown(text) {
    if (!text) return "";
    var lines = text.split("\n");
    var out = [];
    var inCode = false, codeLang = "", codeLines = [];
    var inList = false, listOl = false, listItems = [];
    var inTable = false, tableHeader = false;

    function flushList() {
      if (!inList) return;
      var tag = listOl ? "ol" : "ul";
      var cls = listOl ? 'class="list-decimal list-inside space-y-0.5 my-1"' : 'class="list-disc list-inside space-y-0.5 my-1"';
      out.push("<" + tag + " " + cls + ">" + listItems.join("") + "</" + tag + ">");
      inList = false; listOl = false; listItems = [];
    }
    function flushTable() {
      if (!inTable) return;
      out.push("</tbody></table></div>");
      inTable = false; tableHeader = false;
    }

    for (var i = 0; i < lines.length; i++) {
      var line = lines[i];
      var fenceMatch = line.match(/^```(\w*)$/);
      if (fenceMatch) {
        if (!inCode) {
          flushList();
          inCode = true; codeLang = fenceMatch[1]; codeLines = [];
        } else {
          var langLabel = codeLang ? '<span class="text-[10px] text-black-600 dark:text-black-700 uppercase tracking-wide">' + esc(codeLang) + '</span>' : '';
          out.push(
            '<div class="my-2 rounded-lg overflow-hidden border border-white-300 dark:border-navy-600">' +
            (codeLang ? '<div class="flex items-center justify-between px-3 py-1 bg-white-300 dark:bg-navy-600">' + langLabel + '</div>' : '') +
            '<pre class="overflow-x-auto px-4 py-3 text-xs font-mono text-black-900 dark:text-white-100 bg-white-200 dark:bg-navy-800 leading-relaxed"><code>' +
            esc(codeLines.join("\n")) + '</code></pre></div>'
          );
          inCode = false; codeLang = ""; codeLines = [];
        }
        continue;
      }
      if (inCode) { codeLines.push(line); continue; }

      if (line.trim() === "") { flushList(); flushTable(); out.push('<div class="h-2"></div>'); continue; }

      var h = line.match(/^(#{1,3})\s+(.+)$/);
      if (h) {
        flushList(); flushTable();
        var lvl = h[1].length;
        var hcls = lvl === 1 ? "text-base font-semibold text-black-900 dark:text-white-100 mt-3 mb-1"
          : lvl === 2 ? "text-sm font-semibold text-black-900 dark:text-white-100 mt-2 mb-1"
          : "text-sm font-medium text-black-800 dark:text-black-600 mt-2 mb-0.5";
        out.push('<p class="' + hcls + '">' + inlineMarkdown(h[2]) + '</p>');
        continue;
      }

      var bq = line.match(/^>\s?(.*)$/);
      if (bq) {
        flushList(); flushTable();
        out.push('<blockquote class="border-l-2 border-green-400 dark:border-green-700 pl-3 my-1 text-black-700 dark:text-black-600 italic">' + inlineMarkdown(bq[1]) + '</blockquote>');
        continue;
      }

      var ul = line.match(/^[-*+]\s+(.+)$/);
      if (ul) {
        flushTable();
        if (inList && listOl) flushList();
        inList = true; listOl = false;
        listItems.push('<li class="text-sm text-black-900 dark:text-white-100">' + inlineMarkdown(ul[1]) + '</li>');
        continue;
      }

      var ol = line.match(/^\d+\.\s+(.+)$/);
      if (ol) {
        flushTable();
        if (inList && !listOl) flushList();
        inList = true; listOl = true;
        listItems.push('<li class="text-sm text-black-900 dark:text-white-100">' + inlineMarkdown(ol[1]) + '</li>');
        continue;
      }

      if (line.trim().startsWith("|")) {
        var trimmed = line.trim();
        if (/^\|[-:\s|]+\|?$/.test(trimmed)) {
          if (inTable && !tableHeader) { out.push("</thead><tbody>"); tableHeader = true; }
          continue;
        }
        var cells = trimmed.replace(/^\||\|$/g, "").split("|");
        if (!inTable) {
          flushList();
          out.push('<div class="overflow-x-auto my-2"><table class="w-full text-xs border-collapse">');
          out.push("<thead><tr>" + cells.map(function(c) {
            return '<th class="border border-white-300 dark:border-navy-600 px-3 py-1.5 text-left font-semibold text-black-900 dark:text-white-100 bg-white-300 dark:bg-navy-700">' + inlineMarkdown(c.trim()) + "</th>";
          }).join("") + "</tr>");
          inTable = true; tableHeader = false;
        } else {
          out.push("<tr>" + cells.map(function(c) {
            return '<td class="border border-white-300 dark:border-navy-600 px-3 py-1.5 text-black-900 dark:text-white-100">' + inlineMarkdown(c.trim()) + "</td>";
          }).join("") + "</tr>");
        }
        continue;
      }

      flushTable(); flushList();
      out.push('<p class="text-sm text-black-900 dark:text-white-100 leading-relaxed">' + inlineMarkdown(line) + '</p>');
    }
    flushTable(); flushList();
    if (inCode && codeLines.length) {
      out.push('<pre class="text-xs font-mono bg-white-200 dark:bg-navy-800 px-4 py-3 rounded-lg overflow-x-auto"><code>' + esc(codeLines.join("\n")) + '</code></pre>');
    }
    return out.join("");
  }

  document.addEventListener("DOMContentLoaded", function () {
    // Render markdown preview for [data-md] elements.
    document.querySelectorAll("[data-md]").forEach(function (el) {
      var raw = el.textContent;
      el.innerHTML = renderMarkdown(raw);
      el.removeAttribute("data-md");
    });

    // Clickable rows — navigate to data-row-link on click.
    document.addEventListener("click", function (e) {
      if (e.target.closest("[data-row-action]")) return;
      if (e.target.closest("a, button, summary, input, select, textarea, label")) return;
      var row = e.target.closest("[data-row-link]");
      if (!row) return;
      var href = row.dataset.rowLink;
      if (!href) return;
      if (e.metaKey || e.ctrlKey || e.button === 1) {
        window.open(href, "_blank");
      } else {
        window.location.href = href;
      }
    });

    // Auto-close open <details data-row-action> on outside click.
    document.addEventListener("click", function (e) {
      document.querySelectorAll("details[data-row-action][open]").forEach(function (d) {
        if (!d.contains(e.target)) d.removeAttribute("open");
      });
    });

    // Sync skill — data-sync-skill holds the POST URL.
    document.addEventListener("click", function (e) {
      var btn = e.target.closest("[data-sync-skill]");
      if (!btn) return;
      var url = btn.dataset.syncSkill;
      var form = document.createElement("form");
      form.method = "POST";
      form.action = url;
      document.body.appendChild(form);
      form.submit();
    });

    // Delete skill — data-delete-skill holds the POST URL.
    document.addEventListener("click", function (e) {
      var btn = e.target.closest("[data-delete-skill]");
      if (!btn) return;
      if (!confirm("Delete from all dirs?")) return;
      var url = btn.dataset.deleteSkill;
      var form = document.createElement("form");
      form.method = "POST";
      form.action = url;
      document.body.appendChild(form);
      form.submit();
    });
  });
}());
