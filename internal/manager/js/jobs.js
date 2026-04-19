// jobs.js — handles manual run triggering, polling, and safe markdown rendering.
(function () {
  "use strict";

  const runBtn = document.getElementById("run-btn");
  if (!runBtn) return;

  const jobID = runBtn.dataset.jobId;
  const resultArea = document.getElementById("run-result");
  const runStatus = document.getElementById("run-status");
  const runOutput = document.getElementById("run-output");

  runBtn.addEventListener("click", async function () {
    runBtn.disabled = true;
    runBtn.textContent = "Starting…";
    resultArea.classList.remove("hidden");
    runStatus.textContent = "Running";
    runStatus.className =
      "rounded-full bg-prog-100 px-2 py-0.5 text-[10px] font-medium text-prog-400";
    runOutput.innerHTML =
      '<p class="text-black-700 dark:text-black-600 text-sm">Waiting for result…</p>';

    try {
      const resp = await fetch("/jobs/" + jobID + "/run", {
        method: "POST",
      });
      const data = await resp.json();

      if (data.error) {
        runStatus.textContent = "Error";
        runStatus.className =
          "rounded-full bg-neg-100 px-2 py-0.5 text-[10px] font-medium text-neg-400";
        runOutput.textContent = data.error;
        runBtn.disabled = false;
        runBtn.textContent = "Run Now";
        return;
      }

      // Poll for completion
      pollRun(data.run_id);
    } catch (err) {
      runStatus.textContent = "Error";
      runStatus.className =
        "rounded-full bg-neg-100 px-2 py-0.5 text-[10px] font-medium text-neg-400";
      runOutput.textContent = err.message;
      runBtn.disabled = false;
      runBtn.textContent = "Run Now";
    }
  });

  function pollRun(runID) {
    const interval = setInterval(async function () {
      try {
        const resp = await fetch("/jobs/" + jobID + "/runs/" + runID);
        const data = await resp.json();

        if (data.status === "running") return; // keep polling

        clearInterval(interval);

        if (data.status === "success") {
          runStatus.textContent = "Success";
          runStatus.className =
            "rounded-full bg-pos-100 px-2 py-0.5 text-[10px] font-medium text-pos-400";
        } else {
          runStatus.textContent = "Error";
          runStatus.className =
            "rounded-full bg-neg-100 px-2 py-0.5 text-[10px] font-medium text-neg-400";
        }

        if (data.result) {
          runOutput.innerHTML = renderMarkdownSafe(data.result);
        } else {
          runOutput.innerHTML =
            '<p class="text-black-700 dark:text-black-600 text-sm">No output.</p>';
        }

        runBtn.disabled = false;
        runBtn.textContent = "Run Now";
      } catch (_) {
        // Ignore transient fetch errors, keep polling
      }
    }, 1500);
  }

  // renderMarkdownSafe converts a subset of markdown to safe HTML.
  // No raw HTML is passed through — all user content is escaped first.
  function renderMarkdownSafe(md) {
    // Step 1: escape all HTML entities to prevent XSS
    var s = md
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");

    // Step 2: convert safe markdown constructs

    // Code blocks (``` ... ```)
    s = s.replace(/```(\w*)\n([\s\S]*?)```/g, function (_, lang, code) {
      return (
        '<pre class="rounded-lg bg-white-200 dark:bg-navy-800 p-3 text-xs font-mono overflow-x-auto"><code>' +
        code.trim() +
        "</code></pre>"
      );
    });

    // Inline code
    s = s.replace(/`([^`]+)`/g, function (_, code) {
      return (
        '<code class="rounded bg-white-200 dark:bg-navy-800 px-1.5 py-0.5 text-xs font-mono">' +
        code +
        "</code>"
      );
    });

    // Headers
    s = s.replace(
      /^### (.+)$/gm,
      '<h3 class="text-sm font-semibold mt-3 mb-1">$1</h3>'
    );
    s = s.replace(
      /^## (.+)$/gm,
      '<h2 class="text-base font-semibold mt-4 mb-1">$1</h2>'
    );
    s = s.replace(
      /^# (.+)$/gm,
      '<h1 class="text-lg font-semibold mt-4 mb-2">$1</h1>'
    );

    // Bold and italic
    s = s.replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>");
    s = s.replace(/\*(.+?)\*/g, "<em>$1</em>");

    // Unordered lists
    s = s.replace(/^[*-] (.+)$/gm, '<li class="ml-4 list-disc">$1</li>');

    // Ordered lists
    s = s.replace(/^\d+\. (.+)$/gm, '<li class="ml-4 list-decimal">$1</li>');

    // Horizontal rule
    s = s.replace(
      /^---$/gm,
      '<hr class="my-3 border-white-300 dark:border-navy-600"/>'
    );

    // Line breaks — convert double newline to paragraph break
    s = s.replace(/\n\n/g, '</p><p class="mt-2">');

    // Single newlines to <br>
    s = s.replace(/\n/g, "<br/>");

    return '<div class="text-sm">' + s + "</div>";
  }

  // toggleResult — used in the history table to expand/collapse result cells
  window.toggleResult = function (btn) {
    var container = btn.nextElementSibling;
    if (container.classList.contains("hidden")) {
      container.classList.remove("hidden");
      container.innerHTML = renderMarkdownSafe(btn.dataset.result);
      btn.textContent = "Hide result";
    } else {
      container.classList.add("hidden");
      container.innerHTML = "";
      btn.textContent = "Show result";
    }
  };
})();
