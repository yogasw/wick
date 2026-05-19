// http inspector module. Uses the shared wickEditorHelpers from
// editor.js to render the ArgForm chrome (Fixed | Expression toggle,
// live preview, description) instead of hand-coded inputs. Each arg
// row maps back to the typed wf.Node field on save so existing
// workflow.yaml stays untouched.
(function () {
  'use strict';

  const ARG_KEYS = ['method', 'url', 'headers', 'query', 'body', 'parse_response', 'timeout_sec'];

  function container() { return document.getElementById('ins-http-args'); }

  // repaintKVListRows rebuilds the visible row table inside a kvlist
  // editor from saved JSON. Mirrors the helper in editor.js but is
  // scoped here so module file can repaint on subsequent inspector
  // opens without relying on editor.js to export it.
  function repaintKVListRows(editor, jsonValue) {
    if (!editor) return;
    const cols = (editor.getAttribute('data-cols') || '').split('|').filter(Boolean);
    const tbody = editor.querySelector('.kvlist-rows');
    if (!tbody || cols.length === 0) return;
    let rows = [];
    try { rows = JSON.parse(jsonValue || '[]'); } catch (_) { rows = []; }
    if (!Array.isArray(rows)) rows = [];
    const inputClass = 'w-full rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1 text-xs font-mono text-black-900 dark:text-white-100 outline-none focus:border-green-500 focus:ring-1 focus:ring-green-200 dark:focus:ring-green-800';
    tbody.innerHTML = '';
    rows.forEach((entry) => {
      const tr = document.createElement('tr');
      tr.className = 'border-b border-white-300 dark:border-navy-600 last:border-0';
      cols.forEach((c) => {
        const td = document.createElement('td');
        td.className = 'px-2 py-1';
        const inp = document.createElement('input');
        inp.type = 'text';
        inp.setAttribute('data-col', c);
        inp.value = (entry && entry[c]) || '';
        inp.className = inputClass;
        td.appendChild(inp);
        tr.appendChild(td);
      });
      const td2 = document.createElement('td');
      td2.className = 'px-2 py-1 text-center';
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.setAttribute('data-kvlist-remove', '');
      btn.className = 'text-black-700 dark:text-black-600 hover:text-neg-400 text-base leading-none';
      btn.setAttribute('aria-label', 'Remove row');
      btn.textContent = '×';
      td2.appendChild(btn);
      tr.appendChild(td2);
      tbody.appendChild(tr);
    });
  }

  // Convert a map[string]string (the typed Node field) into the
  // [{name,value},...] JSON-string the kvlist widget expects on its
  // hidden input.
  function mapToKVJSON(v) {
    if (!v || typeof v !== 'object') return '[]';
    const rows = [];
    for (const k of Object.keys(v)) {
      if (k === '') continue;
      rows.push({ name: k, value: String(v[k] == null ? '' : v[k]) });
    }
    return JSON.stringify(rows);
  }

  // Inverse — read the kvlist JSON ([{name,value},...]) back to a
  // map[string]string. Empty/blank rows are dropped so the YAML stays
  // tidy. Returns {} when input is malformed (caller decides whether
  // to overwrite the previous value).
  function kvJSONToMap(s) {
    const trimmed = (s || '').trim();
    if (!trimmed) return {};
    let arr = [];
    try { arr = JSON.parse(trimmed); } catch (_) { return {}; }
    if (!Array.isArray(arr)) return {};
    const out = {};
    arr.forEach((row) => {
      if (!row || typeof row !== 'object') return;
      const name = String(row.name == null ? '' : row.name).trim();
      if (!name) return;
      out[name] = String(row.value == null ? '' : row.value);
    });
    return out;
  }

  // Build the args+modes map that hydrateArgsForm wants from saved
  // typed Node data. Headers/Query are passed as a JSON array because
  // the schema row uses the kvlist widget; the rest are plain strings.
  function buildArgsFromInner(inner) {
    return {
      method: inner.method || 'GET',
      url: inner.url || '',
      headers: mapToKVJSON(inner.headers),
      query: mapToKVJSON(inner.query),
      body: inner.body || '',
      parse_response: inner.parse_response || '',
      timeout_sec: inner.timeout_sec ? String(inner.timeout_sec) : '',
    };
  }

  const mod = {
    meta: {
      kind: 'http',
      head: 'http',
      hint: 'GET / POST',
      cssType: 'http',
      inputs: 1,
      outputs: 1,
      defaults: { method: 'GET', url: '' },
    },

    onDrop(data) {
      if (!data.method) data.method = 'GET';
      if (!('url' in data)) data.url = '';
    },

    hydrate(inner) {
      const helpers = window.wickEditorHelpers;
      const c = container();
      if (!helpers || !c) return;
      // ArgForm HTML is already server-rendered into the container at
      // page boot. Treat it as the cached template and call hydrate
      // with empty html to skip re-injection — but hydrateArgsForm
      // overwrites innerHTML when html is non-empty, so pass the
      // current markup back to itself the first time and skip on
      // subsequent opens.
      if (!c.dataset.hydrated) {
        // First open — wire toggle + listeners against the server-
        // rendered markup. hydrateArgsForm expects html != '' so we
        // pass the existing innerHTML; it re-injects identical HTML.
        const html = c.innerHTML;
        helpers.hydrateArgsForm(c, html, buildArgsFromInner(inner), inner.__arg_modes || {}, '');
        c.dataset.hydrated = '1';
      } else {
        // Subsequent opens — just restore values + mode on the same
        // DOM without reinjecting (keeps focus + scroll position).
        // kvlist editors need explicit row repaint because the value
        // lives on a hidden input that the table doesn't watch.
        const args = buildArgsFromInner(inner);
        const modes = inner.__arg_modes || {};
        c.querySelectorAll('.wf-arg-field').forEach((wrap) => {
          const k = wrap.dataset.fieldKey;
          const kvEditor = wrap.querySelector('.kvlist-editor');
          if (kvEditor && k in args) {
            const hidden = kvEditor.querySelector('input[type="hidden"][data-field-key]');
            if (hidden) hidden.value = args[k];
            repaintKVListRows(kvEditor, args[k]);
          } else {
            const editable = wrap.querySelector('input:not([type="hidden"]), select, textarea');
            if (editable && k in args) editable.value = args[k];
          }
          helpers.setArgFieldMode(wrap, modes[k] || 'fixed', false);
        });
      }
    },

    save(inner) {
      const helpers = window.wickEditorHelpers;
      const c = container();
      if (!helpers || !c) return;
      const args = helpers.collectArgs(c);
      const modes = helpers.collectArgModes(c);

      inner.url = args.url || '';
      inner.method = args.method || 'GET';
      inner.body = args.body || '';
      inner.parse_response = args.parse_response || '';

      // Headers/Query — kvlist JSON array → map. kvJSONToMap drops
      // blank rows so the YAML stays tidy.
      inner.headers = kvJSONToMap(args.headers || '');
      inner.query = kvJSONToMap(args.query || '');

      const t = parseInt(args.timeout_sec || '', 10);
      inner.timeout_sec = Number.isFinite(t) && t > 0 ? t : 0;

      // ArgModes — strip unknown keys to keep the YAML tidy.
      const trimmed = {};
      for (const k of ARG_KEYS) {
        if (modes[k] && modes[k] !== 'fixed') trimmed[k] = modes[k];
      }
      if (Object.keys(trimmed).length > 0) inner.__arg_modes = trimmed;
      else delete inner.__arg_modes;
    },

    attach({ requestUpdate }) {
      // editor.js delegates input/change on document.body and the
      // .wf-arg-field ancestor — no extra wiring needed.
      void requestUpdate;
    },
  };

  window.WickNodes = window.WickNodes || {};
  window.WickNodes.http = mod;
})();
