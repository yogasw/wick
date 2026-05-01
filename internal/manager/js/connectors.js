// connectors.js — drives the per-row test panel on the manager
// connector detail page. Toggles the visible input form per operation
// and POSTs collected inputs to the /test endpoint, rendering the
// returned status/latency/response inline.
(function () {
  "use strict";

  const panel = document.querySelector("[data-connector-test]");
  if (!panel) return;

  const url = panel.dataset.testUrl;
  const select = panel.querySelector("[data-test-op]");
  const runBtn = panel.querySelector("[data-test-run]");
  const result = panel.querySelector("[data-test-result]");
  const statusEl = panel.querySelector("[data-test-status]");
  const latencyEl = panel.querySelector("[data-test-latency]");
  const bodyEl = panel.querySelector("[data-test-body]");
  const forms = panel.querySelectorAll("[data-test-form]");

  function showActiveForm() {
    const opKey = select.value;
    forms.forEach((f) => {
      f.classList.toggle("hidden", f.dataset.testForm !== opKey);
    });
  }

  function activeForm() {
    const opKey = select.value;
    return panel.querySelector(`[data-test-form="${opKey}"]`);
  }

  function collectInput() {
    const form = activeForm();
    const out = {};
    if (!form) return out;
    form.querySelectorAll("[data-test-input]").forEach((el) => {
      const key = el.dataset.testInput;
      if (el.type === "checkbox") {
        out[key] = el.checked ? "true" : "false";
      } else {
        out[key] = el.value;
      }
    });
    return out;
  }

  function setStatus(label, kind) {
    statusEl.textContent = label;
    const variants = {
      running: "bg-prog-100 text-prog-400",
      success: "bg-pos-100 text-pos-400",
      error: "bg-neg-100 text-neg-400",
    };
    statusEl.className =
      "rounded-full px-2 py-0.5 font-medium " +
      (variants[kind] || variants.running);
  }

  async function run() {
    const operation = select.value;
    const input = collectInput();
    result.classList.remove("hidden");
    setStatus("running", "running");
    latencyEl.textContent = "";
    bodyEl.textContent = "";
    runBtn.disabled = true;
    runBtn.textContent = "Running…";

    try {
      const resp = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ operation, input }),
      });
      const data = await resp.json();
      const status = data.status || (data.error ? "error" : "success");
      setStatus(status, status === "success" ? "success" : "error");
      if (typeof data.latency_ms === "number") {
        latencyEl.textContent = data.latency_ms + " ms";
      }
      if (data.error) {
        bodyEl.textContent = data.error;
      } else {
        bodyEl.textContent = JSON.stringify(data.response, null, 2);
      }
    } catch (err) {
      setStatus("error", "error");
      bodyEl.textContent = err.message;
    } finally {
      runBtn.disabled = false;
      runBtn.textContent = "Run";
    }
  }

  select.addEventListener("change", showActiveForm);
  runBtn.addEventListener("click", run);
  showActiveForm();
})();
