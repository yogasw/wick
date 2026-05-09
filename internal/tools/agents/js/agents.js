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
        if (ev.type === "text_delta") {
          appendDelta(ev.data);
        } else if (ev.type === "done") {
          finalizeAssistantTurn();
          updateStatusBadge("idle");
        } else if (ev.type === "session_start") {
          updateStatusBadge("running");
        } else if (ev.type === "error") {
          finalizeAssistantTurn();
          updateStatusBadge("idle");
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

      function updateStatusBadge(status) {
        // Re-fetch the badge area by reloading the header — simple approach
        // for MVP: reload the page header area. Full SPA update is phase 6.
        var badges = document.querySelectorAll("[class*='rounded-full']");
        // lightweight: just let the next page load reflect status.
        void badges;
      }
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

    // ── Kill agent ────────────────────────────────────────────────────
    document.querySelectorAll("[data-kill]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var id = btn.dataset.kill;
        if (!confirm("Kill the running agent in this session?")) return;
        fetch(base + "/sessions/" + encodeURIComponent(id) + "/kill", {
          method: "POST",
        })
          .then(function () { location.reload(); })
          .catch(function (err) { console.error("kill failed:", err); });
      });
    });

    // ── Delete session ─────────────────────────────────────────────────
    document.querySelectorAll("[data-delete-session]").forEach(function (btn) {
      btn.addEventListener("click", function () {
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
      btn.addEventListener("click", function () {
        var name = btn.dataset.deleteProject;
        if (!confirm("Delete project \"" + name + "\"? This cannot be undone.")) return;
        var b = resolveBase();
        if (!b) return;
        fetch(b + "/projects/" + encodeURIComponent(name), { method: "DELETE" })
          .then(function () { location.reload(); })
          .catch(function (err) { console.error("delete project failed:", err); });
      });
    });

    // ── Delete preset ─────────────────────────────────────────────────
    document.querySelectorAll("[data-delete-preset]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var name = btn.dataset.deletePreset;
        if (!confirm("Delete preset \"" + name + "\"?")) return;
        var b = resolveBase();
        if (!b) return;
        fetch(b + "/presets/" + encodeURIComponent(name), { method: "DELETE" })
          .then(function () { location.reload(); })
          .catch(function (err) { console.error("delete preset failed:", err); });
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
