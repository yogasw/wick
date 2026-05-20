// datatable inspector module. Registered into window.WickNodes for
// every datatable_* node type — hydrate/save are field-driven so a
// single module covers all seven variants. Per-op visibility is
// declared via data-when-op="<space-separated ops>" on each field
// group; we map the node's kind (e.g. "datatable_query") to its
// short op key ("query") and toggle the matching wrappers.
(function () {
  'use strict';

  // Map full node type → short op key used in data-when-op.
  const opOf = {
    datatable_get:    'get',
    datatable_exists: 'exists',
    datatable_query:  'query',
    datatable_count:  'count',
    datatable_insert: 'insert',
    datatable_upsert: 'upsert',
    datatable_delete: 'delete',
  };

  function panel() {
    return document.querySelector('.wf-inspector-panel[data-node-type~="datatable_get"]');
  }

  // Show only the field groups whose data-when-op list contains `op`.
  function applyVisibility(op) {
    const root = panel();
    if (!root) return;
    root.querySelectorAll('[data-when-op]').forEach(el => {
      const ops = (el.dataset.whenOp || '').split(/\s+/);
      el.classList.toggle('hidden', !ops.includes(op));
    });
  }

  function $(field) {
    const root = panel();
    if (!root) return null;
    return root.querySelector('[data-field="' + field + '"]');
  }

  function setIfPresent(field, v) {
    const el = $(field);
    if (!el) return;
    if (v == null) { el.value = ''; return; }
    el.value = typeof v === 'string' ? v : String(v);
  }

  function readVal(field) {
    const el = $(field);
    return el ? el.value : '';
  }

  function readInt(field) {
    const el = $(field);
    if (!el) return 0;
    const n = parseInt(el.value, 10);
    return Number.isFinite(n) ? n : 0;
  }

  function hintFor(kind) {
    switch (kind) {
      case 'datatable_get':    return 'load by id';
      case 'datatable_exists': return 'row match?';
      case 'datatable_query':  return 'multi-row';
      case 'datatable_count':  return 'count rows';
      case 'datatable_insert': return 'new row';
      case 'datatable_upsert': return 'insert/update';
      case 'datatable_delete': return 'drop rows';
    }
    return '';
  }

  // Build one WickNodes entry per node type. The Render() server-side
  // already declared port counts; cssType stays "datatable" so the
  // shared canvas style applies, defaults match the executor's
  // expectations (e.g. table = "" until the user picks one).
  function buildModule(kind) {
    const op = opOf[kind];
    return {
      meta: {
        kind: kind,
        head: kind.replace('datatable_', 'datatable '),
        hint: hintFor(kind),
        cssType: 'datatable',
        inputs:  1,
        outputs: (kind === 'datatable_get' || kind === 'datatable_exists') ? 2 : 1,
        defaults: { table: '' },
      },

      onDrop(data) {
        if (!data.table) data.table = '';
      },

      hydrate(inner) {
        applyVisibility(op);
        setIfPresent('table',      inner.table);
        setIfPresent('key',        inner.key);
        setIfPresent('where',      inner.where);
        setIfPresent('conditions', inner.conditions);
        setIfPresent('order_by',   inner.order_by);
        setIfPresent('limit',      inner.limit);
        setIfPresent('offset',     inner.offset);
        setIfPresent('row',        inner.row);
      },

      save(target) {
        target.table = readVal('table');
        if (op === 'get') target.key = readVal('key');
        if (op === 'exists' || op === 'delete' || op === 'count' || op === 'query') {
          target.where      = readVal('where');
          target.conditions = readVal('conditions');
        }
        if (op === 'query') {
          target.order_by = readVal('order_by');
          target.limit    = readInt('limit');
          target.offset   = readInt('offset');
        }
        if (op === 'insert' || op === 'upsert') target.row = readVal('row');
      },
    };
  }

  window.WickNodes = window.WickNodes || {};
  Object.keys(opOf).forEach(kind => { window.WickNodes[kind] = buildModule(kind); });
})();
