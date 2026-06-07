// scm-dock.js — thin shell for the docked Source Control sidebar. Most
// logic lives in the Svelte island (window.WickSCM); this script only:
//   1. Toggles the docked <aside> (push layout) from the rail "Source" tab.
//   2. Lazily injects the SCM bundle on first open, then mounts the island
//      in sidebar mode into [data-scm-host].
//   3. Persists pin state + dock width in localStorage.
//   4. Drives the resize drag handle.
//   5. Keeps the rail badge live from git_status SSE events.
(function () {
  "use strict";

  var dock = document.querySelector("[data-scm-dock]");
  var openBtn = document.querySelector("[data-scm-open]");
  if (!dock) return;

  var sessionID = dock.getAttribute("data-scm-session") || "";
  var base = dock.getAttribute("data-scm-base") || "";
  var assetURL = dock.getAttribute("data-scm-asset") || "";
  if (!sessionID) return;

  var host = dock.querySelector("[data-scm-host]");
  var loading = dock.querySelector("[data-scm-loading]");
  var resizeHandle = dock.querySelector("[data-scm-resize]");
  var badge = document.querySelector("[data-scm-fab-count]");

  var PIN_KEY = "wick.scm.pinned." + sessionID;
  var WIDTH_KEY = "wick.scm.width";
  var MIN_W = 240, MAX_W = 640, DEFAULT_W = 260;

  // ── State ───────────────────────────────────────────────────────
  function isOpen() { return !dock.classList.contains("hidden"); }
  function savedWidth() {
    var w = parseInt(localStorage.getItem(WIDTH_KEY) || String(DEFAULT_W), 10);
    return isNaN(w) ? DEFAULT_W : Math.min(MAX_W, Math.max(MIN_W, w));
  }
  function applyWidth(w) {
    w = Math.min(MAX_W, Math.max(MIN_W, w));
    dock.style.width = w + "px";
    localStorage.setItem(WIDTH_KEY, String(w));
  }

  // ── Bundle + island ─────────────────────────────────────────────
  var bundleLoading = null;
  var mounted = false;

  function loadBundle() {
    if (bundleLoading) return bundleLoading;
    if (!assetURL) {
      bundleLoading = Promise.reject(new Error("scm bundle not built"));
      return bundleLoading;
    }
    bundleLoading = new Promise(function (resolve, reject) {
      if (window.WickSCM) return resolve();
      var s = document.createElement("script");
      s.type = "module";
      s.src = assetURL;
      s.onload = function () {
        if (window.WickSCM) resolve();
        else reject(new Error("WickSCM not installed"));
      };
      s.onerror = function () { reject(new Error("failed to load scm bundle")); };
      document.head.appendChild(s);
    });
    return bundleLoading;
  }

  function mountIsland() {
    if (mounted) return;
    loadBundle()
      .then(function () {
        if (loading) loading.remove();
        window.WickSCM.mount(host, {
          sessionID: sessionID,
          mode: "sidebar",
          pinned: isPinned(),
          onPinToggle: togglePin,
          onClose: closeDock,
        });
        mounted = true;
      })
      .catch(function (err) {
        if (loading) loading.textContent = "Source control unavailable: " + err.message;
      });
  }

  // Re-mount so the island gets the fresh pinned flag for its toggle UI.
  function remountIsland() {
    if (!mounted || !window.WickSCM) return;
    window.WickSCM.mount(host, {
      sessionID: sessionID,
      mode: "sidebar",
      pinned: isPinned(),
      onPinToggle: togglePin,
      onClose: closeDock,
    });
  }

  // ── Open / close / pin ──────────────────────────────────────────
  function openDock() {
    applyWidth(savedWidth());
    dock.classList.remove("hidden");
    mountIsland();
  }
  function closeDock() {
    dock.classList.add("hidden");
    setPinned(false);
    remountIsland();
  }
  function isPinned() { return localStorage.getItem(PIN_KEY) === "1"; }
  function setPinned(v) {
    localStorage.setItem(PIN_KEY, v ? "1" : "0");
    dock.setAttribute("data-scm-pinned", v ? "1" : "0");
  }
  function togglePin() {
    var next = !isPinned();
    setPinned(next);
    if (next && !isOpen()) openDock();
    remountIsland();
  }

  if (openBtn) {
    openBtn.addEventListener("click", function () {
      if (isOpen()) closeDock();
      else openDock();
    });
  }

  // Restore pinned dock on load.
  if (isPinned()) openDock();

  // ── Resize drag ─────────────────────────────────────────────────
  if (resizeHandle) {
    var dragging = false;
    resizeHandle.addEventListener("mousedown", function (e) {
      dragging = true;
      e.preventDefault();
      document.body.style.userSelect = "none";
    });
    window.addEventListener("mousemove", function (e) {
      if (!dragging) return;
      // Dock is on the right; width grows as the cursor moves left.
      var rect = dock.getBoundingClientRect();
      applyWidth(rect.right - e.clientX);
    });
    window.addEventListener("mouseup", function () {
      if (dragging) { dragging = false; document.body.style.userSelect = ""; }
    });
  }

  // ── Live badge ──────────────────────────────────────────────────
  function setCount(n) {
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
  try {
    var worker = new SharedWorker(base + "/static/js/sse-worker.js");
    worker.port.start();
    worker.port.onmessage = function (msg) {
      var d = msg.data;
      if (!d || d.type !== "event") return;
      var ev = d.event;
      if (!ev || ev.type !== "git_status") return;
      try { setCount(JSON.parse(ev.data).total_changed || 0); } catch (_) {}
    };
    worker.port.postMessage({ type: "subscribe", sessionID: sessionID, base: base });
    window.addEventListener("beforeunload", function () {
      worker.port.postMessage({ type: "unsubscribe", sessionID: sessionID });
    });
  } catch (_) {}
})();
