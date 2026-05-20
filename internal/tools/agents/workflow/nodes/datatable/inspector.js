// datatable inspector module. Interactive builders for conditions,
// row fields, and order-by. Table dropdown populated from
// GET /api/data-tables. Insert/upsert auto-populates columns from
// GET /api/data-tables/{slug}/columns when table is selected.
// Expression mode per-value field persisted in __dt_modes map.
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

  // ── panel helpers ──────────────────────────────────────────────

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

  function agentsBase() {
    const el = document.querySelector('[data-wf-base]');
    if (el && el.dataset.wfBase) return el.dataset.wfBase.replace(/\/workflows$/, '');
    const m = location.pathname.match(/^(.*?)\/workflows\b/);
    if (m) return m[1];
    return '';
  }

  let tablesCache = null;
  function loadTables(currentSlug, callback) {
    const sel = field('table');
    if (!sel) return;
    if (tablesCache) {
      populateTableSelect(sel, tablesCache, currentSlug);
      if (callback) callback(currentSlug);
      return;
    }
    const base = agentsBase();
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

  // ── column loader ──────────────────────────────────────────────

  const columnsCache = {};
  let _colNames = [];

  function loadColumns(slug, callback) {
    if (!slug) return;
    if (columnsCache[slug]) { callback(columnsCache[slug]); return; }
    const base = agentsBase();
    fetch(base + '/api/data-tables/' + encodeURIComponent(slug) + '/columns', { credentials: 'include' })
      .then(r => r.ok ? r.json() : [])
      .then(cols => { columnsCache[slug] = cols; callback(cols); })
      .catch(() => {});
  }

  function loadColumnsForInsert(slug, existingRow) {
    loadColumns(slug, cols => {
      _colNames = cols.map(c => c.name);
      const l = lst('row');
      if (!l) return;
      const hasRows = l.querySelectorAll('[data-dt-col]').length > 0;
      if (hasRows) return;
      cols.forEach(c => {
        const existing = existingRow ? (existingRow[c.name] || '') : '';
        l.appendChild(rowFieldRow(c.name, existing, 'fixed'));
      });
    });
  }

  function loadColumnsForHints(slug) {
    loadColumns(slug, cols => { _colNames = cols.map(c => c.name); });
  }

  // ── column combobox ────────────────────────────────────────────

  function makeColCombobox(initialValue) {
    const wrap = document.createElement('div');
    wrap.className = 'relative w-full min-w-0';
    wrap.dataset.dtColCombo = '1';

    const inp = document.createElement('input');
    inp.type = 'text';
    inp.placeholder = 'column';
    inp.className = 'wf-input font-mono w-full';
    inp.setAttribute('data-dt-col', '');
    inp.value = initialValue || '';
    inp.autocomplete = 'off';

    const drop = document.createElement('div');
    drop.className = 'absolute z-50 left-0 right-0 bg-white dark:bg-navy-800 border border-white-300 dark:border-navy-600 rounded shadow-lg max-h-48 overflow-y-auto hidden';
    drop.style.top = '100%';

    function showDrop(filter) {
      const names = filter
        ? _colNames.filter(n => n.toLowerCase().includes(filter.toLowerCase()))
        : _colNames;
      if (!names.length) { drop.classList.add('hidden'); return; }
      drop.innerHTML = '';
      names.forEach(name => {
        const item = document.createElement('div');
        item.className = 'px-3 py-1.5 text-sm font-mono cursor-pointer hover:bg-green-50 dark:hover:bg-navy-700 text-black-900 dark:text-white-100';
        item.textContent = name;
        item.addEventListener('mousedown', e => {
          e.preventDefault();
          inp.value = name;
          drop.classList.add('hidden');
          inp.dispatchEvent(new Event('input', { bubbles: true }));
        });
        drop.appendChild(item);
      });
      drop.classList.remove('hidden');
    }

    inp.addEventListener('focus', () => showDrop(inp.value));
    inp.addEventListener('input', () => showDrop(inp.value));
    inp.addEventListener('blur', () => setTimeout(() => drop.classList.add('hidden'), 150));

    wrap.appendChild(inp);
    wrap.appendChild(drop);
    return wrap;
  }

  // ── value field with Fixed/Expression toggle ───────────────────
  // Uses .wf-arg-field structure so wickEditorHelpers.setArgFieldMode,
  // preview rendering, and drag-drop from INPUT pane all work natively.

  function makeValWithToggle(initialVal, initialMode) {
    const wrap = document.createElement('div');
    wrap.className = 'wf-arg-field flex-1 min-w-0';
    wrap.setAttribute('data-field-key', '_dt_val');

    const head = document.createElement('div');
    head.className = 'wf-arg-field-head';
    const modeDiv = document.createElement('div');
    modeDiv.className = 'wf-arg-mode';
    const fixedBtn = document.createElement('button');
    fixedBtn.type = 'button';
    fixedBtn.setAttribute('data-arg-mode', 'fixed');
    fixedBtn.textContent = 'Fixed';
    const exprBtn = document.createElement('button');
    exprBtn.type = 'button';
    exprBtn.setAttribute('data-arg-mode', 'expression');
    exprBtn.textContent = 'Expression';
    modeDiv.appendChild(fixedBtn);
    modeDiv.appendChild(exprBtn);
    head.appendChild(modeDiv);

    const inp = document.createElement('input');
    inp.type = 'text';
    inp.className = 'wf-input w-full';
    inp.setAttribute('data-dt-val', '');
    inp.value = initialVal || '';

    const preview = document.createElement('div');
    preview.setAttribute('data-arg-preview', '');
    preview.className = 'wf-arg-preview';

    wrap.appendChild(head);
    wrap.appendChild(inp);
    wrap.appendChild(preview);

    inp.addEventListener('input', () => {
      if (window.wickEditorHelpers && window.wickEditorHelpers.updateArgPreview) {
        window.wickEditorHelpers.updateArgPreview(wrap);
      }
      requestSave();
    });

    function wireArgField() {
      const helpers = window.wickEditorHelpers;
      if (!helpers || wrap._argModeWired) return;
      wrap._argModeWired = true;
      const mode = initialMode || ((initialVal || '').includes('{{') ? 'expression' : 'fixed');
      helpers.setArgFieldMode(wrap, mode, false);
      wrap.querySelectorAll('[data-arg-mode]').forEach(btn => {
        btn.addEventListener('click', () => {
          helpers.setArgFieldMode(wrap, btn.dataset.argMode, true);
          requestSave();
        });
      });
      if (typeof helpers.attachTemplateDropTarget === 'function') {
        helpers.attachTemplateDropTarget(inp, () => {
          helpers.setArgFieldMode(wrap, 'expression', true);
          requestSave();
        });
      }
    }

    if (window.wickEditorHelpers) {
      wireArgField();
    } else {
      setTimeout(wireArgField, 0);
    }

    return wrap;
  }

  // ── condition builder ──────────────────────────────────────────

  function conditionRow(col, op, val, valMode) {
    const div = document.createElement('div');
    div.className = 'dt-row';

    const colBox = makeColCombobox(col);
    const opSel = document.createElement('select');
    opSel.className = 'wf-input';
    opSel.setAttribute('data-dt-op', '');
    CONDITION_OPS.forEach(o => {
      const opt = document.createElement('option');
      opt.value = o; opt.textContent = o;
      if (o === (op || 'equals')) opt.selected = true;
      opSel.appendChild(opt);
    });

    const valWrap = makeValWithToggle(val != null ? String(val) : '', valMode);
    const valIn = valWrap.querySelector('[data-dt-val]');

    const rmBtn = document.createElement('button');
    rmBtn.type = 'button';
    rmBtn.className = 'text-black-600 dark:text-black-700 hover:text-rose-500 px-1 text-base leading-none self-start pt-1 shrink-0';
    rmBtn.setAttribute('data-dt-rm', '');
    rmBtn.textContent = '×';

    const grid = document.createElement('div');
    grid.className = 'grid gap-1 items-start';
    grid.style.gridTemplateColumns = '2fr 2fr 3fr auto';
    grid.appendChild(colBox);
    grid.appendChild(opSel);
    grid.appendChild(valWrap);
    grid.appendChild(rmBtn);
    div.appendChild(grid);

    rmBtn.addEventListener('click', () => { div.remove(); requestSave(); });
    colBox.querySelector('input').addEventListener('input', requestSave);
    opSel.addEventListener('input', requestSave);

    function syncValVisibility() {
      const noVal = opSel.value === 'is_empty' || opSel.value === 'is_not_empty';
      valWrap.classList.toggle('hidden', noVal);
    }
    syncValVisibility();
    opSel.addEventListener('change', syncValVisibility);
    return div;
  }

  function readConditions() {
    const l = lst('condition');
    if (!l) return '';
    const rows = [];
    l.querySelectorAll('.dt-row').forEach(row => {
      const col = (row.querySelector('[data-dt-col]') || {}).value || '';
      const op  = (row.querySelector('[data-dt-op]')  || {}).value || 'equals';
      const val = (row.querySelector('[data-dt-val]') || {}).value || '';
      if (!col) return;
      const noVal = op === 'is_empty' || op === 'is_not_empty';
      rows.push('- column: ' + col + '\n  op: ' + op + (noVal ? '' : '\n  value: ' + val));
    });
    return rows.join('\n');
  }

  function readConditionModes() {
    const l = lst('condition');
    if (!l) return {};
    const modes = {};
    let i = 0;
    l.querySelectorAll('.wf-arg-field').forEach(wrap => {
      const m = wrap.dataset.argMode;
      if (m === 'expression') modes['c' + i] = 'expression';
      i++;
    });
    return modes;
  }

  function renderConditions(yaml, modes) {
    const l = lst('condition');
    if (!l) return;
    l.innerHTML = '';
    if (!yaml) return;
    const blocks = yaml.split(/\n(?=-)/).map(s => s.trim()).filter(Boolean);
    blocks.forEach((block, i) => {
      const colM = block.match(/column:\s*(.+)/);
      const opM  = block.match(/op:\s*(.+)/);
      const valM = block.match(/value:\s*([\s\S]+)/);
      l.appendChild(conditionRow(
        colM ? colM[1].trim() : '',
        opM  ? opM[1].trim()  : 'equals',
        valM ? valM[1].trim() : '',
        modes && modes['c' + i],
      ));
    });
  }

  // ── row (field) builder ────────────────────────────────────────

  function rowFieldRow(col, val, valMode) {
    const div = document.createElement('div');
    div.className = 'dt-row grid gap-1 items-start';
    div.style.gridTemplateColumns = '2fr 4fr auto';

    const colBox = makeColCombobox(col);
    const valWrap = makeValWithToggle(val != null ? String(val) : '', valMode);

    const rmBtn = document.createElement('button');
    rmBtn.type = 'button';
    rmBtn.className = 'text-black-600 dark:text-black-700 hover:text-rose-500 shrink-0 px-1 text-base leading-none self-start pt-1';
    rmBtn.setAttribute('data-dt-rm', '');
    rmBtn.textContent = '×';
    rmBtn.addEventListener('click', () => { div.remove(); requestSave(); });
    colBox.querySelector('input').addEventListener('input', requestSave);

    div.appendChild(colBox);
    div.appendChild(valWrap);
    div.appendChild(rmBtn);
    return div;
  }

  function readRow() {
    const l = lst('row');
    if (!l) return '';
    const lines = [];
    l.querySelectorAll('.dt-row').forEach(row => {
      const col = (row.querySelector('[data-dt-col]') || {}).value || '';
      const val = (row.querySelector('[data-dt-val]') || {}).value || '';
      if (!col) return;
      lines.push(col + ': ' + val);
    });
    return lines.join('\n');
  }

  function readRowModes() {
    const l = lst('row');
    if (!l) return {};
    const modes = {};
    let i = 0;
    l.querySelectorAll('.wf-arg-field').forEach(wrap => {
      if (wrap.dataset.argMode === 'expression') modes['r' + i] = 'expression';
      i++;
    });
    return modes;
  }

  function parseRowYAML(yaml) {
    const out = {};
    if (!yaml) return out;
    yaml.split('\n').forEach(line => {
      const m = line.match(/^([^:]+):\s*([\s\S]*)/);
      if (m) out[m[1].trim()] = m[2].trim();
    });
    return out;
  }

  function renderRow(yaml, modes) {
    const l = lst('row');
    if (!l) return;
    l.innerHTML = '';
    if (!yaml) return;
    const map = parseRowYAML(yaml);
    Object.entries(map).forEach(([col, val], i) => {
      l.appendChild(rowFieldRow(col, val, modes && modes['r' + i]));
    });
  }

  // ── order builder ──────────────────────────────────────────────

  function orderRow(col, dir) {
    const div = document.createElement('div');
    div.className = 'dt-row flex items-center gap-1';

    const colBox = makeColCombobox(col);
    colBox.classList.add('flex-1', 'min-w-0');

    const dirSel = document.createElement('select');
    dirSel.className = 'wf-input w-24 shrink-0';
    dirSel.setAttribute('data-dt-dir', '');
    ['asc', 'desc'].forEach(d => {
      const opt = document.createElement('option');
      opt.value = d; opt.textContent = d;
      if (d === (dir || 'asc')) opt.selected = true;
      dirSel.appendChild(opt);
    });

    const rmBtn = document.createElement('button');
    rmBtn.type = 'button';
    rmBtn.className = 'text-black-600 dark:text-black-700 hover:text-rose-500 shrink-0 px-1 text-base leading-none';
    rmBtn.setAttribute('data-dt-rm', '');
    rmBtn.textContent = '×';
    rmBtn.addEventListener('click', () => { div.remove(); requestSave(); });

    div.appendChild(colBox);
    div.appendChild(dirSel);
    div.appendChild(rmBtn);
    [colBox.querySelector('input'), dirSel].forEach(el => el && el.addEventListener('input', requestSave));
    return div;
  }

  function readOrder() {
    const l = lst('order');
    if (!l) return '';
    const lines = [];
    l.querySelectorAll('.dt-row').forEach(row => {
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

  // ── wiring ─────────────────────────────────────────────────────

  let _requestSave = null;
  function requestSave() { if (_requestSave) _requestSave(); }

  let _currentOp = null;

  function wireButtons() {
    const root = panel();
    if (!root || root._dtWired) return;
    root._dtWired = true;

    const base = agentsBase();
    const link = root.querySelector('[data-dt-tables-link]');
    if (link) link.href = base + '/data-tables';

    root.querySelectorAll('[data-dt-add]').forEach(btn => {
      btn.addEventListener('click', () => {
        const kind = btn.dataset.dtAdd;
        if (kind === 'condition') lst('condition').appendChild(conditionRow('', 'equals', '', 'fixed'));
        if (kind === 'row')       lst('row').appendChild(rowFieldRow('', '', 'fixed'));
        if (kind === 'order')     lst('order').appendChild(orderRow('', 'asc'));
        requestSave();
      });
    });

    const tblSel = field('table');
    if (tblSel) {
      tblSel.addEventListener('change', () => {
        requestSave();
        const slug = tblSel.value;
        if (_currentOp === 'insert' || _currentOp === 'upsert') {
          const l = lst('row');
          if (l) l.innerHTML = '';
          loadColumnsForInsert(slug, null);
        } else {
          loadColumnsForHints(slug);
        }
      });
    }
  }

  // ── WickNodes modules ──────────────────────────────────────────

  const outputsOf = {
    datatable_get: 2, datatable_exists: 2,
    datatable_query: 1, datatable_count: 1,
    datatable_insert: 1, datatable_upsert: 1, datatable_delete: 1,
  };

  function buildModule(kind) {
    const op = opOf[kind];
    return {
      meta: {
        kind,
        head: kind.replace('datatable_', 'datatable '),
        hint: { get: 'load by id', exists: 'row match?', query: 'multi-row search', count: 'count rows', insert: 'new row', upsert: 'insert/update', delete: 'drop rows' }[op] || '',
        cssType: 'datatable',
        inputs: 1,
        outputs: outputsOf[kind] || 1,
        defaults: { table: '' },
      },

      attach({ requestUpdate }) {
        _requestSave = requestUpdate;
        wireButtons();
      },

      hydrate(inner) {
        _currentOp = op;
        applyVisibility(op);
        const slug = inner.table || '';
        const modes = inner.__dt_modes || {};
        loadTables(slug, (s) => {
          if (!s) return;
          if (op === 'insert' || op === 'upsert') {
            if (!inner.row) loadColumnsForInsert(s, null);
          } else {
            loadColumnsForHints(s);
          }
        });
        const keyEl = field('key');
        if (keyEl) keyEl.value = inner.key || '';
        renderConditions(inner.conditions || inner.where || '', modes);
        if (inner.row) renderRow(inner.row, modes);
        else if (op === 'insert' || op === 'upsert') {
          lst('row') && (lst('row').innerHTML = '');
        }
        renderOrder(inner.order_by || '');
        const lim = field('limit');  if (lim) lim.value = inner.limit  || '';
        const off = field('offset'); if (off) off.value = inner.offset || '';
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
          target.limit  = lim ? (parseInt(lim.value,  10) || 0) : 0;
          target.offset = off ? (parseInt(off.value, 10) || 0) : 0;
        }
        if (op === 'insert' || op === 'upsert') {
          target.row = readRow();
        }
        // Persist expression modes so they survive refresh.
        const modes = Object.assign({}, readConditionModes(), readRowModes());
        if (Object.keys(modes).length > 0) {
          target.__dt_modes = modes;
        } else {
          delete target.__dt_modes;
        }
      },
    };
  }

  window.WickNodes = window.WickNodes || {};
  Object.keys(opOf).forEach(kind => { window.WickNodes[kind] = buildModule(kind); });
})();
