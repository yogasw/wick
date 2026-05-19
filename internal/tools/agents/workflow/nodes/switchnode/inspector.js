// switch inspector module. Hand-coded rows builder — Add / Remove /
// drag-reorder. No ArgForm dependency. Persists to inner.cases as
// [{when, case}, ...] and inner.default_case as a string.
(function () {
  'use strict';

  function rowsEl() { return document.getElementById('ins-switch-rows'); }
  function defaultEl() { return document.getElementById('ins-switch-default'); }
  function tplEl() { return document.getElementById('ins-switch-row-template'); }
  function addEl() { return document.getElementById('ins-switch-add'); }

  function flushUpdate() {
    if (typeof window._wickSwitchRequestUpdate === 'function') {
      window._wickSwitchRequestUpdate();
    }
  }

  // makeRow clones the <template> row and wires input/remove
  // listeners on the freshly inserted DOM. Returns the row element so
  // hydrate can populate when/case after insertion.
  function makeRow(when, caseLabel) {
    const tpl = tplEl();
    if (!tpl) return null;
    const frag = tpl.content.cloneNode(true);
    const row = frag.querySelector('.switch-row');
    if (!row) return null;
    const whenInp = row.querySelector('[data-switch-when]');
    const caseInp = row.querySelector('[data-switch-case]');
    const removeBtn = row.querySelector('[data-switch-remove]');
    if (whenInp) whenInp.value = when || '';
    if (caseInp) caseInp.value = caseLabel || '';
    [whenInp, caseInp].forEach((el) => {
      if (!el) return;
      el.addEventListener('input', flushUpdate);
      el.addEventListener('change', flushUpdate);
    });
    if (removeBtn) {
      removeBtn.addEventListener('click', () => {
        row.remove();
        flushUpdate();
      });
    }
    wireDragHandle(row);
    return row;
  }

  // wireDragHandle implements drag-reorder using the HTML5 drag API.
  // The handle is the ⋮⋮ icon; dragging anywhere else on the row is
  // a noop so users can still select input text without triggering a
  // drag. Persistence fires on drop so the YAML stays in sync.
  function wireDragHandle(row) {
    const handle = row.querySelector('[data-switch-move]');
    if (!handle) return;
    handle.addEventListener('mousedown', () => { row.setAttribute('draggable', 'true'); });
    handle.addEventListener('mouseup', () => { row.removeAttribute('draggable'); });
    row.addEventListener('dragstart', (e) => {
      row.classList.add('switch-row-dragging');
      e.dataTransfer.effectAllowed = 'move';
      e.dataTransfer.setData('text/plain', 'switch-row');
    });
    row.addEventListener('dragend', () => {
      row.classList.remove('switch-row-dragging');
      row.removeAttribute('draggable');
      flushUpdate();
    });
    row.addEventListener('dragover', (e) => {
      e.preventDefault();
      const dragging = rowsEl()?.querySelector('.switch-row-dragging');
      if (!dragging || dragging === row) return;
      const rect = row.getBoundingClientRect();
      const before = e.clientY < rect.top + rect.height / 2;
      row.parentNode.insertBefore(dragging, before ? row : row.nextSibling);
    });
  }

  function clearRows() {
    const c = rowsEl();
    if (c) c.innerHTML = '';
  }

  function appendRow(when, caseLabel) {
    const c = rowsEl();
    const row = makeRow(when, caseLabel);
    if (c && row) c.appendChild(row);
    return row;
  }

  function collectRows() {
    const c = rowsEl();
    if (!c) return [];
    const out = [];
    c.querySelectorAll('.switch-row').forEach((row) => {
      const when = row.querySelector('[data-switch-when]')?.value || '';
      const caseLabel = row.querySelector('[data-switch-case]')?.value || '';
      if (!when && !caseLabel) return;
      out.push({ when, case: caseLabel });
    });
    return out;
  }

  const mod = {
    meta: {
      kind: 'switch',
      head: 'switch',
      hint: 'first match wins',
      cssType: 'switch',
      inputs: 1,
      outputs: 1,
      defaults: { cases: [], default_case: '' },
    },

    onDrop(data) {
      if (!Array.isArray(data.cases)) data.cases = [];
      if (typeof data.default_case !== 'string') data.default_case = '';
    },

    hydrate(inner) {
      clearRows();
      const cases = Array.isArray(inner.cases) ? inner.cases : [];
      if (cases.length === 0) {
        // Seed one empty row so the user has something to type into.
        appendRow('', '');
      } else {
        cases.forEach((c) => appendRow(c.when || '', c.case || ''));
      }
      const def = defaultEl();
      if (def) def.value = inner.default_case || '';
    },

    save(inner) {
      inner.cases = collectRows();
      const def = defaultEl();
      inner.default_case = def ? def.value.trim() : '';
    },

    // attach runs once per page load. Wire the Add button + capture
    // editor.js's requestUpdate hook into a module-level reference so
    // inputs created later (after Add) can flush updates too.
    attach({ requestUpdate }) {
      window._wickSwitchRequestUpdate = requestUpdate;
      addEl()?.addEventListener('click', () => {
        appendRow('', '');
        flushUpdate();
      });
      defaultEl()?.addEventListener('input', flushUpdate);
      defaultEl()?.addEventListener('change', flushUpdate);
    },
  };

  window.WickNodes = window.WickNodes || {};
  window.WickNodes.switch = mod;
})();
