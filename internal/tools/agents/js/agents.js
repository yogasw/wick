(function () {
  "use strict";

  document.addEventListener("DOMContentLoaded", function () {
    var root = document.querySelector("[data-session-id]");
    var base = root ? root.dataset.base : null;
    var sessionID = root ? root.dataset.sessionId : null;

    // ── SSE (session detail page only) ────────────────────────────────
    if (sessionID && base) {
      var es = new EventSource(base + "/stream?session=" + encodeURIComponent(sessionID));
      var pendingTurnEl = null;

      es.addEventListener("agent", function (e) {
        var ev = JSON.parse(e.data);
        if (ev.type === "lifecycle") {
          // Pool-driven transition (spawning / killed). PID arrives
          // here for fresh spawns; idle / working transitions are
          // inferred from substate events below.
          applyLifecycle(ev.lifecycle, ev.pid || 0);
          return;
        }
        if (ev.type === "text_delta") {
          appendDelta(ev.data);
        } else if (ev.type === "done") {
          finalizeAssistantTurn();
          applyLifecycle("idle", 0);
        } else if (ev.type === "session_start") {
          applyLifecycle("working", 0);
        } else if (ev.type === "error") {
          finalizeAssistantTurn();
          applyLifecycle("idle", 0);
        } else if (
          ev.type === "thinking" || ev.type === "tool_use" ||
          ev.type === "tool_result" || ev.type === "text_delta"
        ) {
          applyLifecycle("working", 0);
        }
      });

      es.addEventListener("error", function () {
        // Browser will auto-reconnect; nothing to do here.
      });

      function appendDelta(text) {
        var container = document.querySelector("[data-turns]");
        if (!container) return;
        if (!pendingTurnEl) {
          pendingTurnEl = document.createElement("div");
          pendingTurnEl.className = "flex justify-start";
          var inner = document.createElement("div");
          inner.className =
            "max-w-[80%] rounded-2xl rounded-tl-sm border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-4 py-2.5 text-sm text-black-900 dark:text-white-100 whitespace-pre-wrap break-words";
          pendingTurnEl.appendChild(inner);
          container.appendChild(pendingTurnEl);
        }
        pendingTurnEl.querySelector("div").textContent += text;
        pendingTurnEl.scrollIntoView({ behavior: "smooth", block: "end" });
      }

      function finalizeAssistantTurn() {
        pendingTurnEl = null;
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

    function setBadgeLifecycle(badge, lifecycle, pid) {
      badge.dataset.lifecycle = lifecycle;
      if (pid > 0) badge.dataset.pid = String(pid);
      if (lifecycle === "idle" || lifecycle === "working") {
        // Fresh activity → reset the countdown clock.
        badge.dataset.lastActiveMs = String(Date.now());
      }
      ALL_BADGE_CLASSES.forEach(function (c) { badge.classList.remove(c); });
      (BADGE_CLASS_MAP[lifecycle] || []).forEach(function (c) { badge.classList.add(c); });

      var label = badge.querySelector("[data-lifecycle-label]");
      if (label) label.textContent = lifecycle || "—";

      var countdown = badge.querySelector("[data-lifecycle-countdown]");
      if (countdown && lifecycle !== "idle") countdown.textContent = "";

      paintRing(badge, lifecycle);
      if (lifecycle === "killed") badge.dataset.pid = "0";
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

    function applyLifecycle(lifecycle, pid) {
      // Session detail SSE only updates its own badge — list pages
      // have their own row each tied to a different session id, and
      // wiring them all to one EventSource would require per-row
      // subscribers (out of scope here).
      if (!primaryBadge) return;
      setBadgeLifecycle(primaryBadge, lifecycle, pid);
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
              'class="rounded-md bg-red-500 px-2.5 py-1 text-xs font-medium text-white-100 ' +
              'hover:bg-red-600 active:bg-red-700 transition-colors">' +
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

    // ── Composer (send message) ───────────────────────────────────────
    var sendForm = document.querySelector("[data-send-form]");
    if (sendForm && sessionID && base) {
      sendForm.addEventListener("submit", function (e) {
        e.preventDefault();
        var textarea = sendForm.querySelector("textarea");
        var text = textarea.value.trim();
        if (!text) return;
        var btn = sendForm.querySelector("button[type=submit]");
        textarea.disabled = true;
        if (btn) btn.disabled = true;

        fetch(base + "/sessions/" + encodeURIComponent(sessionID) + "/send", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ text: text }),
        })
          .then(function (r) { return r.json(); })
          .then(function () {
            // Append user turn immediately to the conversation
            var container = document.querySelector("[data-turns]");
            if (container) {
              var wrap = document.createElement("div");
              wrap.className = "flex justify-end";
              var bubble = document.createElement("div");
              bubble.className =
                "max-w-[80%] rounded-2xl rounded-tr-sm bg-green-500 px-4 py-2.5 text-sm text-white-100 whitespace-pre-wrap break-words";
              bubble.textContent = text;
              wrap.appendChild(bubble);
              container.appendChild(wrap);
              wrap.scrollIntoView({ behavior: "smooth", block: "end" });
            }
            textarea.value = "";
          })
          .catch(function (err) {
            console.error("send failed:", err);
          })
          .finally(function () {
            textarea.disabled = false;
            if (btn) btn.disabled = false;
            textarea.focus();
          });
      });

      // Ctrl+Enter submits
      sendForm.querySelector("textarea").addEventListener("keydown", function (e) {
        if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
          e.preventDefault();
          sendForm.dispatchEvent(new Event("submit"));
        }
      });
    }

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

    // ── Delete workspace ──────────────────────────────────────────────
    document.querySelectorAll("[data-delete-workspace]").forEach(function (btn) {
      btn.addEventListener("click", function (e) {
        e.stopPropagation();
        var name = btn.dataset.deleteWorkspace;
        confirmAt(btn, 'Delete workspace "' + name + '"? This cannot be undone.', { confirmLabel: "Delete" }).then(function (ok) {
          if (!ok) return;
          var b = resolveBase();
          if (!b) return;
          fetch(b + "/workspaces/" + encodeURIComponent(name), { method: "DELETE" })
            .then(function () { location.reload(); })
            .catch(function (err) { console.error("delete workspace failed:", err); });
        });
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

    // ── Workspace custom path: toggle + folder picker ─────────────────
    var customToggle = document.querySelector("[data-custom-path-toggle]");
    var customFields = document.querySelector("[data-custom-path-fields]");
    var customInput = document.querySelector("[data-custom-path-input]");
    if (customToggle && customFields && customInput) {
      customToggle.addEventListener("change", function () {
        if (customToggle.checked) {
          customFields.classList.remove("hidden");
        } else {
          customFields.classList.add("hidden");
          customInput.value = "";
        }
      });
    }

    // Native folder picker (webkitdirectory). Browser only exposes
    // File.webkitRelativePath (e.g. "myfolder/sub/file.txt"), so we
    // grab the first segment as the chosen folder name and let the
    // user complete the absolute parent path themselves. This keeps
    // the picker honest about what the browser will give us.
    var folderPicker = document.querySelector("[data-folder-picker]");
    var customErr = document.querySelector("[data-custom-path-error]");
    var createForm = document.querySelector("[data-create-workspace-form]");

    function isAbsolutePath(p) {
      if (!p) return false;
      // POSIX: starts with /
      if (p.charAt(0) === "/") return true;
      // Windows: C:\, D:/, \\server\share
      if (/^[A-Za-z]:[\\/]/.test(p)) return true;
      if (p.indexOf("\\\\") === 0) return true;
      return false;
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

    if (folderPicker && customInput) {
      folderPicker.addEventListener("change", function () {
        var files = folderPicker.files;
        if (!files || !files.length) return;
        var rel = files[0].webkitRelativePath || files[0].name;
        var folderName = rel.split("/")[0];
        if (!folderName) return;
        // Preserve existing parent path if user already typed one.
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
        // Reset the input so picking the same folder again still fires change.
        folderPicker.value = "";
        if (isAbsolutePath(customInput.value.trim())) clearCustomErr();
      });

      customInput.addEventListener("input", function () {
        var v = customInput.value.trim();
        if (!v || isAbsolutePath(v)) clearCustomErr();
      });
    }

    if (createForm && customToggle && customInput) {
      createForm.addEventListener("submit", function (e) {
        if (!customToggle.checked) return;
        var v = customInput.value.trim();
        if (!v) {
          e.preventDefault();
          showCustomErr("Custom path is required when the toggle is on.");
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
  });
})();
