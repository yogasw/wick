// Connectors index: client-side search + category filter chips, plus a
// two-mode layout.
//
//   • browse mode (default — "All" chip, empty search): category boxes sit
//     side by side in a page grid and their cards stack vertically inside,
//     so the page reads like the home grid with no large empty gaps.
//   • filter mode (a category chip active OR search typed): boxes go full
//     width and their cards spread across an auto-fill grid, so a filtered
//     or searched result fans out across the width instead of stacking into
//     one tall narrow column.
//
// Cards carry data-name / data-desc / data-tags / data-category; chips
// toggle the active category. Search text and the active category combine
// with AND.
(function () {
  var activeCategory = 'all';

  function currentQuery() {
    var s = document.getElementById('search');
    return s ? s.value.toLowerCase().trim() : '';
  }

  function applyMode(filtering) {
    var g = document.getElementById('groups');
    if (g) {
      g.className = filtering
        ? 'mt-6 flex flex-col gap-4'
        : 'mt-6 grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3';
    }
    document.querySelectorAll('[data-cards]').forEach(function (el) {
      if (filtering) {
        el.className = 'grid gap-4';
        el.style.gridTemplateColumns = 'repeat(auto-fill,minmax(260px,1fr))';
      } else {
        el.className = 'grid gap-3 grid-cols-1';
        el.style.gridTemplateColumns = '';
      }
    });
  }

  function hideEmptyGroups() {
    document.querySelectorAll('[data-group]').forEach(function (section) {
      var anyVisible = false;
      section.querySelectorAll('[data-conn-card]').forEach(function (card) {
        if (card.style.display !== 'none') anyVisible = true;
      });
      section.style.display = anyVisible ? '' : 'none';
    });
  }

  function apply() {
    var q = currentQuery();
    var filtering = q !== '' || activeCategory !== 'all';
    applyMode(filtering);

    var visible = 0;
    document.querySelectorAll('[data-conn-card]').forEach(function (card) {
      var name = (card.dataset.name || '').toLowerCase();
      var desc = (card.dataset.desc || '').toLowerCase();
      var tags = (card.dataset.tags || '').toLowerCase();
      var cat = card.dataset.category || '';
      var matchText = !q || name.includes(q) || desc.includes(q) || tags.includes(q);
      var matchCat = activeCategory === 'all' || cat === activeCategory;
      var show = matchText && matchCat;
      card.style.display = show ? '' : 'none';
      if (show) visible++;
    });
    hideEmptyGroups();
    var noResults = document.getElementById('no-results');
    if (noResults) noResults.classList.toggle('hidden', visible > 0);
  }

  window.filterConnectors = apply;

  function paintChips() {
    document.querySelectorAll('[data-chip]').forEach(function (chip) {
      var on = chip.dataset.chip === activeCategory;
      chip.classList.toggle('bg-green-500', on);
      chip.classList.toggle('text-white-100', on);
      chip.classList.toggle('border-green-500', on);
      chip.classList.toggle('text-black-800', !on);
      chip.classList.toggle('dark:text-black-600', !on);
    });
  }

  document.addEventListener('click', function (e) {
    var chip = e.target.closest('[data-chip]');
    if (!chip) return;
    activeCategory = chip.dataset.chip;
    paintChips();
    apply();
  });

  document.addEventListener('keydown', function (e) {
    if (e.key === '/' && document.activeElement !== document.getElementById('search')) {
      e.preventDefault();
      var s = document.getElementById('search');
      if (s) s.focus();
    }
    if (e.key === 'Escape') {
      var s = document.getElementById('search');
      if (s && document.activeElement === s) { s.value = ''; apply(); s.blur(); }
    }
  });

  paintChips();
})();
