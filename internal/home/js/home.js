// Home-specific behaviors: tool search, bookmark toggling, empty-group hiding.
(function () {
  function hideEmptyGroups() {
    document.querySelectorAll('[data-group]').forEach(function (section) {
      var anyVisible = false;
      section.querySelectorAll('[data-tool-card]').forEach(function (card) {
        if (card.style.display !== 'none') anyVisible = true;
      });
      section.style.display = anyVisible ? '' : 'none';
    });
  }

  window.filterTools = function (query) {
    var q = (query || '').toLowerCase().trim();
    var cards = document.querySelectorAll('[data-tool-card]');
    var visible = 0;
    cards.forEach(function (card) {
      var name = (card.dataset.name || '').toLowerCase();
      var desc = (card.dataset.desc || '').toLowerCase();
      var tags = (card.dataset.tags || '').toLowerCase();
      var match = !q || name.includes(q) || desc.includes(q) || tags.includes(q);
      card.style.display = match ? '' : 'none';
      if (match) visible++;
    });
    hideEmptyGroups();
    var noResults = document.getElementById('no-results');
    if (noResults) noResults.classList.toggle('hidden', visible > 0);
  };

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      var s = document.getElementById('search');
      if (s && document.activeElement === s) s.blur();
    }
  });

  // ── Bookmarks ────────────────────────────────────────────────
  // Initial state: a tool is bookmarked iff it appears in the
  // "Bookmarks" group. We read that on load and reflect it on every
  // star button for the same tool across all groups.
  var bookmarked = new Set();
  document.querySelectorAll('[data-group-kind="bookmarks"] [data-bookmark-btn]').forEach(function (btn) {
    bookmarked.add(btn.dataset.toolPath);
  });

  function paintStars() {
    document.querySelectorAll('[data-bookmark-btn]').forEach(function (btn) {
      var icon = btn.querySelector('[data-bookmark-icon]');
      if (!icon) return;
      var on = bookmarked.has(btn.dataset.toolPath);
      icon.textContent = on ? '★' : '☆';
      btn.classList.toggle('text-green-500', on);
    });
  }
  paintStars();

  document.addEventListener('click', function (e) {
    var btn = e.target.closest('[data-bookmark-btn]');
    if (!btn) return;
    e.preventDefault();
    var path = btn.dataset.toolPath;
    var body = 'tool_path=' + encodeURIComponent(path);
    // Optimistic toggle.
    var wasOn = bookmarked.has(path);
    if (wasOn) bookmarked.delete(path); else bookmarked.add(path);
    paintStars();
    fetch('/api/bookmarks/toggle', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body,
    }).then(function (res) {
      if (!res.ok) throw new Error(String(res.status));
      return res.json();
    }).then(function (data) {
      // Reconcile against the server's view.
      if (data.bookmarked) bookmarked.add(path); else bookmarked.delete(path);
      paintStars();
      // Reload so the Bookmarks group appears/disappears to match.
      window.location.reload();
    }).catch(function () {
      // Revert optimistic change.
      if (wasOn) bookmarked.add(path); else bookmarked.delete(path);
      paintStars();
    });
  });
})();
