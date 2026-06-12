// custom_manual.js — stepper layer on top of custom_review.js for the
// manual builder: shows one draft-form section per step (Meta →
// Configs → Operations) and swaps the form footer for step navigation
// until the last step.
(function () {
  const root = document.querySelector("[data-cc-manual]");
  if (!root) return;

  const steps = ["meta", "configs", "ops"];
  let current = 0;

  const sections = {};
  root.querySelectorAll("[data-cc-section]").forEach((s) => {
    sections[s.dataset.ccSection] = s;
  });
  const nav = root.querySelector("[data-cc-step-nav]");
  const prevBtn = root.querySelector("[data-cc-step-prev]");
  const nextBtn = root.querySelector("[data-cc-step-next]");
  const footer = sections["footer"];

  function paint() {
    steps.forEach((name, i) => {
      if (sections[name]) sections[name].classList.toggle("hidden", i !== current);
    });
    // Access section + footer ride with the last step.
    const last = current === steps.length - 1;
    sections["access"]?.classList.toggle("hidden", !last);
    footer?.classList.toggle("hidden", !last);
    nav?.classList.toggle("hidden", last);
    if (prevBtn) prevBtn.disabled = current === 0;
    if (nextBtn) nextBtn.textContent = current === steps.length - 2 ? "Step 3 — Operations →" : "Next →";
    root.querySelectorAll("[data-cc-step]").forEach((pill) => {
      const idx = parseInt(pill.dataset.ccStep, 10) - 1;
      const active = idx === current;
      pill.classList.toggle("bg-green-200", active);
      pill.classList.toggle("text-green-700", active);
      const num = pill.querySelector("[data-cc-step-num]");
      if (num) {
        num.classList.toggle("bg-green-500", active);
        num.classList.toggle("text-white-100", active);
      }
    });
  }

  prevBtn?.addEventListener("click", () => {
    if (current > 0) current--;
    paint();
  });
  nextBtn?.addEventListener("click", () => {
    if (current < steps.length - 1) current++;
    paint();
  });

  paint();
})();
