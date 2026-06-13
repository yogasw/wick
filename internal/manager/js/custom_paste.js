// custom_paste.js — paste page of the custom-connector builder.
// Tab switching preserves the textarea; Parse posts to the parse
// endpoint and hands the returned draft to the review page via
// sessionStorage.
(function () {
  const root = document.querySelector("[data-cc-paste]");
  if (!root) return;

  const parseURL = root.dataset.parseUrl;
  const reviewURL = root.dataset.reviewUrl;
  const box = document.getElementById("cc-paste-box");
  const parseBtn = root.querySelector("[data-cc-parse]");
  const errBox = root.querySelector("[data-cc-error]");
  const errText = root.querySelector("[data-cc-error-text]");
  const label = root.querySelector("[data-cc-paste-label]");
  let parser = "curl";

  const tabClasses = {
    on: ["bg-white-100", "dark:bg-navy-700", "text-green-600", "shadow-sm"],
    off: [],
  };

  function selectTab(name) {
    parser = name;
    root.querySelectorAll("[data-cc-tab]").forEach((btn) => {
      const active = btn.dataset.ccTab === name;
      tabClasses.on.forEach((c) => btn.classList.toggle(c, active));
    });
    root.querySelectorAll("[data-cc-tab-help]").forEach((el) => {
      el.classList.toggle("hidden", el.dataset.ccTabHelp !== name);
    });
    if (label) {
      label.textContent = name === "ai" ? "Paste anything — fetch(), axios, API docs, prose" : "cURL command";
    }
    hideError();
  }

  function showError(msg) {
    if (errText) errText.textContent = msg;
    if (errBox) errBox.classList.remove("hidden");
  }
  function hideError() {
    if (errBox) errBox.classList.add("hidden");
  }

  root.querySelectorAll("[data-cc-tab]").forEach((btn) => {
    btn.addEventListener("click", () => selectTab(btn.dataset.ccTab));
  });
  selectTab("curl");

  parseBtn?.addEventListener("click", async () => {
    const paste = (box?.value || "").trim();
    if (!paste) {
      showError("Paste something first.");
      return;
    }
    hideError();
    parseBtn.disabled = true;
    const original = parseBtn.textContent;
    parseBtn.textContent = parser === "ai" ? "Extracting…" : "Parsing…";
    try {
      const resp = await fetch(parseURL, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          parser,
          paste,
          provider: (document.getElementById("cc-ai-provider") || {}).value || "",
        }),
      });
      const data = await resp.json();
      if (!resp.ok) {
        showError(data.error || "Parse failed.");
        return;
      }
      sessionStorage.setItem("wick_custom_draft", JSON.stringify(data));
      location.href = reviewURL;
    } catch (err) {
      showError(String(err));
    } finally {
      parseBtn.disabled = false;
      parseBtn.textContent = original;
    }
  });
})();
