// datatable inspector module. Interactive builders for conditions,
// row fields, and order-by. Table dropdown populated from
// GET /api/data-tables. Insert/upsert auto-populates columns from
// GET /api/data-tables/{slug}/columns when table is selected.
(function () {
  'use strict';

  const opOf = {
    datatable_get:    'get',
    datatable_exists: 'exists',
    datatable_query:  'query',
    datatable_count:  'count',
    datatable_insert: 'insert',
    datatable_upsert: 'upsert',
    datatable_delete: 'delete',
  };

  const CONDITION_OPS = [
    'equals', 'not_equals', 'gt', 'gte', 'lt', 'lte',
    'contains', 'in', 'is_empty', 'is_not_empty',
  ];

  function panel() {
    return document.querySelector('.wf-inspector-panel[data-node-type~="datatable_get"]');
  }

  function applyVisibility(op) {
    const root = panel();
    if (!root) return;
    root.querySelectorAll('[data-when-op]').forEach(el => {
      const ops = (el.dataset.whenOp || '').split(/\s+/);
      el.classList.toggle('hidden', !ops.includes(op));
    });
  }

  function field(name) {
    const root = panel();
    return root ? root.querySelector('[data-field="' + name + '"]') : null;
  }

  function lst(name) {
    const root = panel();
    return root ? root.querySelector('[data-dt-list="' + name + '"]') : null;
  }

  // ── table dropdown ─────────────────────────────────────────────

  let tablesCache = null;
  function loadTables(currentSlug, callback) {
    const sel = field('table');
    if (!sel) return;
    if (tablesCache) {
      populateTableSelect(sel, tablesCache, currentSlug);
      if (callback) callback(currentSlug);
      return;
    }
    const base = (window.wickBase || '').replace(/\/$/, '');
    fetch(base + '/api/data-tables', { credentials: 'include' })
      .then(r => r.ok ? r.json() : [])
      .then(tables => {
        tablesCache = tables;
        populateTableSelect(sel, tables, currentSlug);
        if (callback) callback(currentSlug);
      })
      .catch(() => {});
  }

  function populateTableSelect(sel, tables, currentSlug) {
    while (sel.options.length > 1) sel.remove(1);
    tables.forEach(t => {
      const opt = document.createElement('option');
      opt.value = t.slug;
      opt.textContent = t.name ? t.name + ' (' + t.slug + ')' : t.slug;
      sel.appendChild(opt);
    });
    if (currentSlug) sel.value = currentSlug;
  }

  // ── column loader for insert/upsert ───────────────────────────

  function loadColumnsForInsert(slug, existingRow) {
    if (!slug) return;
    const base = (window.wickBase || '').replace(/\/$/, '');
    fetch(base + '/api/data-tables/' + encodeURIComponent(slug) + '/columns', { credentials: 'include' })
      .then(r => r.ok ? r.json() : [])
      .then(cols => {
        const l = lst('row');
        if (!l) return;
        // Only auto-populate when list is empty (don't overwrite user edits)
        const hasRows = l.querySelectorAll('div.flex').length > 0;
        if (hasRows) return;
        cols.forEach(c => {
          const existing = existingRow ? (existingRow[c.name] || '') : '';
          l.appendChild(rowFieldRow(c.name, existing));
        });
      })
      .catch(() => {});
  }

  // ── condition builder ──────────────────────────────────────────

  function conditionRow(col, op, val) {
    const div = document.createElement('div');
    div.className = 'flex items-center gap-1';
    div.innerHTML =
      '<input type="text" placeholder="column" class="wf-input font-mono flex-1 min-w-0" data-dt-col/>' +
      '<select class="wf-input w-32 shrink-0" data-dt-op>' +
        CONDITION_OPS.map(o => '<option value="' + o + '"' + (o === op ? ' selected' : '') + '>' + o + '</option>').join('') +
      '</select>' +
      '<input type="text" placeholder="value" class="wf-input font-mono flex-1 min-w-0" data-dt-val/>' +
      '<button type="button" class="text-black-600 dark:text-black-700 hover:text-rose-500 shrink-0 px-1 text-base leading-none" data-dt-rm>×</button>';
    div.querySelector('[data-dt-col]').value = col || '';
    div.querySelector('[data-dt-val]').value = val != null ? String(val) : '';
    div.querySelector('[data-dt-rm]').addEventListener('click', () => { div.remove(); requestSave(); });
    div.querySelectorAll('input,select').forEach(el => el.addEventListener('input', requestSave));
    // hide value input for no-arg ops
    const opSel = div.querySelector('[data-dt-op]');
    const valIn = div.querySelector('[data-dt-val]');
    function syncValVisibility() {
      const noVal = opSel.value === 'is_empty' || opSel.value === 'is_not_empty';
      valIn.classList.toggle('hidden', noVal);
    }
    syncValVisibility();
    opSel.addEventListener('change', syncValVisibility);
    return div;
  }

  function readConditions() {
    const l = lst('condition');
    if (!l) return '';
    const rows = [];
    l.querySelectorAll('div.flex').forEach(row => {
      const col = (row.querySelector('[data-dt-col]') || {}).value || '';
      const op  = (row.querySelector('[data-dt-op]')  || {}).value || 'equals';
      const val = (row.querySelector('[data-dt-val]') || {}).value || '';
      if (!col) return;
      const noVal = op === 'is_empty' || op === 'is_not_empty';
      rows.push('- column: ' + col + '\n  op: ' + op + (noVal ? '' : '\n  value: ' + val));
    });
    return rows.join('\n');
  }

  function renderConditions(yaml) {
    const l = lst('condition');
    if (!l) return;
    l.innerHTML = '';
    if (!yaml) return;
    const blocks = yaml.split(/\n(?=-)/).map(s => s.trim()).filter(Boolean);
    blocks.forEach(block => {
      const colM = block.match(/column:\s*(.+)/);
      const opM  = block.match(/op:\s*(.+)/);
      const valM = block.match(/value:\s*([\s\S]+)/);
      l.appendChild(conditionRow(
        colM ? colM[1].trim() : '',
        opM  ? opM[1].trim()  : 'equals',
        valM ? valM[1].trim() : '',
      ));
    });
  }

  // ── row (field) builder ────────────────────────────────────────

  function rowFieldRow(col, val) {
    const div = document.createElement('div');
    div.className = 'flex items-center gap-1';
    div.innerHTML =
      '<input type="text" placeholder="column" class="wf-input font-mono w-28 shrink-0" data-dt-col/>' +
      '<input type="text" placeholder="value / template" class="wf-input font-mono flex-1 min-w-0" data-dt-val/>' +
      '<button type="button" class="text-black-600 dark:text-black-700 hover:text-rose-500 shrink-0 px-1 text-base leading-none" data-dt-rm>×</button>';
    div.querySelector('[data-dt-col]').value = col || '';
    div.querySelector('[data-dt-val]').value = val != null ? String(val) : '';
    div.querySelector('[data-dt-rm]').addEventListener('click', () => { div.remove(); requestSave(); });
    div.querySelectorAll('input').forEach(el => el.addEventListener('input', requestSave));
    return div;
  }

  function readRow() {
    const l = lst('row');
    if (!l) return '';
    const lines = [];
    l.querySelectorAll('div.flex').forEach(row => {
      const col = (row.querySelector('[data-dt-col]') || {}).value || '';
      const val = (row.querySelector('[data-dt-val]') || {}).value || '';
      if (!col) return;
      lines.push(col + ': ' + val);
    });
    return lines.join('\n');
  }

  // Parse "col: val" YAML lines into {col→val} map
  function parseRowYAML(yaml) {
    const out = {};
    if (!yaml) return out;
    yaml.split('\n').forEach(line => {
      const m = line.match(/^([^:]+):\s*([\s\S]*)/);
      if (m) out[m[1].trim()] = m[2].trim();
    });
    return out;
  }

  function renderRow(yaml) {
    const l = lst('row');
    if (!l) return;
    l.innerHTML = '';
    if (!yaml) return;
    const map = parseRowYAML(yaml);
    Object.entries(map).forEach(([col, val]) => l.appendChild(rowFieldRow(col, val)));
  }

  // ── order builder ──────────────────────────────────────────────

  function orderRow(col, dir) {
    const div = document.createElement('div');
    div.className = 'flex items-center gap-1';
    div.innerHTML =
      '<input type="text" placeholder="column" class="wf-input font-mono flex-1 min-w-0" data-dt-col/>' +
      '<select class="wf-input w-24 shrink-0" data-dt-dir>' +
        '<option value="asc">asc</option>' +
        '<option value="desc">desc</option>' +
      '</select>' +
      '<button type="button" class="text-black-600 dark:text-black-700 hover:text-rose-500 shrink-0 px-1 text-base leading-none" data-dt-rm>×</button>';
    div.querySelector('[data-dt-col]').value = col || '';
    div.querySelector('[data-dt-dir]').value = dir || 'asc';
    div.querySelector('[data-dt-rm]').addEventListener('click', () => { div.remove(); requestSave(); });
    div.querySelectorAll('input,select').forEach(el => el.addEventListener('input', requestSave));
    return div;
  }

  function readOrder() {
    const l = lst('order');
    if (!l) return '';
    const lines = [];
    l.querySelectorAll('div.flex').forEach(row => {
      const col = (row.querySelector('[data-dt-col]') || {}).value || '';
      const dir = (row.querySelector('[data-dt-dir]') || {}).value || 'asc';
      if (!col) return;
      lines.push('- column: ' + col + '\n  direction: ' + dir);
    });
    return lines.join('\n');
  }

  function renderOrder(yaml) {
    const l = lst('order');
    if (!l) return;
    l.innerHTML = '';
    if (!yaml) return;
    const blocks = yaml.split(/\n(?=-)/).map(s => s.trim()).filter(Boolean);
    blocks.forEach(block => {
      const colM = block.match(/column:\s*(.+)/);
      const dirM = block.match(/direction:\s*(.+)/);
      l.appendChild(orderRow(
        colM ? colM[1].trim() : '',
        dirM ? dirM[1].trim() : 'asc',
      ));
    });
  }

  // ── add-button + table-change wiring ──────────────────────────

  let _requestSave = null;
  function requestSave() { if (_requestSave) _requestSave(); }

  let _currentOp = null;

  function wireButtons() {
    const root = panel();
    if (!root || root._dtWired) return;
    root._dtWired = true;

    root.querySelectorAll('[data-dt-add]').forEach(btn => {
      btn.addEventListener('click', () => {
        const kind = btn.dataset.dtAdd;
        if (kind === 'condition') lst('condition').appendChild(conditionRow('', 'equals', ''));
        if (kind === 'row')       lst('row').appendChild(rowFieldRow('', ''));
        if (kind === 'order')     lst('order').appendChild(orderRow('', 'asc'));
        requestSave();
      });
    });

    const tblSel = field('table');
    if (tblSel) {
      tblSel.addEventListener('change', () => {
        requestSave();
        // Auto-populate columns for insert/upsert when table changes
        if (_currentOp === 'insert' || _currentOp === 'upsert') {
          const l = lst('row');
          if (l) l.innerHTML = '';
          loadColumnsForInsert(tblSel.value, null);
        }
      });
    }
  }

  // ── WickNodes module ───────────────────────────────────────────

  function buildModule(kind) {
    const op = opOf[kind];
    return {
      attach({ requestUpdate }) {
        _requestSave = requestUpdate;
        wireButtons();
        loadTables('');
      },

      hydrate(inner) {
        _currentOp = op;
        applyVisibility(op);
        const slug = inner.table || '';
        loadTables(slug, (s) => {
          // After table dropdown populated, auto-populate row fields
          // for insert/upsert if no row data saved yet
          if ((op === 'insert' || op === 'upsert') && !inner.row && s) {
            loadColumnsForInsert(s, null);
          }
        });
        const keyEl = field('key');
        if (keyEl) keyEl.value = inner.key || '';
        // conditions may have been saved as YAML string or legacy "where" map string
        renderConditions(inner.conditions || inner.where || '');
        if (inner.row) renderRow(inner.row);
        else if (op === 'insert' || op === 'upsert') {
          // will be populated by loadColumnsForInsert callback above
          lst('row') && (lst('row').innerHTML = '');
        }
        renderOrder(inner.order_by || '');
        const lim = field('limit');  if (lim)  lim.value  = inner.limit  || '';
        const off = field('offset'); if (off)  off.value  = inner.offset || '';
      },

      save(target) {
        const tbl = field('table');
        target.table = tbl ? tbl.value : '';
        if (op === 'get') {
          const k = field('key'); target.key = k ? k.value : '';
        }
        if (op === 'exists' || op === 'delete' || op === 'count' || op === 'query') {
          target.conditions = readConditions();
          delete target.where;
        }
        if (op === 'query') {
          target.order_by = readOrder();
          const lim = field('limit');
          const off = field('offset');
          target.limit  = lim  ? (parseInt(lim.value,  10) || 0) : 0;
          target.offset = off ? (parseInt(off.value, 10) || 0) : 0;
        }
        if (op === 'insert' || op === 'upsert') {
          target.row = readRow();
        }
      },
    };
  }

  window.WickNodes = window.WickNodes || {};
  Object.keys(opOf).forEach(kind => { window.WickNodes[kind] = buildModule(kind); });
})();
