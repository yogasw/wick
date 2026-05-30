// Session "Context" slide-over: browse the agent cwd, read/edit/save/
// delete/download files. Lazy-loads Ace + marked + DOMPurify from CDN
// the first time the user opens preview/edit. Wired to the markup
// emitted by view/context_panel.templ.
(function () {
  "use strict";

  // Vendored locally so we don't depend on a third-party CDN.
  // Resolved against the tool base at runtime via vendorURL().
  var ACE_BASE = "/static/js/vendor/ace";
  var MARKED_PATH = "/static/js/vendor/marked.min.js";
  var DOMPURIFY_PATH = "/static/js/vendor/purify.min.js";

  var panel, backdrop, modal, editorHost, previewHost;
  var aceEditor = null;
  var aceLoading = null;
  var mdLoading = null;
  var allEntries = []; // {path,name,size,mtime,isDir}
  var openDirs = Object.create(null); // dirPath → true
  var currentFile = null; // {path,size,mtime,content,binary}
  var currentMode = "preview"; // "preview" | "edit"

  function $(sel, root) { return (root || document).querySelector(sel); }
  function $$(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }

  function init() {
    panel = $("[data-context-panel]");
    if (!panel) return;
    backdrop = $("[data-context-backdrop]");
    modal = $("[data-context-modal]");
    editorHost = $("[data-context-editor]", modal);
    previewHost = $("[data-context-preview]", modal);

    var openBtn = $("[data-context-open]");
    if (openBtn) openBtn.addEventListener("click", openPanel);

    $("[data-context-close]", panel).addEventListener("click", closePanel);
    if (backdrop) backdrop.addEventListener("click", closePanel);
    $("[data-context-refresh]", panel).addEventListener("click", function () { loadList(true); });
    $("[data-context-search]", panel).addEventListener("input", function () { renderTree(); });
    var newFileBtn = $("[data-context-new-file]", panel);
    if (newFileBtn) newFileBtn.addEventListener("click", function () { createEntry(false, ""); });
    var newDirBtn = $("[data-context-new-dir]", panel);
    if (newDirBtn) newDirBtn.addEventListener("click", function () { createEntry(true, ""); });

    $("[data-context-modal-close]", modal).addEventListener("click", closeModal);
    $("[data-context-modal-download]", modal).addEventListener("click", downloadCurrent);
    $("[data-context-modal-save]", modal).addEventListener("click", saveCurrent);
    $$("[data-context-tab]", modal).forEach(function (btn) {
      btn.addEventListener("click", function () { switchMode(btn.dataset.contextTab); });
    });

    document.addEventListener("keydown", function (e) {
      // Esc: close topmost layer.
      if (e.key === "Escape") {
        if (!modal.classList.contains("hidden")) closeModal();
        else if (!panel.classList.contains("hidden")) closePanel();
        return;
      }
      // Ctrl/Cmd+B: toggle Context panel (skip when typing in inputs).
      if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && (e.key === "b" || e.key === "B")) {
        var t = e.target;
        var tag = t && t.tagName;
        var typing = tag === "INPUT" || tag === "TEXTAREA" || (t && t.isContentEditable);
        if (typing) return;
        e.preventDefault();
        if (panel.classList.contains("hidden")) openPanel();
        else closePanel();
      }
    });

    // Prefetch file count so the FAB badge shows up immediately.
    prefetchCount();
  }

  function prefetchCount() {
    fetch(base() + "/sessions/" + sessionID() + "/files")
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (data) {
        if (!data || !data.files) return;
        var n = data.files.filter(function (f) { return !f.isDir; }).length;
        updateFabBadge(n);
        // Cache cwd on the panel root so openFileByAbsPath works
        // even before the user opens the panel (a markdown link in
        // chat can be clicked while the file tree is still cold).
        if (data.cwd) {
          var cwdEl = $("[data-context-cwd]", panel);
          if (cwdEl && !cwdEl.textContent) cwdEl.textContent = data.cwd;
        }
      })
      .catch(function () {});
  }

  function base() { return panel.dataset.base || ""; }
  function sessionID() { return panel.dataset.sessionId || ""; }

  // ── Panel open/close ───────────────────────────────────────────────
  function openPanel() {
    panel.classList.remove("hidden");
    if (backdrop) backdrop.classList.remove("hidden");
    var fab = $("[data-context-open]");
    if (fab) fab.classList.add("hidden");
    loadList(true);
  }
  function closePanel() {
    panel.classList.add("hidden");
    if (backdrop) backdrop.classList.add("hidden");
    var fab = $("[data-context-open]");
    if (fab) fab.classList.remove("hidden");
  }

  function updateFabBadge(n) {
    var badge = $("[data-context-fab-count]");
    if (!badge) return;
    if (n > 0) {
      badge.textContent = n > 99 ? "99+" : String(n);
      badge.classList.remove("hidden");
      badge.classList.add("inline-flex");
    } else {
      badge.classList.add("hidden");
      badge.classList.remove("inline-flex");
    }
  }

  // ── File tree ──────────────────────────────────────────────────────
  var listAbort = null;
  function loadList(force) {
    var list = $("[data-context-list]", panel);
    var cwd = $("[data-context-cwd]", panel);
    list.innerHTML = '<div class="flex items-center justify-center py-12 text-xs text-black-700 dark:text-black-600">Loading…</div>';
    if (listAbort) listAbort.abort();
    listAbort = ("AbortController" in window) ? new AbortController() : null;
    var url = base() + "/sessions/" + sessionID() + "/files" + (force ? "?t=" + Date.now() : "");
    console.debug("[context] GET", url);
    var timer = setTimeout(function () { if (listAbort) listAbort.abort(); }, 15000);
    fetch(url, { signal: listAbort ? listAbort.signal : undefined })
      .then(function (r) {
        clearTimeout(timer);
        console.debug("[context] status", r.status);
        return r.text().then(function (txt) {
          var data = null;
          try { data = txt ? JSON.parse(txt) : {}; } catch (_) { data = { error: txt || ("HTTP " + r.status) }; }
          return { ok: r.ok, status: r.status, data: data };
        });
      })
      .then(function (res) {
        if (!res.ok) {
          cwd.textContent = "";
          $("[data-context-count]", panel).textContent = "";
          var msg = (res.data && res.data.error) ? res.data.error : ("HTTP " + res.status);
          list.innerHTML = '<div class="px-4 py-8 text-xs text-neg-600 dark:text-neg-400">' + escapeHtml(msg) + "</div>";
          return;
        }
        var data = res.data || {};
        cwd.textContent = data.cwd || "";
        allEntries = data.files || [];
        var files = allEntries.filter(function (f) { return !f.isDir; }).length;
        var dirs = allEntries.length - files;
        $("[data-context-count]", panel).textContent =
          files + " file" + (files === 1 ? "" : "s") +
          (dirs ? " · " + dirs + " folder" + (dirs === 1 ? "" : "s") : "");
        updateFabBadge(files);
        renderTree();
      })
      .catch(function (e) {
        console.error("context list failed", e);
        list.innerHTML = '<div class="px-4 py-8 text-xs text-neg-600 dark:text-neg-400">Failed: ' + escapeHtml(String(e)) + "</div>";
      });
  }

  // Build {path → node} tree from the flat list. node = {entry, children:[]}.
  function buildTree(entries) {
    var byPath = { "": { entry: { path: "", name: "", isDir: true }, children: [] } };
    entries.slice().sort(function (a, b) { return a.path.localeCompare(b.path); }).forEach(function (e) {
      byPath[e.path] = { entry: e, children: [] };
    });
    Object.keys(byPath).forEach(function (p) {
      if (p === "") return;
      var parent = p.indexOf("/") === -1 ? "" : p.slice(0, p.lastIndexOf("/"));
      if (byPath[parent]) byPath[parent].children.push(byPath[p]);
    });
    return byPath[""];
  }

  function matchesFilter(node, q) {
    if (!q) return true;
    if (node.entry.path && node.entry.path.toLowerCase().indexOf(q) !== -1) return true;
    return node.children.some(function (c) { return matchesFilter(c, q); });
  }

  function renderTree() {
    var list = $("[data-context-list]", panel);
    var q = ($("[data-context-search]", panel).value || "").toLowerCase().trim();
    var root = buildTree(allEntries);
    if (!root.children.length) {
      list.innerHTML = '<div class="px-4 py-12 text-center text-xs text-black-700 dark:text-black-600">Empty. Use + to add a file or folder.</div>';
      return;
    }
    var visible = root.children.filter(function (c) { return matchesFilter(c, q); });
    if (!visible.length) {
      list.innerHTML = '<div class="px-4 py-12 text-center text-xs text-black-700 dark:text-black-600">No matches.</div>';
      return;
    }
    // Force-open dirs when searching so matches inside are visible.
    var forceOpen = !!q;
    list.innerHTML = visible.map(function (c) { return renderNode(c, 0, forceOpen); }).join("");
    bindNodeEvents(list);
  }

  function renderNode(node, depth, forceOpen) {
    var e = node.entry;
    var indent = (depth * 14) + 8;
    if (e.isDir) {
      var open = forceOpen || openDirs[e.path];
      var caret = '<svg viewBox="0 0 16 16" class="h-3 w-3 transition-transform ' + (open ? "rotate-90" : "") + '" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>';
      var folder = '<svg viewBox="0 0 16 16" class="h-3.5 w-3.5 text-green-500" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 4a1 1 0 011-1h3l2 2h5a1 1 0 011 1v6a1 1 0 01-1 1H3a1 1 0 01-1-1V4z" stroke-linejoin="round"/></svg>';
      var head = '' +
        '<div class="group relative flex items-center gap-1.5 py-1.5 border-b border-white-300 dark:border-navy-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors" style="padding-left:' + indent + 'px;padding-right:8px;">' +
          '<button type="button" data-context-toggle-dir="' + escapeAttr(e.path) + '" class="flex items-center gap-1.5 min-w-0 flex-1 text-left pr-16">' +
            '<span class="shrink-0 text-black-700 dark:text-black-600">' + caret + '</span>' +
            '<span class="shrink-0">' + folder + '</span>' +
            '<span class="text-xs font-medium text-black-900 dark:text-white-100 truncate">' + escapeHtml(e.name) + '</span>' +
          '</button>' +
          '<div class="hidden group-hover:flex items-center gap-0.5 absolute right-2 top-1/2 -translate-y-1/2 bg-white-200 dark:bg-navy-800 rounded-md shadow-sm">' +
            '<button type="button" data-context-new-here="' + escapeAttr(e.path) + '" title="New file here" class="inline-flex h-6 w-6 items-center justify-center rounded text-black-700 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-600">' +
              '<svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 3v6M3 6h6" stroke-linecap="round"/></svg>' +
            '</button>' +
            '<button type="button" data-context-delete="' + escapeAttr(e.path) + '" title="Delete folder" class="inline-flex h-6 w-6 items-center justify-center rounded text-neg-600 dark:text-neg-400 hover:bg-neg-50 dark:hover:bg-neg-900/20">' +
              '<svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 3h8M4 3V2h4v1M5 5v4M7 5v4M3 3l.5 7h5L9 3" stroke-linecap="round" stroke-linejoin="round"/></svg>' +
            '</button>' +
          '</div>' +
        '</div>';
      if (!open) return head;
      var kids = node.children.map(function (c) { return renderNode(c, depth + 1, forceOpen); }).join("");
      return head + kids;
    }
    // File row
    var icon = iconForFile(e.name);
    return '' +
      '<div class="group relative flex items-center gap-1.5 py-1.5 border-b border-white-300 dark:border-navy-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors" style="padding-left:' + (indent + 18) + 'px;padding-right:8px;">' +
        '<span class="shrink-0 text-black-700 dark:text-black-600">' + icon + '</span>' +
        '<button type="button" data-context-open-file="' + escapeAttr(e.path) + '" class="min-w-0 flex-1 text-left pr-16">' +
          '<div class="text-xs text-black-900 dark:text-white-100 truncate">' + escapeHtml(e.name) + '</div>' +
          '<div class="text-[10px] text-black-700 dark:text-black-600 truncate font-mono">' + formatSize(e.size) + ' · ' + formatTime(e.mtime) + '</div>' +
        '</button>' +
        '<div class="hidden group-hover:flex items-center gap-0.5 absolute right-2 top-1/2 -translate-y-1/2 bg-white-200 dark:bg-navy-800 rounded-md shadow-sm">' +
          '<button type="button" data-context-download="' + escapeAttr(e.path) + '" title="Download" class="inline-flex h-6 w-6 items-center justify-center rounded text-black-700 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-600">' +
            '<svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 2v6m0 0l-2-2m2 2l2-2M3 10h6" stroke-linecap="round" stroke-linejoin="round"/></svg>' +
          '</button>' +
          '<button type="button" data-context-delete="' + escapeAttr(e.path) + '" title="Delete" class="inline-flex h-6 w-6 items-center justify-center rounded text-neg-600 dark:text-neg-400 hover:bg-neg-50 dark:hover:bg-neg-900/20">' +
            '<svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 3h8M4 3V2h4v1M5 5v4M7 5v4M3 3l.5 7h5L9 3" stroke-linecap="round" stroke-linejoin="round"/></svg>' +
          '</button>' +
        '</div>' +
      '</div>';
  }

  function bindNodeEvents(root) {
    $$("[data-context-toggle-dir]", root).forEach(function (btn) {
      btn.addEventListener("click", function () {
        var p = btn.dataset.contextToggleDir;
        openDirs[p] = !openDirs[p];
        renderTree();
      });
    });
    $$("[data-context-open-file]", root).forEach(function (btn) {
      btn.addEventListener("click", function () { openFile(btn.dataset.contextOpenFile); });
    });
    $$("[data-context-download]", root).forEach(function (btn) {
      btn.addEventListener("click", function () { downloadFile(btn.dataset.contextDownload); });
    });
    $$("[data-context-delete]", root).forEach(function (btn) {
      btn.addEventListener("click", function () { deleteFile(btn.dataset.contextDelete); });
    });
    $$("[data-context-new-here]", root).forEach(function (btn) {
      btn.addEventListener("click", function () { createEntry(false, btn.dataset.contextNewHere); });
    });
  }

  // ── Create file / folder ───────────────────────────────────────────
  function createEntry(isDir, parentDir) {
    var name = prompt(isDir ? "New folder name:" : "New file name:");
    if (!name) return;
    name = name.trim();
    if (!name) return;
    if (name.indexOf("..") !== -1 || name.indexOf("/") !== -1 || name.indexOf("\\") !== -1) {
      alert("Invalid name. No slashes, no '..'. To create nested files, open the folder first.");
      return;
    }
    var path = parentDir ? parentDir + "/" + name : name;
    fetch(base() + "/sessions/" + sessionID() + "/files/create", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path: path, isDir: isDir }),
    })
      .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, data: d }; }); })
      .then(function (res) {
        if (!res.ok) { alert(res.data.error || "Create failed"); return; }
        if (parentDir) openDirs[parentDir] = true;
        loadList(true);
      })
      .catch(function (e) { alert("Create failed: " + e); });
  }

  // ── Open / preview ─────────────────────────────────────────────────
  function openFile(path) {
    fetch(base() + "/sessions/" + sessionID() + "/files/read?path=" + encodeURIComponent(path))
      .then(function (r) { return r.json(); })
      .then(function (data) {
        if (data.error) { alert("Read failed: " + data.error); return; }
        currentFile = data;
        currentFile.path = path;
        openModal();
      })
      .catch(function (e) { alert("Read failed: " + e); });
  }

  function openModal() {
    modal.classList.remove("hidden");
    $("[data-context-modal-path]", modal).textContent = currentFile.path;
    var meta = formatSize(currentFile.size) + " · " + formatTime(currentFile.mtime);
    if (currentFile.tooBig) meta += " · too large to preview";
    $("[data-context-modal-meta]", modal).textContent = meta;

    var tabs = $("[data-context-tabs]", modal);
    var saveBtn = $("[data-context-modal-save]", modal);
    // Uploads / external blobs are read-only — disable the Edit tab so
    // the user can't try to /files/save a path that lives outside cwd.
    var editable = !currentFile.binary && !currentFile.tooBig && !currentFile._externalURL;
    if (editable) {
      tabs.classList.remove("hidden");
      tabs.classList.add("flex");
    } else {
      tabs.classList.add("hidden");
      tabs.classList.remove("flex");
    }
    saveBtn.classList.add("hidden");
    $("[data-context-modal-status]", modal).textContent = "";
    switchMode("preview");
  }

  function switchMode(mode) {
    currentMode = mode;
    $$("[data-context-tab]", modal).forEach(function (btn) {
      var active = btn.dataset.contextTab === mode;
      btn.className = "px-2.5 py-1 text-[11px] font-medium rounded-md transition-colors " +
        (active
          ? "bg-white-100 dark:bg-navy-700 text-black-900 dark:text-white-100 shadow-sm"
          : "text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100");
    });
    var saveBtn = $("[data-context-modal-save]", modal);
    if (mode === "edit") {
      previewHost.classList.add("hidden");
      editorHost.classList.remove("hidden");
      saveBtn.classList.remove("hidden");
      saveBtn.classList.add("inline-flex");
      mountEditor();
    } else {
      editorHost.classList.add("hidden");
      previewHost.classList.remove("hidden");
      saveBtn.classList.add("hidden");
      saveBtn.classList.remove("inline-flex");
      renderPreview();
    }
  }

  function renderPreview() {
    var ext = extOf(currentFile.path);
    previewHost.innerHTML = '<div class="flex items-center justify-center py-12 text-xs text-black-700 dark:text-black-600">Loading preview…</div>';

    // Uploads + arbitrary external blobs use _externalURL; in-tree
    // files use the /files/download path.
    var url = currentFile._externalURL ||
      (base() + "/sessions/" + sessionID() + "/files/download?path=" + encodeURIComponent(currentFile.path));

    if (isImage(ext)) {
      previewHost.innerHTML = '<div class="flex items-center justify-center h-full bg-white-200 dark:bg-navy-800 p-4"><img src="' + escapeAttr(url) + '" alt="" class="max-w-full max-h-full object-contain"/></div>';
      return;
    }
    if (ext === "pdf") {
      previewHost.innerHTML = '<iframe src="' + escapeAttr(url) + '" class="w-full h-full border-0" title="PDF preview"></iframe>';
      return;
    }
    if (currentFile.binary || currentFile.tooBig) {
      previewHost.innerHTML = '<div class="flex flex-col items-center justify-center h-full gap-3 px-6 text-center">' +
        '<div class="h-12 w-12 rounded-full bg-white-200 dark:bg-navy-800 flex items-center justify-center">' +
          '<svg viewBox="0 0 24 24" class="h-6 w-6 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8l-6-6z M14 2v6h6" stroke-linejoin="round"/></svg>' +
        '</div>' +
        '<p class="text-sm text-black-900 dark:text-white-100">' + (currentFile.tooBig ? "File too large to preview" : "Binary file") + '</p>' +
        '<p class="text-xs text-black-700 dark:text-black-600">Use download to fetch the raw bytes.</p>' +
      '</div>';
      return;
    }
    if (ext === "md" || ext === "markdown") {
      loadMarked().then(function () {
        var html = window.DOMPurify.sanitize(window.marked.parse(currentFile.content || ""));
        previewHost.innerHTML = '<div class="prose prose-sm dark:prose-invert max-w-none p-6 text-sm text-black-900 dark:text-white-100">' + html + '</div>';
      }).catch(function (e) {
        previewHost.textContent = String(e);
      });
      return;
    }
    if (ext === "html" || ext === "htm") {
      // Sandbox: allow-scripts so the page can run its own JS, but
      // omit allow-same-origin so it stays isolated from the wick app
      // (no cookies, no DOM access to parent).
      var blob = new Blob([currentFile.content || ""], { type: "text/html" });
      var blobURL = URL.createObjectURL(blob);
      previewHost.innerHTML = '<iframe sandbox="allow-scripts" src="' + blobURL + '" class="w-full h-full border-0" title="HTML preview"></iframe>';
      return;
    }
    // Plain text / code → preformatted
    previewHost.innerHTML = '<pre class="m-0 p-4 text-xs font-mono text-black-900 dark:text-white-100 whitespace-pre-wrap break-words"></pre>';
    previewHost.firstChild.textContent = currentFile.content || "";
  }

  // ── Editor (Ace) ───────────────────────────────────────────────────
  function mountEditor() {
    loadAce().then(function () {
      if (!aceEditor) {
        editorHost.innerHTML = '<div id="context-ace" class="w-full h-full"></div>';
        aceEditor = window.ace.edit("context-ace");
        aceEditor.setOptions({ fontSize: "12px", showPrintMargin: false, useWorker: false });
      }
      aceEditor.session.setMode(aceModeFor(currentFile.path));
      aceEditor.session.setUseWrapMode(true);
      aceEditor.setTheme(prefersDark() ? "ace/theme/twilight" : "ace/theme/chrome");
      aceEditor.setValue(currentFile.content || "", -1);
      aceEditor.focus();
    }).catch(function (e) {
      editorHost.innerHTML = '<div class="px-4 py-8 text-xs text-neg-600 dark:text-neg-400">Editor failed to load: ' + escapeHtml(String(e)) + '</div>';
    });
  }

  function saveCurrent() {
    if (!aceEditor) return;
    var body = { path: currentFile.path, content: aceEditor.getValue() };
    var status = $("[data-context-modal-status]", modal);
    status.textContent = "Saving…";
    fetch(base() + "/sessions/" + sessionID() + "/files/save", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    })
      .then(function (r) { return r.json(); })
      .then(function (data) {
        if (data.error) { status.textContent = "Error: " + data.error; return; }
        currentFile.size = data.size;
        currentFile.mtime = data.mtime;
        currentFile.content = body.content;
        status.textContent = "Saved · " + formatSize(data.size) + " · " + formatTime(data.mtime);
        $("[data-context-modal-meta]", modal).textContent = formatSize(data.size) + " · " + formatTime(data.mtime);
        loadList(true);
      })
      .catch(function (e) { status.textContent = "Save failed: " + e; });
  }

  function downloadCurrent() {
    if (!currentFile) return;
    if (currentFile._externalURL) {
      var a = document.createElement("a");
      a.href = currentFile._externalURL;
      a.download = currentFile.path.split("/").pop();
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      return;
    }
    downloadFile(currentFile.path);
  }

  function downloadFile(path) {
    var a = document.createElement("a");
    a.href = base() + "/sessions/" + sessionID() + "/files/download?path=" + encodeURIComponent(path);
    a.download = path.split("/").pop();
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  }

  function deleteFile(path) {
    var entry = allEntries.find(function (e) { return e.path === path; });
    var label = entry && entry.isDir ? "folder " + path + " and its contents" : path;
    if (!confirm("Delete " + label + "?")) return;
    fetch(base() + "/sessions/" + sessionID() + "/files?path=" + encodeURIComponent(path), { method: "DELETE" })
      .then(function (r) { return r.json(); })
      .then(function (data) {
        if (data.error) { alert("Delete failed: " + data.error); return; }
        loadList(true);
      })
      .catch(function (e) { alert("Delete failed: " + e); });
  }

  function closeModal() {
    modal.classList.add("hidden");
    currentFile = null;
  }

  // ── CDN loaders ────────────────────────────────────────────────────
  function loadScript(src) {
    return new Promise(function (resolve, reject) {
      var existing = document.querySelector('script[data-src="' + src + '"]');
      if (existing && existing.dataset.loaded === "1") { resolve(); return; }
      var s = existing || document.createElement("script");
      if (!existing) {
        s.src = src; s.async = true; s.dataset.src = src;
        document.head.appendChild(s);
      }
      s.addEventListener("load", function () { s.dataset.loaded = "1"; resolve(); });
      s.addEventListener("error", function () { reject(new Error("load " + src)); });
    });
  }

  function vendorURL(p) { return base() + p; }

  function loadAce() {
    if (aceLoading) return aceLoading;
    var aceBase = vendorURL(ACE_BASE);
    aceLoading = loadScript(aceBase + "/ace.js").then(function () {
      window.ace.config.set("basePath", aceBase);
    });
    return aceLoading;
  }

  function loadMarked() {
    if (mdLoading) return mdLoading;
    mdLoading = Promise.all([loadScript(vendorURL(MARKED_PATH)), loadScript(vendorURL(DOMPURIFY_PATH))]);
    return mdLoading;
  }

  // ── Helpers ────────────────────────────────────────────────────────
  function extOf(p) {
    var i = p.lastIndexOf(".");
    return i === -1 ? "" : p.slice(i + 1).toLowerCase();
  }

  function isImage(ext) {
    return ["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp", "ico"].indexOf(ext) !== -1;
  }

  function aceModeFor(p) {
    var e = extOf(p);
    var map = {
      js: "javascript", mjs: "javascript", ts: "typescript", tsx: "tsx", jsx: "jsx",
      go: "golang", py: "python", rb: "ruby", rs: "rust", java: "java", c: "c_cpp",
      cpp: "c_cpp", h: "c_cpp", cs: "csharp", php: "php", swift: "swift", kt: "kotlin",
      sh: "sh", bash: "sh", zsh: "sh", ps1: "powershell",
      json: "json", yaml: "yaml", yml: "yaml", toml: "toml", xml: "xml",
      html: "html", htm: "html", css: "css", scss: "scss", sass: "sass",
      md: "markdown", markdown: "markdown",
      sql: "sql", dockerfile: "dockerfile",
    };
    return "ace/mode/" + (map[e] || "text");
  }

  function prefersDark() {
    return document.documentElement.classList.contains("dark") ||
      (window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);
  }

  function iconForFile(name) {
    return '<svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M3 2h6l3 3v9a1 1 0 01-1 1H3a1 1 0 01-1-1V3a1 1 0 011-1z M9 2v3h3" stroke-linejoin="round"/></svg>';
  }

  function formatSize(n) {
    if (!n && n !== 0) return "";
    if (n < 1024) return n + " B";
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
    return (n / 1024 / 1024).toFixed(1) + " MB";
  }

  function formatTime(ms) {
    if (!ms) return "";
    var d = new Date(ms);
    var diff = (Date.now() - ms) / 1000;
    if (diff < 60) return "just now";
    if (diff < 3600) return Math.floor(diff / 60) + "m ago";
    if (diff < 86400) return Math.floor(diff / 3600) + "h ago";
    return d.toLocaleDateString();
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function escapeAttr(s) { return escapeHtml(s); }

  // Public API: surface a tiny namespace so other modules (chat
  // markdown renderer in agents.js) can drive the panel without
  // reaching into private state. Kept narrow on purpose — only what
  // the clickable-path feature needs.
  window.AgentContext = {
    // getCwd returns the absolute path of the current session cwd,
    // or "" when the panel hasn't been initialised / prefetch hasn't
    // resolved yet. Cached on the [data-context-cwd] node.
    getCwd: function () {
      if (!panel) return "";
      var el = $("[data-context-cwd]", panel);
      return (el && el.textContent) || "";
    },
    // openFileByAbsPath opens the preview modal for absPath when it
    // lives under the current session cwd. Returns true on success
    // (caller should preventDefault); false when the path is outside
    // cwd or the panel isn't ready — caller should fall back to its
    // own behaviour (e.g. show a "raw path" popup).
    openFileByAbsPath: function (absPath) {
      if (!panel || !absPath) return false;
      var cwd = this.getCwd();
      if (!cwd) return false;
      // Normalise trailing slash so the prefix match doesn't false-
      // negative when cwd ends with "/".
      var prefix = cwd.replace(/\/+$/, "") + "/";
      if (absPath === cwd) return false; // it's the dir itself
      if (absPath.indexOf(prefix) !== 0) return false;
      var rel = absPath.slice(prefix.length);
      if (!rel) return false;
      openFile(rel);
      return true;
    },
    // previewExternal opens the preview modal for a file whose bytes
    // live at an arbitrary URL (e.g. user-uploaded attachments served
    // by /sessions/<id>/uploads/<storedName>). opts:
    //   {name, url, size?, mime?, mtime?}
    // For text-y MIME the body is fetched and rendered inline; images
    // / PDFs are pointed at the URL directly. The Edit tab is always
    // hidden — uploads are read-only.
    previewExternal: function (opts) {
      if (!modal || !opts || !opts.url) return false;
      var name = String(opts.name || "file");
      var mime = String(opts.mime || "").toLowerCase();
      var isImg = mime.indexOf("image/") === 0;
      var isText = mime.indexOf("text/") === 0 || mime === "application/json" ||
        mime === "application/javascript" || mime === "application/yaml" ||
        mime === "application/x-yaml" || mime === "application/toml";
      var isPDF = mime === "application/pdf";

      currentFile = {
        path: name,
        size: opts.size || 0,
        mtime: opts.mtime || Math.floor(Date.now() / 1000),
        binary: !isText,
        tooBig: false,
        content: "",
        _externalURL: opts.url,
      };

      function show() { openModal(); }

      if (isText) {
        // Fetch the bytes so renderPreview's text branch shows the raw
        // contents (instead of needing an iframe).
        fetch(opts.url, { credentials: "same-origin" })
          .then(function (r) { return r.text(); })
          .then(function (t) {
            currentFile.content = t || "";
            currentFile.binary = false;
            show();
          })
          .catch(function (e) {
            currentFile.binary = true;
            currentFile.content = "";
            show();
          });
        return true;
      }
      // Image / PDF / unknown — modal renders via URL src directly.
      if (!isImg && !isPDF) currentFile.binary = true;
      show();
      return true;
    },
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
