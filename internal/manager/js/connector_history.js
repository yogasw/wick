// connector_history.js — drives filter selects + row expand/collapse
// on the connector history page. Filter changes navigate to the same
// page with updated query params so links stay shareable.
(function () {
  "use strict";

  const filtersEl = document.querySelector("[data-history-filters]");
  if (filtersEl) {
    const baseUrl = filtersEl.dataset.baseUrl;
    const selects = filtersEl.querySelectorAll("[data-history-filter]");

    function applyFilters() {
      const params = new URLSearchParams();
      selects.forEach((sel) => {
        const value = sel.value;
        if (value) params.set(sel.dataset.historyFilter, value);
      });
      const qs = params.toString();
      window.location.href = qs ? baseUrl + "?" + qs : baseUrl;
    }

    selects.forEach((sel) => sel.addEventListener("change", applyFilters));
  }

  // Row expand/collapse — clicking a summary row toggles the matching
  // detail row. Chevron rotates to mark open state. The Retry link
  // inside the detail panel stops propagation so clicking it does not
  // collapse the row.
  document.querySelectorAll("[data-history-toggle]").forEach((tr) => {
    const id = tr.dataset.historyToggle;
    const detail = document.querySelector(`[data-history-detail="${id}"]`);
    const chevron = tr.querySelector("[data-history-chevron]");
    if (!detail) return;
    tr.addEventListener("click", () => {
      const open = !detail.classList.contains("hidden");
      detail.classList.toggle("hidden", open);
      if (chevron) {
        chevron.style.transform = open ? "rotate(0deg)" : "rotate(90deg)";
      }
    });
  });

  document.querySelectorAll("[data-history-retry]").forEach((a) => {
    a.addEventListener("click", (e) => e.stopPropagation());
  });
})();
