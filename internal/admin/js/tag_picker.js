// Tag picker: multi-select with autocomplete, chips, create-with-type,
// and click-chip to edit a tag's type via popover. Selected tag IDs are
// synced into hidden <input name="tag_ids[]"> elements inside the host.
(function () {
  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, c => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c]));
  }

  // Shared registry so "create" / "edit" in one picker propagates to others.
  const pickers = new Set();
  function broadcast(kind, tag) {
    pickers.forEach(p => p.onExternal(kind, tag));
  }

  function init(el) {
    if (el.dataset.tpInit === '1') return;
    el.dataset.tpInit = '1';

    const disabled = el.dataset.disabled === 'true';
    const canCreate = el.dataset.canCreate === 'true';
    const raw = JSON.parse(el.dataset.tags || '[]');
    const all = new Map(raw.map(t => [t.id, { id: t.id, name: t.name, is_group: !!t.is_group, is_filter: !!t.is_filter }]));
    let selected = (el.dataset.selected || '')
      .split(',').map(s => s.trim()).filter(id => all.has(id));

    el.classList.add('relative');
    el.innerHTML = `
      <div class="tp-wrap flex min-h-[32px] flex-wrap items-center gap-1 rounded-md border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1 ${disabled ? 'opacity-60 cursor-not-allowed' : 'cursor-text focus-within:border-green-500'}">
        <div class="tp-chips flex flex-wrap gap-1"></div>
        <input type="text" class="tp-input min-w-[80px] flex-1 bg-transparent text-xs text-black-900 dark:text-white-100 outline-none placeholder:text-black-700" placeholder="${disabled ? '' : 'add tag...'}" ${disabled ? 'disabled' : ''}/>
      </div>
      <div class="tp-menu absolute left-0 right-0 z-50 mt-1 hidden max-h-64 overflow-auto rounded-md border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-xs shadow-lg"></div>
      <div class="tp-popover absolute left-0 z-50 mt-1 hidden rounded-md border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-3 text-xs shadow-lg" style="min-width:180px"></div>
      <div class="tp-hidden"></div>
    `;

    const wrap = el.querySelector('.tp-wrap');
    const chips = el.querySelector('.tp-chips');
    const input = el.querySelector('.tp-input');
    const menu = el.querySelector('.tp-menu');
    const pop = el.querySelector('.tp-popover');
    const hidden = el.querySelector('.tp-hidden');

    function syncHidden() {
      hidden.innerHTML = selected
        .map(id => `<input type="hidden" name="tag_ids[]" value="${escapeHTML(id)}"/>`)
        .join('');
    }

    function typeLabel(tag) {
      // Subtle text label used only in the dropdown, not in selected chips.
      const parts = [];
      if (tag.is_group) parts.push('group');
      if (tag.is_filter) parts.push('filter');
      if (!parts.length) return '';
      return `<span class="ml-1 text-[10px] text-black-700 dark:text-black-600">(${parts.join(', ')})</span>`;
    }

    function renderChips() {
      chips.innerHTML = selected.map(id => {
        const tag = all.get(id) || { id, name: id, is_group: false, is_filter: false };
        return `
        <span class="tp-chip inline-flex items-center gap-1 rounded-full bg-green-200 px-2 py-0.5 text-xs text-green-700" data-id="${escapeHTML(id)}">
          <button type="button" class="tp-chip-name text-green-700 hover:underline" data-id="${escapeHTML(id)}" title="Edit tag type">${escapeHTML(tag.name)}</button>
          ${disabled ? '' : `<button type="button" data-id="${escapeHTML(id)}" class="tp-remove text-green-700 hover:text-green-900" aria-label="remove">&times;</button>`}
        </span>
      `;
      }).join('');
      chips.querySelectorAll('.tp-remove').forEach(b => {
        b.addEventListener('click', e => {
          e.stopPropagation();
          const id = b.dataset.id;
          selected = selected.filter(x => x !== id);
          renderChips(); syncHidden();
        });
      });
      chips.querySelectorAll('.tp-chip-name').forEach(b => {
        b.addEventListener('click', e => {
          e.stopPropagation();
          if (disabled) return;
          openEditPopover(b.dataset.id, b);
        });
      });
    }

    function renderMenu() {
      const q = input.value.trim().toLowerCase();
      const items = [];
      for (const t of all.values()) {
        if (selected.includes(t.id)) continue;
        if (q && !t.name.toLowerCase().includes(q)) continue;
        items.push(t);
      }
      items.sort((a, b) => a.name.localeCompare(b.name));

      const hasCreate = canCreate && q && ![...all.values()].some(n => n.name.toLowerCase() === q);
      const createRow = hasCreate
        ? `<div class="border-b border-white-300 dark:border-navy-600 p-2">
             <button type="button" class="tp-create inline-flex items-center rounded-full bg-green-200 px-3 py-1 text-xs font-medium text-green-700 hover:bg-green-300">+ create "${escapeHTML(input.value.trim())}"…</button>
           </div>`
        : '';

      const pills = items.map(t => `
        <button type="button" data-id="${escapeHTML(t.id)}" class="tp-opt inline-flex items-center rounded-full border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-3 py-1 text-xs text-black-900 dark:text-white-100 hover:border-green-400 hover:bg-green-200 hover:text-green-700">
          ${escapeHTML(t.name)}${typeLabel(t)}
        </button>
      `).join('');

      const pillWrap = pills
        ? `<div class="flex flex-wrap gap-1.5 p-2">${pills}</div>`
        : '';

      if (!pillWrap && !createRow) {
        menu.innerHTML = `<div class="px-3 py-2 text-black-700 dark:text-black-600">No tags${q ? ' match' : ''}.</div>`;
      } else {
        menu.innerHTML = createRow + pillWrap;
      }

      menu.querySelectorAll('.tp-opt').forEach(b => {
        b.addEventListener('mousedown', e => {
          e.preventDefault();
          const id = b.dataset.id;
          if (!selected.includes(id)) selected.push(id);
          input.value = '';
          renderChips(); syncHidden(); renderMenu();
          input.focus();
        });
      });
      const createBtn = menu.querySelector('.tp-create');
      if (createBtn) {
        createBtn.addEventListener('mousedown', e => {
          e.preventDefault();
          openCreatePopover(input.value.trim());
        });
      }
    }

    function openCreatePopover(name) {
      closeMenu();
      // Anchor the popover just below the wrap so it replaces the menu.
      const wrapRect = wrap.getBoundingClientRect();
      const hostRect = el.getBoundingClientRect();
      pop.style.left = '0px';
      pop.style.top = (wrapRect.bottom - hostRect.top + 4) + 'px';
      pop.innerHTML = `
        <p class="mb-2 font-semibold text-black-900 dark:text-white-100">Create "${escapeHTML(name)}"</p>
        <label class="flex items-center gap-2 py-1 text-black-900 dark:text-white-100">
          <input type="checkbox" class="tp-new-group h-4 w-4 text-green-500"/>
          Group on home
        </label>
        <label class="flex items-center gap-2 py-1 text-black-900 dark:text-white-100">
          <input type="checkbox" class="tp-new-filter h-4 w-4 text-green-500"/>
          Access filter
        </label>
        <div class="mt-2 flex justify-end gap-2">
          <button type="button" class="tp-new-cancel rounded px-2 py-1 text-black-800 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100">Cancel</button>
          <button type="button" class="tp-new-ok rounded bg-green-500 px-3 py-1 font-medium text-white-100 hover:bg-green-600">Create</button>
        </div>
      `;
      pop.classList.remove('hidden');
      pop.querySelector('.tp-new-cancel').addEventListener('click', () => { pop.classList.add('hidden'); input.focus(); });
      pop.querySelector('.tp-new-ok').addEventListener('click', async () => {
        const isGroup = pop.querySelector('.tp-new-group').checked;
        const isFilter = pop.querySelector('.tp-new-filter').checked;
        try {
          const body = new URLSearchParams({ name, is_group: isGroup ? 'on' : '', is_filter: isFilter ? 'on' : '' });
          const res = await fetch('/admin/tags', {
            method: 'POST',
            headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'Accept': 'application/json' },
            body: body.toString(),
          });
          if (!res.ok) throw new Error(await res.text());
          const tag = await res.json();
          const norm = { id: tag.id, name: tag.name, is_group: !!tag.is_group, is_filter: !!tag.is_filter };
          all.set(norm.id, norm);
          if (!selected.includes(norm.id)) selected.push(norm.id);
          pop.classList.add('hidden');
          input.value = '';
          renderChips(); syncHidden(); renderMenu();
          input.focus();
          broadcast('create', norm);
        } catch (err) {
          alert('Could not create tag: ' + err.message);
        }
      });
    }

    function openEditPopover(id, anchor) {
      const tag = all.get(id);
      if (!tag) return;
      closeMenu();
      pop.innerHTML = `
        <p class="mb-2 font-semibold text-black-900 dark:text-white-100">Edit "${escapeHTML(tag.name)}"</p>
        <label class="flex items-center gap-2 py-1 text-black-900 dark:text-white-100">
          <input type="checkbox" class="tp-edit-group h-4 w-4 text-green-500" ${tag.is_group ? 'checked' : ''}/>
          Group on home
        </label>
        <label class="flex items-center gap-2 py-1 text-black-900 dark:text-white-100">
          <input type="checkbox" class="tp-edit-filter h-4 w-4 text-green-500" ${tag.is_filter ? 'checked' : ''}/>
          Access filter
        </label>
        <p class="mt-1 text-[10px] text-black-700 dark:text-black-600">Changes apply to every tool and user carrying this tag.</p>
        <div class="mt-2 flex justify-end gap-2">
          <button type="button" class="tp-edit-cancel rounded px-2 py-1 text-black-800 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100">Cancel</button>
          <button type="button" class="tp-edit-ok rounded bg-green-500 px-3 py-1 font-medium text-white-100 hover:bg-green-600">Save</button>
        </div>
      `;
      // Position below the anchor chip.
      const rect = anchor.getBoundingClientRect();
      const hostRect = el.getBoundingClientRect();
      pop.style.left = Math.max(0, rect.left - hostRect.left) + 'px';
      pop.style.top = (rect.bottom - hostRect.top + 4) + 'px';
      pop.classList.remove('hidden');
      pop.querySelector('.tp-edit-cancel').addEventListener('click', () => pop.classList.add('hidden'));
      pop.querySelector('.tp-edit-ok').addEventListener('click', async () => {
        const isGroup = pop.querySelector('.tp-edit-group').checked;
        const isFilter = pop.querySelector('.tp-edit-filter').checked;
        try {
          const body = new URLSearchParams({
            name: tag.name,
            description: '',
            is_group: isGroup ? 'on' : '',
            is_filter: isFilter ? 'on' : '',
            sort_order: '0',
          });
          const res = await fetch('/admin/tags/' + encodeURIComponent(id) + '/update', {
            method: 'POST',
            headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
            body: body.toString(),
          });
          if (!res.ok && res.status !== 302) throw new Error(await res.text());
          tag.is_group = isGroup;
          tag.is_filter = isFilter;
          pop.classList.add('hidden');
          renderChips();
          broadcast('update', tag);
        } catch (err) {
          alert('Could not update tag: ' + err.message);
        }
      });
    }

    function openMenu() { if (!disabled) { pop.classList.add('hidden'); renderMenu(); menu.classList.remove('hidden'); } }
    function closeMenu() { menu.classList.add('hidden'); }

    if (!disabled) {
      wrap.addEventListener('click', e => {
        if (e.target.closest('.tp-chip')) return;
        input.focus();
      });
      input.addEventListener('focus', openMenu);
      input.addEventListener('input', renderMenu);
      input.addEventListener('blur', () => setTimeout(() => {
        // Don't close if focus moved into popover.
        if (document.activeElement && el.contains(document.activeElement)) return;
        closeMenu();
      }, 120));
      input.addEventListener('keydown', e => {
        if (e.key === 'Backspace' && !input.value && selected.length) {
          selected.pop();
          renderChips(); syncHidden(); renderMenu();
        }
      });
      // Clicks outside this picker close the popover. Clicks inside
      // (menu, chips, popover itself) must not, otherwise the popover
      // would close the instant it opens from a menu button.
      document.addEventListener('mousedown', e => {
        if (!pop.classList.contains('hidden') && !el.contains(e.target)) {
          pop.classList.add('hidden');
        }
      });
    }

    renderChips();
    syncHidden();

    const handle = {
      el,
      onExternal(kind, tag) {
        if (kind === 'create') {
          if (!all.has(tag.id)) all.set(tag.id, { ...tag });
        } else if (kind === 'update') {
          if (all.has(tag.id)) {
            const existing = all.get(tag.id);
            existing.is_group = tag.is_group;
            existing.is_filter = tag.is_filter;
            renderChips();
          }
        }
      },
    };
    pickers.add(handle);
  }

  function initAll(root) {
    (root || document).querySelectorAll('.tag-picker').forEach(init);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => initAll());
  } else {
    initAll();
  }
  window.TagPicker = { init, initAll };
})();
