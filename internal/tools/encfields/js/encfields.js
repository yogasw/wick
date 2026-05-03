// encfields.js — JSON-only submit + inline render for the encrypt
// and decrypt forms. The form posts to the same path it sits on (./
// for encrypt, ./decrypt for decrypt) and the handler returns
// { token, plain, error }. No page reload — browser back goes
// straight to wherever the user came from.
(function () {
  // toolBase strips the trailing path segment ("decrypt" or "")
  // from the current location so the encrypt API ends at /tools/
  // encfields and the decrypt API at /tools/encfields/decrypt — same
  // routes the form sits on.
  function endpointFor(form) {
    return form.dataset.encryptForm !== undefined
      ? window.location.pathname.replace(/\/decrypt\/?$/, "")
      : window.location.pathname;
  }

  function show(el) { el.classList.remove("hidden"); }
  function hide(el) { el.classList.add("hidden"); }

  function setError(form, msg) {
    const banner = form.parentElement.querySelector("[data-error]");
    if (!banner) return;
    if (!msg) { hide(banner); banner.textContent = ""; return; }
    banner.textContent = msg;
    show(banner);
  }

  function setResult(form, text) {
    const block = form.parentElement.querySelector("[data-result]");
    const out = block && block.querySelector("[data-result-text]");
    if (!block || !out) return;
    if (!text) { hide(block); out.textContent = ""; return; }
    out.textContent = text;
    show(block);
  }

  async function submit(form, payload) {
    const btn = form.querySelector("button[type=submit]");
    if (btn) btn.disabled = true;
    setError(form, "");
    try {
      const res = await fetch(endpointFor(form), {
        method: "POST",
        headers: { "Content-Type": "application/json", "Accept": "application/json" },
        body: JSON.stringify(payload),
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok || body.error) {
        setError(form, body.error || ("Request failed (" + res.status + ")"));
        setResult(form, "");
        return;
      }
      setResult(form, body.token || body.plain || "");
    } catch (err) {
      setError(form, "Network error: " + (err && err.message ? err.message : err));
      setResult(form, "");
    } finally {
      if (btn) btn.disabled = false;
    }
  }

  function flash(btn, label, ms) {
    const original = btn.dataset.label || btn.textContent;
    btn.dataset.label = original;
    btn.textContent = label;
    setTimeout(() => { btn.textContent = original; }, ms || 1200);
  }

  document.addEventListener("DOMContentLoaded", () => {
    document.querySelectorAll("[data-encrypt-form]").forEach((form) => {
      form.addEventListener("submit", (e) => {
        e.preventDefault();
        const fd = new FormData(form);
        submit(form, {
          value: fd.get("value") || "",
          source: fd.get("source") || "",
        });
      });
    });
    document.querySelectorAll("[data-decrypt-form]").forEach((form) => {
      form.addEventListener("submit", (e) => {
        e.preventDefault();
        const fd = new FormData(form);
        submit(form, { token: fd.get("token") || "" });
      });
    });

    document.querySelectorAll("[data-copy-target]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        // Resolve relative to the button's own card so two cards on
        // the page (none today, but cheap to be safe) don't collide.
        const card = btn.closest("[data-result]") || document;
        const target = card.querySelector(btn.getAttribute("data-copy-target"));
        if (!target) return;
        const text = (target.textContent || "").trim();
        if (!text) return;
        try {
          await navigator.clipboard.writeText(text);
          flash(btn, "Copied!");
        } catch {
          const range = document.createRange();
          range.selectNodeContents(target);
          const sel = window.getSelection();
          sel.removeAllRanges();
          sel.addRange(range);
          flash(btn, "Selected — Ctrl+C", 1800);
        }
      });
    });
  });
})();
