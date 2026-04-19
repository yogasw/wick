// Global command palette. Loaded on every page via ui.Layout.
(function () {
  var isMac = /Mac|iPhone|iPad|iPod/i.test(navigator.platform || navigator.userAgent || '');
  var toolsCache = null;
  var loading = false;

  // ── Shortcut label swap (Ctrl K / ⌘K) ──────────────────────
  document.addEventListener('DOMContentLoaded', function () {
    var label = isMac ? '⌘K' : 'Ctrl K';
    document.querySelectorAll('.kbd-shortcut').forEach(function (el) {
      el.textContent = label;
    });
  });

  function isOpen() {
    var el = document.getElementById('cmdk');
    return el && !el.classList.contains('hidden');
  }

  function renderItems(tools) {
    var host = document.getElementById('cmdk-items');
    if (!host) return;
    if (!tools || !tools.length) {
      host.innerHTML = '<p class="px-4 py-4 text-sm text-black-700 dark:text-black-600">No tools available.</p>';
      return;
    }
    host.innerHTML = tools.map(function (t, i) {
      var tagList = (t.tags || []).slice();
      if (t.bookmarked) tagList.unshift('bookmark');
      var tagText = tagList.join(' ');
      var tagHtml = tagList.length
        ? '<div class="truncate text-[11px] italic text-green-700 dark:text-green-300">' + escapeHtml(tagList.join(' · ')) + '</div>'
        : '';
      var isExternal = !!t.external_url;
      var href = isExternal ? t.external_url : t.path;
      var extAttrs = isExternal ? ' target="_blank" rel="noopener"' : '';
      var extBadge = isExternal
        ? '<span class="ml-2 inline-flex h-4 w-4 items-center justify-center rounded-full bg-white-200 dark:bg-navy-800 text-[10px] text-black-700 dark:text-black-600" aria-label="Opens in new tab">↗</span>'
        : '';
      return (
        '<a href="' + href + '"' + extAttrs + ' data-cmdk-item data-name="' + escapeAttr(t.name) + '" data-desc="' + escapeAttr(t.description) + '" data-tags="' + escapeAttr(tagText) + '"' +
        (i === 0 ? ' data-active="true"' : '') +
        ' class="cmdk-item flex items-center gap-3 px-4 py-2.5 text-sm text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-800 data-[active=true]:bg-white-200 dark:data-[active=true]:bg-navy-800">' +
          '<div class="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-md bg-green-200 dark:bg-green-800 text-sm text-green-700 dark:text-green-300">' + escapeHtml(t.icon || '•') + '</div>' +
          '<div class="min-w-0 flex-1">' +
            '<div class="flex items-center truncate font-medium">' + escapeHtml(t.name) + extBadge + '</div>' +
            '<div class="truncate text-xs text-black-700 dark:text-black-600">' + escapeHtml(t.description || '') + '</div>' +
            tagHtml +
          '</div>' +
        '</a>'
      );
    }).join('');
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }
  function escapeAttr(s) { return escapeHtml(s); }

  function loadTools() {
    if (toolsCache || loading) return Promise.resolve(toolsCache || []);
    loading = true;
    return fetch('/api/tools', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : []; })
      .then(function (data) { toolsCache = data || []; renderItems(toolsCache); return toolsCache; })
      .catch(function () { return []; })
      .then(function (v) { loading = false; return v; });
  }

  window.openPalette = function () {
    var el = document.getElementById('cmdk');
    if (!el) return;
    el.classList.remove('hidden');
    el.classList.add('flex');
    var input = document.getElementById('cmdk-input');
    if (input) { input.value = ''; input.focus(); }
    if (!toolsCache) loadTools().then(function () { paletteFilter(''); });
    else paletteFilter('');
  };

  window.closePalette = function () {
    var el = document.getElementById('cmdk');
    if (!el) return;
    el.classList.add('hidden');
    el.classList.remove('flex');
  };

  window.paletteFilter = function (query) {
    var q = (query || '').toLowerCase().trim();
    var items = document.querySelectorAll('#cmdk-list [data-cmdk-item]');
    var visible = 0, firstVisible = null;
    items.forEach(function (item) {
      var name = (item.dataset.name || '').toLowerCase();
      var desc = (item.dataset.desc || '').toLowerCase();
      var tags = (item.dataset.tags || '').toLowerCase();
      var match = !q || name.includes(q) || desc.includes(q) || tags.includes(q);
      item.style.display = match ? '' : 'none';
      item.removeAttribute('data-active');
      if (match) { visible++; if (!firstVisible) firstVisible = item; }
    });
    if (firstVisible) firstVisible.setAttribute('data-active', 'true');
    var empty = document.getElementById('cmdk-empty');
    if (empty) empty.classList.toggle('hidden', visible > 0);
  };

  function move(dir) {
    var items = Array.prototype.filter.call(
      document.querySelectorAll('#cmdk-list [data-cmdk-item]'),
      function (it) { return it.style.display !== 'none'; }
    );
    if (!items.length) return;
    var idx = items.findIndex(function (it) { return it.getAttribute('data-active') === 'true'; });
    if (idx >= 0) items[idx].removeAttribute('data-active');
    idx = (idx + dir + items.length) % items.length;
    items[idx].setAttribute('data-active', 'true');
    items[idx].scrollIntoView({ block: 'nearest' });
  }

  function activate() {
    var a = document.querySelector('#cmdk-list [data-cmdk-item][data-active="true"]');
    if (a) a.click();
  }

  // Palette is only wired up when the navbar rendered a Search button,
  // i.e. the user is authenticated. Anonymous visitors get no shortcut.
  function paletteEnabled() {
    return !!document.querySelector('button[onclick="openPalette()"]');
  }

  document.addEventListener('keydown', function (e) {
    var modifier = isMac ? e.metaKey : e.ctrlKey;
    if (modifier && e.key.toLowerCase() === 'k') {
      if (!paletteEnabled()) return;
      e.preventDefault();
      if (isOpen()) closePalette(); else openPalette();
      return;
    }
    if (isOpen()) {
      if (e.key === 'Escape') { e.preventDefault(); closePalette(); return; }
      if (e.key === 'ArrowDown') { e.preventDefault(); move(1); return; }
      if (e.key === 'ArrowUp') { e.preventDefault(); move(-1); return; }
      if (e.key === 'Enter') { e.preventDefault(); activate(); return; }
    }
  });
})();
