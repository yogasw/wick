(function () {
  "use strict";

  // Base URL for the agents tool — read from the session detail element
  // so we never hard-code a path and it works under any mount prefix.
  function agentsBase() {
    var el = document.querySelector("[data-session-id]");
    return el ? (el.dataset.base || "") : "";
  }
  function sessionID() {
    var el = document.querySelector("[data-session-id]");
    return el ? (el.dataset.sessionId || "") : "";
  }

  // Shared trace toggle — wired by inline onclick on both top and
  // bottom toggle buttons inside an assistant turn. Reads the wrap's
  // hidden state, flips it, then syncs label + chevron + bottom-button
  // visibility so the user can collapse from either end without
  // scrolling.
  window.wickToggleTrace = function (btn) {
    var section = btn.closest("[data-turn-events]");
    if (!section) return;
    var container = section.querySelector("[data-trace-container]");
    var wrap = section.querySelector("[data-trace-wrap]");
    var top = section.querySelector("[data-trace-toggle]");
    var bottom = section.querySelector("[data-trace-toggle-bottom]");
    if (!wrap || !top) return;
    var willOpen = wrap.classList.contains("hidden");

    // Lazy-load trace from server when wrap is empty and turn-id is known.
    var turnID = container && container.dataset.turnId;
    var alreadyLoaded = wrap.dataset.loaded === "1";
    if (willOpen && turnID && !alreadyLoaded && wrap.children.length === 0) {
      var sid = sessionID();
      if (sid) {
        var lbl = top.querySelector("[data-trace-label]");
        if (lbl) lbl.textContent = "loading…";
        fetch(agentsBase() + "/sessions/" + sid + "/turns/" + turnID, { credentials: "include" })
          .then(function(r) { return r.json(); })
          .then(function(data) {
            wrap.dataset.loaded = "1";
            var events = data.events || [];
            events.forEach(function(ev) {
              var card = buildTraceCard(ev, sessionID, turnID);
              if (card) wrap.appendChild(card);
            });
            if (lbl) lbl.textContent = "hide trace";
            wrap.classList.remove("hidden"); wrap.classList.add("flex", "flex-col");
            var chev = top.querySelector("[data-chevron]");
            if (chev) chev.style.transform = "";
            if (bottom) { bottom.classList.remove("hidden"); bottom.classList.add("flex"); }
          })
          .catch(function() {
            if (lbl) lbl.textContent = "show trace";
          });
        return;
      }
    }

    if (willOpen) { wrap.classList.remove("hidden"); wrap.classList.add("flex", "flex-col"); }
    else { wrap.classList.add("hidden"); wrap.classList.remove("flex", "flex-col"); }
    var chev = top.querySelector("[data-chevron]");
    if (chev) chev.style.transform = willOpen ? "" : "rotate(-90deg)";
    var loading = top.dataset.loading === "1";
    var lbl = top.querySelector("[data-trace-label]");
    if (lbl) lbl.textContent = willOpen ? "hide trace" : (loading ? "working…" : "show trace");
    if (bottom) {
      if (willOpen) { bottom.classList.remove("hidden"); bottom.classList.add("flex"); }
      else { bottom.classList.add("hidden"); bottom.classList.remove("flex"); }
      var bspin = bottom.querySelector("[data-trace-spin-bottom]");
      var bicon = bottom.querySelector("[data-trace-icon-bottom]");
      var blbl = bottom.querySelector("[data-trace-label-bottom]");
      if (bspin) bspin.classList.toggle("hidden", !loading);
      if (bicon) bicon.classList.toggle("hidden", loading);
      if (blbl) blbl.textContent = loading ? "working… (hide)" : "hide trace";
    }
    if (willOpen) {
      var target = bottom && !bottom.classList.contains("hidden") ? bottom : wrap;
      if (target && target.scrollIntoView) {
        target.scrollIntoView({ behavior: "smooth", block: "end" });
      }
    }
  };

  // buildTraceCard renders one TurnEventIndex as an HTML element.
  // For large events (large:true), fetches payload on expand.
  window.buildTraceCard = function(ev, sessionID, turnID) {
    var d = document.createElement("div");
    if (ev.type === "thinking") {
      var text = ev.text || "";
      d.className = "rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs";
      d.innerHTML =
        '<button type="button" onclick="var b=this.parentElement.querySelector(\'[data-thinking-body]\');b.classList.toggle(\'hidden\');this.querySelector(\'[data-chevron]\').style.transform=b.classList.contains(\'hidden\')?\'rotate(-90deg)\':\'\'" ' +
        'class="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-white-200 dark:hover:bg-navy-800 transition-colors text-black-600 dark:text-black-700">' +
        '<svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="8" cy="8" r="5.5"></circle><path d="M8 5.5v3l1.5 1.5" stroke-linecap="round" stroke-linejoin="round"></path></svg>' +
        '<span class="italic">thinking</span>' +
        '<svg data-chevron viewBox="0 0 16 16" class="ml-auto h-3 w-3 shrink-0 text-black-500 transition-transform" style="transform:rotate(-90deg)" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"></path></svg>' +
        '</button>' +
        '<div data-thinking-body class="hidden border-t border-white-300 dark:border-navy-600 px-3 py-2 italic text-black-600 dark:text-black-700 leading-relaxed break-words text-xs">' + esc(text) + '</div>';
    } else if (ev.type === "tool_use") {
      d.className = "rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs";
      d.innerHTML =
        '<div class="flex w-full items-center gap-2 px-3 py-2">' +
        '<svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-green-600 dark:text-green-400" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 4h4v8H2zM10 4h4v8h-4z" stroke-linejoin="round"></path><path d="M6 8h4" stroke-linecap="round"></path></svg>' +
        '<span class="font-mono font-medium text-black-900 dark:text-white-100">' + esc(ev.tool_name || "") + '</span>' +
        '<span class="ml-auto text-[10px] text-black-500 dark:text-black-600 uppercase tracking-wide shrink-0">tool call</span>' +
        '</div>';
    } else if (ev.type === "tool_result") {
      var resultText = ev.large
        ? '<span class="italic text-black-500 dark:text-black-600">' + Math.round((ev.size||0)/1024) + ' KB — click to load</span>'
        : esc(ev.text || "");
      d.className = "rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs";
      if (ev.large) {
        d.setAttribute("data-large-event", ev.event_id);
        d.setAttribute("data-turn-id", turnID);
        d.setAttribute("data-session-id", sessionID);
        d.style.cursor = "pointer";
        d.onclick = function() {
          var eid = d.getAttribute("data-large-event");
          var sid = d.getAttribute("data-session-id");
          var tid = d.getAttribute("data-turn-id");
          fetch(agentsBase() + "/sessions/" + sid + "/turns/" + tid + "/events/" + eid, { credentials: "include" })
            .then(function(r) { return r.json(); })
            .then(function(payload) {
              d.onclick = null;
              d.style.cursor = "";
              d.innerHTML = '<div class="px-3 py-2 font-mono break-all whitespace-pre-wrap text-black-900 dark:text-white-100">' + esc(payload.text || "") + '</div>';
            });
        };
      }
      d.innerHTML = '<div class="flex items-center gap-2 px-3 py-2 border-b border-white-300 dark:border-navy-600">' +
        (ev.is_error ? '<svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-red-500" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="8" cy="8" r="5.5"></circle><path d="M8 5.5v3" stroke-linecap="round"></path><circle cx="8" cy="11" r="0.5" fill="currentColor"></circle></svg>' : '<svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-green-500" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M3 8l3.5 3.5L13 5" stroke-linecap="round" stroke-linejoin="round"></path></svg>') +
        '<span class="text-[10px] text-black-500 dark:text-black-600 uppercase tracking-wide">' + (ev.is_error ? "error" : "result") + '</span>' +
        '</div>' +
        '<div class="px-3 py-2 font-mono break-all whitespace-pre-wrap text-black-900 dark:text-white-100">' + resultText + '</div>';
    } else {
      return null;
    }
    return d;
  };

  // ── Minimal markdown renderer ─────────────────────────────────────────
  // No external deps — handles bold, italic, inline code, fenced code
  // blocks, headers, bullet lists, numbered lists, blockquotes, and
  // bare URLs. Enough for typical LLM output.
  function renderMarkdown(text) {
    if (!text) return "";
    var lines = text.split("\n");
    var out = [];
    var inCode = false, codeLang = "", codeLines = [];
    var inList = false, listOl = false;
    var inTable = false, tableHeader = false;

    function flushList() {
      if (!inList) return;
      var tag = listOl ? "ol" : "ul";
      var cls = listOl
        ? 'class="list-decimal list-inside space-y-0.5 my-1"'
        : 'class="list-disc list-inside space-y-0.5 my-1"';
      out.push("<" + tag + " " + cls + ">" + listItems.join("") + "</" + tag + ">");
      inList = false; listOl = false; listItems = [];
    }
    function flushTable() {
      if (!inTable) return;
      out.push("</tbody></table>");
      inTable = false; tableHeader = false;
    }
    var listItems = [];

    for (var i = 0; i < lines.length; i++) {
      var line = lines[i];

      // Fenced code block open/close
      var fenceMatch = line.match(/^```(\w*)$/);
      if (fenceMatch) {
        if (!inCode) {
          flushList();
          inCode = true; codeLang = fenceMatch[1]; codeLines = [];
        } else {
          var langLabel = codeLang
            ? '<span class="text-[10px] text-black-600 dark:text-black-700 uppercase tracking-wide">' + esc(codeLang) + '</span>'
            : '';
          var codeText = codeLines.join("\n");
          var copyBtn = '<button type="button" onclick="var btn=this;navigator.clipboard.writeText(this.dataset.code).then(function(){btn.textContent=\'Copied\';setTimeout(function(){btn.textContent=\'Copy\'},1500)}).catch(function(){})" ' +
            'data-code="' + codeText.replace(/"/g, '&quot;') + '" ' +
            'class="text-[10px] text-black-500 dark:text-black-600 hover:text-black-700 dark:hover:text-black-400 transition-colors px-1.5 py-0.5 rounded hover:bg-white-400 dark:hover:bg-navy-500">Copy</button>';
          out.push(
            '<div class="my-2 rounded-lg overflow-hidden border border-white-300 dark:border-navy-600">' +
            '<div class="flex items-center justify-between px-3 py-1 bg-white-300 dark:bg-navy-600">' + langLabel + copyBtn + '</div>' +
            '<pre class="overflow-x-auto px-4 py-3 text-xs font-mono text-black-900 dark:text-white-100 bg-white-200 dark:bg-navy-800 leading-relaxed"><code>' +
            esc(codeText) + '</code></pre></div>'
          );
          inCode = false; codeLang = ""; codeLines = [];
        }
        continue;
      }
      if (inCode) { codeLines.push(line); continue; }

      // Blank line
      if (line.trim() === "") { flushList(); out.push('<div class="h-2"></div>'); continue; }

      // Headings
      var h = line.match(/^(#{1,3})\s+(.+)$/);
      if (h) {
        flushList();
        var lvl = h[1].length;
        var cls = lvl === 1
          ? "text-base font-semibold text-black-900 dark:text-white-100 mt-3 mb-1"
          : lvl === 2
            ? "text-sm font-semibold text-black-900 dark:text-white-100 mt-2 mb-1"
            : "text-sm font-medium text-black-800 dark:text-black-600 mt-2 mb-0.5";
        out.push('<p class="' + cls + '">' + inlineMarkdown(h[2]) + '</p>');
        continue;
      }

      // Blockquote
      var bq = line.match(/^>\s?(.*)$/);
      if (bq) {
        flushList();
        out.push(
          '<blockquote class="border-l-2 border-green-400 dark:border-green-700 pl-3 my-1 text-black-700 dark:text-black-600 italic">' +
          inlineMarkdown(bq[1]) + '</blockquote>'
        );
        continue;
      }

      // Bullet list
      var ul = line.match(/^[-*+]\s+(.+)$/);
      if (ul) {
        if (inList && listOl) flushList();
        inList = true; listOl = false;
        listItems.push('<li class="text-sm text-black-900 dark:text-white-100">' + inlineMarkdown(ul[1]) + '</li>');
        continue;
      }

      // Numbered list
      var ol = line.match(/^\d+\.\s+(.+)$/);
      if (ol) {
        if (inList && !listOl) flushList();
        inList = true; listOl = true;
        listItems.push('<li class="text-sm text-black-900 dark:text-white-100">' + inlineMarkdown(ol[1]) + '</li>');
        continue;
      }

      // Table
      if (line.trim().startsWith("|")) {
        var trimmed = line.trim();
        // separator row |---|---| → marks end of header
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
      flushTable();
      flushList();
      out.push('<p class="text-sm text-black-900 dark:text-white-100 leading-relaxed">' + inlineMarkdown(line) + '</p>');
    }
    flushTable();
    flushList();
    if (inCode && codeLines.length) {
      var codeText2 = codeLines.join("\n");
      var copyBtn2 = '<button type="button" onclick="var btn=this;navigator.clipboard.writeText(this.dataset.code).then(function(){btn.textContent=\'Copied\';setTimeout(function(){btn.textContent=\'Copy\'},1500)}).catch(function(){})" data-code="' + codeText2.replace(/"/g, '&quot;') + '" class="text-[10px] text-black-500 dark:text-black-600 hover:text-black-700 dark:hover:text-black-400 transition-colors px-1.5 py-0.5 rounded hover:bg-white-400 dark:hover:bg-navy-500">Copy</button>';
      out.push('<div class="my-2 rounded-lg overflow-hidden border border-white-300 dark:border-navy-600"><div class="flex items-center justify-between px-3 py-1 bg-white-300 dark:bg-navy-600"><span></span>' + copyBtn2 + '</div><pre class="text-xs font-mono bg-white-200 dark:bg-navy-800 px-4 py-3 overflow-x-auto"><code>' + esc(codeText2) + '</code></pre></div>');
    }
    return out.join("");
  }

  function inlineMarkdown(s) {
    s = esc(s);
    // Bold + italic
    s = s.replace(/\*\*\*(.+?)\*\*\*/g, '<strong><em>$1</em></strong>');
    s = s.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
    s = s.replace(/\*(.+?)\*/g, '<em>$1</em>');
    // Underscore emphasis — only at word boundaries so identifiers like
    // mcp_claude_ai_Slack don't get eaten as italic. Asterisks above
    // still handle the intraword case.
    s = s.replace(/(^|\W)__(\S(?:.*?\S)?)__(?=\W|$)/g, '$1<strong>$2</strong>');
    s = s.replace(/(^|\W)_(\S(?:.*?\S)?)_(?=\W|$)/g, '$1<em>$2</em>');
    // Inline code
    s = s.replace(/`([^`]+)`/g, '<code class="font-mono text-xs bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 rounded text-black-900 dark:text-white-100">$1</code>');
    // Links — http(s) external (open in new tab) + arbitrary path
    // links (intercepted by chatLinkClick: open in Context panel when
    // under session cwd, otherwise show a path popup).
    s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^\)]+)\)/g, '<a href="$2" class="text-green-600 dark:text-green-400 underline" target="_blank" rel="noopener">$1</a>');
    s = s.replace(/\[([^\]]+)\]\(([^\)]+)\)/g, function (_m, label, href) {
      // Note: inlineMarkdown's first line calls esc(s), so both label
      // and href arrive already HTML-escaped (e.g. " → &quot;).
      // Putting href into the attribute as-is is correct — the DOM
      // unescapes on getAttribute() so chatLinkClick sees the
      // original string. Skip URIs the previous pass already
      // rewrote — those carry " class=" and never match this regex.
      return '<a href="#" data-chat-path="' + href + '" class="text-green-600 dark:text-green-400 underline">' + label + '</a>';
    });
    // Bare URLs
    s = s.replace(/(^|[\s(])((https?:\/\/)[^\s<>"']+)/g, '$1<a href="$2" class="text-green-600 dark:text-green-400 underline break-all" target="_blank" rel="noopener">$2</a>');
    return s;
  }

  function esc(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  // Render all server-side [data-md] nodes on load
  function renderExistingMarkdown() {
    document.querySelectorAll("[data-md]").forEach(function (el) {
      var raw = el.textContent;
      el.innerHTML = renderMarkdown(raw);
      el.removeAttribute("data-md");
    });
    document.querySelectorAll("[data-linkify]").forEach(function (el) {
      var raw = el.textContent;
      el.innerHTML = linkifyText(raw);
      el.removeAttribute("data-linkify");
    });
  }

  // Lightweight: escape HTML + turn bare/markdown URLs into anchors.
  // Used for user bubble where we don't want full markdown parsing
  // (so `**foo**` typed by the user stays literal).
  function linkifyText(s) {
    s = esc(s);
    s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^\)]+)\)/g, '<a href="$2" class="underline break-all" target="_blank" rel="noopener">$1</a>');
    s = s.replace(/(^|[\s(])((https?:\/\/)[^\s<>"']+)/g, '$1<a href="$2" class="underline break-all" target="_blank" rel="noopener">$2</a>');
    return s;
  }

  // chatLinkClick is the click handler for non-http markdown links
  // rendered by inlineMarkdown(). It tries the Context panel first
  // (so paths under the current session cwd open inline) and falls
  // back to a tiny popup that just shows the raw path so the user
  // can read or copy it.
  function chatLinkClick(ev) {
    var a = ev.target.closest && ev.target.closest("a[data-chat-path]");
    if (!a) return;
    ev.preventDefault();
    var path = a.getAttribute("data-chat-path") || "";
    if (window.AgentContext && window.AgentContext.openFileByAbsPath) {
      if (window.AgentContext.openFileByAbsPath(path)) return;
    }
    showPathPopup(path);
  }

  // showPathPopup renders a lightweight modal with the path text +
  // a Copy button. Used when the link can't be opened in the panel
  // (path outside session cwd, panel not initialised, etc.) so the
  // user still has *something* to act on.
  function showPathPopup(path) {
    var existing = document.querySelector("[data-chat-path-popup]");
    if (existing) existing.remove();
    var wrap = document.createElement("div");
    wrap.setAttribute("data-chat-path-popup", "");
    wrap.className = "fixed inset-0 z-[60] flex items-center justify-center bg-black/40";
    wrap.innerHTML =
      '<div class="bg-white-100 dark:bg-navy-700 rounded-lg shadow-xl max-w-xl w-[90vw] p-4 space-y-3">' +
        '<div class="flex items-start justify-between gap-3">' +
          '<div class="text-sm font-medium text-black-900 dark:text-white-100">External path</div>' +
          '<button type="button" data-chat-path-close class="text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100">' +
            '<svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"/></svg>' +
          '</button>' +
        '</div>' +
        '<div class="text-xs text-black-700 dark:text-black-600">Path is outside the current session — opening here is not supported.</div>' +
        '<div class="font-mono text-xs bg-white-200 dark:bg-navy-800 border border-white-300 dark:border-navy-600 rounded px-3 py-2 break-all select-all">' + esc(path) + '</div>' +
        '<div class="flex justify-end gap-2">' +
          '<button type="button" data-chat-path-copy class="text-xs px-3 py-1.5 rounded bg-green-500 hover:bg-green-600 text-white-100">Copy</button>' +
        '</div>' +
      '</div>';
    document.body.appendChild(wrap);
    var onKey = function (e) { if (e.key === "Escape") close(); };
    var close = function () { wrap.remove(); document.removeEventListener("keydown", onKey); };
    wrap.addEventListener("click", function (e) { if (e.target === wrap) close(); });
    wrap.querySelector("[data-chat-path-close]").addEventListener("click", close);
    wrap.querySelector("[data-chat-path-copy]").addEventListener("click", function () {
      if (navigator.clipboard) navigator.clipboard.writeText(path).catch(function () {});
    });
    document.addEventListener("keydown", onKey);
  }

  document.addEventListener("DOMContentLoaded", function () {
    renderExistingMarkdown();
    document.addEventListener("click", chatLinkClick);


    // ── Session search + client-side pagination ───────────────────────
    // All rows are rendered server-side; search filters them and we page
    // the matches 10-at-a-time entirely in the browser. Paging never
    // reloads, so the compose box + search text stay put.
    (function () {
      var list = document.querySelector("[data-session-list]");
      var searchInput = document.querySelector("[data-session-search]");
      if (!list) return;
      var pageSize = parseInt(list.dataset.pageSize || "10", 10) || 10;
      var rows = Array.prototype.slice.call(list.querySelectorAll("[data-search-row]"));
      var empty = document.querySelector("[data-search-empty]");
      var pager = document.querySelector("[data-client-pager]");
      var prevBtn = pager && pager.querySelector("[data-pager-prev]");
      var nextBtn = pager && pager.querySelector("[data-pager-next]");
      var label = pager && pager.querySelector("[data-pager-label]");
      var page = 1;

      function render() {
        var q = searchInput ? searchInput.value.trim().toLowerCase() : "";
        var matched = rows.filter(function (row) {
          return !q || (row.dataset.searchText || "").toLowerCase().includes(q);
        });
        var totalPages = Math.max(1, Math.ceil(matched.length / pageSize));
        if (page > totalPages) page = totalPages;
        if (page < 1) page = 1;
        var startI = (page - 1) * pageSize;
        var endI = startI + pageSize;
        // Hide everything, then show only this page's matched slice.
        rows.forEach(function (row) { row.style.display = "none"; });
        matched.slice(startI, endI).forEach(function (row) { row.style.display = ""; });
        if (empty) empty.classList.toggle("hidden", matched.length > 0);
        if (pager) {
          pager.classList.toggle("hidden", matched.length <= pageSize);
          if (label) label.textContent = "Page " + page + " / " + totalPages;
          if (prevBtn) prevBtn.disabled = page <= 1;
          if (nextBtn) nextBtn.disabled = page >= totalPages;
        }
      }

      if (searchInput) {
        searchInput.addEventListener("input", function () { page = 1; render(); });
        searchInput.focus();
      }
      if (prevBtn) prevBtn.addEventListener("click", function () { if (page > 1) { page--; render(); } });
      if (nextBtn) nextBtn.addEventListener("click", function () { page++; render(); });
      render();
    })();

    var root = document.querySelector("[data-session-id]");
    var base = root ? root.dataset.base : null;
    var sessionID = root ? root.dataset.sessionId : null;

    // Scroll chat to bottom on page load
    requestAnimationFrame(function() {
      var bottom = document.getElementById("chat-bottom");
      if (bottom) bottom.scrollIntoView({ block: "end" });
    });

    // ── Floating scroll-to-bottom button ──────────────────────────────
    // IntersectionObserver on the #chat-bottom sentinel: button shown
    // whenever the sentinel is off-screen (user scrolled up OR new
    // content grew below the viewport during streaming).
    //
    // To avoid a show→hide flicker on page load and after sending, the
    // observer is "settled" only after the initial scroll-to-bottom
    // lands. Before settling it can only hide the button, never show.
    (function () {
      var panel = document.querySelector("[data-chat-panel]");
      var btn = document.querySelector("[data-scroll-bottom]");
      var sentinel = document.getElementById("chat-bottom");
      if (!panel || !btn || !sentinel || !("IntersectionObserver" in window)) return;
      var settled = false;
      window.__wickArmScrollBtn = function () {
        settled = false;
        setTimeout(function () { settled = true; }, 250);
      };
      window.__wickArmScrollBtn();
      var io = new IntersectionObserver(function (entries) {
        entries.forEach(function (e) {
          if (e.isIntersecting) {
            btn.classList.add("hidden");
          } else if (settled) {
            btn.classList.remove("hidden");
          }
        });
      }, { root: panel, threshold: 0 });
      io.observe(sentinel);
      btn.addEventListener("click", function () {
        sentinel.scrollIntoView({ behavior: "smooth", block: "end" });
      });
      // Ctrl+ArrowDown: jump to latest from anywhere, including while
      // typing in the composer.
      document.addEventListener("keydown", function (e) {
        if (e.key !== "ArrowDown" || !e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return;
        e.preventDefault();
        sentinel.scrollIntoView({ behavior: "smooth", block: "end" });
      });
    })();

    // ── Auto-resize textarea ──────────────────────────────────────────
    document.querySelectorAll("[data-auto-resize]").forEach(function (ta) {
      function resize() {
        ta.style.height = "auto";
        ta.style.height = Math.min(ta.scrollHeight, 160) + "px";
      }
      ta.addEventListener("input", resize);
      resize();
    });

    // ── Auto-focus composer when user starts typing anywhere ──────────
    // If the user hits a printable key (or Backspace/Enter) while focus
    // is on body / a non-editable element, jump focus into the composer
    // textarea so they can type without clicking it first. Skips when
    // modal/overlay is open, when modifier keys are involved, or when
    // focus is already on an editable element.
    // Skip on touch devices: there's no physical keyboard to "start typing"
    // with, and force-focusing the composer just pops the on-screen keyboard.
    var composerTA = document.querySelector("[data-send-form] textarea");
    if (composerTA && !window.matchMedia("(pointer: coarse)").matches) {
      document.addEventListener("keydown", function (e) {
        if (e.ctrlKey || e.metaKey || e.altKey) return;
        if (e.key === "Escape" || e.key === "Tab") return;
        // Only act on single-char keys, Enter, Backspace, Space.
        var isPrintable = e.key.length === 1;
        if (!isPrintable && e.key !== "Enter" && e.key !== "Backspace") return;
        var t = e.target;
        if (!t) return;
        var tag = t.tagName;
        if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || t.isContentEditable) return;
        if (t.closest && t.closest("button, a, summary")) return;
        // Skip when an overlay (modal/drawer) is visible above the chat.
        var openModal = document.querySelector("[data-context-modal]:not(.hidden), [data-context-panel]:not(.hidden)");
        if (openModal) return;
        if (composerTA.disabled) return;
        composerTA.focus();
        // Don't synthesize the keystroke — letting the browser deliver
        // it after focus is unreliable cross-browser. For printable
        // keys, append manually so the first char isn't lost.
        if (isPrintable) {
          e.preventDefault();
          var pos = composerTA.selectionStart;
          var v = composerTA.value;
          composerTA.value = v.slice(0, pos) + e.key + v.slice(pos);
          var newPos = pos + e.key.length;
          composerTA.setSelectionRange(newPos, newPos);
          composerTA.dispatchEvent(new Event("input", { bubbles: true }));
        }
      });
    }

    // ── SSE via SharedWorker (session detail page only) ───────────────
    // SharedWorker holds EventSource connections across page navigations —
    // navigating to another session reuses the worker's existing socket
    // instead of tearing down and reconnecting.
    if (sessionID && base) {
      var pendingTurnEl = null;
      var pendingRawText = "";
      var typingIndicatorEl = null;
      // keyed by tool_use_id → the card element, so tool_result can attach to it
      var pendingToolCards = {};
      // true once first text_delta arrives in this turn — used to auto-collapse event cards
      var turnHasText = false;

      var ssePort = null;
      var sseConnectedHideTimer = null;
      // Render the SSE connection pill in the header. state: "" = hide,
      // "connected" = green dot (auto-hide 2s), "reconnecting" = amber spinner.
      function setSseStatus(state) {
        var pill = document.querySelector("[data-sse-status]");
        if (!pill) return;
        if (sseConnectedHideTimer) { clearTimeout(sseConnectedHideTimer); sseConnectedHideTimer = null; }
        var spin = pill.querySelector("[data-sse-spin]");
        var dot = pill.querySelector("[data-sse-dot]");
        var lbl = pill.querySelector("[data-sse-label]");
        pill.classList.remove(
          "border-green-300","dark:border-green-700","bg-green-50","dark:bg-green-900/20","text-green-700","dark:text-green-300",
          "border-amber-300","dark:border-amber-700","bg-amber-50","dark:bg-amber-900/20","text-amber-700","dark:text-amber-300"
        );
        if (state === "connected") {
          pill.classList.remove("hidden");
          pill.classList.add("border-green-300","dark:border-green-700","bg-green-50","dark:bg-green-900/20","text-green-700","dark:text-green-300");
          if (spin) spin.classList.add("hidden");
          if (dot) dot.classList.remove("hidden");
          if (lbl) lbl.textContent = "connected";
          sseConnectedHideTimer = setTimeout(function () { pill.classList.add("hidden"); }, 2000);
        } else if (state === "reconnecting") {
          pill.classList.remove("hidden");
          pill.classList.add("border-amber-300","dark:border-amber-700","bg-amber-50","dark:bg-amber-900/20","text-amber-700","dark:text-amber-300");
          if (spin) spin.classList.remove("hidden");
          if (dot) dot.classList.add("hidden");
          if (lbl) lbl.textContent = "reconnecting…";
        } else {
          pill.classList.add("hidden");
        }
        pill.dataset.state = state || "";
      }

      if (typeof SharedWorker !== "undefined") {
        var worker = new SharedWorker(base + "/static/js/sse-worker.js?v=2");
        ssePort = worker.port;
        ssePort.start();
        ssePort.postMessage({ type: "subscribe", sessionID: sessionID, base: base });
        window.addEventListener("pagehide", function () {
          ssePort.postMessage({ type: "unsubscribe", sessionID: sessionID });
        });
        ssePort.onmessage = function (msg) {
          var data = msg.data;
          if (!data) return;
          if (data.type === "event") handleAgentEvent(data.event);
          else if (data.type === "status") {
            if (data.status === "connected") setSseStatus("connected");
            else if (data.status === "error") setSseStatus("reconnecting");
          }
        };
      } else {
        // Fallback for browsers without SharedWorker support.
        var es = new EventSource(base + "/stream?session=" + encodeURIComponent(sessionID));
        es.addEventListener("agent", function (e) {
          var ev; try { ev = JSON.parse(e.data); } catch(_) { return; }
          handleAgentEvent(ev);
        });
        es.onopen = function () { setSseStatus("connected"); };
        es.onerror = function () {
          // EventSource auto-reconnects unless readyState === CLOSED.
          if (es.readyState !== EventSource.CLOSED) setSseStatus("reconnecting");
        };
      }

      var typingSubstate = "";

      function showTypingIndicator() {
        if (typingIndicatorEl) return;
        var container = document.querySelector("[data-turns]");
        if (!container) return;
        typingIndicatorEl = document.createElement("div");
        typingIndicatorEl.className = "flex justify-start items-end";
        typingIndicatorEl.innerHTML =
          '<div class="rounded-2xl rounded-tl-sm border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-4 py-2.5">' +
          '<div class="flex items-center gap-2 text-xs text-black-600 dark:text-black-700">' +
          '<svg class="h-3 w-3 shrink-0 animate-spin text-green-500" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path></svg>' +
          '<span data-typing-label class="italic">' + typingLabel(typingSubstate) + '</span>' +
          '</div></div>';
        var bottom = document.getElementById("chat-bottom");
        if (bottom) container.insertBefore(typingIndicatorEl, bottom);
        else container.appendChild(typingIndicatorEl);
        scrollToBottom();
      }

      function typingLabel(substate) {
        if (!substate) return "thinking…";
        if (substate === "thinking") return "thinking…";
        if (substate === "spawning") return "spawning…";
        return "running " + substate + "…";
      }

      function updateTypingSubstate(substate) {
        typingSubstate = substate || "";
        if (!typingIndicatorEl) return;
        var lbl = typingIndicatorEl.querySelector("[data-typing-label]");
        if (lbl) lbl.textContent = typingLabel(typingSubstate);
      }

      function hideTypingIndicator() {
        if (typingIndicatorEl) { typingIndicatorEl.remove(); typingIndicatorEl = null; }
      }

      function handleAgentEvent(ev) {
        if (ev.type === "lifecycle") {
          applyLifecycle(ev.lifecycle, ev.pid || 0, ev.data || "", ev.at || 0);
          // Show typing indicator (its own avatar + bubble) for any
          // in-flight lifecycle. Do NOT call ensurePendingTurn here —
          // that adds a second empty avatar above the typing indicator
          // since the assistant turn has no text yet. The pending turn
          // is materialised by appendDelta when the first text_delta
          // actually arrives.
          if (ev.lifecycle === "spawning" || ev.lifecycle === "working") {
            var sub = ev.lifecycle === "spawning" ? "spawning" : (ev.data || "");
            updateTypingSubstate(sub);
            // Delay so in-flight event replay (thinking/tool cards) fires
            // first — those call hideTypingIndicator then showTypingIndicator
            // themselves. If nothing replayed, this fallback ensures the
            // indicator appears.
            setTimeout(function() {
              if (!typingIndicatorEl) showTypingIndicator();
            }, 0);
          } else if (ev.lifecycle === "idle" || ev.lifecycle === "killed") {
            hideTypingIndicator();
          }
          return;
        }
        if (ev.type === "approval_request") {
          showApprovalModal(JSON.parse(ev.data));
          return;
        }
        if (ev.type === "approval_resolved") {
          hideApprovalModal(JSON.parse(ev.data));
          refreshApprovedPanel();
          return;
        }
        if (ev.type === "ask_user") {
          showAskUserCard(JSON.parse(ev.data));
          return;
        }
        if (ev.type === "ask_user_resolved") {
          hideAskUserCard(JSON.parse(ev.data));
          return;
        }
        if (ev.type === "system_turn") {
          var d = JSON.parse(ev.data || "{}");
          appendSystemTurn(d.text || "", d.steps || []);
          return;
        }
        // Lifecycle (working/idle/spawning/killed) is BE-driven via the
        // "lifecycle" event handled above. These per-event branches only
        // update the visible turn content + the typing-indicator label.
        if (ev.type === "text_delta") {
          hideTypingIndicator();
          appendDelta(ev.data);
        } else if (ev.type === "done") {
          finalizeAssistantTurn();
        } else if (ev.type === "session_start") {
          showTypingIndicator();
        } else if (ev.type === "error") {
          hideTypingIndicator();
          finalizeAssistantTurn();
        } else if (ev.type === "thinking") {
          hideTypingIndicator();
          appendThinkingCard(ev.data || "");
          updateTypingSubstate("thinking");
          showTypingIndicator();
        } else if (ev.type === "tool_use") {
          hideTypingIndicator();
          appendToolUseCard(ev);
          updateTypingSubstate(ev.tool_name || "tool");
          showTypingIndicator();
        } else if (ev.type === "tool_result") {
          hideTypingIndicator();
          appendToolResultCard(ev);
          updateTypingSubstate("");
          if (!turnHasText) showTypingIndicator();
        }
      }

      // ── System turn helper ────────────────────────────────────────────

      function appendSystemTurn(text, steps) {
        var container = document.querySelector("[data-turns]");
        if (!container) return;
        var stepsHtml = "";
        if (steps && steps.length) {
          stepsHtml = '<div class="flex flex-col items-center gap-0.5 mt-0.5">';
          for (var i = 0; i < steps.length; i++) {
            stepsHtml +=
              '<div class="inline-flex items-center gap-1 text-xs text-black-600 dark:text-black-500">' +
              '<svg viewBox="0 0 12 12" class="h-2.5 w-2.5 text-green-500" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 6l3 3 5-5" stroke-linecap="round" stroke-linejoin="round"/></svg>' +
              escapeHtml(steps[i]) +
              '</div>';
          }
          stepsHtml += '</div>';
        }
        var el = document.createElement("div");
        el.className = "flex justify-center py-1";
        el.innerHTML =
          '<div class="flex flex-col items-center gap-1">' +
          '<div class="inline-flex items-center gap-1.5 rounded-full border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-3 py-1 text-xs text-black-700 dark:text-black-600">' +
          '<svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="6" cy="6" r="4.5"></circle><path d="M6 4v2l1 1" stroke-linecap="round"></path></svg>' +
          escapeHtml(text) +
          '</div>' +
          stepsHtml +
          '</div>';
        var bottom = document.getElementById("chat-bottom");
        if (bottom) container.insertBefore(el, bottom);
        else container.appendChild(el);
        el.scrollIntoView({ behavior: "smooth", block: "nearest" });
      }

      // ── Event card helpers ────────────────────────────────────────────

      // Returns the outer turn body div (data-turn-events), creating the
      // bubble if it doesn't exist yet.
      function ensurePendingTurn() {
        var container = document.querySelector("[data-turns]");
        if (!container) return null;
        if (!pendingTurnEl) {
          pendingTurnEl = document.createElement("div");
          pendingTurnEl.className = "flex justify-start group";
          var label = currentTurnLabel();
          var labelHtml = label
            ? '<span class="text-xs font-medium text-black-800 dark:text-black-600">' + escapeHtml(label) + '</span>'
            : '';
          pendingTurnEl.innerHTML =
            '<div class="flex flex-col gap-1.5 max-w-[92%] min-w-0" data-turn-events>' +
            labelHtml +
            '</div>';
          var bottom = document.getElementById("chat-bottom");
          if (bottom) container.insertBefore(pendingTurnEl, bottom);
          else container.appendChild(pendingTurnEl);
        }
        return pendingTurnEl.querySelector("[data-turn-events]");
      }

      // currentTurnLabel mirrors view.formatTurnLabel: "{agent}.{providerName}"
      // with default agent "main" hidden and provider compacted to its
      // name half. Reads live attrs so a provider/agent swap before
      // first delta arrives still produces the right label.
      function currentTurnLabel() {
        var root = document.querySelector("[data-session-id]");
        if (!root) return "";
        var agent = (root.getAttribute("data-active-agent") || "").trim();
        var provider = (root.getAttribute("data-active-provider") || "").trim();
        var name = provider;
        var slash = provider.lastIndexOf("/");
        if (slash >= 0) name = provider.slice(slash + 1);
        if (!agent || agent === "main") return name;
        if (!name) return agent;
        return agent + "." + name;
      }

      // Returns data-trace-wrap, creating the trace section (toggle btn +
      // wrap div) inside the turn body if it doesn't exist yet.
      function ensureTraceWrap() {
        var body = ensurePendingTurn();
        if (!body) return null;
        var wrap = body.querySelector("[data-trace-wrap]");
        if (!wrap) {
          var traceSection = document.createElement("div");
          traceSection.className = "flex flex-col gap-1";
          traceSection.innerHTML =
            '<button type="button" data-trace-toggle data-loading="1" ' +
            'onclick="window.wickToggleTrace(this);" ' +
            'class="self-start flex items-center gap-1.5 text-[11px] text-black-500 dark:text-black-600 hover:text-black-700 dark:hover:text-black-500 transition-colors py-0.5">' +
            '<svg data-trace-spin viewBox="0 0 16 16" class="h-3 w-3 shrink-0 animate-spin" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path></svg>' +
            '<svg data-trace-icon viewBox="0 0 16 16" class="h-3 w-3 shrink-0 hidden" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="8" cy="8" r="5.5"></circle><path d="M6 8h4M8 6v4" stroke-linecap="round"></path></svg>' +
            '<span data-trace-label class="italic">working…</span>' +
            '<svg data-chevron viewBox="0 0 16 16" class="h-3 w-3 shrink-0 transition-transform" style="transform:rotate(-90deg)" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"></path></svg>' +
            '</button>' +
            '<div data-trace-wrap class="hidden gap-1"></div>' +
            '<button type="button" data-trace-toggle-bottom ' +
            'onclick="window.wickToggleTrace(this);" ' +
            'class="hidden self-start items-center gap-1.5 text-[11px] text-black-500 dark:text-black-600 hover:text-black-700 dark:hover:text-black-500 transition-colors py-0.5">' +
            '<svg data-trace-spin-bottom viewBox="0 0 16 16" class="h-3 w-3 shrink-0 animate-spin hidden" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path></svg>' +
            '<svg data-trace-icon-bottom viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="8" cy="8" r="5.5"></circle><path d="M6 8h4M8 6v4" stroke-linecap="round"></path></svg>' +
            '<span data-trace-label-bottom>hide trace</span>' +
            '<svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"></path></svg>' +
            '</button>';
          body.appendChild(traceSection);
          wrap = traceSection.querySelector("[data-trace-wrap]");
        }
        return wrap;
      }

      function appendThinkingCard(text) {
        var body = ensureTraceWrap();
        if (!body) return;
        var card = document.createElement("div");
        card.className = "rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs";
        card.innerHTML =
          '<button type="button" onclick="var b=this.parentElement.querySelector(\'[data-thinking-body]\');b.classList.toggle(\'hidden\');this.querySelector(\'[data-chevron]\').style.transform=b.classList.contains(\'hidden\')?\'rotate(-90deg)\':\'\';" ' +
          'class="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-white-200 dark:hover:bg-navy-800 transition-colors text-black-600 dark:text-black-700">' +
          '<svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="8" cy="8" r="5.5"></circle><path d="M8 5.5v3l1.5 1.5" stroke-linecap="round" stroke-linejoin="round"></path></svg>' +
          '<span class="italic">thinking</span>' +
          '<svg data-chevron viewBox="0 0 16 16" class="ml-auto h-3 w-3 shrink-0 text-black-500 transition-transform" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"></path></svg>' +
          '</button>' +
          '<div data-thinking-body class="border-t border-white-300 dark:border-navy-600 px-3 py-2 italic text-black-600 dark:text-black-700 leading-relaxed break-words">' +
          esc(text) +
          '</div>';
        body.appendChild(card);
        scrollToBottom();
      }

      function fmtTime(ms) {
        if (!ms) return "";
        var d = new Date(ms);
        return d.toTimeString().slice(0, 8); // HH:MM:SS
      }

      function fmtDuration(startMs, endMs) {
        if (!startMs || !endMs || endMs <= startMs) return "";
        var s = Math.round((endMs - startMs) / 1000);
        if (s < 60) return s + "s";
        return Math.floor(s / 60) + "m " + (s % 60) + "s";
      }

      function appendToolUseCard(ev) {
        var body = ensureTraceWrap();
        if (!body) return;
        var toolName = ev.tool_name || ev.data || "tool";
        var inputRaw = ev.tool_input || "";
        var prettyInput = "";
        if (inputRaw) {
          try { prettyInput = JSON.stringify(JSON.parse(inputRaw), null, 2); }
          catch (_) { prettyInput = inputRaw; }
        }
        var startLabel = ev.at ? fmtTime(ev.at) : "";
        var card = document.createElement("div");
        card.className = "rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs";
        card.setAttribute("data-tool-card", ev.tool_use_id || "");
        if (ev.at) card.setAttribute("data-tool-start-ms", ev.at);
        card.innerHTML =
          '<button type="button" onclick="var b=this.parentElement.querySelector(\'[data-tool-body]\');b.classList.toggle(\'hidden\');this.querySelector(\'[data-chevron]\').style.transform=b.classList.contains(\'hidden\')?\'rotate(-90deg)\':\'\';" ' +
          'class="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">' +
          '<svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-green-600 dark:text-green-400" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 4h4v8H2zM10 4h4v8h-4z" stroke-linejoin="round"></path><path d="M6 8h4" stroke-linecap="round"></path></svg>' +
          '<span class="font-mono font-medium text-black-900 dark:text-white-100">' + esc(toolName) + '</span>' +
          (startLabel ? '<span class="font-mono text-[10px] text-black-500 dark:text-black-600">' + startLabel + '</span>' : '') +
          '<span data-tool-duration class="font-mono text-[10px] text-black-500 dark:text-black-600"></span>' +
          '<span class="ml-auto text-[10px] text-black-500 dark:text-black-600 uppercase tracking-wide shrink-0">tool call</span>' +
          '<svg data-chevron viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-black-500 transition-transform" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"></path></svg>' +
          '</button>' +
          '<div data-tool-body class="border-t border-white-300 dark:border-navy-600">' +
          (prettyInput
            ? '<pre class="overflow-x-auto px-3 py-2 font-mono text-[11px] text-black-900 dark:text-white-100 leading-relaxed whitespace-pre-wrap break-words">' + esc(prettyInput) + '</pre>'
            : '<p class="px-3 py-2 text-black-500 dark:text-black-600 italic">no input</p>') +
          '</div>';
        if (ev.tool_use_id) pendingToolCards[ev.tool_use_id] = card;
        // Snapshot replay: end_at already known (tool finished before refresh).
        if (ev.end_at && ev.at) {
          var dur = fmtDuration(ev.at, ev.end_at);
          var durEl = card.querySelector("[data-tool-duration]");
          if (durEl && dur) durEl.textContent = "· " + dur;
        }
        body.appendChild(card);
        scrollToBottom();
      }

      function appendToolResultCard(ev) {
        var body = ensureTraceWrap();
        if (!body) return;
        var resultText = ev.data || "";
        var isError = ev.is_error === true || ev.is_error === "true";
        // Try to find the matching tool_use card to append inline; else append standalone.
        var parent = ev.tool_use_id ? pendingToolCards[ev.tool_use_id] : null;
        if (parent && ev.at) {
          var startMs = parseInt(parent.getAttribute("data-tool-start-ms") || "0", 10);
          var dur = fmtDuration(startMs, ev.at);
          var durEl = parent.querySelector("[data-tool-duration]");
          if (durEl && dur) durEl.textContent = "· " + dur;
        }
        var resultEl = document.createElement("div");
        if (parent) {
          // Attach result section to the existing tool card.
          resultEl.className = "border-t border-white-300 dark:border-navy-600";
          resultEl.innerHTML =
            '<button type="button" onclick="var b=this.parentElement.querySelector(\'[data-result-body]\');b.classList.toggle(\'hidden\');this.querySelector(\'[data-chevron]\').style.transform=b.classList.contains(\'hidden\')?\'rotate(-90deg)\':\'\';" ' +
            'class="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-white-200 dark:hover:bg-navy-800 transition-colors ' +
            (isError ? 'text-red-600 dark:text-red-400' : 'text-black-600 dark:text-black-700') + '">' +
            '<svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5">' +
            (isError
              ? '<circle cx="8" cy="8" r="5.5"></circle><path d="M8 5v4M8 11v.5" stroke-linecap="round"></path>'
              : '<path d="M3 8l3 3 7-7" stroke-linecap="round" stroke-linejoin="round"></path>') +
            '</svg>' +
            '<span class="text-[10px] uppercase tracking-wide shrink-0">' + (isError ? 'error' : 'result') + '</span>' +
            '<span class="ml-2 truncate font-mono opacity-60">' + esc(resultText.slice(0, 80).replace(/\n/g, " ")) + (resultText.length > 80 ? "…" : "") + '</span>' +
            '<svg data-chevron viewBox="0 0 16 16" class="ml-auto h-3 w-3 shrink-0 text-black-500 transition-transform" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"></path></svg>' +
            '</button>' +
            '<div data-result-body class="border-t border-white-300 dark:border-navy-600">' +
            '<pre class="overflow-x-auto px-3 py-2 font-mono text-[11px] text-black-900 dark:text-white-100 leading-relaxed whitespace-pre-wrap break-words">' + esc(resultText) + '</pre>' +
            '</div>';
          parent.appendChild(resultEl);
          if (ev.tool_use_id) delete pendingToolCards[ev.tool_use_id];
        } else {
          // Standalone result (no matching tool_use card).
          resultEl.className = "rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs";
          resultEl.innerHTML =
            '<div class="flex items-center gap-2 px-3 py-2 ' + (isError ? 'text-red-600 dark:text-red-400' : 'text-black-600 dark:text-black-700') + '">' +
            '<svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5">' +
            (isError
              ? '<circle cx="8" cy="8" r="5.5"></circle><path d="M8 5v4M8 11v.5" stroke-linecap="round"></path>'
              : '<path d="M3 8l3 3 7-7" stroke-linecap="round" stroke-linejoin="round"></path>') +
            '</svg><span class="text-[10px] uppercase tracking-wide">' + (isError ? 'error' : 'result') + '</span></div>' +
            '<pre class="overflow-x-auto px-3 py-2 font-mono text-[11px] text-black-900 dark:text-white-100 leading-relaxed whitespace-pre-wrap break-words border-t border-white-300 dark:border-navy-600">' + esc(resultText) + '</pre>';
          body.appendChild(resultEl);
        }
        scrollToBottom();
      }

      // Returns true when user is already pinned near the bottom of the
      // chat scroll area. Used to skip auto-scroll while streaming so the
      // user can read older messages without being yanked back down.
      function isNearBottom() {
        var panel = document.querySelector("[data-chat-panel]");
        if (panel) {
          var slack = panel.scrollHeight - panel.scrollTop - panel.clientHeight;
          return slack < 80;
        }
        var w = window.innerHeight + window.scrollY;
        return (document.documentElement.scrollHeight - w) < 80;
      }

      function scrollToBottom() {
        if (!isNearBottom()) return;
        var bottom = document.getElementById("chat-bottom");
        if (bottom) { bottom.scrollIntoView({ behavior: "smooth", block: "end" }); return; }
        var container = document.querySelector("[data-turns]");
        if (container) container.scrollTop = container.scrollHeight;
      }

      function appendDelta(text) {
        pendingRawText += text;
        if (!turnHasText) {
          turnHasText = true;
          collapseAllEventCards();
        }
        var body = ensurePendingTurn(); // data-turn-events
        if (!body) return;
        // Find or create the text bubble directly inside data-turn-events.
        var textBubble = body.querySelector("[data-stream-content]");
        if (!textBubble) {
          var wrapper = document.createElement("div");
          wrapper.className = "rounded-2xl rounded-tl-sm border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-4 py-3 text-sm text-black-900 dark:text-white-100 break-words leading-relaxed shadow-sm";
          wrapper.innerHTML = '<div data-stream-content></div>';
          body.appendChild(wrapper);
          textBubble = wrapper.querySelector("[data-stream-content]");
        }
        textBubble.innerHTML = renderMarkdown(pendingRawText);
        scrollToBottom();
      }

      function finalizeAssistantTurn() {
        collapseAllEventCards();
        pendingTurnEl = null;
        pendingRawText = "";
        pendingToolCards = {};
        turnHasText = false;
      }

      // Collapse the trace wrap and update the toggle button label.
      // Called on first text_delta so the trace folds away cleanly.
      function collapseAllEventCards() {
        if (!pendingTurnEl) return;
        var wrap = pendingTurnEl.querySelector("[data-trace-wrap]");
        if (!wrap) return;
        var open = !wrap.classList.contains("hidden");
        var traceSection = wrap.parentElement;
        if (!traceSection) return;
        var btn = traceSection.querySelector("button[data-trace-toggle]");
        if (!btn) return;
        btn.dataset.loading = "0";
        var spin = btn.querySelector("[data-trace-spin]");
        if (spin) spin.classList.add("hidden");
        var icon = btn.querySelector("[data-trace-icon]");
        if (icon) icon.classList.remove("hidden");
        var lbl = btn.querySelector("[data-trace-label]");
        if (lbl) { lbl.classList.remove("italic"); lbl.textContent = open ? "hide trace" : "show trace"; }
        var bottom = traceSection.querySelector("button[data-trace-toggle-bottom]");
        if (bottom) {
          if (open) { bottom.classList.remove("hidden"); bottom.classList.add("flex"); }
          else { bottom.classList.add("hidden"); bottom.classList.remove("flex"); }
          var bspin = bottom.querySelector("[data-trace-spin-bottom]");
          if (bspin) bspin.classList.add("hidden");
          var bicon = bottom.querySelector("[data-trace-icon-bottom]");
          if (bicon) bicon.classList.remove("hidden");
          var blbl = bottom.querySelector("[data-trace-label-bottom]");
          if (blbl) blbl.textContent = "hide trace";
        }
      }
    }

    // ── Lifecycle badges (countdown ring + colour swap on SSE) ────────
    // Pages can render many badges (sessions list table, Recent Spawns
    // table, session detail header). All of them share the same
    // lifecycle vocabulary; a single render pass keeps them consistent.
    var primaryBadge = document.querySelector("[data-session-id] [data-lifecycle-badge]");

    var BADGE_CLASS_MAP = {
      spawning: ["border-amber-300","dark:border-amber-700","bg-amber-50","dark:bg-amber-900/20","text-amber-700","dark:text-amber-300"],
      working:  ["border-green-300","dark:border-green-700","bg-green-50","dark:bg-green-900/20","text-green-700","dark:text-green-300"],
      idle:     ["border-blue-300","dark:border-blue-700","bg-blue-50","dark:bg-blue-900/20","text-blue-700","dark:text-blue-300"],
      killed:   ["border-red-300","dark:border-red-700","bg-red-50","dark:bg-red-900/20","text-red-700","dark:text-red-300"],
    };
    var ALL_BADGE_CLASSES = [].concat(
      BADGE_CLASS_MAP.spawning, BADGE_CLASS_MAP.working,
      BADGE_CLASS_MAP.idle, BADGE_CLASS_MAP.killed
    );
    // 2π·r where r=6 in the SVG viewBox. JS sets stroke-dashoffset to
    // shrink the arc as the idle countdown burns down.
    var RING_CIRCUMFERENCE = 37.7;

    function setBadgeLifecycle(badge, lifecycle, pid, atMs) {
      // "killed" is a transient BE signal — the subprocess died, but we
      // don't want to show a red "killed" pill forever. Render it as an
      // empty placeholder badge instead (same as fresh load with no pool
      // entry). Lifecycle history is visible in the spawn log if needed.
      var visual = lifecycle === "killed" ? "" : lifecycle;
      badge.dataset.lifecycle = visual;
      if (pid > 0) badge.dataset.pid = String(pid);
      if (lifecycle === "killed") badge.dataset.pid = "0";
      if (visual === "idle" || visual === "working") {
        // Trust the BE-supplied timestamp (snapshot replay on refresh
        // carries the actual LastActive); fall back to now() for live
        // transitions where the BE hasn't attached an At field.
        badge.dataset.lastActiveMs = String(atMs > 0 ? atMs : Date.now());
      }
      ALL_BADGE_CLASSES.forEach(function (c) { badge.classList.remove(c); });
      (BADGE_CLASS_MAP[visual] || []).forEach(function (c) { badge.classList.add(c); });

      var label = badge.querySelector("[data-lifecycle-label]");
      if (label) label.textContent = visual || "—";

      var countdown = badge.querySelector("[data-lifecycle-countdown]");
      if (countdown && visual !== "idle") countdown.textContent = "";

      paintRing(badge, visual);
    }

    function paintRing(badge, lifecycle) {
      var svg = badge.querySelector("[data-lifecycle-ring]");
      if (!svg) return;
      var arc = svg.querySelector("[data-lifecycle-arc]");
      var centre = svg.querySelector("[data-lifecycle-centre]");
      svg.classList.remove("lifecycle-svg-spin");
      if (centre) centre.classList.remove("lifecycle-centre-pulse");
      if (lifecycle === "spawning") {
        // Indeterminate spinner: the arc is a 25% chord that rotates.
        if (arc) arc.setAttribute("stroke-dashoffset", String(RING_CIRCUMFERENCE * 0.75));
        svg.classList.add("lifecycle-svg-spin");
        if (centre) centre.setAttribute("r", "0");
      } else if (lifecycle === "working") {
        if (arc) arc.setAttribute("stroke-dashoffset", "0");
        if (centre) {
          centre.setAttribute("r", "2.5");
          centre.classList.add("lifecycle-centre-pulse");
        }
      } else if (lifecycle === "idle") {
        // Real value gets written by the tick below; default to full
        // until the countdown loop has data.
        if (arc) arc.setAttribute("stroke-dashoffset", "0");
        if (centre) centre.setAttribute("r", "1.5");
      } else if (lifecycle === "killed") {
        if (arc) arc.setAttribute("stroke-dashoffset", String(RING_CIRCUMFERENCE));
        if (centre) centre.setAttribute("r", "0");
      } else {
        if (arc) arc.setAttribute("stroke-dashoffset", String(RING_CIRCUMFERENCE));
        if (centre) centre.setAttribute("r", "1");
      }
    }

    function applyLifecycle(lifecycle, pid, substate, atMs) {
      // Session detail SSE only updates its own badge — list pages
      // have their own row each tied to a different session id, and
      // wiring them all to one EventSource would require per-row
      // subscribers (out of scope here).
      if (!primaryBadge) return;
      setBadgeLifecycle(primaryBadge, lifecycle, pid, atMs || 0);
      var sub = primaryBadge.querySelector("[data-lifecycle-substate]");
      if (sub) {
        var label = substate || "";
        sub.textContent = label ? "· " + label : "";
      }
    }

    // Initial paint for every badge on the page (list rows, spawn
    // rows, detail header). The server already set data-lifecycle;
    // this pass reflects it visually (ring + colours).
    document.querySelectorAll("[data-lifecycle-badge]").forEach(function (badge) {
      paintRing(badge, badge.dataset.lifecycle || "");
    });

    // 1-second countdown tick — sweeps every badge on the page so
    // sessions list / spawns list / detail all render the same shrink
    // animation without per-row subscribers.
    setInterval(function () {
      document.querySelectorAll('[data-lifecycle-badge][data-lifecycle="idle"]').forEach(function (badge) {
        var idleTimeout = parseInt(badge.dataset.idleTimeoutMs || "0", 10);
        var lastActive = parseInt(badge.dataset.lastActiveMs || "0", 10);
        var countdown = badge.querySelector("[data-lifecycle-countdown]");
        var arc = badge.querySelector("[data-lifecycle-arc]");
        if (!idleTimeout || !lastActive) return;
        var remaining = Math.max(0, lastActive + idleTimeout - Date.now());
        var ratio = remaining / idleTimeout; // 1 → full, 0 → empty
        if (arc) {
          var offset = RING_CIRCUMFERENCE * (1 - ratio);
          arc.setAttribute("stroke-dashoffset", String(offset.toFixed(2)));
        }
        if (countdown) {
          var s = Math.ceil(remaining / 1000);
          countdown.textContent = s > 0 ? "kill in " + s + "s" : "0s";
        }
      });
    }, 1000);

    // ── Clickable rows ────────────────────────────────────────────────
    // Any element with [data-row-link] navigates on click. Children
    // marked [data-row-action] (kebab menu, inline buttons) opt out so
    // opening a dropdown or hitting a button doesn't also navigate.
    document.addEventListener("click", function (e) {
      if (e.target.closest("[data-row-action]")) return;
      // Native link/button inside the row should win — let it.
      if (e.target.closest("a, button, summary, input, select, textarea, label")) return;
      var row = e.target.closest("[data-row-link]");
      if (!row) return;
      var href = row.dataset.rowLink;
      if (!href) return;
      // Middle-click / cmd-click open in new tab.
      if (e.metaKey || e.ctrlKey || e.button === 1) {
        window.open(href, "_blank");
        return;
      }
      window.location.href = href;
    });

    // Auto-close any open kebab menu when the user clicks elsewhere
    // — <details> stays open by default, which leaves stale dropdowns
    // floating after navigation aborts or after picking an action.
    document.addEventListener("click", function (e) {
      document.querySelectorAll("details[data-row-action][open]").forEach(function (d) {
        if (!d.contains(e.target)) d.removeAttribute("open");
      });
    });

    // ── Inline confirm popover ────────────────────────────────────────
    // confirmAt(anchor, msg, opts) opens a small Tailwind popover next
    // to anchor and resolves true on Confirm, false on Cancel /
    // outside-click / Esc. Replaces window.confirm so confirms blend
    // with the rest of the design system instead of using the OS
    // dialog. Only one popover exists at a time — opening a second
    // closes the first.
    var openConfirmPopover = null;
    function confirmAt(anchor, message, opts) {
      opts = opts || {};
      return new Promise(function (resolve) {
        if (openConfirmPopover) openConfirmPopover.dismiss(false);
        var pop = document.createElement("div");
        pop.className =
          "fixed z-50 w-56 rounded-lg border border-white-300 dark:border-navy-600 " +
          "bg-white-100 dark:bg-navy-700 shadow-lg p-3 space-y-2 text-sm";
        pop.setAttribute("role", "dialog");
        pop.setAttribute("data-row-action", "");
        pop.innerHTML =
          '<p class="text-xs text-black-800 dark:text-black-600">' + escapeHtml(message) + "</p>" +
          '<div class="flex justify-end gap-2">' +
            '<button type="button" data-cancel ' +
              'class="rounded-md border border-white-400 dark:border-navy-600 px-2.5 py-1 text-xs ' +
              'text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">' +
              (opts.cancelLabel || "Cancel") + "</button>" +
            '<button type="button" data-confirm autofocus ' +
              'class="rounded-md bg-neg-400 px-2.5 py-1 text-xs font-medium text-white-100 ' +
              'hover:bg-neg-300 active:bg-neg-300 transition-colors">' +
              (opts.confirmLabel || "Confirm") + "</button>" +
          "</div>";
        document.body.appendChild(pop);
        positionPopover(pop, anchor);

        var settled = false;
        function dismiss(ok) {
          if (settled) return;
          settled = true;
          pop.remove();
          document.removeEventListener("click", onOutside, true);
          document.removeEventListener("keydown", onKey);
          window.removeEventListener("resize", onResize);
          window.removeEventListener("scroll", onResize, true);
          openConfirmPopover = null;
          resolve(ok);
        }
        function onOutside(e) {
          if (pop.contains(e.target) || e.target === anchor) return;
          dismiss(false);
        }
        function onKey(e) {
          if (e.key === "Escape") dismiss(false);
          if (e.key === "Enter") dismiss(true);
        }
        function onResize() { positionPopover(pop, anchor); }

        pop.querySelector("[data-confirm]").addEventListener("click", function () { dismiss(true); });
        pop.querySelector("[data-cancel]").addEventListener("click", function () { dismiss(false); });
        // Defer outside listener so the click that opened us doesn't
        // immediately close us.
        setTimeout(function () {
          document.addEventListener("click", onOutside, true);
        }, 0);
        document.addEventListener("keydown", onKey);
        window.addEventListener("resize", onResize);
        window.addEventListener("scroll", onResize, true);
        pop.querySelector("[data-confirm]").focus();

        openConfirmPopover = { dismiss: dismiss };
      });
    }

    function positionPopover(pop, anchor) {
      var r = anchor.getBoundingClientRect();
      var pr = pop.getBoundingClientRect();
      // Prefer below + right-aligned to anchor; flip up if below would
      // overflow viewport.
      var top = r.bottom + 6;
      if (top + pr.height > window.innerHeight - 8) {
        top = Math.max(8, r.top - pr.height - 6);
      }
      var left = r.right - pr.width;
      if (left < 8) left = 8;
      if (left + pr.width > window.innerWidth - 8) {
        left = window.innerWidth - pr.width - 8;
      }
      pop.style.top = top + "px";
      pop.style.left = left + "px";
    }

    function escapeHtml(s) {
      return String(s).replace(/[&<>"']/g, function (c) {
        return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
      });
    }

    // ── Attachments (paperclip, paste, drag-drop) ─────────────────────
    // Wire any [data-composer-drop] container with the matching
    // [data-attach-input], [data-attach-toggle], [data-attach-chips]
    // children. Files are mirrored into the hidden <input>.files via a
    // DataTransfer so native form submission picks them up untouched —
    // the session-detail composer's fetch handler also reads
    // input.files when building its FormData.
    var MAX_FILES = 5;
    var MAX_FILE_BYTES = 25 * 1024 * 1024;
    function humanSize(n) {
      if (n < 1024) return n + "B";
      if (n < 1024 * 1024) return (n / 1024).toFixed(1) + "KB";
      return (n / 1024 / 1024).toFixed(2) + "MB";
    }
    function wickInitAttachments(root) {
      var input = root.querySelector("[data-attach-input]");
      var chips = root.querySelector("[data-attach-chips]");
      var toggle = root.querySelector("[data-attach-toggle]");
      if (!input || !chips) return null;
      var pending = []; // {file, key, previewURL?}
      function rerender() {
        chips.innerHTML = "";
        if (pending.length === 0) {
          chips.classList.add("hidden");
          chips.classList.remove("flex");
        } else {
          chips.classList.remove("hidden");
          chips.classList.add("flex");
        }
        pending.forEach(function (item, idx) {
          var chip = document.createElement("div");
          chip.className =
            "relative inline-flex items-center gap-2 rounded-xl border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-700 pl-1.5 pr-7 py-1 text-xs text-black-900 dark:text-white-100 max-w-[200px]";
          var isImg = item.file.type && item.file.type.indexOf("image/") === 0;
          if (isImg) {
            var img = document.createElement("img");
            if (!item.previewURL) item.previewURL = URL.createObjectURL(item.file);
            img.src = item.previewURL;
            img.className = "h-7 w-7 rounded-md object-cover shrink-0";
            chip.appendChild(img);
          } else {
            var ic = document.createElement("span");
            ic.className = "inline-flex h-7 w-7 items-center justify-center rounded-md bg-white-300 dark:bg-navy-800 shrink-0 text-green-500";
            ic.innerHTML = '<svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M9 2H4a1 1 0 00-1 1v10a1 1 0 001 1h8a1 1 0 001-1V6L9 2z" stroke-linejoin="round"></path><path d="M9 2v4h4" stroke-linejoin="round"></path></svg>';
            chip.appendChild(ic);
          }
          var meta = document.createElement("div");
          meta.className = "flex flex-col min-w-0";
          var name = document.createElement("span");
          name.className = "truncate font-medium";
          name.textContent = item.file.name;
          var size = document.createElement("span");
          size.className = "text-[10px] text-black-600 dark:text-black-700";
          size.textContent = humanSize(item.file.size);
          meta.appendChild(name);
          meta.appendChild(size);
          chip.appendChild(meta);
          var rm = document.createElement("button");
          rm.type = "button";
          rm.className =
            "absolute top-1/2 -translate-y-1/2 right-1.5 h-5 w-5 rounded-full bg-black-300/40 dark:bg-navy-900/60 text-white-100 hover:bg-neg-500 transition-colors flex items-center justify-center";
          rm.innerHTML = '<svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 3l6 6M9 3l-6 6" stroke-linecap="round"></path></svg>';
          rm.addEventListener("click", function () {
            if (item.previewURL) URL.revokeObjectURL(item.previewURL);
            pending.splice(idx, 1);
            sync();
            rerender();
          });
          chip.appendChild(rm);
          chips.appendChild(chip);
        });
      }
      function sync() {
        // Mirror pending list into input.files so native form submit
        // picks up paste/drag-drop additions and removals.
        try {
          var dt = new DataTransfer();
          pending.forEach(function (it) { dt.items.add(it.file); });
          input.files = dt.files;
        } catch (err) {
          // DataTransfer not available — keep going; fetch path uses
          // pending[] directly so the session-detail composer still works.
        }
      }
      function add(files) {
        var arr = Array.from(files || []);
        for (var i = 0; i < arr.length; i++) {
          var f = arr[i];
          if (pending.length >= MAX_FILES) {
            alert("Max " + MAX_FILES + " files per message.");
            break;
          }
          if (f.size > MAX_FILE_BYTES) {
            alert(f.name + ": exceeds " + (MAX_FILE_BYTES / 1024 / 1024) + " MB cap.");
            continue;
          }
          pending.push({ file: f, key: f.name + ":" + f.size + ":" + f.lastModified });
        }
        sync();
        rerender();
      }
      if (toggle) toggle.addEventListener("click", function () { input.click(); });
      input.addEventListener("change", function () {
        // Move freshly-picked files into pending then clear input so the
        // next change event fires even when the same file is reselected.
        var picked = input.files;
        if (picked && picked.length > 0) {
          // Avoid double-counting: input.files was already set by sync()
          // on prior adds. Only add files that aren't already in pending.
          var existing = {};
          pending.forEach(function (it) { existing[it.key] = true; });
          for (var i = 0; i < picked.length; i++) {
            var f = picked[i];
            var k = f.name + ":" + f.size + ":" + f.lastModified;
            if (!existing[k]) {
              if (pending.length >= MAX_FILES) break;
              if (f.size > MAX_FILE_BYTES) continue;
              pending.push({ file: f, key: k });
            }
          }
          sync();
          rerender();
        }
      });
      // Paste — pull image/file items off the clipboard and add them.
      var ta = root.querySelector("textarea");
      if (ta) {
        ta.addEventListener("paste", function (e) {
          if (!e.clipboardData) return;
          var items = e.clipboardData.items || [];
          var picked = [];
          for (var i = 0; i < items.length; i++) {
            if (items[i].kind === "file") {
              var f = items[i].getAsFile();
              if (f) {
                // Paste'd images often have no filename — give them one.
                if (!f.name || f.name === "image.png") {
                  var ext = (f.type && f.type.split("/")[1]) || "png";
                  try { f = new File([f], "pasted-" + Date.now() + "." + ext, { type: f.type }); } catch (e2) {}
                }
                picked.push(f);
              }
            }
          }
          if (picked.length > 0) {
            e.preventDefault();
            add(picked);
          }
        });
      }
      // Drag-drop on the composer box.
      ["dragenter", "dragover"].forEach(function (ev) {
        root.addEventListener(ev, function (e) {
          if (e.dataTransfer && Array.from(e.dataTransfer.types || []).indexOf("Files") !== -1) {
            e.preventDefault();
            root.classList.add("ring-2", "ring-green-500");
          }
        });
      });
      ["dragleave", "drop"].forEach(function (ev) {
        root.addEventListener(ev, function (e) {
          root.classList.remove("ring-2", "ring-green-500");
          if (ev === "drop" && e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length > 0) {
            e.preventDefault();
            add(e.dataTransfer.files);
          }
        });
      });
      return {
        getPending: function () { return pending.map(function (it) { return it.file; }); },
        clear: function () {
          pending.forEach(function (it) { if (it.previewURL) URL.revokeObjectURL(it.previewURL); });
          pending = [];
          sync();
          rerender();
        },
      };
    }
    var composerAttach = null;
    document.querySelectorAll("[data-composer-drop]").forEach(function (root) {
      var inst = wickInitAttachments(root);
      // The session-detail composer is the one wrapping [data-send-form];
      // remember its instance so the fetch submitter can read pending files.
      if (inst && root.querySelector("[data-send-form]")) composerAttach = inst;
    });

    // ── Attachment preview (click chip → open shared modal) ───────────
    // Event delegation on document so dynamically appended optimistic
    // chips work without re-wiring. URL is validated as same-origin
    // (relative path or blob:) before being passed to the modal — guards
    // against javascript: / cross-origin URIs in the unlikely event a
    // chip is injected with a tampered data-attach-url.
    function isSafeAttachURL(u) {
      if (!u) return false;
      if (u.indexOf("blob:") === 0) return true;        // optimistic chips
      if (u.charAt(0) === "/" && u.charAt(1) !== "/") return true; // same-origin path
      try {
        var resolved = new URL(u, window.location.origin);
        return resolved.origin === window.location.origin &&
          (resolved.protocol === "http:" || resolved.protocol === "https:");
      } catch (_) { return false; }
    }
    document.addEventListener("click", function (e) {
      var btn = e.target && e.target.closest && e.target.closest("[data-attach-preview]");
      if (!btn) return;
      e.preventDefault();
      var rawURL = btn.dataset.attachUrl;
      if (!isSafeAttachURL(rawURL)) {
        console.warn("attach preview: rejecting unsafe url", rawURL);
        return;
      }
      var opts = {
        url: rawURL,
        name: btn.dataset.attachName,
        mime: btn.dataset.attachMime,
        size: parseInt(btn.dataset.attachSize, 10) || 0,
      };
      if (window.AgentContext && typeof window.AgentContext.previewExternal === "function") {
        if (window.AgentContext.previewExternal(opts)) return;
      }
      window.open(opts.url, "_blank", "noopener");
    });

    // ── Composer (send message) ───────────────────────────────────────
    var sendForm = document.querySelector("[data-send-form]");
    if (sendForm && sessionID && base) {
      sendForm.addEventListener("submit", function (e) {
        e.preventDefault();
        var textarea = sendForm.querySelector("textarea");
        var text = textarea.value.trim();
        var files = composerAttach ? composerAttach.getPending() : [];
        if (!text && files.length === 0) return;
        var btn = sendForm.querySelector("button[type=submit]");
        textarea.disabled = true;
        if (btn) btn.disabled = true;

        // Optimistically append user bubble before #chat-bottom sentinel
        var container = document.querySelector("[data-turns]");
        if (container) {
          var wrap = document.createElement("div");
          wrap.className = "flex justify-end gap-2 group";
          var col = document.createElement("div");
          col.className = "flex flex-col items-end gap-1 max-w-[80%] min-w-0";
          if (files.length > 0) {
            var attRow = document.createElement("div");
            attRow.className = "flex flex-wrap justify-end gap-1.5 max-w-full";
            files.forEach(function (f) {
              var isImg = f.type && f.type.indexOf("image/") === 0;
              // Blob URL works for the optimistic bubble's lifetime
              // (until the server-rendered turn replaces it). Click
              // delegation in [data-attach-preview] feeds these into
              // window.AgentContext.previewExternal too.
              var blobURL = URL.createObjectURL(f);
              var btn = document.createElement("button");
              btn.type = "button";
              btn.title = f.name;
              btn.dataset.attachPreview = "";
              btn.dataset.attachUrl = blobURL;
              btn.dataset.attachName = f.name;
              btn.dataset.attachMime = f.type || "";
              btn.dataset.attachSize = String(f.size);
              if (isImg) {
                btn.className = "block rounded-xl overflow-hidden border border-white-300 dark:border-navy-600 shadow-sm bg-white-100 dark:bg-navy-800 cursor-pointer";
                var im = document.createElement("img");
                im.src = blobURL;
                im.alt = f.name;
                im.className = "block max-h-56 max-w-[240px] object-contain bg-white-200 dark:bg-navy-900";
                btn.appendChild(im);
              } else {
                btn.className = "inline-flex items-center gap-2 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-xs text-black-900 dark:text-white-100 max-w-[240px] cursor-pointer text-left";
                btn.innerHTML = '<svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-green-500" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M9 2H4a1 1 0 00-1 1v10a1 1 0 001 1h8a1 1 0 001-1V6L9 2z" stroke-linejoin="round"></path><path d="M9 2v4h4" stroke-linejoin="round"></path></svg><span class="truncate">' + escapeHtml(f.name) + '</span><span class="text-black-600 dark:text-black-700 shrink-0">' + humanSize(f.size) + '</span>';
              }
              attRow.appendChild(btn);
            });
            col.appendChild(attRow);
          }
          if (text) {
            var bubble = document.createElement("div");
            bubble.className = "rounded-2xl rounded-tr-sm bg-green-500 px-4 py-3 text-sm text-white-100 whitespace-pre-wrap break-words [overflow-wrap:anywhere] leading-relaxed shadow-sm user-bubble";
            bubble.innerHTML = linkifyText(text);
            col.appendChild(bubble);
          }
          wrap.appendChild(col);
          var bottom = document.getElementById("chat-bottom");
          if (bottom) container.insertBefore(wrap, bottom);
          else container.appendChild(wrap);
          if (window.__wickArmScrollBtn) window.__wickArmScrollBtn();
          requestAnimationFrame(function() {
            var p = document.querySelector("[data-chat-panel]");
            if (p) p.scrollTop = p.scrollHeight;
            var b = document.getElementById("chat-bottom");
            if (b) b.scrollIntoView({ block: "end" });
          });
        }

        var fetchOpts;
        if (files.length > 0) {
          var fd = new FormData();
          fd.append("text", text);
          files.forEach(function (f) { fd.append("files", f, f.name); });
          fetchOpts = { method: "POST", body: fd };
        } else {
          fetchOpts = {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ text: text }),
          };
        }
        fetch(base + "/sessions/" + encodeURIComponent(sessionID) + "/send", fetchOpts)
          .then(function (r) { return r.json(); })
          .then(function () {
            textarea.value = "";
            textarea.style.height = "auto";
            if (composerAttach) composerAttach.clear();
          })
          .catch(function (err) {
            console.error("send failed:", err);
          })
          .finally(function () {
            textarea.disabled = false;
            if (btn) btn.disabled = false;
            // Don't refocus on touch devices — it re-summons the on-screen
            // keyboard right after sending, which feels like an accidental tap.
            if (!window.matchMedia("(pointer: coarse)").matches) textarea.focus();
          });
      });

      // Enter = send, Shift+Enter = newline. On touch devices Enter inserts a
      // newline instead (send via the button), matching chat-app conventions.
      sendForm.querySelector("textarea").addEventListener("keydown", function (e) {
        if (window.matchMedia("(pointer: coarse)").matches) return;
        if (e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.metaKey) {
          e.preventDefault();
          sendForm.dispatchEvent(new Event("submit"));
        }
      });
    }

    // ── Provider switcher (creates new session, redirects) ───────────
    var providerMenuToggle = document.querySelector("[data-provider-menu-toggle]");
    var providerMenu = document.querySelector("[data-provider-menu]");
    if (providerMenuToggle && providerMenu) {
      providerMenuToggle.addEventListener("click", function (e) {
        e.stopPropagation();
        providerMenu.classList.toggle("hidden");
      });
      document.addEventListener("click", function () {
        providerMenu.classList.add("hidden");
      });
      providerMenu.querySelectorAll("[data-provider-choice]").forEach(function (btn) {
        btn.addEventListener("click", function (e) {
          e.stopPropagation();
          providerMenu.classList.add("hidden");
          var prov = btn.dataset.providerChoice;
          var b = base || resolveBase();
          if (!b || !sessionID) return;
          btn.disabled = true;
          fetch(b + "/sessions/" + encodeURIComponent(sessionID) + "/provider", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ provider: prov }),
          }).then(function (r) { return r.json(); }).then(function (res) {
            if (res.redirect) window.location.href = res.redirect;
          }).catch(function () { btn.disabled = false; });
        });
      });
    }

    // ── Project move menu (in-place, kills subprocess) ────────────────
    var projectMenuToggle = document.querySelector("[data-project-menu-toggle]");
    var projectMenu = document.querySelector("[data-project-menu]");
    if (projectMenuToggle && projectMenu) {
      projectMenuToggle.addEventListener("click", function (e) {
        e.stopPropagation();
        projectMenu.classList.toggle("hidden");
      });
      document.addEventListener("click", function () {
        projectMenu.classList.add("hidden");
      });
      projectMenu.querySelectorAll("[data-project-choice]").forEach(function (btn) {
        btn.addEventListener("click", function (e) {
          e.stopPropagation();
          projectMenu.classList.add("hidden");
          var pid = btn.dataset.projectChoice;
          var b = base || resolveBase();
          if (!b || !sessionID) return;
          btn.disabled = true;
          fetch(b + "/sessions/" + encodeURIComponent(sessionID) + "/project", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ project_id: pid }),
          }).then(function (r) { return r.json(); }).then(function (res) {
            if (res.status === "moved") {
              var label = document.querySelector("[data-active-project-label]");
              if (label) label.textContent = btn.textContent.trim() || "default";
            }
          }).catch(function () {}).finally(function () { btn.disabled = false; });
        });
      });
    }

    // ── Composer: Enter submits, Shift+Enter newline (shared card) ────
    document.querySelectorAll("[data-ns-form]").forEach(function (form) {
      var ta = form.querySelector("[data-ns-textarea]");
      if (!ta) return;
      var input = form.querySelector("[data-attach-input]");
      function hasFiles() { return input && input.files && input.files.length > 0; }
      ta.addEventListener("keydown", function (e) {
        if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
          e.preventDefault();
          if (ta.value.trim() || hasFiles()) form.submit();
        }
      });
    });

    // ── Remember <details open> across reloads (e.g. Recent Spawns) ───
    // Server-rendered pagination reloads the page, which resets <details>
    // to collapsed. Persist the open state so paginating keeps it open.
    document.querySelectorAll("details[data-remember-open]").forEach(function (d) {
      var key = "wick:open:" + d.dataset.rememberOpen;
      try {
        if (localStorage.getItem(key) === "1") d.open = true;
      } catch (e) {}
      d.addEventListener("toggle", function () {
        try {
          if (d.open) localStorage.setItem(key, "1");
          else localStorage.removeItem(key);
        } catch (e) {}
      });
    });

    // ── Queue panel: search filter + select-all + bulk kill ───────────
    (function () {
      var panel = document.querySelector("[data-queue-panel]");
      if (!panel) return;
      var b = panel.dataset.base || resolveBase();
      var search = panel.querySelector("[data-queue-search]");
      var checkAll = panel.querySelector("[data-queue-check-all]");
      var killBtn = panel.querySelector("[data-queue-kill-selected]");
      var countEl = panel.querySelector("[data-queue-selected-count]");
      var emptyEl = panel.querySelector("[data-queue-empty]");
      var rows = function () {
        return Array.prototype.slice.call(panel.querySelectorAll("[data-queue-row]"));
      };
      function visibleRows() {
        return rows().filter(function (r) { return r.style.display !== "none"; });
      }
      function checkedRows() {
        return rows().filter(function (r) {
          var c = r.querySelector("[data-queue-check]");
          return c && c.checked && r.style.display !== "none";
        });
      }
      function refresh() {
        var sel = checkedRows();
        if (countEl) countEl.textContent = String(sel.length);
        if (killBtn) killBtn.disabled = sel.length === 0;
        if (checkAll) {
          var vis = visibleRows();
          checkAll.checked = vis.length > 0 && sel.length === vis.length;
          checkAll.indeterminate = sel.length > 0 && sel.length < vis.length;
        }
      }
      if (search) {
        search.addEventListener("input", function () {
          var q = search.value.trim().toLowerCase();
          var shown = 0;
          rows().forEach(function (r) {
            var t = (r.dataset.queueSearchText || "").toLowerCase();
            var ok = !q || t.indexOf(q) >= 0;
            r.style.display = ok ? "" : "none";
            if (!ok) {
              var c = r.querySelector("[data-queue-check]");
              if (c) c.checked = false; // don't kill filtered-out rows
            } else { shown++; }
          });
          if (emptyEl) emptyEl.classList.toggle("hidden", shown > 0);
          refresh();
        });
      }
      if (checkAll) {
        checkAll.addEventListener("change", function () {
          visibleRows().forEach(function (r) {
            var c = r.querySelector("[data-queue-check]");
            if (c) c.checked = checkAll.checked;
          });
          refresh();
        });
      }
      panel.querySelectorAll("[data-queue-check]").forEach(function (c) {
        c.addEventListener("change", refresh);
      });
      if (killBtn) {
        killBtn.addEventListener("click", function () {
          var sel = checkedRows();
          if (!sel.length || !b) return;
          confirmAt(killBtn, "Kill " + sel.length + " queued session(s)? They won't execute.", { confirmLabel: "Kill" }).then(function (ok) {
            if (!ok) return;
            killBtn.disabled = true;
            Promise.all(sel.map(function (r) {
              return fetch(b + "/sessions/" + encodeURIComponent(r.dataset.queueId) + "/dequeue", { method: "POST" }).catch(function () {});
            })).then(function () { location.reload(); });
          });
        });
      }
      refresh();
    })();

    // ── Pin project as personal default (1 per user, UserMetadata) ────
    document.querySelectorAll("[data-pin-project]").forEach(function (btn) {
      btn.addEventListener("click", function (e) {
        e.stopPropagation();
        var id = btn.dataset.pinProject;
        var b = resolveBase();
        if (!id || !b) return;
        btn.disabled = true;
        fetch(b + "/projects/" + encodeURIComponent(id) + "/pin", { method: "POST" })
          .then(function (r) { return r.json(); })
          .then(function () { location.reload(); })
          .catch(function (err) { console.error("pin failed:", err); btn.disabled = false; });
      });
    });

    // ── Drag a session onto a project to move it (mockup ③) ───────────
    // Session rows are draggable; project rows in the sidebar are drop
    // targets. Drop → POST /sessions/{sid}/project → reload.
    (function () {
      var dragSid = null;
      document.querySelectorAll("[data-session-drag]").forEach(function (row) {
        row.addEventListener("dragstart", function (e) {
          dragSid = row.dataset.sessionDrag;
          if (e.dataTransfer) {
            e.dataTransfer.setData("text/plain", dragSid);
            e.dataTransfer.effectAllowed = "move";
          }
        });
        row.addEventListener("dragend", function () { dragSid = null; });
      });
      var dropHi = "ring-2 ring-green-400 ring-inset";
      document.querySelectorAll("[data-project-drop]").forEach(function (target) {
        target.addEventListener("dragover", function (e) {
          e.preventDefault();
          if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
          dropHi.split(" ").forEach(function (c) { target.classList.add(c); });
        });
        target.addEventListener("dragleave", function () {
          dropHi.split(" ").forEach(function (c) { target.classList.remove(c); });
        });
        target.addEventListener("drop", function (e) {
          e.preventDefault();
          dropHi.split(" ").forEach(function (c) { target.classList.remove(c); });
          var sid = (e.dataTransfer && e.dataTransfer.getData("text/plain")) || dragSid;
          var pid = target.dataset.projectDrop;
          var b = resolveBase();
          if (!sid || !b) return;
          fetch(b + "/sessions/" + encodeURIComponent(sid) + "/project", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ project_id: pid }),
          }).then(function () { location.reload(); })
            .catch(function (err) { console.error("move session failed:", err); });
        });
      });
    })();

    // ── Dequeue (kill from queue) ────────────────────────────────────
    document.querySelectorAll("[data-dequeue-session]").forEach(function (btn) {
      btn.addEventListener("click", function (e) {
        e.stopPropagation();
        var id = btn.dataset.dequeueSession;
        confirmAt(btn, "Drop this queued session?", { confirmLabel: "Drop" }).then(function (ok) {
          if (!ok) return;
          var b = base || document.querySelector("[data-base]")?.dataset.base || "/tools/agents";
          fetch(b + "/sessions/" + encodeURIComponent(id) + "/dequeue", { method: "POST" })
            .then(function () { location.reload(); })
            .catch(function (err) { console.error("dequeue failed:", err); });
        });
      });
    });

    // ── Kill agent ────────────────────────────────────────────────────
    document.querySelectorAll("[data-kill]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var id = btn.dataset.kill;
        confirmAt(btn, "Kill the running agent?", { confirmLabel: "Kill" }).then(function (ok) {
          if (!ok) return;
          fetch(base + "/sessions/" + encodeURIComponent(id) + "/kill", { method: "POST" })
            .then(function () { location.reload(); })
            .catch(function (err) { console.error("kill failed:", err); });
        });
      });
    });

    // ── Delete session ─────────────────────────────────────────────────
    document.querySelectorAll("[data-delete-session]").forEach(function (btn) {
      btn.addEventListener("click", function (e) {
        e.stopPropagation();
        var id = btn.dataset.deleteSession;
        if (!confirm("Delete this session? This cannot be undone.")) return;
        var b = base || document.querySelector("[data-base]")?.dataset.base;
        if (!b) return;
        fetch(b + "/sessions/" + encodeURIComponent(id), { method: "DELETE" })
          .then(function () {
            if (window.location.pathname.includes("/sessions/" + id)) {
              window.location.href = b + "/sessions";
            } else {
              location.reload();
            }
          })
          .catch(function (err) { console.error("delete session failed:", err); });
      });
    });

    // ── Delete project ────────────────────────────────────────────────
    document.querySelectorAll("[data-delete-project]").forEach(function (btn) {
      btn.addEventListener("click", function (e) {
        e.stopPropagation();
        var id = btn.dataset.deleteProject;
        confirmAt(btn, "Delete this project? Sessions are kept but unscoped. This cannot be undone.", { confirmLabel: "Delete" }).then(function (ok) {
          if (!ok) return;
          var b = resolveBase();
          if (!b) return;
          var redirect = btn.dataset.redirect;
          fetch(b + "/projects/" + encodeURIComponent(id), { method: "DELETE" })
            .then(function () {
              if (redirect) { window.location.href = redirect; }
              else { location.reload(); }
            })
            .catch(function (err) { console.error("delete project failed:", err); });
        });
      });
    });

    // ── Modal open/close (projects create + edit) ─────────────────────
    document.querySelectorAll("[data-open-modal]").forEach(function (btn) {
      btn.addEventListener("click", function (e) {
        e.stopPropagation();
        var el = document.getElementById(btn.dataset.openModal);
        if (el) el.classList.remove("hidden");
      });
    });
    document.querySelectorAll("[data-close-modal]").forEach(function (btn) {
      btn.addEventListener("click", function (e) {
        e.stopPropagation();
        var el = document.getElementById(btn.dataset.closeModal);
        if (el) el.classList.add("hidden");
      });
    });

    // ── Delete preset ─────────────────────────────────────────────────
    document.querySelectorAll("[data-delete-preset]").forEach(function (btn) {
      btn.addEventListener("click", function (e) {
        e.stopPropagation();
        var name = btn.dataset.deletePreset;
        confirmAt(btn, 'Delete preset "' + name + '"?', { confirmLabel: "Delete" }).then(function (ok) {
          if (!ok) return;
          var b = resolveBase();
          if (!b) return;
          fetch(b + "/presets/" + encodeURIComponent(name), { method: "DELETE" })
            .then(function () { location.reload(); })
            .catch(function (err) { console.error("delete preset failed:", err); });
        });
      });
    });

    // ── Preset save (fetch, no reload) ────────────────────────────────
    var presetForm = document.querySelector("[data-preset-form]");
    if (presetForm) {
      presetForm.addEventListener("submit", function (e) {
        e.preventDefault();
        var data = new URLSearchParams(new FormData(presetForm));
        fetch(presetForm.action, {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: data.toString(),
        })
          .then(function (r) { return r.json(); })
          .then(function (res) {
            if (res.status === "saved") {
              showMsg("preset-save-msg");
            } else {
              showErr("preset-err-msg", res.error || "Save failed.");
            }
          })
          .catch(function () { showErr("preset-err-msg", "Network error."); });
      });
    }

    // ── Project custom path: toggle + folder picker (per form) ────────
    // There may be several project forms on the page (create + one edit
    // modal per project), so wire each independently scoped to its form.
    function isAbsolutePath(p) {
      if (!p) return false;
      if (p.charAt(0) === "/") return true; // POSIX
      if (/^[A-Za-z]:[\\/]/.test(p)) return true; // Windows C:\ D:/
      if (p.indexOf("\\\\") === 0) return true; // UNC
      return false;
    }

    document.querySelectorAll("[data-project-form]").forEach(function (form) {
      var folderRadios = form.querySelectorAll("[data-folder-mode]");
      var customFields = form.querySelector("[data-custom-path-fields]");
      var customInput = form.querySelector("[data-custom-path-input]");
      var folderPicker = form.querySelector("[data-folder-picker]");
      var customErr = form.querySelector("[data-custom-path-error]");

      function customMode() {
        var checked = form.querySelector("[data-folder-mode]:checked");
        return checked && checked.value === "custom";
      }
      function showCustomErr(msg) {
        if (!customErr) return;
        customErr.textContent = msg;
        customErr.classList.remove("hidden");
        if (customInput) {
          customInput.classList.add("border-red-500", "dark:border-red-700");
          customInput.classList.remove("border-white-400", "dark:border-navy-600");
        }
      }
      function clearCustomErr() {
        if (!customErr) return;
        customErr.classList.add("hidden");
        if (customInput) {
          customInput.classList.remove("border-red-500", "dark:border-red-700");
          customInput.classList.add("border-white-400", "dark:border-navy-600");
        }
      }

      // Folder mode radios: show/hide the custom path input block.
      folderRadios.forEach(function (radio) {
        radio.addEventListener("change", function () {
          if (customMode()) {
            if (customFields) customFields.classList.remove("hidden");
          } else {
            if (customFields) customFields.classList.add("hidden");
            clearCustomErr();
          }
        });
      });

      // Native folder picker (webkitdirectory). Browser only exposes
      // File.webkitRelativePath, so grab the first segment as the chosen
      // folder name and let the user complete the absolute parent path.
      if (folderPicker && customInput) {
        folderPicker.addEventListener("change", function () {
          var files = folderPicker.files;
          if (!files || !files.length) return;
          var rel = files[0].webkitRelativePath || files[0].name;
          var folderName = rel.split("/")[0];
          if (!folderName) return;
          var current = customInput.value.trim();
          if (current && (current.endsWith("/") || current.endsWith("\\"))) {
            customInput.value = current + folderName;
          } else {
            customInput.value = folderName;
            showCustomErr(
              "Add the absolute parent path before \"" + folderName + "\" (e.g. D:/code/" + folderName + ")."
            );
          }
          customInput.focus();
          folderPicker.value = "";
          if (isAbsolutePath(customInput.value.trim())) clearCustomErr();
        });
        customInput.addEventListener("input", function () {
          var v = customInput.value.trim();
          if (!v || isAbsolutePath(v)) clearCustomErr();
        });
      }

      // Validate custom path on submit only when custom mode is active.
      form.addEventListener("submit", function (e) {
        if (!customMode() || !customInput) return;
        var v = customInput.value.trim();
        if (!v) {
          e.preventDefault();
          showCustomErr("Custom path is required in Custom mode.");
          customInput.focus();
          return;
        }
        if (!isAbsolutePath(v)) {
          e.preventDefault();
          showCustomErr("Path must be absolute (e.g. D:/code/work or /home/user/work).");
          customInput.focus();
          return;
        }
        clearCustomErr();
      });

      // Unpin a session from this project.
      form.querySelectorAll("[data-unpin-session]").forEach(function (btn) {
        btn.addEventListener("click", function () {
          var pid = form.getAttribute("action").split("/").pop();
          var sid = btn.dataset.unpinSession;
          var b = resolveBase();
          if (!b || !pid) return;
          var fd = new FormData();
          fd.append("unpin", sid);
          fetch(b + "/projects/" + encodeURIComponent(pid), { method: "POST", body: fd })
            .then(function () { location.reload(); })
            .catch(function (err) { console.error("unpin failed:", err); });
        });
      });
    });

    // ── Approval modal (gate Stage 5) ─────────────────────────────────
    // The modal is a fixed overlay rendered once per session detail
    // page (see view/approvals.templ). We populate fields from the
    // SSE `approval_request` payload, run a 25s countdown, and POST
    // the user's pick to /approve. Rehydrate runs on page load so a
    // tab opened mid-pending sees the modal immediately.
    var approvalCountdownTimer = null;
    var approvalCurrent = null;

    function showApprovalModal(req) {
      var modal = document.getElementById("approval-modal");
      if (!modal || !req || !req.id) return;
      approvalCurrent = req;
      modal.querySelector("[data-approval-agent]").textContent = req.agent_name || "—";
      modal.querySelector("[data-approval-tool]").textContent = req.tool || "—";
      modal.querySelector("[data-approval-workdir]").textContent = req.work_dir || "—";
      modal.querySelector("[data-approval-cmd]").textContent = req.cmd || "";
      // Re-enable buttons — they may be disabled from a previous approval click.
      modal.querySelectorAll("[data-approval-decision]").forEach(function (b) {
        b.disabled = false;
      });
      modal.classList.remove("hidden");
      startApprovalCountdown(modal);
    }

    function hideApprovalModal(payload) {
      var modal = document.getElementById("approval-modal");
      if (!modal) return;
      // Only dismiss if the resolved id matches the one currently open
      // (or no payload — defensive close from page hide).
      if (payload && approvalCurrent && payload.id !== approvalCurrent.id) return;
      modal.classList.add("hidden");
      approvalCurrent = null;
      stopApprovalCountdown();
    }

    function startApprovalCountdown(modal) {
      stopApprovalCountdown();
      var el = modal.querySelector("[data-approval-countdown]");
      if (!el) return;
      var remaining = 25;
      el.textContent = remaining + "s";
      approvalCountdownTimer = setInterval(function () {
        remaining -= 1;
        if (remaining <= 0) {
          el.textContent = "0s";
          stopApprovalCountdown();
          sendApprovalDecision("block");
          return;
        }
        el.textContent = remaining + "s";
      }, 1000);
    }

    function stopApprovalCountdown() {
      if (approvalCountdownTimer) {
        clearInterval(approvalCountdownTimer);
        approvalCountdownTimer = null;
      }
    }

    function sendApprovalDecision(decision, btn) {
      if (!approvalCurrent) return;
      var b = resolveBase();
      if (!b || !sessionID) return;
      if (btn) btn.disabled = true;
      fetch(b + "/sessions/" + encodeURIComponent(sessionID) + "/approve", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          id: approvalCurrent.id,
          decision: decision,
          match_key: approvalCurrent.match_key || "",
        }),
      }).then(function (r) {
        if (!r.ok) {
          if (btn) btn.disabled = false;
          r.json().catch(function () { return {}; }).then(function (body) {
            var msg = (body && body.error) ? body.error : ("request failed (" + r.status + ")");
            var modal = document.getElementById("approval-modal");
            if (!modal) return;
            var err = modal.querySelector("[data-approval-error]");
            if (!err) return;
            err.textContent = msg;
            err.classList.remove("hidden");
            setTimeout(function () { err.classList.add("hidden"); }, 5000);
          });
        }
      }).catch(function () {
        if (btn) btn.disabled = false;
      });
    }

    document.addEventListener("click", function (e) {
      // Click on backdrop (outside dialog card) → block.
      var modal = document.getElementById("approval-modal");
      if (modal && !modal.classList.contains("hidden") && e.target === modal) {
        sendApprovalDecision("block");
        return;
      }
      var btn = e.target.closest("[data-approval-decision]");
      if (!btn || !approvalCurrent) return;
      sendApprovalDecision(btn.dataset.approvalDecision, btn);
    });

    // Rehydrate: on session-detail page load, ask the server whether
    // there's already a pending request and pop the modal if so. Also
    // hydrates the Approved-commands panel.
    if (sessionID) {
      refreshApprovedPanel();
    }

    function refreshApprovedPanel() {
      var b = resolveBase();
      if (!b || !sessionID) return;
      fetch(b + "/sessions/" + encodeURIComponent(sessionID) + "/approvals")
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (data) {
          if (!data) return;
          if (Array.isArray(data.pending) && data.pending.length > 0 && !approvalCurrent) {
            showApprovalModal(data.pending[0]);
          }
          renderApprovedPanel(data);
        })
        .catch(function () { /* gate disabled = silent */ });
    }

    function renderApprovedPanel(data) {
      var panel = document.querySelector("[data-approved-panel]");
      if (!panel) return;
      var sessionKeys = data.session_approved || [];
      var alwaysKeys = data.always_approved || [];
      var total = sessionKeys.length + alwaysKeys.length;
      var countEl = panel.querySelector("[data-approved-count]");
      var emptyEl = panel.querySelector("[data-approved-empty]");
      var listEl = panel.querySelector("[data-approved-list]");
      if (countEl) countEl.textContent = total;
      if (total === 0) {
        if (emptyEl) emptyEl.classList.remove("hidden");
        if (listEl) listEl.classList.add("hidden");
        return;
      }
      if (emptyEl) emptyEl.classList.add("hidden");
      if (!listEl) return;
      listEl.classList.remove("hidden");
      listEl.innerHTML = "";
      sessionKeys.forEach(function (k) { listEl.appendChild(approvedRow(k, "session")); });
      alwaysKeys.forEach(function (k) { listEl.appendChild(approvedRow(k, "always")); });
    }

    function approvedRow(matchKey, scope) {
      var li = document.createElement("li");
      li.className = "flex items-center justify-between gap-3 rounded-lg bg-white-200 dark:bg-navy-800 px-3 py-2";
      var label = document.createElement("div");
      label.className = "flex items-center gap-2 min-w-0";
      var badge = document.createElement("span");
      badge.className = scope === "always"
        ? "inline-block rounded bg-green-500 px-2 py-0.5 text-xs font-medium text-white-100"
        : "inline-block rounded border border-green-500 dark:border-green-600 px-2 py-0.5 text-xs font-medium text-green-700 dark:text-green-400";
      badge.textContent = scope === "always" ? "always" : "session";
      var hash = document.createElement("code");
      hash.className = "font-mono text-xs text-black-900 dark:text-white-100 truncate";
      hash.title = matchKey;
      hash.textContent = matchKey.slice(0, 12) + "…";
      label.appendChild(badge);
      label.appendChild(hash);
      var revoke = document.createElement("button");
      revoke.className = "shrink-0 rounded-md border border-red-300 dark:border-red-800 px-2 py-1 text-xs font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors";
      revoke.textContent = "Revoke";
      revoke.addEventListener("click", function () {
        var b = resolveBase();
        if (!b || !sessionID) return;
        revoke.disabled = true;
        fetch(b + "/sessions/" + encodeURIComponent(sessionID) +
              "/approve/" + encodeURIComponent(matchKey) +
              "?scope=" + scope, { method: "DELETE" })
          .then(function () { refreshApprovedPanel(); })
          .catch(function () { revoke.disabled = false; });
      });
      li.appendChild(label);
      li.appendChild(revoke);
      return li;
    }

    // ── ask_user card (gate Stage 6) ──────────────────────────────────
    // The card sits above the composer in the Conversation tab. Only
    // one ask is in flight per session at a time (the MCP tool blocks
    // the agent), so we don't queue — a new ask_user replaces the
    // current card body.
    var askUserCurrent = null;

    function showAskUserCard(req) {
      var card = document.getElementById("ask-user-card");
      if (!card || !req || !req.id) return;
      askUserCurrent = req;
      card.querySelector("[data-ask-question]").textContent = req.question || "";
      var optsBox = card.querySelector("[data-ask-options]");
      optsBox.innerHTML = "";
      (req.options || []).forEach(function (opt) {
        var btn = document.createElement("button");
        btn.type = "button";
        btn.className = "rounded-lg border border-amber-400 dark:border-amber-700 px-3 py-1.5 text-xs font-medium text-amber-700 dark:text-amber-300 hover:bg-amber-100 dark:hover:bg-amber-900/30 transition-colors";
        btn.textContent = opt.label;
        btn.addEventListener("click", function () {
          submitAskAnswer({ id: req.id, value: opt.value });
        });
        optsBox.appendChild(btn);
      });
      var freeForm = card.querySelector("[data-ask-freeform]");
      var textInput = card.querySelector("[data-ask-text]");
      if (req.allow_freeform) {
        freeForm.classList.remove("hidden");
        if (textInput) textInput.value = "";
      } else {
        freeForm.classList.add("hidden");
      }
      card.classList.remove("hidden");
    }

    function hideAskUserCard(payload) {
      var card = document.getElementById("ask-user-card");
      if (!card) return;
      if (payload && askUserCurrent && payload.id !== askUserCurrent.id) return;
      card.classList.add("hidden");
      askUserCurrent = null;
    }

    function submitAskAnswer(body) {
      var b = resolveBase();
      if (!b || !sessionID) return;
      fetch(b + "/sessions/" + encodeURIComponent(sessionID) + "/answer", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      }).then(function () {
        // ask_user_resolved SSE will dismiss the card across all tabs.
      }).catch(function () {});
    }

    document.addEventListener("submit", function (e) {
      var form = e.target.closest("[data-ask-freeform]");
      if (!form || !askUserCurrent) return;
      e.preventDefault();
      var text = form.querySelector("[data-ask-text]").value.trim();
      if (!text) return;
      submitAskAnswer({ id: askUserCurrent.id, text: text });
    });

    // Rehydrate ask_user state on page load.
    if (sessionID) {
      var b0 = resolveBase();
      if (b0) {
        fetch(b0 + "/sessions/" + encodeURIComponent(sessionID) + "/asks")
          .then(function (r) { return r.ok ? r.json() : null; })
          .then(function (data) {
            if (data && Array.isArray(data.pending) && data.pending.length > 0) {
              showAskUserCard(data.pending[0]);
            }
          })
          .catch(function () {});
      }
    }

    // ── Helpers ───────────────────────────────────────────────────────
    function resolveBase() {
      if (base) return base;
      var el = document.querySelector("[data-base]");
      return el ? el.dataset.base : null;
    }

    function showMsg(id) {
      var el = document.getElementById(id);
      if (!el) return;
      el.classList.remove("hidden");
      setTimeout(function () { el.classList.add("hidden"); }, 3000);
    }

    function showErr(id, msg) {
      var el = document.getElementById(id);
      if (!el) return;
      el.textContent = msg;
      el.classList.remove("hidden");
      setTimeout(function () { el.classList.add("hidden"); }, 5000);
    }

    // ── Hook enable/disable button (Providers page) ───────────────────
    // Universal handler for the Enable / Disable per-card buttons.
    // POSTs to the data-action-url, disables the button while the
    // request runs (Enable can take 5–30s because it includes a probe),
    // and reloads the page on completion so the persisted badge + button
    // trio reflect the new state from disk.
    document.addEventListener("click", function (e) {
      var btn = e.target.closest("[data-hook-action]");
      if (!btn) return;
      var url = btn.dataset.actionUrl;
      if (!url) return;
      var origLabel = btn.textContent;
      btn.disabled = true;
      btn.textContent = "Working…";

      fetch(url, { method: "POST" })
        .then(function (r) { return r.json(); })
        .then(function (res) {
          if (res.error && !res.enabled) {
            alert("Hook action failed: " + res.error);
          }
        })
        .catch(function (err) {
          alert("Request failed: " + err);
        })
        .finally(function () {
          // Always reload — persisted state on disk is the source of truth.
          window.location.reload();
        });
    });

    // ── Hook capability check button (Providers page) ──────────────────
    // Single delegated click handler for every per-card Command Gate
    // Test button. Disables the button while the spawn runs (5–30s)
    // and shows a one-line result inline; the persisted status reloads
    // on next page render via the userconfig cache so the badge sticks.
    document.addEventListener("click", function (e) {
      var btn = e.target.closest("[data-check-hook]");
      if (!btn) return;
      var url = btn.dataset.checkUrl;
      if (!url) return;
      var card = btn.closest(".rounded-xl") || btn.parentElement;
      var origLabel = btn.textContent;
      btn.disabled = true;
      btn.textContent = "Testing…";

      fetch(url, { method: "POST" })
        .then(function (r) { return r.json(); })
        .then(function (res) {
          var note = card.querySelector("[data-hook-result]");
          if (!note) {
            note = document.createElement("p");
            note.dataset.hookResult = "";
            note.className = "text-xs mt-2 leading-relaxed";
            card.appendChild(note);
          }
          if (res.verified) {
            note.className = "text-xs mt-2 leading-relaxed text-green-600 dark:text-green-400";
            note.textContent = "✓ hook honored — provider respects deny envelope";
          } else {
            note.className = "text-xs mt-2 leading-relaxed text-red-600 dark:text-red-400";
            note.textContent = "✗ hook NOT honored — " + (res.error || "unknown reason");
          }
          // Reload the page so the persisted badge updates from disk.
          setTimeout(function () { window.location.reload(); }, 1200);
        })
        .catch(function (err) {
          alert("probe failed: " + err);
        })
        .finally(function () {
          btn.disabled = false;
          btn.textContent = origLabel;
        });
    });
  });
})();
