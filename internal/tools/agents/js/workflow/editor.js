// editor.js — wires Drawflow to the wick workflow editor.
//
// Lifecycle:
//   1. Read base URL + serialized graph from <script id="wf-graph-data">.
//   2. Init Drawflow on #wf-canvas, import existing graph (when present).
//   3. Bind palette drag-source → canvas drop-target.
//   4. Inspector reads/writes the selected node's data.
//   5. Save button serializes Drawflow → POSTs JSON body.
(function () {
  'use strict';

  const root = document.querySelector('[data-wf-base]');
  if (!root) return;
  const baseURL = root.dataset.wfBase;
  const canvasEl = document.getElementById('wf-canvas');
  if (!canvasEl || typeof Drawflow === 'undefined') {
    console.error('[wf] Drawflow lib or canvas missing');
    return;
  }

  const editor = new Drawflow(canvasEl);
  editor.reroute = true;
  editor.editor_mode = 'edit';
  editor.start();

  // Top→bottom bezier override. Drawflow's stock createCurvature
  // emits horizontal-tangent control points, which produces ugly
  // diagonals when ports sit on the node's top/bottom edges. Replace
  // it with a vertical-tangent variant.
  editor.createCurvature = function (sx, sy, ex, ey) {
    // Top→bottom routing. The path leaves the source straight down,
    // arrives at the target straight from above, and crosses sideways
    // only in the middle band. Two cubics joined at the midpoint give
    // a smooth curve while exposing a vertex for the <marker-mid>
    // arrow.
    const dy = ey - sy;
    const halfY = dy / 2;
    const mx = (sx + ex) / 2;
    const my = sy + halfY;
    return (
      ` M ${sx} ${sy}` +
      ` C ${sx} ${sy + halfY} ${mx} ${my} ${mx} ${my}` +
      ` C ${mx} ${my} ${ex} ${ey - halfY} ${ex} ${ey}`
    );
  };

  // Arrow marker defs — Drawflow draws each edge as its own inline
  // <svg class="connection">, and marker-end: url(#id) resolves
  // per-document, so a single global <defs> block is enough. Without
  // this the CSS marker-end refs return empty and edges render bare.
  // Drawflow renders each edge as its own <svg class="connection">, so
  // a single global <defs> in the document body doesn't reliably
  // resolve across those inner SVGs in every browser. Inject a <defs>
  // block into every connection SVG (and re-inject when new edges are
  // drawn) so the marker-end CSS rule finds the arrow head locally.
  // refX=5 puts the arrow tip right at the input port (Drawflow draws
  // each path with stroke-width 2; marker scales with stroke). orient
  // auto rotates the marker along the bezier tangent so the arrow
  // always points into the target.
  // refX=15 shifts the arrow tip back from the path endpoint so it
  // sits in clear canvas just before the target input circle (Drawflow
  // input port has radius ~7px). Larger markerWidth makes it readable.
  const ARROW_DEFS = `<defs>
    <marker id="wf-arrow" viewBox="0 0 10 10" refX="15" refY="5" markerWidth="10" markerHeight="10" orient="auto" markerUnits="userSpaceOnUse">
      <path d="M0,0 L10,5 L0,10 z" fill="#9aa3b2"/>
    </marker>
    <marker id="wf-arrow-dark" viewBox="0 0 10 10" refX="15" refY="5" markerWidth="10" markerHeight="10" orient="auto" markerUnits="userSpaceOnUse">
      <path d="M0,0 L10,5 L0,10 z" fill="#5b6478"/>
    </marker>
  </defs>`;

  function injectArrowsIntoEdges() {
    document.querySelectorAll('.drawflow svg.connection').forEach((svg) => {
      if (svg.querySelector('defs')) return;
      // Drawflow updates the path via svg.children[0].setAttribute('d', ...)
      // so the path MUST stay as the first child. Append <defs> at the
      // end (or just before the path) — never prepend or you'll be
      // setting `d` on the <defs> block and the real path stops moving.
      svg.insertAdjacentHTML('beforeend', ARROW_DEFS);
    });
  }
  // Initial inject runs after import() settles the connection SVGs.
  // Multiple retries cover slow renders; idempotent so spamming is OK.
  setTimeout(injectArrowsIntoEdges, 0);
  setTimeout(injectArrowsIntoEdges, 100);
  setTimeout(injectArrowsIntoEdges, 500);
  // Re-inject on Drawflow topology events so newly drawn edges get
  // their <defs>. Cheaper than a subtree MutationObserver (which fires
  // hundreds of times per drag and competes with autosave).
  ['connectionCreated', 'connectionRemoved', 'nodeCreated', 'nodeRemoved'].forEach((ev) => {
    editor.on(ev, () => setTimeout(injectArrowsIntoEdges, 0));
  });

  const dataIsland = document.getElementById('wf-graph-data');
  let initialGraph = null;
  const raw = dataIsland && (dataIsland.dataset.graph || dataIsland.textContent.trim());
  if (raw) {
    try { initialGraph = JSON.parse(raw); }
    catch (err) { console.warn('[wf] graph json parse', err); }
  }
  // Hide the inner canvas surface during the brief import + fit
  // window. Without this the page renders at default scale/origin
  // for ~2 frames before fitToView snaps it into place — visible
  // as a "jump from corner to centre". The grid stays visible (the
  // wrap div keeps its background) so the page doesn't flash empty.
  if (editor.precanvas) editor.precanvas.classList.add('wf-fitting');
  if (initialGraph && initialGraph.drawflow) {
    editor.import(initialGraph);
  } else {
    seedEmptyGraph();
  }
  // Double RAF: drawflow's import inserts node DOM synchronously
  // but the browser hasn't laid out their box sizes yet — first
  // RAF lets layout commit, second RAF measures. After fitting,
  // remove the hiding class and CSS fades the canvas in.
  requestAnimationFrame(() => {
    requestAnimationFrame(() => {
      try { fitToView(); }
      catch (err) { console.warn('[wf] fit-to-view', err); }
      if (editor.precanvas) editor.precanvas.classList.remove('wf-fitting');
    });
  });

  // Initial validation report — server renders it into data-validation
  // so badges show up immediately on page load (not just after the
  // first auto-save). Without this the canvas opened clean and only
  // surfaced errors once the user nudged a node.
  let _initialValidation = null;
  if (dataIsland && dataIsland.dataset.validation) {
    try { _initialValidation = JSON.parse(dataIsland.dataset.validation); }
    catch (err) { console.warn('[wf] validation json parse', err); }
  }

  // ── Palette → canvas drop ──────────────────────────────────────
  // Two-level palette: each draggable row carries `data-node-type` plus
  // an optional `data-node-defaults` JSON blob. The defaults blob is how
  // level-2 op rows (e.g. Slack → send_message) ferry their
  // pre-configured data (channel+op or module+op) into the drop handler
  // without a server round-trip. Drill rows have no `data-node-type` —
  // they're click-only.
  document.querySelectorAll('.wf-palette-item[data-node-type]').forEach((el) => {
    el.addEventListener('dragstart', (e) => {
      e.dataTransfer.setData('node-type', el.dataset.nodeType);
      const defaults = el.dataset.nodeDefaults || '';
      if (defaults) {
        e.dataTransfer.setData('node-defaults', defaults);
      }
      e.dataTransfer.effectAllowed = 'copy';
    });
  });
  canvasEl.addEventListener('dragover', (e) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'copy';
  });
  canvasEl.addEventListener('drop', (e) => {
    e.preventDefault();
    // Lock guard — palette drag-drop no-ops while the canvas is
    // locked. canvasLocked is initialized below in the lock toggle
    // block but resolved at call time (drop fires post-load).
    if (canvasLocked) return;
    const type = e.dataTransfer.getData('node-type');
    if (!type) return;
    const rect = canvasEl.getBoundingClientRect();
    const pos = canvasToFlow(e.clientX - rect.left, e.clientY - rect.top);
    let defaults = null;
    const raw = e.dataTransfer.getData('node-defaults');
    if (raw) {
      try { defaults = JSON.parse(raw); }
      catch (err) { console.warn('[wf] node-defaults parse', err); }
    }
    addNodeOfType(type, pos.x, pos.y, defaults);
  });

  // ── Inspector ──────────────────────────────────────────────────
  const insEmpty = document.getElementById('inspector-empty');
  const insNode = document.getElementById('inspector-node');
  const f = {
    id: document.getElementById('ins-id'),
    type: document.getElementById('ins-type'),
    label: document.getElementById('ins-label'),
    prompt: document.getElementById('ins-prompt'),
    cases: document.getElementById('ins-cases'),
    provider: document.getElementById('ins-provider'),
    preset: document.getElementById('ins-preset'),
    command: document.getElementById('ins-command'),
    channel: document.getElementById('ins-channel'),
    op: document.getElementById('ins-op'),
    module: document.getElementById('ins-module'),
    connOp: document.getElementById('ins-conn-op'),
    refs: document.getElementById('ins-refs'),
    channelEmpty: document.getElementById('ins-channel-empty'),
  };

  // ── Registry hydration — channels/connectors/providers ──────────
  // Picker options come from /workflows/api/registry (live channels +
  // connectors registered server-side). No free-text — pickers stay
  // empty if nothing's registered yet, with a visible "configure first"
  // hint to surface the gap.
  let registry = { channels: [], connectors: [], providers: [] };
  fetch(`${baseURL}/api/registry`, { headers: { 'Accept': 'application/json' } })
    .then((r) => r.json())
    .then((data) => {
      registry = data || registry;
      hydrateProviders();
      hydrateChannels();
      hydrateConnectors();
    })
    .catch((err) => console.warn('[wf] registry fetch failed', err));

  function fillSelect(sel, opts, placeholder) {
    if (!sel) return;
    const current = sel.value;
    sel.innerHTML = '';
    const ph = document.createElement('option');
    ph.value = '';
    ph.textContent = placeholder;
    sel.appendChild(ph);
    opts.forEach((o) => {
      const opt = document.createElement('option');
      opt.value = o.value;
      opt.textContent = o.label;
      sel.appendChild(opt);
    });
    if (current && opts.some((o) => o.value === current)) sel.value = current;
  }

  function hydrateProviders() {
    fillSelect(
      f.provider,
      registry.providers.map((p) => ({ value: p.name, label: p.is_default ? `${p.name} (default)` : p.name })),
      '(default)'
    );
  }
  function hydrateChannels() {
    fillSelect(
      f.channel,
      registry.channels.map((c) => ({ value: c.name, label: c.name })),
      '(select channel)'
    );
    if (f.channelEmpty) f.channelEmpty.classList.toggle('hidden', registry.channels.length > 0);
    hydrateChannelOps();
  }
  function hydrateChannelOps() {
    if (!f.op) return;
    const chName = f.channel?.value || '';
    const ch = registry.channels.find((c) => c.name === chName);
    const ops = (ch?.ops || []).map((o) => ({ value: o.id, label: o.description ? `${o.id} — ${o.description}` : o.id }));
    fillSelect(f.op, ops, '(select op)');
  }
  function hydrateConnectors() {
    fillSelect(
      f.module,
      registry.connectors.map((m) => ({ value: m.module, label: m.name || m.module })),
      '(select module)'
    );
    hydrateConnectorOps();
  }
  function hydrateConnectorOps() {
    if (!f.connOp) return;
    const modKey = f.module?.value || '';
    const mod = registry.connectors.find((m) => m.module === modKey);
    const ops = (mod?.ops || []).map((o) => ({ value: o.id, label: o.description ? `${o.id} — ${o.description}` : o.id }));
    fillSelect(f.connOp, ops, '(select op)');
  }

  // Args form — once an op is picked, render one <input> per
  // declared input (key + required flag from the registry). Values
  // round-trip through the node's `args` map. JSON expressions stay
  // valid because we just pass strings; the engine renders templates
  // before invoking the connector/channel.
  const connArgsEl = document.getElementById('ins-conn-args');
  const chanArgsEl = document.getElementById('ins-channel-args');

  // hydrateArgsForm wires up the server-rendered args HTML. The
  // workflow API ships every op's input form as `args_html` (rendered
  // by view/workflow.ArgForm), so the JS side never builds widget
  // markup — it only:
  //   - injects the HTML into the container
  //   - sets each editable's value from the saved args map
  //   - wires the Fixed/Expression toggle, drop target, preview
  //   - attaches a delegated input listener to drive updateNodeData
  //
  // Adding a new widget type lives entirely in
  // internal/manager/view/type/<widget>.templ + the ArgField switch —
  // no JS edit required.
  function hydrateArgsForm(container, html, args, modes, lookupModule) {
    if (!container) return;
    if (!html) {
      container.innerHTML = '<div class="text-xs italic text-black-600 dark:text-black-700">No args required.</div>';
      return;
    }
    container.innerHTML = html;
    // Picker widgets need a lookup URL stamped after inject — the
    // server-rendered HTML doesn't know whether it's serving the
    // workflow editor or admin. Workflow callers pass the channel /
    // module name; the picker JS appends ?source=&q= on top.
    if (lookupModule) {
      const lookupURL = `${baseURL}/api/lookup?module=${encodeURIComponent(lookupModule)}`;
      container.querySelectorAll('.wf-picker').forEach((w) => {
        w.dataset.pickerLookupUrl = lookupURL;
      });
    }
    // Widget scripts no longer ride inline inside the templ HTML
    // (browsers don't execute <script> tags added via innerHTML
    // anyway). Each widget exports a global init function loaded
    // once via <script src=…> in the editor bootstrap; call them
    // here after the lookup URL has been stamped on every picker
    // wrap. Add a new widget = new init function call below.
    if (typeof window.wickInitPickers === 'function') {
      window.wickInitPickers(container);
    }
    // Backward compat: any leftover inline scripts get re-executed
    // (kvlist still uses the inline pattern). Remove once every
    // widget moves to the loaded-once approach.
    reExecInlineScripts(container);
    container.querySelectorAll('.wf-arg-field').forEach((wrap) => {
      const key = wrap.dataset.fieldKey;
      const editable = argEditable(wrap);
      if (!editable) return;
      // Restore stored value. Checkboxes use .checked from "true"/"false".
      // Arrays (picker native YAML format) must be JSON-stringified so the
      // picker hidden input and chip renderer always see a JSON string.
      const raw = args && args[key] != null ? args[key] : '';
      const stored = Array.isArray(raw) ? JSON.stringify(raw) : String(raw);
      if (editable.type === 'checkbox') {
        editable.checked = stored === 'true';
      } else if (stored !== '' || editable.tagName === 'SELECT') {
        editable.value = stored;
        // kvlist hidden inputs need the visible row table repainted
        // after value restoration. The widget's own boot script ran
        // before we set the value, so the rows still reflect the
        // initial server-rendered state (empty for a fresh node).
        if (editable.type === 'hidden' && editable.closest('.kvlist-editor')) {
          repaintKVList(editable.closest('.kvlist-editor'), editable.value);
        }
        // Picker hidden inputs need chips re-rendered after value is set
        // because wickInitPickers ran before value restoration.
        if (editable.type === 'hidden' && editable.closest('.wf-picker')) {
          const pickerWrap = editable.closest('.wf-picker');
          // Re-render chips inline without depending on wickRefreshPicker.
          try {
            const arr = JSON.parse(editable.value || '[]') || [];
            const chipList = pickerWrap.querySelector('[data-picker-chips]');
            if (chipList) {
              chipList.innerHTML = '';
              arr.forEach((item) => {
                const chip = document.createElement('span');
                chip.className = 'wf-picker-chip';
                const label = document.createElement('span');
                label.className = 'wf-picker-chip-label';
                label.textContent = item.name || item.id;
                chip.appendChild(label);
                if (item.name && item.name !== item.id) {
                  const sub = document.createElement('span');
                  sub.className = 'wf-picker-chip-id';
                  sub.textContent = item.id;
                  chip.appendChild(sub);
                }
                const rm = document.createElement('button');
                rm.type = 'button';
                rm.className = 'wf-picker-chip-remove';
                rm.setAttribute('aria-label', 'Remove');
                rm.textContent = '×';
                rm.addEventListener('click', () => {
                  const cur = JSON.parse(editable.value || '[]');
                  editable.value = JSON.stringify(cur.filter((it) => it.id !== item.id));
                  editable.dispatchEvent(new Event('input', { bubbles: true }));
                  editable.dispatchEvent(new Event('change', { bubbles: true }));
                  chip.remove();
                });
                chip.appendChild(rm);
                chipList.appendChild(chip);
              });
            }
          } catch (_) {}
        }
      }
      // Initial mode — Fixed by default. Operator opts into Expression.
      const initialMode = (modes && modes[key]) || 'fixed';
      setArgFieldMode(wrap, initialMode, /*persist*/ false);
      // Wire toggle pill buttons.
      wrap.querySelectorAll('[data-arg-mode]').forEach((btn) => {
        btn.addEventListener('click', () => setArgFieldMode(wrap, btn.dataset.argMode, true));
      });
      // Drop target — INPUT pane JSON leaves drop here, auto-switching
      // the field into Expression mode.
      if (editable.tagName !== 'SELECT' && editable.type !== 'checkbox') {
        attachTemplateDropTarget(editable, () => setArgFieldMode(wrap, 'expression', true));
      }
      // Live-template preview hook — fires for text-like inputs. Also
      // auto-flip the wrap into Expression mode the first time the
      // operator types `{{` — Fixed-mode defaults bite users who paste
      // a template ref and forget to toggle.
      editable.addEventListener('input', () => {
        if (wrap.dataset.argMode !== 'expression' && typeof editable.value === 'string' && editable.value.indexOf('{{') >= 0) {
          setArgFieldMode(wrap, 'expression', true);
        }
        updateArgPreview(wrap);
      });
      updateArgPreview(wrap);
    });
    // wireVisibleWhen runs after value restoration so initial evaluate()
    // sees the correct stored values (not select defaults).
    wireVisibleWhen(container);
  }

  // argEditable returns the value-bearing element inside an arg
  // wrapper. Widget templ files (admin fieldtype.*) emit the editable
  // as a plain input/select/textarea with `name="value"`. The
  // checkbox widget also emits a sibling hidden "false" input; the
  // selector skips hidden inputs so we always land on the visible
  // editable.
  //
  // Picker + kvlist are exceptions: their canonical value lives on a
  // hidden input (chip composite for picker, JSON array for kvlist),
  // not on any of the visible row inputs. Special-case both so
  // collectArgs / preview / visible_when read the right place.
  function argEditable(wrap) {
    const picker = wrap.querySelector('.wf-picker > input[type="hidden"][data-field-key]');
    if (picker) return picker;
    const kvHidden = wrap.querySelector('.kvlist-editor input[type="hidden"][data-field-key]');
    if (kvHidden) return kvHidden;
    return wrap.querySelector('input:not([type="hidden"]), select, textarea');
  }

  // repaintKVList rebuilds the visible row table inside a kvlist
  // editor from a saved JSON value (array of {col:value} objects).
  // Used after node selection so the widget reflects the persisted
  // map[string]string instead of the empty server-rendered table.
  function repaintKVList(editor, jsonValue) {
    if (!editor) return;
    const cols = (editor.getAttribute('data-cols') || '').split('|').filter(Boolean);
    const tbody = editor.querySelector('.kvlist-rows');
    if (!tbody || cols.length === 0) return;
    let rows = [];
    try { rows = JSON.parse(jsonValue || '[]'); } catch (_) { rows = []; }
    if (!Array.isArray(rows)) rows = [];
    tbody.innerHTML = '';
    const inputClass = 'w-full rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1 text-xs font-mono text-black-900 dark:text-white-100 outline-none focus:border-green-500 focus:ring-1 focus:ring-green-200 dark:focus:ring-green-800';
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

  // reExecInlineScripts re-creates every <script> tag inside the
  // container so the browser actually executes it. The HTML5 spec
  // says <script> tags inserted via innerHTML run NOTHING — they
  // exist in the DOM but their JS body is dead. Server-rendered
  // widgets (picker, kvlist, …) bundle inline init JS that wires
  // search / chips / autocomplete; without this re-exec they stay
  // visible but completely inert.
  function reExecInlineScripts(container) {
    container.querySelectorAll('script').forEach((oldScript) => {
      const newScript = document.createElement('script');
      for (const a of oldScript.attributes) newScript.setAttribute(a.name, a.value);
      newScript.text = oldScript.text;
      oldScript.parentNode.replaceChild(newScript, oldScript);
    });
  }

  // wireVisibleWhen wires the conditional-field pattern: a wrapper
  // with `data-cfg-visible-when="otherKey:value"` only shows while
  // the dependency wrapper's editable equals the named value. The
  // value half supports `a|b|c` (pipe-separated OR) so a single field
  // can be gated on a set of allowed values (e.g. method:POST|PUT|PATCH).
  // Fires initial evaluation + listens to the dependency for live toggle.
  function wireVisibleWhen(container) {
    container.querySelectorAll('[data-cfg-visible-when]').forEach((wrap) => {
      const spec = wrap.dataset.cfgVisibleWhen;
      if (!spec) return;
      const sep = spec.indexOf(':');
      if (sep < 0) return;
      const depKey = spec.slice(0, sep);
      const allowed = spec.slice(sep + 1).split('|');
      const depWrap = container.querySelector(`.wf-arg-field[data-field-key="${CSS.escape(depKey)}"]`);
      const depEditable = depWrap && argEditable(depWrap);
      if (!depEditable) return;
      const evaluate = () => {
        let cur;
        if (depEditable.type === 'checkbox') cur = depEditable.checked ? 'true' : 'false';
        else cur = depEditable.value;
        wrap.classList.toggle('hidden', !allowed.includes(cur));
      };
      evaluate();
      depEditable.addEventListener('input', evaluate);
      depEditable.addEventListener('change', evaluate);
    });
  }

  // setArgFieldMode flips the wrap's Fixed/Expression state, updates
  // the toggle buttons + preview, and persists to node data when
  // requested (skipped during initial hydration).
  function setArgFieldMode(wrap, mode, persist) {
    if (!wrap) return;
    wrap.dataset.argMode = mode;
    wrap.querySelectorAll('[data-arg-mode]').forEach((btn) => {
      btn.classList.toggle('is-on', btn.dataset.argMode === mode);
    });
    updateArgPreview(wrap);
    if (persist && selectedID) updateNodeData(selectedID);
  }

  // updateArgPreview rewrites the preview slot beneath one arg field
  // — empty in Fixed mode, rendered template result in Expression
  // mode against the INPUT pane's cached data.
  function updateArgPreview(wrap) {
    const preview = wrap.querySelector('[data-arg-preview]');
    if (!preview) return;
    const editable = argEditable(wrap);
    if (!editable || editable.type === 'checkbox' || editable.tagName === 'SELECT') {
      preview.textContent = '';
      preview.classList.remove('wf-arg-preview-active');
      return;
    }
    const tpl = editable.value || '';
    if (tpl === '' || wrap.dataset.argMode !== 'expression') {
      preview.textContent = '';
      preview.classList.remove('wf-arg-preview-active');
      return;
    }
    preview.textContent = '→ ' + renderTemplatePreview(tpl);
    preview.classList.add('wf-arg-preview-active');
  }

  // hydratePromptModeToggle wires the Fixed/Expression toggle that
  // wraps the agent/classify ins-prompt textarea. Idempotent: rebinds
  // each time the inspector opens for a different node so click
  // handlers don't accumulate.
  function hydratePromptModeToggle(inner) {
    const wrap = document.querySelector('#ins-prompt-panel .wf-arg-field');
    if (!wrap) return;
    const initial = (inner && inner.__arg_modes && inner.__arg_modes.prompt) || 'fixed';
    setArgFieldMode(wrap, initial, /*persist*/ false);
    wrap.querySelectorAll('[data-arg-mode]').forEach((btn) => {
      const fresh = btn.cloneNode(true);
      btn.parentNode.replaceChild(fresh, btn);
      fresh.addEventListener('click', () => setArgFieldMode(wrap, fresh.dataset.argMode, true));
    });
    const ta = wrap.querySelector('textarea');
    if (ta) {
      ta.addEventListener('input', () => {
        if (wrap.dataset.argMode !== 'expression' && ta.value.indexOf('{{') >= 0) {
          setArgFieldMode(wrap, 'expression', true);
        }
        updateArgPreview(wrap);
      });
    }
    updateArgPreview(wrap);
  }

  // collectArgs scans all wf-arg-field wrappers and returns the value
  // map the codec persists under node.data.args. Checkbox value is
  // emitted as "true"/"false" string to match how Go templates and
  // engine helpers read it. Empty strings are skipped so YAML
  // omitempty kicks in.
  function collectArgs(container) {
    const out = {};
    if (!container) return out;
    container.querySelectorAll('.wf-arg-field').forEach((wrap) => {
      const editable = argEditable(wrap);
      if (!editable) return;
      const k = wrap.dataset.fieldKey || editable.dataset.fieldKey;
      if (!k) return;
      let v;
      if (editable.type === 'checkbox') {
        v = editable.checked ? 'true' : 'false';
      } else if (editable.type === 'hidden' && editable.closest('.wf-picker')) {
        // Picker stores JSON string — parse to native array so canvas state
        // and downstream YAML use native format, not escaped JSON string.
        try {
          const arr = JSON.parse(editable.value || '[]');
          v = arr.length > 0 ? arr : '';
        } catch (_) { v = editable.value; }
      } else {
        v = editable.value;
      }
      if (v !== '' && !(Array.isArray(v) && v.length === 0)) out[k] = v;
    });
    return out;
  }

  // collectArgModes reads the persisted Fixed/Expression mode for
  // each arg from the wrapper's dataset. updateNodeData stores this
  // under `__arg_modes` so future inspector opens restore the toggle.
  function collectArgModes(container) {
    const out = {};
    if (!container) return out;
    container.querySelectorAll('.wf-arg-field').forEach((wrap) => {
      const k = wrap.dataset.fieldKey;
      const m = wrap.dataset.argMode;
      if (k && m) out[k] = m;
    });
    return out;
  }

  // Expose the args-form helpers so per-node modules (WickNodes) can
  // reuse the Fixed/Expression chrome + value collection without
  // duplicating the wiring. Used by /static/nodes/http/inspector.js.
  window.wickEditorHelpers = window.wickEditorHelpers || {};
  window.wickEditorHelpers.hydrateArgsForm = hydrateArgsForm;
  window.wickEditorHelpers.collectArgs = collectArgs;
  window.wickEditorHelpers.collectArgModes = collectArgModes;
  window.wickEditorHelpers.setArgFieldMode = setArgFieldMode;

  // attachTemplateDropTarget wires an input/textarea as a drop target
  // for the INPUT pane's draggable JSON leaves. On drop, inserts the
  // template ref (`{{.Event.Payload.text}}` style) at the current
  // cursor position. onTemplateDrop fires after the value is updated
  // so callers can flip the arg into Expression mode automatically.
  function attachTemplateDropTarget(el, onTemplateDrop) {
    el.addEventListener('dragover', (e) => {
      const tpl = e.dataTransfer.types.includes('text/plain');
      if (tpl) {
        e.preventDefault();
        e.dataTransfer.dropEffect = 'copy';
        el.classList.add('wf-arg-input-drop');
      }
    });
    el.addEventListener('dragleave', () => el.classList.remove('wf-arg-input-drop'));
    el.addEventListener('drop', (e) => {
      e.preventDefault();
      el.classList.remove('wf-arg-input-drop');
      const text = e.dataTransfer.getData('text/plain');
      if (!text) return;
      const start = el.selectionStart ?? el.value.length;
      const end = el.selectionEnd ?? el.value.length;
      el.value = el.value.slice(0, start) + text + el.value.slice(end);
      const caret = start + text.length;
      try { el.setSelectionRange(caret, caret); } catch (_) {}
      el.focus();
      el.dispatchEvent(new Event('input', { bubbles: true }));
      if (typeof onTemplateDrop === 'function') onTemplateDrop();
    });
  }

  function refreshConnArgs(currentArgs, modes) {
    if (!connArgsEl) return;
    const mod = registry.connectors.find((m) => m.module === f.module?.value);
    const op = mod?.ops.find((o) => o.id === f.connOp?.value);
    hydrateArgsForm(connArgsEl, op?.args_html || '', currentArgs || {}, modes || {}, mod?.module || '');
  }
  function refreshChannelArgs(currentArgs, modes) {
    if (!chanArgsEl) return;
    const ch = registry.channels.find((c) => c.name === f.channel?.value);
    const op = ch?.ops.find((o) => o.id === f.op?.value);
    // Channels don't ship args_html yet (their WorkflowActionSpec
    // input shape predates the wick-tag form). Fall back to the
    // free-text JSON textarea until they migrate to entity.Config.
    if (!op?.args_html) {
      chanArgsEl.innerHTML = '';
      const ta = document.createElement('textarea');
      ta.id = 'ins-channel-args-json';
      ta.rows = 3;
      ta.placeholder = '{"key":"value"}';
      ta.value = currentArgs ? JSON.stringify(currentArgs, null, 2) : '';
      ta.className = 'w-full bg-white-100 dark:bg-navy-700 border border-white-300 dark:border-navy-600 rounded-lg p-2 font-mono text-xs';
      ta.addEventListener('input', () => { if (selectedID) updateNodeData(selectedID); });
      chanArgsEl.appendChild(ta);
      return;
    }
    hydrateArgsForm(chanArgsEl, op.args_html, currentArgs || {}, modes || {});
  }

  // Delegated change listener — installed once on document.body so it
  // catches every .wf-arg-field edit regardless of which container
  // wraps it (connector args, channel args, trigger match form, future
  // node panels). Per-container attachment used to miss the trigger
  // match panel which lives outside both ins-conn-args and
  // ins-channel-args. The arg-field wrapper class is unique enough to
  // scope safely.
  const editableSelector = 'input:not([type="hidden"]), select, textarea';
  function deliverArgEdit(e) {
    if (!e.target.matches(editableSelector)) return;
    if (!e.target.closest('.wf-arg-field')) return;
    if (selectedID) updateNodeData(selectedID);
  }
  // Picker widgets store their value on a hidden input and fire input+change
  // from it. The selector above excludes hidden inputs, so we need a separate
  // handler to catch picker mutations and persist them.
  function deliverPickerEdit(e) {
    if (e.target.type !== 'hidden') return;
    if (!e.target.dataset.fieldKey) return;
    if (!e.target.closest('.wf-arg-field')) return;
    if (selectedID) updateNodeData(selectedID);
  }
  document.body.addEventListener('input', deliverArgEdit);
  document.body.addEventListener('change', deliverArgEdit);
  document.body.addEventListener('input', deliverPickerEdit);
  document.body.addEventListener('change', deliverPickerEdit);

  // Cascade refresh when parent picker changes.
  f.channel?.addEventListener('change', () => { hydrateChannelOps(); refreshChannelArgs(); if (selectedID) updateNodeData(selectedID); });
  f.module?.addEventListener('change', () => { hydrateConnectorOps(); refreshConnArgs(); if (selectedID) updateNodeData(selectedID); });
  f.op?.addEventListener('change', () => { refreshChannelArgs(); if (selectedID) updateNodeData(selectedID); });
  f.connOp?.addEventListener('change', () => { refreshConnArgs(); if (selectedID) updateNodeData(selectedID); });
  const panels = {
    prompt: document.getElementById('ins-prompt-panel'),
    cases: document.getElementById('ins-cases-panel'),
    preset: document.getElementById('ins-preset-panel'),
    command: document.getElementById('ins-command-panel'),
    transform: document.getElementById('ins-transform-panel'),
    channel: document.getElementById('ins-channel-panel'),
    connector: document.getElementById('ins-connector-panel'),
    trigger: document.getElementById('ins-trigger-panel'),
    agentSession: document.getElementById('ins-agent-session-panel'),
  };
  // Hand control of any wf-inspector-panel block to the per-node JS
  // module that owns it (see internal/tools/agents/workflow/nodes/<type>/inspector.js).
  // editor.js only handles the show/hide dispatch by data-node-type;
  // hydrate/save/onDrop hooks live in window.WickNodes[type].
  function nodeModule(kind) {
    return (window.WickNodes && window.WickNodes[kind]) || null;
  }
  function showModulePanelFor(kind) {
    document.querySelectorAll('.wf-inspector-panel').forEach((el) => {
      el.classList.toggle('hidden', el.dataset.nodeType !== kind);
    });
  }
  // attach() on each module wires DOM listeners once at boot (regen
  // buttons, mode dropdowns, etc.) so the dispatcher doesn't repeat
  // them. requestUpdate flushes the inspector → node data on demand.
  Object.values(window.WickNodes || {}).forEach((mod) => {
    if (mod && typeof mod.attach === 'function') {
      mod.attach({ requestUpdate: () => { if (selectedID) updateNodeData(selectedID); } });
    }
  });
  let selectedID = null;

  // Single click on a node only tracks selection — it does NOT open
  // the modal. Modal opens on double-click or right-click (matches
  // n8n's interaction model: clicking nodes to move/connect them
  // shouldn't pop a heavy debug shell every time).
  editor.on('nodeSelected', (id) => { selectedID = id; });
  editor.on('nodeUnselected', () => {
    // Drawflow fires nodeUnselected the moment focus leaves the canvas
    // — including when the user clicks into an inspector field. That
    // would null out selectedID and silently disable every typing
    // update listener (which all guard with `if (selectedID)`), so the
    // form save / auto-save would serialise the node with its
    // pre-edit data. Keep selectedID pinned while the inspector modal
    // is visible; the explicit hideInspector() callers reset it.
    const modal = document.getElementById('wf-inspector');
    if (modal && !modal.classList.contains('hidden')) {
      return;
    }
    selectedID = null;
  });
  editor.on('nodeRemoved', () => { selectedID = null; hideInspector(); refreshOutputRefs(); });
  editor.on('connectionCreated', () => refreshOutputRefs());

  // Open inspector on double-click. Drawflow doesn't expose a
  // dblclick event but every node lives at #node-<numeric-id>, so we
  // delegate on the canvas wrapper and pull the id off the closest
  // .drawflow-node ancestor.
  canvasEl.addEventListener('dblclick', (e) => {
    const nodeEl = e.target.closest('.drawflow-node');
    if (!nodeEl) return;
    const id = nodeEl.id.replace(/^node-/, '');
    if (!id) return;
    selectedID = id;
    showInspectorFor(id);
  });
  // Right-click on a node also opens the inspector. Suppress the
  // native browser context menu so the gesture stays clean.
  canvasEl.addEventListener('contextmenu', (e) => {
    const nodeEl = e.target.closest('.drawflow-node');
    if (!nodeEl) return;
    e.preventDefault();
    const id = nodeEl.id.replace(/^node-/, '');
    if (!id) return;
    selectedID = id;
    showInspectorFor(id);
  });

  Object.values(f).forEach((el) => {
    if (!el || el === f.cases || el === f.refs) return;
    el.addEventListener('input', () => { if (selectedID) updateNodeData(selectedID); });
  });
  // session_init + agent session controls live outside the `f`
  // object (defined ad-hoc inside the modal), so they need explicit
  // listeners to flush their state into node data on every edit.
  // Agent override controls live in editor_inspector.templ (not
  // migrated to a per-node module yet); wire their input/change
  // listeners here.
  ['ins-agent-session', 'ins-agent-session-from',
   'ins-transform-engine', 'ins-transform-expression'].forEach((id) => {
    const el = document.getElementById(id);
    if (!el) return;
    el.addEventListener('input', () => { if (selectedID) updateNodeData(selectedID); });
    el.addEventListener('change', () => { if (selectedID) updateNodeData(selectedID); });
  });
  // Label input — validate identifier + uniqueness on every keystroke
  // so the operator sees the constraint without surprise rejection on
  // save. Invalid labels paint a red ring; the save still goes through
  // (no enforcement) but cascade rewrite is skipped to avoid corrupting
  // refs with spaces or duplicate labels.
  if (f.label) {
    f.label.addEventListener('input', () => {
      const v = f.label.value.trim();
      const ok = !v || (isValidIdent(v) && !labelTakenByOther(v, selectedID));
      f.label.classList.toggle('wf-input-error', !ok);
      f.label.title = ok ? '' : 'Label must be a valid identifier (letters/digits/_, no spaces) and unique';
      if (selectedID) updateNodeData(selectedID);
    });
  }
  // Per-node modules register their own input/change listeners via
  // attach() above — no global wiring needed for session_init or any
  // future module that owns its own inspector partial. We also
  // delegate input events at the document level so dynamically
  // rendered inputs inside module panels still trigger updates.
  document.addEventListener('input', (e) => {
    const panel = e.target.closest('.wf-inspector-panel');
    if (panel && selectedID) updateNodeData(selectedID);
  });
  document.addEventListener('change', (e) => {
    const panel = e.target.closest('.wf-inspector-panel');
    if (panel && selectedID) updateNodeData(selectedID);
  });

  document.getElementById('ins-add-case').addEventListener('click', () => {
    if (!selectedID) return;
    appendCaseRow('', '');
    persistCases(selectedID);
  });
  document.getElementById('ins-delete').addEventListener('click', async () => {
    if (!selectedID) return;
    if (canvasLocked) {
      await wickAlert('Unlock the canvas to delete nodes.', { title: 'Canvas locked' });
      return;
    }
    const ok = await wickConfirm('Delete this node?', { title: 'Delete node', ok: 'Delete', danger: true });
    if (!ok) return;
    editor.removeNodeId('node-' + selectedID);
  });

  // ── Zoom controls ──────────────────────────────────────────────
  // fitToView centres + scales every node into the viewport with a
  // small padding ring. Clamp range keeps the graph readable: huge
  // graphs floor at drawflow's zoom_min (0.5x) so nodes stay legible
  // even when the workflow grows wide; small graphs cap at 1.0x so a
  // single node doesn't blow up to fill the whole canvas.
  function fitToView() {
    if (!editor.precanvas) return;
    const graph = editor.drawflow.drawflow.Home.data;
    const ids = Object.keys(graph);
    const vw = canvasEl.clientWidth || 800;
    const vh = canvasEl.clientHeight || 600;
    if (ids.length === 0) {
      editor.zoom = 1;
      editor.zoom_last_value = 1;
      editor.canvas_x = 0;
      editor.canvas_y = 0;
      editor.precanvas.style.transform = 'translate(0px, 0px) scale(1)';
      return;
    }
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    ids.forEach((id) => {
      const n = graph[id];
      if (!n) return;
      // offsetWidth/Height are layout-box pixels — unaffected by the
      // precanvas CSS transform, so we get true unscaled dimensions
      // even when the page loads with leftover zoom state.
      const el = document.getElementById('node-' + id);
      const w = el ? el.offsetWidth : 200;
      const h = el ? el.offsetHeight : 120;
      if (n.pos_x < minX) minX = n.pos_x;
      if (n.pos_y < minY) minY = n.pos_y;
      if (n.pos_x + w > maxX) maxX = n.pos_x + w;
      if (n.pos_y + h > maxY) maxY = n.pos_y + h;
    });
    const bboxW = Math.max(1, maxX - minX);
    const bboxH = Math.max(1, maxY - minY);
    const pad = 80;
    const fitZoom = Math.min((vw - pad * 2) / bboxW, (vh - pad * 2) / bboxH);
    const zMin = editor.zoom_min || 0.5;
    const zMax = 1.0;
    let z = fitZoom;
    if (z > zMax) z = zMax;
    if (z < zMin) z = zMin;
    const bcx = (minX + maxX) / 2;
    const bcy = (minY + maxY) / 2;
    editor.zoom = z;
    editor.zoom_last_value = z;
    editor.canvas_x = vw / 2 - bcx * z;
    editor.canvas_y = vh / 2 - bcy * z;
    editor.precanvas.style.transform = `translate(${editor.canvas_x}px, ${editor.canvas_y}px) scale(${z})`;
    // Re-fire drawflow's zoom dispatch so any zoom listeners (edge
    // arrow refresh, etc.) re-evaluate against the new scale.
    editor.dispatch('zoom', z);
  }

  document.getElementById('wf-zoom-in').addEventListener('click', () => editor.zoom_in());
  document.getElementById('wf-zoom-out').addEventListener('click', () => editor.zoom_out());
  document.getElementById('wf-zoom-reset').addEventListener('click', fitToView);

  // ── Lock toggle ────────────────────────────────────────────────
  // Drawflow's stock 'fixed' mode disables node-drag/delete/connect
  // but ALSO swallows clicks on .drawflow-node — so the inspector
  // can't be opened by clicking a locked node. Re-wire selection
  // manually: when locked, intercept clicks on .drawflow-node and
  // dispatch nodeSelected ourselves to keep the inspector reactive.
  const LOCK_KEY = 'wf-canvas-locked';
  let canvasLocked = localStorage.getItem(LOCK_KEY) === '1';
  const lockBtn = document.getElementById('wf-lock');
  function applyLockState() {
    editor.editor_mode = canvasLocked ? 'fixed' : 'edit';
    if (lockBtn) {
      lockBtn.classList.toggle('is-on', canvasLocked);
      lockBtn.setAttribute('aria-pressed', canvasLocked ? 'true' : 'false');
      lockBtn.title = canvasLocked ? 'Canvas locked — click to unlock' : 'Lock canvas — disable node edits';
    }
    canvasEl.classList.toggle('wf-canvas-locked', canvasLocked);
  }
  applyLockState();
  if (lockBtn) {
    lockBtn.addEventListener('click', () => {
      canvasLocked = !canvasLocked;
      localStorage.setItem(LOCK_KEY, canvasLocked ? '1' : '0');
      applyLockState();
    });
  }
  // Manual selection while locked. Drawflow's click handler returns
  // early in fixed mode (only parent-drawflow clicks count, for
  // pan-drag start). Replicate the selection dispatch the editor
  // would normally emit so nodeSelected listeners (inspector) still
  // fire.
  canvasEl.addEventListener('click', (e) => {
    if (!canvasLocked) return;
    const nodeEl = e.target.closest('.drawflow-node');
    if (!nodeEl) {
      // Click on empty canvas while locked → clear selection.
      if (editor.node_selected) {
        editor.node_selected.classList.remove('selected');
        editor.node_selected = null;
        editor.dispatch('nodeUnselected', true);
      }
      return;
    }
    if (editor.node_selected === nodeEl) return;
    if (editor.node_selected) editor.node_selected.classList.remove('selected');
    editor.node_selected = nodeEl;
    nodeEl.classList.add('selected');
    editor.dispatch('nodeSelected', nodeEl.id.slice(5));
  });

  // ── Figma-style scroll/trackpad pan ─────────────────────────────
  // Default browser behaviour for a wheel event over the canvas is
  // to scroll the surrounding page. Two-finger trackpad scrolls and
  // mouse-wheel ticks should pan the canvas instead — matching the
  // Figma/Miro/excalidraw convention. Ctrl+wheel (or trackpad pinch,
  // which the browser reports as ctrlKey-true) falls through to
  // drawflow's existing zoom_enter for zoom.
  //
  // Pan math: subtract the raw delta from canvas_x/canvas_y, then
  // restamp the transform drawflow already maintains on precanvas.
  // We bypass editor.zoom_refresh so the canvas_x/canvas_y/zoom
  // accounting drawflow does on zoom transitions stays intact.
  canvasEl.addEventListener('wheel', (e) => {
    if (e.ctrlKey || e.metaKey) return; // let drawflow / browser handle zoom
    if (!editor.precanvas) return;
    e.preventDefault();
    // Normalize Firefox line mode to ~16px per line. deltaMode 0 =
    // pixels (default in chromium/edge/trackpads), 1 = lines, 2 =
    // pages. Page mode is rare; treat as a big chunk.
    let dx = e.deltaX;
    let dy = e.deltaY;
    if (e.deltaMode === 1) { dx *= 16; dy *= 16; }
    else if (e.deltaMode === 2) { dx *= canvasEl.clientWidth; dy *= canvasEl.clientHeight; }
    editor.canvas_x -= dx;
    editor.canvas_y -= dy;
    editor.precanvas.style.transform = `translate(${editor.canvas_x}px, ${editor.canvas_y}px) scale(${editor.zoom})`;
  }, { passive: false });

  // ── Marquee box-select + multi-drag ─────────────────────────────
  // Default Drawflow behaviour: drag on empty canvas pans the
  // viewport. We replace that with a Figma-style marquee — drag on
  // empty space paints a selection rectangle and every node it
  // intersects joins the "multi-selection" set. Once 2+ nodes are
  // multi-selected:
  //   - Drag any one node → all selected nodes move together (delta
  //     applies to each).
  //   - Delete / Backspace → all selected nodes removed in one shot.
  //   - Shift-click a node → toggle membership without dropping the
  //     rest of the set.
  // Pan stays on scroll/trackpad only (handled by the wheel listener
  // above) so removing drag-pan doesn't strand operators on a long
  // graph.
  //
  // We preempt drawflow's mousedown via a capture-phase listener on
  // canvasEl and call stopImmediatePropagation so drawflow's bubble-
  // phase handler never sees the event when it lands on empty
  // canvas. Node-targeted clicks fall through unchanged (drawflow
  // still handles single-node drag, port hover, connection draw).
  const multiSelected = new Set();

  function addToMultiSelection(id) {
    if (!id) return;
    multiSelected.add(id);
    const el = document.getElementById('node-' + id);
    if (el) el.classList.add('wf-multi-selected');
  }
  function removeFromMultiSelection(id) {
    if (!id) return;
    multiSelected.delete(id);
    const el = document.getElementById('node-' + id);
    if (el) el.classList.remove('wf-multi-selected');
  }
  function clearMultiSelection() {
    multiSelected.forEach((id) => {
      const el = document.getElementById('node-' + id);
      if (el) el.classList.remove('wf-multi-selected');
    });
    multiSelected.clear();
  }

  // isCanvasBackground returns true for clicks that should start a
  // marquee — anywhere inside the canvas wrap that isn't a node,
  // port, or connection line.
  function isCanvasBackground(el) {
    if (!el) return false;
    if (el.closest('.drawflow-node')) return false;
    if (el.closest('.input') || el.closest('.output')) return false;
    if (el.closest('svg.connection')) return false;
    return !!el.closest('#wf-canvas');
  }

  canvasEl.addEventListener('mousedown', (e) => {
    if (canvasLocked) return;
    if (e.button !== 0) return; // primary button only
    const nodeEl = e.target.closest('.drawflow-node');
    if (nodeEl) {
      const nodeID = nodeEl.id.replace(/^node-/, '');
      // Don't intercept drags that start on a port — drawflow needs
      // those to draw a new connection.
      if (e.target.closest('.input') || e.target.closest('.output')) {
        return;
      }
      // Shift-click toggles membership without starting a drag.
      if (e.shiftKey) {
        e.stopImmediatePropagation();
        e.preventDefault();
        if (multiSelected.has(nodeID)) removeFromMultiSelection(nodeID);
        else addToMultiSelection(nodeID);
        return;
      }
      // Mousedown on a node inside an existing multi-selection →
      // take over from drawflow and move the whole group.
      if (multiSelected.size > 1 && multiSelected.has(nodeID)) {
        e.stopImmediatePropagation();
        e.preventDefault();
        beginMultiDrag(e);
        return;
      }
      // Plain click on a node outside the multi-set — drop the
      // multi-selection so the new node becomes the lone target,
      // then let drawflow handle the single-node drag.
      clearMultiSelection();
      return;
    }
    if (!isCanvasBackground(e.target)) return;
    e.stopImmediatePropagation();
    e.preventDefault();
    beginMarquee(e);
  }, true); // capture phase — fire before drawflow's bubble listener

  function beginMarquee(e) {
    const startX = e.clientX;
    const startY = e.clientY;
    const additive = e.shiftKey;
    if (!additive) clearMultiSelection();
    const overlay = document.createElement('div');
    overlay.className = 'wf-marquee';
    overlay.style.left = startX + 'px';
    overlay.style.top = startY + 'px';
    overlay.style.width = '0px';
    overlay.style.height = '0px';
    document.body.appendChild(overlay);
    const onMove = (ev) => {
      const x = ev.clientX;
      const y = ev.clientY;
      const left = Math.min(startX, x);
      const top = Math.min(startY, y);
      overlay.style.left = left + 'px';
      overlay.style.top = top + 'px';
      overlay.style.width = Math.abs(x - startX) + 'px';
      overlay.style.height = Math.abs(y - startY) + 'px';
    };
    const onUp = () => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      const mrect = overlay.getBoundingClientRect();
      const drag = mrect.width > 2 || mrect.height > 2;
      if (drag) {
        // Use DOM rects — drawflow paints each node inside the
        // zoomed/panned precanvas, so getBoundingClientRect already
        // accounts for both. No need to convert back to flow coords.
        document.querySelectorAll('#wf-canvas .drawflow-node').forEach((el) => {
          const r = el.getBoundingClientRect();
          const hit = !(r.right < mrect.left || r.left > mrect.right || r.bottom < mrect.top || r.top > mrect.bottom);
          if (hit) addToMultiSelection(el.id.replace(/^node-/, ''));
        });
      } else if (!additive) {
        // Click on empty canvas (no real drag) — already cleared on
        // mousedown. Nothing more to do.
      }
      overlay.remove();
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  }

  function beginMultiDrag(e) {
    // Snapshot the starting pos for every multi-selected node so we
    // can translate each one by the same delta. Drawflow stores
    // pos_x/pos_y in flow space (unscaled by zoom); the inline
    // style.top/style.left mirror those values in CSS pixels and
    // get re-applied via the precanvas's CSS transform for visual
    // zoom. Hence we divide screen-space delta by zoom before
    // applying.
    const graph = editor.drawflow.drawflow.Home.data;
    const start = new Map();
    multiSelected.forEach((id) => {
      const n = graph[id];
      if (!n) return;
      start.set(id, { x: n.pos_x, y: n.pos_y });
    });
    const startX = e.clientX;
    const startY = e.clientY;
    const z = editor.zoom || 1;
    const onMove = (ev) => {
      const dx = (ev.clientX - startX) / z;
      const dy = (ev.clientY - startY) / z;
      start.forEach((p, id) => {
        const el = document.getElementById('node-' + id);
        if (!el) return;
        el.style.left = (p.x + dx) + 'px';
        el.style.top = (p.y + dy) + 'px';
        // Re-render connections live so edges follow each node card.
        editor.updateConnectionNodes('node-' + id);
      });
    };
    const onUp = (ev) => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      const dx = (ev.clientX - startX) / z;
      const dy = (ev.clientY - startY) / z;
      let changed = false;
      start.forEach((p, id) => {
        const n = graph[id];
        if (!n) return;
        const nx = p.x + dx;
        const ny = p.y + dy;
        if (n.pos_x !== nx || n.pos_y !== ny) changed = true;
        n.pos_x = nx;
        n.pos_y = ny;
      });
      if (changed) scheduleAutoSave();
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  }

  // Delete / Backspace removes every node in the multi-selection
  // (Drawflow's default key handler only deletes its single
  // node_selected). We gate on focus so typing in inspector inputs
  // doesn't blow away nodes mid-edit.
  document.addEventListener('keydown', async (e) => {
    if (canvasLocked) return;
    if (multiSelected.size === 0) return;
    if (e.key !== 'Delete' && !(e.key === 'Backspace' && (e.metaKey || e.ctrlKey))) return;
    const ae = document.activeElement;
    if (ae && (ae.tagName === 'INPUT' || ae.tagName === 'TEXTAREA' || ae.isContentEditable)) return;
    e.preventDefault();
    const count = multiSelected.size;
    const ok = await wickConfirm(`Delete ${count} node${count > 1 ? 's' : ''}?`, { title: 'Delete nodes', ok: 'Delete', danger: true });
    if (!ok) return;
    const ids = Array.from(multiSelected);
    clearMultiSelection();
    ids.forEach((id) => editor.removeNodeId('node-' + id));
  });

  // Single-node drawflow selection drops the multi-set (the click
  // path that lands on a node outside the multi already calls
  // clearMultiSelection in the capture handler — this catches edge
  // cases like programmatic selection from inspector deep links).
  editor.on('nodeSelected', (id) => {
    if (!multiSelected.has(id)) clearMultiSelection();
  });

  // ── Auto-layout ──────────────────────────────────────────────
  // Layered left→right layout via topological ranks. Roots (no
  // incoming edges) sit in column 0; each successor sits at
  // max(parent_rank)+1. Nodes in the same column are stacked
  // vertically. Cycles fall back to row-major order.
  const layoutBtn = document.getElementById('wf-layout');
  if (layoutBtn) layoutBtn.addEventListener('click', () => { autoLayout(); scheduleAutoSave(); });

  function autoLayout() {
    const graph = editor.export().drawflow.Home.data;
    const ids = Object.keys(graph);
    if (ids.length === 0) return;
    // Build adjacency.
    const indeg = {};
    const succ = {};
    ids.forEach((id) => { indeg[id] = 0; succ[id] = []; });
    ids.forEach((id) => {
      const n = graph[id];
      const outputs = n.outputs || {};
      Object.values(outputs).forEach((slot) => {
        (slot.connections || []).forEach((c) => {
          const tgt = String(c.node);
          if (!(tgt in indeg)) return;
          succ[id].push(tgt);
          indeg[tgt] = (indeg[tgt] || 0) + 1;
        });
      });
    });
    // Kahn's algorithm — collect ranks.
    const rank = {};
    let frontier = ids.filter((id) => indeg[id] === 0);
    let cur = 0;
    const remaining = new Set(ids);
    while (frontier.length && cur < ids.length + 1) {
      const next = [];
      frontier.forEach((id) => {
        if (!remaining.has(id)) return;
        rank[id] = cur;
        remaining.delete(id);
        succ[id].forEach((s) => {
          indeg[s]--;
          if (indeg[s] === 0) next.push(s);
        });
      });
      frontier = next;
      cur++;
    }
    // Cycle nodes — append at the next column row-major.
    Array.from(remaining).forEach((id, i) => { rank[id] = cur + Math.floor(i / 5); });
    // Group + position.
    const columns = {};
    ids.forEach((id) => { (columns[rank[id]] = columns[rank[id]] || []).push(id); });
    // Top→bottom layout. Rank = row (Y axis); siblings spread on X.
    // Ports are CSS-repositioned to top/bottom; createCurvature is
    // monkey-patched to emit vertical-tangent control points so the
    // bezier flows down cleanly.
    const colWidth = 280;
    const rowHeight = 200;
    const originX = 420;
    const originY = 60;
    // Mutate the live graph then re-import to force Drawflow to redraw
    // every node + edge from the new positions. Cleaner than poking
    // updateConnectionNodes (which sometimes leaves stale bezier
    // endpoints when stroked off-DOM).
    const live = editor.drawflow.drawflow.Home.data;
    // Rank = row (Y); siblings spread horizontally centered on originX.
    Object.keys(columns).map(Number).sort((a, b) => a - b).forEach((r) => {
      const siblings = columns[r];
      const rowStartX = originX - ((siblings.length - 1) * colWidth) / 2;
      siblings.forEach((id, idx) => {
        const x = rowStartX + idx * colWidth;
        const y = originY + r * rowHeight;
        if (live[id]) {
          live[id].pos_x = x;
          live[id].pos_y = y;
        }
      });
    });
    const snapshot = editor.export();
    editor.clear();
    editor.import(snapshot);
    // Defs vanish on import; re-inject after the new SVGs settle.
    setTimeout(injectArrowsIntoEdges, 0);
    // Force-save immediately. clear()+import() fires many node events
    // that each schedule an autosave; the debounce coalesces them but
    // the final state may be exported before the DOM finishes settling
    // if we rely on the schedule alone. Save explicitly with the
    // post-layout snapshot so the draft yaml gets the new positions.
    setTimeout(autoSave, 50);
  }

  // ── Bottom tab toggle + collapse ───────────────────────────────
  const bottomBody = document.getElementById('wf-bottom-body');
  const bottomToggle = document.getElementById('wf-bottom-toggle');
  document.querySelectorAll('[data-bottom-tab]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const key = btn.dataset.bottomTab;
      document.querySelectorAll('[data-bottom-tab]').forEach((b) => {
        const on = b === btn;
        b.classList.toggle('border-green-500', on);
        b.classList.toggle('text-green-700', on);
        b.classList.toggle('dark:text-green-400', on);
        b.classList.toggle('border-b-2', on);
        b.classList.toggle('font-medium', on);
      });
      document.querySelectorAll('[data-bottom-panel]').forEach((p) => {
        p.classList.toggle('hidden', p.dataset.bottomPanel !== key);
      });
      // Auto-expand body when a tab is clicked.
      if (bottomBody && bottomBody.classList.contains('hidden')) {
        bottomBody.classList.remove('hidden');
        if (bottomToggle) {
          bottomToggle.textContent = '▾ collapse';
          bottomToggle.dataset.collapsed = 'false';
        }
      }
    });
  });
  if (bottomToggle) {
    bottomToggle.addEventListener('click', () => {
      const collapsed = bottomBody.classList.toggle('hidden');
      bottomToggle.textContent = collapsed ? '▴ expand' : '▾ collapse';
      bottomToggle.dataset.collapsed = collapsed ? 'true' : 'false';
    });
  }

  // ── Test Case Manager ───────────────────────────────────────────
  // Tests tab click → load manager panel (GET /test-cases)
  const testsBtn = document.querySelector('[data-bottom-tab="tests"]');
  if (testsBtn) {
    testsBtn.addEventListener('click', () => {
      const target = document.getElementById('wf-test-results');
      if (!target) return;
      const base = document.getElementById('wf-tc-modal')?.dataset.base || '';
      const id = document.getElementById('wf-tc-modal')?.dataset.id || '';
      if (!base || !id) return;
      target.innerHTML = '<span class="p-3 italic text-xs text-black-600 dark:text-black-700">Loading…</span>';
      fetch(`${base}/workflows/edit/${id}/test-cases`)
        .then(r => r.text())
        .then(html => { target.innerHTML = html; bindTestManager(base, id); })
        .catch(err => { target.innerHTML = `<span class="text-red-600 text-xs p-3">${err.message}</span>`; });
    });
  }

  // Run All (delegated — button is injected dynamically)
  document.getElementById('wf-test-results')?.addEventListener('click', (e) => {
    const runAll = e.target.closest('[data-wf-tc-run-all]');
    if (runAll) {
      const url = runAll.dataset.wfTcRunAll;
      const target = document.getElementById('wf-test-results');
      target.innerHTML = '<span class="p-3 italic text-xs text-black-600 dark:text-black-700">Running all tests…</span>';
      fetch(url, { method: 'POST' })
        .then(r => r.text())
        .then(html => { target.innerHTML = html; })
        .catch(err => { target.innerHTML = `<span class="text-red-600 text-xs p-3">${err.message}</span>`; });
    }
  });

  function bindTestManager(base, id) {
    const panel = document.getElementById('wf-test-results');
    if (!panel) return;

    // + New button
    panel.querySelector('[data-wf-tc-new]')?.addEventListener('click', () => openTCModal());

    // Run single (delegated)
    panel.addEventListener('click', (e) => {
      const btn = e.target.closest('[data-wf-tc-run]');
      if (!btn) return;
      const url = btn.dataset.wfTcRun;
      const rowKey = btn.dataset.wfTcRow;
      const row = document.getElementById('wf-tc-row-' + rowKey);
      if (row) row.style.opacity = '0.5';
      fetch(url, { method: 'POST' })
        .then(r => r.text())
        .then(html => {
          if (row) row.outerHTML = html;
        })
        .catch(err => { if (row) row.style.opacity = '1'; console.warn(err); });
    });

    // Edit (delegated)
    panel.addEventListener('click', (e) => {
      const btn = e.target.closest('[data-wf-tc-edit]');
      if (!btn) return;
      const name = btn.dataset.wfTcEdit;
      let tc = {};
      try { tc = JSON.parse(btn.dataset.wfTcJson || '{}'); } catch (_) {}
      openTCModal(name, tc);
    });

    // Delete (delegated)
    panel.addEventListener('click', (e) => {
      const btn = e.target.closest('[data-wf-tc-delete]');
      if (!btn) return;
      const rowKey = btn.dataset.wfTcRow;
      if (!confirm(`Delete test case "${btn.dataset.wfTcDelete.split('/').pop()}"?`)) return;
      fetch(btn.dataset.wfTcDelete, { method: 'DELETE' })
        .then(r => r.json())
        .then(d => {
          if (d.ok) {
            const row = document.getElementById('wf-tc-row-' + rowKey);
            if (row) row.remove();
          }
        });
    });
  }

  // ── Test Case Modal ───────────────────────────────────────────────
  const tcModal   = document.getElementById('wf-tc-modal');
  const tcSaveBtn = document.getElementById('wf-tc-save');
  const tcAddAsrt = document.getElementById('wf-tc-add-assertion');
  const tcAsrtBox = document.getElementById('wf-tc-assertions');
  const tcEvtType = document.getElementById('wf-tc-evt-type');
  const tcError   = document.getElementById('wf-tc-error');

  function openTCModal(editName, tc) {
    if (!tcModal) return;
    const isEdit = !!editName;
    document.getElementById('wf-tc-modal-title').textContent = isEdit ? 'Edit Test Case' : 'New Test Case';
    document.getElementById('wf-tc-edit-name').value = editName || '';
    document.getElementById('wf-tc-name').value = editName || '';
    document.getElementById('wf-tc-name').disabled = isEdit;

    // Fill event fields
    const evt = tc?.input?.Event || {};
    if (tcEvtType) tcEvtType.value = evt.type || 'manual';
    const ch = document.getElementById('wf-tc-channel');
    const st = document.getElementById('wf-tc-subtype');
    if (ch) ch.value = evt.channel || 'slack';
    if (st) st.value = evt.subtype || 'message';
    tcUpdateChannelVis();
    const payload = evt.payload || {};
    const ta = document.getElementById('wf-tc-payload');
    if (ta) ta.value = Object.keys(payload).length ? JSON.stringify(payload, null, 2) : '';

    // Fill assertions
    if (tcAsrtBox) {
      tcAsrtBox.innerHTML = '';
      (tc?.assertions || [{ subject: 'status', operator: '==', value: 'completed' }])
        .forEach(a => tcAddAssertionRow(a));
    }
    if (tcError) { tcError.textContent = ''; tcError.classList.add('hidden'); }
    tcModal.classList.remove('hidden');
    document.getElementById('wf-tc-name')?.focus();
  }

  function closeTCModal() { tcModal?.classList.add('hidden'); }

  // Cancel triggers
  tcModal?.querySelectorAll('[data-wf-tc-cancel]').forEach(el =>
    el.addEventListener('click', closeTCModal)
  );
  document.addEventListener('keydown', e => { if (e.key === 'Escape') closeTCModal(); });

  // Channel/subtype visibility
  function tcUpdateChannelVis() {
    const isChannel = tcEvtType?.value === 'channel';
    document.getElementById('wf-tc-channel-col')?.classList.toggle('hidden', !isChannel);
    document.getElementById('wf-tc-subtype-col')?.classList.toggle('hidden', !isChannel);
  }
  tcEvtType?.addEventListener('change', tcUpdateChannelVis);
  tcUpdateChannelVis();

  // Add assertion row
  function tcAddAssertionRow(a) {
    if (!tcAsrtBox) return;
    const row = document.createElement('div');
    row.className = 'wf-tc-assertion-row';
    const ops = ['==','!=','contains','case_fired','node_skipped','path_taken','edge_traversed'];
    const sel = ops.map(o => `<option value="${o}"${a?.operator===o?' selected':''}>${o}</option>`).join('');
    row.innerHTML = `
      <input class="wf-input text-xs" placeholder="status or node.id.field" value="${a?.subject||''}"/>
      <select class="wf-input text-xs">${sel}</select>
      <input class="wf-input text-xs" placeholder="completed" value="${a?.value||''}"/>
      <button type="button" class="text-red-500 hover:text-red-700 text-sm font-bold" data-remove-row>✕</button>`;
    row.querySelector('[data-remove-row]').addEventListener('click', () => row.remove());
    tcAsrtBox.appendChild(row);
  }
  tcAddAsrt?.addEventListener('click', () => tcAddAssertionRow(null));

  // Save
  tcSaveBtn?.addEventListener('click', async () => {
    const base   = tcModal.dataset.base;
    const id   = tcModal.dataset.id;
    const name   = document.getElementById('wf-tc-name').value.trim();
    if (!name) { showTCError('Name is required'); return; }

    let payload = {};
    const rawPayload = document.getElementById('wf-tc-payload').value.trim();
    if (rawPayload) {
      try { payload = JSON.parse(rawPayload); }
      catch (_) { showTCError('Payload is not valid JSON'); return; }
    }

    const assertions = [];
    tcAsrtBox?.querySelectorAll('.wf-tc-assertion-row').forEach(row => {
      const [subj, op, val] = row.querySelectorAll('input, select');
      if (subj.value.trim()) assertions.push({ subject: subj.value.trim(), operator: op.value, value: val.value.trim() });
    });

    const evt = {
      type: tcEvtType?.value || 'manual',
      payload,
    };
    if (evt.type === 'channel') {
      evt.channel = document.getElementById('wf-tc-channel')?.value || 'slack';
      evt.subtype = document.getElementById('wf-tc-subtype')?.value || 'message';
    }

    tcSaveBtn.disabled = true;
    tcSaveBtn.textContent = 'Saving…';
    try {
      const resp = await fetch(`${base}/workflows/edit/${id}/test-cases`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, input: { Event: evt }, assertions }),
      });
      const data = await resp.json();
      if (!resp.ok) { showTCError(data.error || `HTTP ${resp.status}`); return; }
      closeTCModal();
      // Reload the manager panel
      const target = document.getElementById('wf-test-results');
      fetch(`${base}/workflows/edit/${id}/test-cases`)
        .then(r => r.text())
        .then(html => { if (target) { target.innerHTML = html; bindTestManager(base, id); } });
    } catch (err) {
      showTCError(err.message);
    } finally {
      tcSaveBtn.disabled = false;
      tcSaveBtn.textContent = 'Save Test Case';
    }
  });

  function showTCError(msg) {
    if (!tcError) return;
    tcError.textContent = msg;
    tcError.classList.remove('hidden');
  }

  // ── Save: serialize Drawflow → JSON ────────────────────────────
  // Manual click: classic form post (server redirects back).
  document.getElementById('save-form').addEventListener('submit', () => {
    document.getElementById('save-body').value = JSON.stringify(editor.export());
  });

  // Auto-save: debounce 800ms after canvas mutations, POST JSON,
  // surface status inline in the toolbar. Server writes to
  // workflow.draft.yaml — published workflow.yaml untouched until
  // user clicks Publish.
  const statusEl = document.getElementById('wf-save-status');
  // baseURL = `<base>/workflows` (from data-wf-base). So the id-bound
  // path is `${baseURL}/edit/${id}/save`. The registry catalog lives
  // at `${baseURL}/api/registry`.
  const id = window.location.pathname.split('/').filter(Boolean).pop();
  const saveURL = `${baseURL}/edit/${id}/save`;
  let saveTimer = null;
  let lastSavedAt = null;
  let savedRefreshTimer = null;

  function setStatus(state, text) {
    if (!statusEl) return;
    statusEl.dataset.state = state;
    statusEl.textContent = text;
    statusEl.classList.toggle('text-rose-600', state === 'error');
    statusEl.classList.toggle('text-emerald-700', state === 'ok');
    statusEl.classList.toggle('text-amber-600', state === 'saving' || state === 'warn');
  }

  function refreshSavedAge() {
    if (!lastSavedAt) return;
    const ageSec = Math.max(1, Math.round((Date.now() - lastSavedAt) / 1000));
    const label = ageSec < 60 ? `${ageSec}s ago` : `${Math.round(ageSec / 60)}m ago`;
    setStatus('ok', `✓ Saved ${label}`);
  }

  async function autoSave() {
    setStatus('saving', '⟳ Saving…');
    const body = new URLSearchParams();
    body.set('body', JSON.stringify(editor.export()));
    try {
      const resp = await fetch(saveURL, {
        method: 'POST',
        headers: { 'Accept': 'application/json', 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
      });
      let data = null;
      try { data = await resp.json(); } catch (_) {}
      if (!resp.ok) {
        const msg = (data && data.error) || `HTTP ${resp.status}`;
        setStatus('error', `✕ Save failed: ${msg}`);
        applyValidation(null);
        return;
      }
      applyValidation((data && data.validation) || null);
      lastSavedAt = Date.now();
      refreshSavedAge();
      if (savedRefreshTimer) clearInterval(savedRefreshTimer);
      savedRefreshTimer = setInterval(refreshSavedAge, 10000);
    } catch (err) {
      setStatus('error', `✕ Save failed: ${err.message || err}`);
    }
  }

  // applyValidation paints per-node error badges + a toolbar summary.
  // Null clears all badges (used when the save errored out entirely).
  function applyValidation(v) {
    document.querySelectorAll('.drawflow-node').forEach((el) => {
      el.classList.remove('wf-node-error');
      const old = el.querySelector('.wf-error-badge');
      if (old) old.remove();
    });
    if (!v || !v.by_node) return;
    Object.entries(v.by_node).forEach(([nodeID, msgs]) => {
      // Drawflow numeric ids vs workflow string ids — look up by data.id.
      const live = editor.drawflow && editor.drawflow.drawflow.Home && editor.drawflow.drawflow.Home.data;
      if (!live) return;
      let domID = null;
      for (const k in live) {
        if (live[k].data && live[k].data.id === nodeID) { domID = k; break; }
        if (String(k) === nodeID) { domID = k; break; }
      }
      if (!domID) return;
      const el = document.getElementById('node-' + domID);
      if (!el) return;
      el.classList.add('wf-node-error');
      const badge = document.createElement('div');
      badge.className = 'wf-error-badge';
      badge.title = msgs.join('\n');
      badge.textContent = '!';
      el.appendChild(badge);
    });
    // Update toolbar — show counts so the user knows something needs
    // attention without hunting the canvas.
    if (statusEl && !v.ok) {
      const count = (v.errors && v.errors.length) || 0;
      setStatus('warn', `⚠ Saved — ${count} validation issue${count === 1 ? '' : 's'}`);
    }
  }

  // Paint the server-rendered validation report once the editor has
  // settled — applyValidation needs the nodes in the DOM, and import()
  // populates them synchronously above. Without this badges only
  // surfaced after the first auto-save, and disappeared on refresh.
  if (_initialValidation) {
    setTimeout(() => applyValidation(_initialValidation), 0);
  }

  function scheduleAutoSave() {
    if (saveTimer) clearTimeout(saveTimer);
    setStatus('saving', '⟳ Pending…');
    saveTimer = setTimeout(autoSave, 800);
  }

  // Hook every canvas mutation that should persist.
  editor.on('nodeCreated', scheduleAutoSave);
  editor.on('nodeRemoved', scheduleAutoSave);
  editor.on('connectionCreated', scheduleAutoSave);
  editor.on('connectionRemoved', scheduleAutoSave);
  editor.on('nodeMoved', scheduleAutoSave);
  editor.on('nodeDataChanged', scheduleAutoSave);

  // Drawflow's manual drag updates a node's .style.left/top during
  // mousemove but doesn't sync pos_x/pos_y into the live data store
  // until mouseup on some builds, and even then the edge-redraw can
  // skip a frame. Instead of trying to hook every drag event, just
  // run a permanent rAF poll that reconciles DOM → live state and
  // redraws affected edges. Cheap (few nodes, parseFloat, dict
  // compare per frame) and survives whatever event quirks the
  // bundled lib carries.
  // Snap config — node positions round to multiples of GRID (16px is
  // a comfortable default at the current node size). ALIGN_THRESHOLD
  // is the snap radius for "this node is close to vertically/
  // horizontally aligned with another node, lock it onto that line."
  const GRID = 16;
  const ALIGN_THRESHOLD = 8;

  // Track mouse-button state so snap only kicks in during user drags,
  // not during programmatic moves (autoLayout, edge connection
  // creation, etc).
  let mouseIsDown = false;
  window.addEventListener('mousedown', () => { mouseIsDown = true; }, true);
  window.addEventListener('mouseup', () => { mouseIsDown = false; }, true);
  window.addEventListener('blur', () => { mouseIsDown = false; });

  // Reverse drag-to-connect — Drawflow only fires drawConnection from
  // an output. Users also expect to drag from an input back to a
  // source output, so we render a dashed ghost line and, on drop over
  // an output, call addConnection with the args flipped.
  let revDrag = null; // { input, path, svg, x0, y0 }
  const canvasParent = canvasEl.parentElement;
  const portCenter = (el) => {
    const r = el.getBoundingClientRect();
    const p = canvasParent.getBoundingClientRect();
    return [r.left + r.width / 2 - p.left, r.top + r.height / 2 - p.top];
  };
  const portChannel = (el) => Array.from(el.classList).find((c) => /^(input|output)_\d+$/.test(c));

  document.addEventListener('mousedown', (e) => {
    if (!e.target || e.target.classList[0] !== 'input') return;
    const [x0, y0] = portCenter(e.target);
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.style.cssText = 'position:absolute;inset:0;width:100%;height:100%;pointer-events:none;z-index:6;';
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    path.setAttribute('stroke', '#9aa3b2');
    path.setAttribute('stroke-width', '2');
    path.setAttribute('stroke-dasharray', '4 4');
    path.setAttribute('fill', 'none');
    svg.appendChild(path);
    canvasParent.appendChild(svg);
    revDrag = { input: e.target, path, svg, x0, y0 };
    e.preventDefault();
    e.stopPropagation();
  }, true);

  document.addEventListener('mousemove', (e) => {
    if (!revDrag) return;
    const p = canvasParent.getBoundingClientRect();
    const cx = e.clientX - p.left;
    const cy = e.clientY - p.top;
    const offset = Math.max(Math.abs(cy - revDrag.y0) / 2, 40);
    revDrag.path.setAttribute(
      'd',
      ` M ${revDrag.x0} ${revDrag.y0} C ${revDrag.x0} ${revDrag.y0 - offset} ${cx} ${cy + offset} ${cx} ${cy}`,
    );
  }, true);

  document.addEventListener('mouseup', (e) => {
    if (!revDrag) return;
    const { input, svg } = revDrag;
    revDrag = null;
    svg.remove();
    const dropOut = document.elementFromPoint(e.clientX, e.clientY)?.closest('.output');
    if (!dropOut) return;
    const srcNode = dropOut.closest('.drawflow-node');
    const dstNode = input.closest('.drawflow-node');
    if (!srcNode || !dstNode || srcNode === dstNode) return;
    try {
      editor.addConnection(
        srcNode.id.slice(5),
        dstNode.id.slice(5),
        portChannel(dropOut) || 'output_1',
        portChannel(input) || 'input_1',
      );
    } catch (err) {
      console.warn('[wf] reverse connect failed', err);
    }
  }, true);

  // Alignment guide layer — two absolutely-positioned lines that
  // appear when a moved node locks onto another node's X or Y axis.
  // Hidden by default; updateAlignGuides toggles + repositions them.
  const guideLayer = document.createElement('div');
  guideLayer.style.cssText = 'position:absolute;top:0;left:0;width:100%;height:100%;pointer-events:none;z-index:5;';
  const guideV = document.createElement('div');
  guideV.style.cssText = 'position:absolute;width:1px;background:#16a34a;display:none;top:0;bottom:0;';
  const guideH = document.createElement('div');
  guideH.style.cssText = 'position:absolute;height:1px;background:#16a34a;display:none;left:0;right:0;';
  guideLayer.appendChild(guideV);
  guideLayer.appendChild(guideH);
  canvasEl.parentElement.appendChild(guideLayer);

  function hideAlignGuides() {
    guideV.style.display = 'none';
    guideH.style.display = 'none';
  }
  function updateAlignGuides(movedItems, live) {
    if (!movedItems.length || !mouseIsDown) {
      hideAlignGuides();
      return;
    }
    let showV = false;
    let showH = false;
    // The guides are drawn in the canvas's coordinate space which is
    // panned/zoomed by Drawflow via transform on .drawflow. Reading
    // the actual DOM rect lets us position guides in viewport space
    // so they match what the user sees.
    for (const m of movedItems) {
      if (m.alignedX !== null) {
        const el = document.getElementById('node-' + m.id);
        if (el) {
          const r = el.getBoundingClientRect();
          const parent = canvasEl.parentElement.getBoundingClientRect();
          guideV.style.left = (r.left + r.width / 2 - parent.left) + 'px';
          guideV.style.display = 'block';
          showV = true;
        }
      }
      if (m.alignedY !== null) {
        const el = document.getElementById('node-' + m.id);
        if (el) {
          const r = el.getBoundingClientRect();
          const parent = canvasEl.parentElement.getBoundingClientRect();
          guideH.style.top = (r.top + r.height / 2 - parent.top) + 'px';
          guideH.style.display = 'block';
          showH = true;
        }
      }
    }
    if (!showV) guideV.style.display = 'none';
    if (!showH) guideH.style.display = 'none';
  }

  function reconcileNodes() {
    const home = editor && editor.drawflow && editor.drawflow.drawflow && editor.drawflow.drawflow.Home;
    if (!home || !home.data) return false;
    const live = home.data;
    let dirty = false;
    const moved = [];
    // Collect snap candidates from every node NOT currently being
    // moved (in practice: scan all, the dragger's own coords get
    // overwritten before they're used as a candidate).
    const xs = [];
    const ys = [];
    for (const id in live) {
      const el = document.getElementById('node-' + id);
      if (!el) continue;
      const left = parseFloat(el.style.left);
      const top = parseFloat(el.style.top);
      if (Number.isNaN(left) || Number.isNaN(top)) continue;
      // Snap target candidate = LIVE state (not DOM) so a dragging
      // node never aligns to itself.
      xs.push({ id, v: live[id].pos_x });
      ys.push({ id, v: live[id].pos_y });
      if (left === live[id].pos_x && top === live[id].pos_y) continue;

      // Alignment-only snap. Look for a sibling whose X (or Y) is
      // within ALIGN_THRESHOLD; if found, lock onto it. No grid snap
      // — grid snap fought every frame against the user's mousemove
      // and made the drag feel choppy. Alignment is opt-in: the
      // node only locks when the user moves it close to a peer.
      let alignedX = null;
      let alignedY = null;
      for (const cand of xs) {
        if (cand.id === id) continue;
        if (Math.abs(cand.v - left) <= ALIGN_THRESHOLD) {
          alignedX = { partner: cand.id, value: cand.v };
          break;
        }
      }
      for (const cand of ys) {
        if (cand.id === id) continue;
        if (Math.abs(cand.v - top) <= ALIGN_THRESHOLD) {
          alignedY = { partner: cand.id, value: cand.v };
          break;
        }
      }

      // Only write back to DOM when we actually locked onto a peer,
      // and only during real user drags. Otherwise leave the raw
      // mousemove coords alone — that's what makes the drag feel
      // smooth.
      const finalX = mouseIsDown && alignedX ? alignedX.value : left;
      const finalY = mouseIsDown && alignedY ? alignedY.value : top;
      if (mouseIsDown && (finalX !== left || finalY !== top)) {
        el.style.left = finalX + 'px';
        el.style.top = finalY + 'px';
      }

      if (finalX !== live[id].pos_x || finalY !== live[id].pos_y) {
        live[id].pos_x = finalX;
        live[id].pos_y = finalY;
        dirty = true;
        moved.push({
          id,
          alignedX: alignedX ? alignedX.partner : null,
          alignedY: alignedY ? alignedY.partner : null,
          x: finalX,
          y: finalY,
        });
      }
    }
    if (!dirty) {
      hideAlignGuides();
      return false;
    }
    // Show guide lines for every moved node that locked onto a
    // sibling. Guides hide automatically on the next idle frame.
    updateAlignGuides(moved, live);
    // Downstream code expects `moved` to be a flat list of node ids.
    const movedIds = moved.map((m) => m.id);
    const movedSet = new Set(movedIds);
    const toRefresh = new Set(movedIds);
    for (const id in live) {
      const outs = live[id].outputs || {};
      for (const key in outs) {
        const slot = outs[key];
        const conns = (slot && slot.connections) || [];
        for (let i = 0; i < conns.length; i++) {
          if (movedSet.has(String(conns[i].node))) toRefresh.add(id);
        }
      }
    }
    toRefresh.forEach((id) => {
      try { editor.updateConnectionNodes('node-' + id); } catch (_) {}
    });
    return true;
  }

  // Persistent rAF loop. Errors are caught so a single throw never
  // breaks the chain silently — without this guard, the loop would
  // stop firing the first time Drawflow's internal state was mid-
  // mutation and we accessed a half-built node.
  let pendingSaveAfterDrag = false;
  function frame() {
    try {
      if (reconcileNodes()) pendingSaveAfterDrag = true;
    } catch (err) {
      console.error('[wf] reconcile threw', err);
    }
    requestAnimationFrame(frame);
  }
  requestAnimationFrame(frame);
  document.addEventListener(
    'mouseup',
    () => {
      if (pendingSaveAfterDrag) {
        pendingSaveAfterDrag = false;
        scheduleAutoSave();
      }
    },
    true,
  );

  // ── Helpers ───────────────────────────────────────────────────
  function canvasToFlow(x, y) {
    const zoom = editor.zoom || 1;
    const cx = editor.canvas_x || 0;
    const cy = editor.canvas_y || 0;
    return { x: (x - cx) / zoom, y: (y - cy) / zoom };
  }

  // generateUUID returns a v4-ish UUID string. crypto.randomUUID is
  // present in modern browsers; fall back to a templated random for
  // file:// or older environments.
  function generateUUID() {
    if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
      return crypto.randomUUID();
    }
    return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
      const r = (Math.random() * 16) | 0;
      const v = c === 'x' ? r : (r & 0x3) | 0x8;
      return v.toString(16);
    });
  }

  function addNodeOfType(type, x, y, defaults) {
    // Sanitize prefix to underscore — Go templates reject `.Node.<id>`
    // when id contains dashes. Palette items use kebab-case (`trigger-
    // channel`); convert before passing to uniqueID.
    const safePrefix = type.replace(/-/g, '_');
    const id = uniqueID(safePrefix);
    const meta = nodeMeta(type);
    // Merge palette-supplied defaults (e.g. {channel:"slack",op:"send_message"})
    // over the generic kind defaults so level-2 drops land already wired.
    const data = Object.assign({}, meta.defaults, defaults || {});
    // Per-node module onDrop hook seeds type-specific defaults
    // (session_init's preset/session_id, etc). Modules without an
    // onDrop fn skip silently.
    const mod = window.WickNodes && window.WickNodes[type];
    if (mod && typeof mod.onDrop === 'function') {
      mod.onDrop(data);
    }
    let hint = meta.hint;
    if (defaults) {
      if (defaults.channel && defaults.op) hint = `${defaults.channel} · ${defaults.op}`;
      else if (defaults.module && defaults.op) hint = `${defaults.module} · ${defaults.op}`;
      else if (defaults.channel && defaults.event) hint = `${defaults.channel} · ${defaults.event}`;
    }
    const html = nodeHTML(meta.head, id, hint);
    editor.addNode(id, meta.inputs, meta.outputs, x, y, 'node-' + meta.cssType, {
      id, type: meta.kind, data,
    }, html);
    refreshOutputRefs();
  }

  function uniqueID(prefix) {
    let i = 1, id = prefix;
    while (idTaken(id)) { i++; id = `${prefix}_${i}`; }
    return id;
  }
  function idTaken(id) {
    const all = editor.export();
    const nodes = all.drawflow.Home.data;
    return Object.values(nodes).some((n) => n.name === id);
  }
  function nodeHTML(head, title, hint) {
    return `<div class="node-head">${head}</div><div class="node-body"><div class="title">${title}</div><div class="meta">${hint}</div></div>`;
  }

  // nodeMetaRegistry caches per-node module fixtures contributed via
  // window.WickNodes[type].meta. Each module declares head/hint/css/
  // port info so the canvas card renders consistently with what the
  // Go-side codec expects.
  function nodeMetaFromModule(t) {
    const mod = window.WickNodes && window.WickNodes[t];
    if (mod && mod.meta) return mod.meta;
    return null;
  }

  function nodeMeta(type) {
    const t = type.startsWith('trigger-') ? 'trigger' : type;
    const fixtures = {
      trigger:   { kind: 'trigger', head: 'trigger', hint: type.replace('trigger-', ''), cssType: 'trigger', inputs: 0, outputs: 1, defaults: { triggerKind: type.replace('trigger-', '') } },
      classify:  { kind: 'classify', head: 'classify', hint: 'bug | question | feature', cssType: 'classify', inputs: 1, outputs: 3, defaults: { prompt: '', cases: ['bug', 'question', 'default'] } },
      agent:     { kind: 'agent', head: 'agent', hint: 'reasoning', cssType: 'agent', inputs: 1, outputs: 1, defaults: { prompt: '' } },
      channel:   { kind: 'channel', head: 'channel', hint: 'send_message', cssType: 'channel', inputs: 1, outputs: 1, defaults: { channel: 'slack', op: 'reply_thread' } },
      connector: { kind: 'connector', head: 'connector', hint: 'module · op', cssType: 'connector', inputs: 1, outputs: 1, defaults: { module: '', op: '' } },
      shell:     { kind: 'shell', head: 'shell', hint: 'cmd', cssType: 'shell', inputs: 1, outputs: 1, defaults: { command: [] } },
      http:      { kind: 'http', head: 'http', hint: 'GET / POST', cssType: 'http', inputs: 1, outputs: 1, defaults: { url: '', method: 'GET' } },
      db_query:  { kind: 'db_query', head: 'db_query', hint: 'sql', cssType: 'db_query', inputs: 1, outputs: 1, defaults: { sql: '' } },
      branch:    { kind: 'branch', head: 'branch', hint: 'expr', cssType: 'branch', inputs: 1, outputs: 2, defaults: { expr: '' } },
      parallel:  { kind: 'parallel', head: 'parallel', hint: 'fan-out', cssType: 'parallel', inputs: 1, outputs: 3, defaults: {} },
      end:       { kind: 'end', head: 'end', hint: 'terminator', cssType: 'end', inputs: 1, outputs: 0, defaults: { result: '' } },
      transform: { kind: 'transform', head: 'transform', hint: 'gotemplate', cssType: 'transform', inputs: 1, outputs: 1, defaults: { engine: 'gotemplate', expression: '' } },
    };
    return fixtures[t] || nodeMetaFromModule(t) || fixtures.shell;
  }

  function seedEmptyGraph() {
    const trig = editor.addNode('trigger', 0, 1, 50, 200, 'node-trigger',
      { id: 'trigger', type: 'trigger', data: { triggerKind: 'manual' } },
      nodeHTML('trigger', 'trigger', 'manual'));
    const end = editor.addNode('end', 1, 0, 420, 200, 'node-end',
      { id: 'end', type: 'end', data: {} },
      nodeHTML('end', 'end', 'terminator'));
    editor.addConnection(trig, end, 'output_1', 'input_1');
  }

  function showInspectorFor(id) {
    insEmpty.classList.add('hidden');
    insNode.classList.remove('hidden');
    // Open the modal overlay — n8n debug shell. Closes via ESC, the
    // X button, or clicking on the backdrop.
    const modal = document.getElementById('wf-inspector');
    if (modal) modal.classList.remove('hidden');
    const node = editor.getNodeFromId(id);
    if (!node) return;
    const d = node.data || {};
    const kind = d.type || 'shell';
    f.id.textContent = d.id || node.name;
    // ins-type is a hidden input now (used by save), ins-type-head is
    // the visible chip in the modal header.
    if (f.type) f.type.value = kind;
    const headType = document.getElementById('ins-type-head');
    if (headType) headType.textContent = kind;
    const headLabel = document.getElementById('ins-label-head');
    if (headLabel) headLabel.textContent = node.name || d.id || kind;
    f.label.value = node.name || '';
    // Hydrate the Input pane from the parent's last node output (if a
    // run has happened) so the operator sees the upstream payload
    // without having to mock it. OUTPUT pane is filled from the
    // node's own last output (replay cache, live SSE, or last Execute
    // step result) for the same reason.
    hydrateInputPane(node);
    hydrateOutputPane(node);
    Object.values(panels).forEach((p) => p.classList.add('hidden'));
    const inner = d.data || {};
    if (kind === 'classify' || kind === 'agent') {
      panels.prompt.classList.remove('hidden');
      panels.preset.classList.remove('hidden');
      f.prompt.value = inner.prompt || '';
      f.preset.value = inner.preset || '';
      if (f.provider) f.provider.value = inner.provider || '';
      hydratePromptModeToggle(inner);
    }
    if (kind === 'agent') {
      panels.agentSession?.classList.remove('hidden');
      const sel = document.getElementById('ins-agent-session');
      const from = document.getElementById('ins-agent-session-from');
      if (sel) sel.value = inner.session || '';
      if (from) from.value = inner.session_from || '';
    }
    // Hand off to the per-node module's hydrate hook (registered via
    // window.WickNodes by /static/nodes/<type>/inspector.js). The
    // module owns its inspector partial + DOM IDs; editor.js just
    // toggles which panel is visible.
    showModulePanelFor(kind);
    const mod = nodeModule(kind);
    if (mod && typeof mod.hydrate === 'function') {
      mod.hydrate(inner);
    }
    if (kind === 'classify') {
      panels.cases.classList.remove('hidden');
      renderCaseRows(inner.cases || []);
    }
    if (kind === 'shell') {
      panels.command.classList.remove('hidden');
      f.command.value = (inner.command || []).join('\n');
    }
    if (kind === 'transform') {
      panels.transform.classList.remove('hidden');
      const tEng = document.getElementById('ins-transform-engine');
      const tExpr = document.getElementById('ins-transform-expression');
      if (tEng) tEng.value = inner.engine || 'gotemplate';
      if (tExpr) tExpr.value = inner.expression || '';
    }
    if (kind === 'channel') {
      panels.channel.classList.remove('hidden');
      f.channel.value = inner.channel || '';
      hydrateChannelOps();
      f.op.value = inner.op || '';
      refreshChannelArgs(inner.args, inner.__arg_modes);
    }
    if (kind === 'connector') {
      panels.connector.classList.remove('hidden');
      f.module.value = inner.module || '';
      hydrateConnectorOps();
      f.connOp.value = inner.op || '';
      refreshConnArgs(inner.args, inner.__arg_modes);
    }
    if (kind === 'trigger') {
      panels.trigger.classList.remove('hidden');
      hydrateTriggerPanel(inner);
    }
    refreshOutputRefs();
  }

  // hydrateTriggerPanel fills the trigger node inspector. The visible
  // subpanel swaps based on inner.triggerKind:
  //   channel     → channel + event dropdown + MatchSchema filter form
  //   cron        → schedule + timezone fields
  //   webhook     → path + method
  //   manual      → button label
  //   * (default) → empty-state caption
  function hydrateTriggerPanel(inner) {
    const kind = inner.triggerKind || 'manual';
    const sections = {
      channel: document.getElementById('ins-trig-channel-section'),
      cron:    document.getElementById('ins-trig-cron-section'),
      webhook: document.getElementById('ins-trig-webhook-section'),
      manual:  document.getElementById('ins-trig-manual-section'),
    };
    const empty = document.getElementById('ins-trig-empty-section');
    Object.values(sections).forEach((el) => el && el.classList.add('hidden'));
    if (empty) empty.classList.add('hidden');

    if (kind === 'channel') {
      sections.channel?.classList.remove('hidden');
      hydrateChannelTrigger(inner);
      return;
    }
    if (kind === 'cron') {
      sections.cron?.classList.remove('hidden');
      hydrateCronTrigger(inner);
      return;
    }
    if (kind === 'webhook') {
      sections.webhook?.classList.remove('hidden');
      hydrateWebhookTrigger(inner);
      return;
    }
    if (kind === 'manual') {
      sections.manual?.classList.remove('hidden');
      hydrateManualTrigger(inner);
      return;
    }
    if (empty) empty.classList.remove('hidden');
  }

  function hydrateChannelTrigger(inner) {
    const chSel = document.getElementById('ins-trig-channel');
    const evSel = document.getElementById('ins-trig-event');
    const desc = document.getElementById('ins-trig-event-desc');
    const matchWrap = document.getElementById('ins-trig-match-wrap');
    const matchEnabled = document.getElementById('ins-trig-match-enabled');
    const matchPanel = document.getElementById('ins-trig-match');
    if (!chSel || !evSel) return;

    chSel.innerHTML = '<option value="">(select channel)</option>';
    (registry.channels || []).forEach((ch) => {
      if (!ch.events || !ch.events.length) return;
      const opt = document.createElement('option');
      opt.value = ch.name;
      opt.textContent = ch.name;
      chSel.appendChild(opt);
    });
    chSel.value = inner.channel || '';

    function populateEvents(channelName, selectedEvent) {
      evSel.innerHTML = '<option value="">(select event)</option>';
      const ch = (registry.channels || []).find((c) => c.name === channelName);
      (ch?.events || []).forEach((ev) => {
        const opt = document.createElement('option');
        opt.value = ev.id;
        opt.textContent = ev.name || ev.id;
        opt.dataset.description = ev.description || '';
        opt.dataset.matchHtml = ev.match_html || '';
        evSel.appendChild(opt);
      });
      evSel.value = selectedEvent || '';
      applyEventChoice();
    }

    function applyEventChoice() {
      const selected = evSel.selectedOptions[0];
      if (desc) desc.textContent = selected?.dataset.description || '';
      const html = selected?.dataset.matchHtml || '';
      if (!html) {
        matchWrap.classList.add('hidden');
        return;
      }
      matchWrap.classList.remove('hidden');
      const enabled = !!inner.match_enabled;
      matchEnabled.checked = enabled;
      matchPanel.classList.toggle('hidden', !enabled);
        hydrateArgsForm(matchPanel, html, inner.match || {}, inner.__match_modes || {}, chSel.value);
    }

    chSel.onchange = () => {
      populateEvents(chSel.value, '');
      if (selectedID) updateNodeData(selectedID);
    };
    evSel.onchange = () => {
      applyEventChoice();
      if (selectedID) updateNodeData(selectedID);
    };
    matchEnabled.onchange = () => {
      matchPanel.classList.toggle('hidden', !matchEnabled.checked);
      if (selectedID) updateNodeData(selectedID);
    };

    populateEvents(inner.channel || '', inner.event || '');
  }

  function hydrateCronTrigger(inner) {
    const sched = document.getElementById('ins-trig-schedule');
    const tz = document.getElementById('ins-trig-timezone');
    if (sched) {
      sched.value = inner.schedule || '';
      sched.oninput = () => { if (selectedID) updateNodeData(selectedID); };
    }
    if (tz) {
      tz.value = inner.timezone || '';
      tz.oninput = () => { if (selectedID) updateNodeData(selectedID); };
    }
  }

  function hydrateWebhookTrigger(inner) {
    const path = document.getElementById('ins-trig-path');
    const method = document.getElementById('ins-trig-method');
    if (path) {
      path.value = inner.path || '';
      path.oninput = () => { if (selectedID) updateNodeData(selectedID); };
    }
    if (method) {
      method.value = inner.method || '';
      method.onchange = () => { if (selectedID) updateNodeData(selectedID); };
    }
  }

  function hydrateManualTrigger(inner) {
    const label = document.getElementById('ins-trig-manual-label');
    if (label) {
      label.value = inner.label || '';
      label.oninput = () => { if (selectedID) updateNodeData(selectedID); };
    }
  }
  function hideInspector() {
    insEmpty.classList.remove('hidden');
    insNode.classList.add('hidden');
    const modal = document.getElementById('wf-inspector');
    if (modal) modal.classList.add('hidden');
    // Flush any pending edit into drawflow BEFORE we drop selectedID
    // so the in-progress field values aren't lost when the inspector
    // closes (nodeUnselected pinned selectedID while the modal was up).
    if (selectedID) updateNodeData(selectedID);
    selectedID = null;
  }

  // hydrateOutputPane fills the right "OUTPUT" column from the node's
  // own last output. Populated whenever a replay loaded historical
  // events, when the live SSE stream emitted node_completed, or when
  // the user clicked Execute step on this node. Trigger nodes get
  // their output from the run's source event (cached during replay).
  function hydrateOutputPane(node) {
    const empty = document.getElementById('ins-output-empty');
    const out = document.getElementById('ins-exec-output');
    const json = document.getElementById('ins-exec-json');
    const schema = document.getElementById('ins-exec-schema');
    const status = document.getElementById('ins-exec-status');
    const latency = document.getElementById('ins-exec-latency');
    if (!empty || !out) return;
    if (!node || !node.data) {
      empty.classList.remove('hidden');
      out.classList.add('hidden');
      return;
    }
    const id = node.data.id || node.name;
    const cached = lastRunOutputs[id];
    if (!cached) {
      empty.classList.remove('hidden');
      out.classList.add('hidden');
      if (status) status.textContent = '';
      if (latency) latency.textContent = '';
      return;
    }
    empty.classList.add('hidden');
    out.classList.remove('hidden');
    if (cached && cached.__error) {
      if (status) { status.textContent = '✕ ' + cached.__error; status.className = 'text-xs text-red-500'; }
      if (json) json.textContent = '';
      if (schema) schema.textContent = '';
    } else {
      if (json) json.textContent = JSON.stringify(cached, null, 2);
      if (schema) schema.textContent = inferSchema(cached);
      if (status) { status.textContent = '✓ Last recorded output'; status.className = 'text-xs text-green-600 dark:text-green-400'; }
    }
  }

  // hydrateInputPane fills the left INPUT column. Trigger nodes have
  // no parent so always show empty state. For all other nodes, populate
  // the dropdown with upstream nodes that have run output, defaulting
  // to the direct parent. User can switch to any upstream node.
  function hydrateInputPane(node) {
    const inputEmpty = document.getElementById('ins-input-empty');
    const inputData = document.getElementById('ins-input-data');
    if (!inputEmpty || !inputData) return;

    // Trigger nodes have no input — always empty state.
    if (node && node.data && node.data.type === 'trigger') {
      inputEmpty.classList.remove('hidden');
      inputData.classList.add('hidden');
      lastInputPrefix = null;
      lastInputData = null;
      return;
    }

    const nodeID = node && node.data && (node.data.id || node.name);
    const upstream = nodeID ? getUpstreamNodeIDs(nodeID) : null;

    // Collect upstream node IDs that have run output, exclude self.
    const available = upstream
      ? [...upstream].filter(id => id !== nodeID && lastRunOutputs[id])
      : Object.keys(lastRunOutputs);

    if (available.length === 0) {
      inputEmpty.classList.remove('hidden');
      inputData.classList.add('hidden');
      lastInputPrefix = null;
      lastInputData = null;
      return;
    }

    inputEmpty.classList.add('hidden');
    inputData.classList.remove('hidden');

    // Populate dropdown — default to direct parent if available.
    const sel = document.getElementById('ins-allnodes-select');
    if (sel) {
      const parentID = findParentNodeID(node);
      const prevVal = sel.value;
      sel.innerHTML = '';
      available.forEach(id => {
        const n = getNodeByID(id);
        const label = (n && n.data && n.data.label) || id;
        const opt = document.createElement('option');
        opt.value = id;
        opt.textContent = label + (label !== id ? ' (' + id + ')' : '');
        sel.appendChild(opt);
      });
      // Prefer: previous selection > direct parent > first available
      if (prevVal && available.includes(prevVal)) sel.value = prevVal;
      else if (parentID && available.includes(parentID)) sel.value = parentID;
      else sel.value = available[0];
    }

    renderInputFromSelected();
  }

  // renderInputFromSelected reads the dropdown selection and renders
  // that node's output into the JSON/Schema panes.
  function renderInputFromSelected() {
    const sel = document.getElementById('ins-allnodes-select');
    const jsonEl = document.getElementById('ins-input-json');
    const schemaEl = document.getElementById('ins-input-schema');
    if (!sel) return;
    const nodeID = sel.value;
    if (!nodeID) return;
    const output = lastRunOutputs[nodeID];
    if (!output) return;
    const prefix = inputPrefixForParent(nodeID);
    lastInputPrefix = prefix;
    lastInputData = output;
    if (jsonEl) renderInteractiveJSON(jsonEl, output, prefix);
    if (schemaEl) schemaEl.textContent = inferSchema(output);
    refreshArgPreviews();
  }

  // getNodeByID returns the Drawflow node data object for a node ID string.
  function getNodeByID(nodeID) {
    const live = editor.drawflow && editor.drawflow.drawflow.Home && editor.drawflow.drawflow.Home.data;
    if (!live) return null;
    for (const k in live) {
      const n = live[k];
      if (n && n.data && (n.data.id || n.name) === nodeID) return n;
    }
    return null;
  }

  // getUpstreamNodeIDs returns all node IDs that are upstream of
  // (or equal to) a given node id by walking edges backwards.
  // Returns a Set of node id strings. Trigger nodes are always included.
  function getUpstreamNodeIDs(nodeID) {
    const live = editor.drawflow && editor.drawflow.drawflow.Home && editor.drawflow.drawflow.Home.data;
    if (!live) return new Set();
    // Build id→drawflow-key map and edges map (to→[from,...])
    const idToKey = {};
    for (const k in live) {
      const n = live[k];
      if (n && n.data) idToKey[n.data.id || n.name] = k;
    }
    // Build reverse edge map: nodeID → [parentIDs]
    const parents = {};
    for (const k in live) {
      const n = live[k];
      if (!n || !n.inputs) continue;
      const nid = n.data && (n.data.id || n.name);
      if (!nid) continue;
      for (const slot of Object.values(n.inputs)) {
        for (const c of (slot.connections || [])) {
          const parentNode = live[c.node];
          const pid = parentNode && parentNode.data && (parentNode.data.id || parentNode.name);
          if (pid) {
            if (!parents[nid]) parents[nid] = [];
            parents[nid].push(pid);
          }
        }
      }
    }
    // BFS upwards
    const visited = new Set();
    const queue = [nodeID];
    while (queue.length) {
      const cur = queue.shift();
      if (visited.has(cur)) continue;
      visited.add(cur);
      (parents[cur] || []).forEach(p => queue.push(p));
    }
    // Always include trigger nodes (they carry the event payload)
    for (const k in live) {
      const n = live[k];
      if (n && n.data && n.data.type === 'trigger') {
        visited.add(n.data.id || n.name);
      }
    }
    return visited;
  }


  // inputPrefixForParent returns the template-path prefix for drags
  // out of the INPUT pane. Always rooted under `.Node.<label-or-id>.…`
  // — the engine injects trigger nodes into the Node map alongside
  // regular node outputs, so triggers and downstream nodes share the
  // same access pattern. Falls back to id when the user hasn't named
  // the node yet.
  function inputPrefixForParent(parentID) {
    if (!parentID) return '.Node';
    const live = editor.drawflow && editor.drawflow.drawflow.Home && editor.drawflow.drawflow.Home.data;
    if (!live) return '.Node.' + parentID;
    for (const k in live) {
      const n = live[k];
      if (!n || !n.data) continue;
      if ((n.data.id || n.name) !== parentID) continue;
      const inner = n.data.data || {};
      const label = (inner.label || n.data.label || '').trim();
      if (label && isValidIdent(label)) return '.Node.' + label;
      return '.Node.' + parentID;
    }
    return '.Node.' + parentID;
  }

  // isValidIdent reports whether a label can be used as a Go template
  // field path segment — `{{.Node.<label>.x}}` only parses when label
  // matches Go's identifier rule.
  function isValidIdent(s) {
    return /^[A-Za-z_][A-Za-z0-9_]*$/.test(s);
  }

  // cascadeRenameLabel rewrites every template ref + structural ref to
  // oldLabel across the canvas after a node's label changes. Affected
  // surfaces: any string-shaped field on every node's data
  // (prompt/url/body/headers vals/query vals/args vals/expr/command
  // items/session_from), graph edges (from/to), and trigger entry_node.
  // The numeric Drawflow id stays the same; this only updates payloads.
  function cascadeRenameLabel(oldLabel, newLabel, skipDrawflowID) {
    const live = editor.drawflow && editor.drawflow.drawflow.Home && editor.drawflow.drawflow.Home.data;
    if (!live) return;
    // Match `.Node.oldLabel` followed by a non-identifier char so
    // `oldLabel_v2` doesn't get rewritten. The trailing group is captured
    // and re-emitted verbatim.
    const dotRe = new RegExp('\\.Node\\.' + escapeRegExp(oldLabel) + '(?=[^A-Za-z0-9_]|$)', 'g');
    const idxRe = new RegExp('(index\\s+\\.Node\\s+)"' + escapeRegExp(oldLabel) + '"', 'g');
    const replace = (val) => {
      if (typeof val !== 'string') return val;
      return val.replace(dotRe, '.Node.' + newLabel).replace(idxRe, '$1"' + newLabel + '"');
    };
    const walk = (val) => {
      if (typeof val === 'string') return replace(val);
      if (Array.isArray(val)) return val.map(walk);
      if (val && typeof val === 'object') {
        const out = {};
        for (const k in val) out[k] = walk(val[k]);
        return out;
      }
      return val;
    };
    for (const k in live) {
      if (String(k) === String(skipDrawflowID)) continue;
      const n = live[k];
      if (!n) continue;
      if (n.data && n.data.data) n.data.data = walk(n.data.data);
      // session_from + entry_node are stored on the inner data block
      // already covered above; structural edges live on the data.data
      // for trigger nodes ("entry_node" key). Nothing else to touch.
    }
  }

  function escapeRegExp(s) {
    return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }

  // labelTakenByOther returns true when another canvas node already
  // owns this label, scoped per-canvas (multiple workflows in tabs
  // each get their own editor instance).
  function labelTakenByOther(label, selfDrawflowID) {
    const live = editor.drawflow && editor.drawflow.drawflow.Home && editor.drawflow.drawflow.Home.data;
    if (!live) return false;
    for (const k in live) {
      if (String(k) === String(selfDrawflowID)) continue;
      const n = live[k];
      const lbl = (n && n.data && n.data.label) || (n && n.name) || '';
      if (lbl === label) return true;
    }
    return false;
  }

  // canonicalEventKey maps a json key on the Event envelope to its
  // Go field name so emitted templates work with text/template's
  // case-sensitive field access. The JSON tag is lowercase (type,
  // subtype, channel, at, payload) but the Go struct uses Pascal
  // case — `{{.Event.payload.x}}` fails with "can't evaluate field
  // payload in type workflow.Event". Children of Payload are keys
  // in a map[string]any, so map-style access keeps the JSON case.
  function canonicalEventKey(key) {
    switch (key) {
      case 'type':    return 'Type';
      case 'subtype': return 'Subtype';
      case 'channel': return 'Channel';
      case 'at':      return 'At';
      case 'payload': return 'Payload';
      default:        return key;
    }
  }

  // renderInteractiveJSON walks `obj` and writes a JSON pretty-print
  // into `host` where each leaf value is wrapped in a draggable
  // <span data-path="…"> carrying the full template path. Drops land
  // in any [data-arg-input] field and insert `{{<path>}}` at the
  // cursor — the n8n drag-from-INPUT UX.
  function renderInteractiveJSON(host, obj, prefix) {
    host.innerHTML = '';
    writeJSONNode(host, obj, prefix, 0);
  }

  function writeJSONNode(host, v, path, depth) {
    const pad = '  '.repeat(depth);
    if (v === null) { host.appendChild(leafSpan(path, 'null', 'null')); return; }
    if (Array.isArray(v)) {
      host.appendChild(textSpan('[\n'));
      v.forEach((item, i) => {
        host.appendChild(textSpan(pad + '  '));
        writeJSONNode(host, item, path + '.' + i, depth + 1);
        host.appendChild(textSpan(i < v.length - 1 ? ',\n' : '\n'));
      });
      host.appendChild(textSpan(pad + ']'));
      return;
    }
    if (typeof v === 'object') {
      const keys = Object.keys(v);
      host.appendChild(textSpan('{\n'));
      keys.forEach((k, i) => {
        // Root-level Event keys are rendered AND emit using canonical
        // Go field names (Payload, Type, …) so the display matches
        // the template path exactly — operators won't see "payload"
        // in the JSON pane and wonder why the drop puts "Payload" in
        // the field. Past the root, every level is a map[string]any
        // so JSON case is the right access path.
        const isEventRoot = path === '.Event' && depth === 0;
        const displayKey = isEventRoot ? canonicalEventKey(k) : k;
        const childPath = path + '.' + displayKey;
        host.appendChild(textSpan(pad + '  '));
        const keySpan = leafSpan(childPath, '"' + displayKey + '"', 'key');
        host.appendChild(keySpan);
        host.appendChild(textSpan(': '));
        writeJSONNode(host, v[k], childPath, depth + 1);
        host.appendChild(textSpan(i < keys.length - 1 ? ',\n' : '\n'));
      });
      host.appendChild(textSpan(pad + '}'));
      return;
    }
    let display;
    if (typeof v === 'string') display = '"' + v + '"';
    else display = String(v);
    host.appendChild(leafSpan(path, display, typeof v));
  }

  function textSpan(s) {
    return document.createTextNode(s);
  }

  function leafSpan(path, display, kind) {
    const span = document.createElement('span');
    span.className = 'wf-json-leaf wf-json-' + kind;
    span.textContent = display;
    span.draggable = true;
    span.dataset.tplPath = '{{' + path + '}}';
    span.title = `Drag to an arg field — inserts ${span.dataset.tplPath}`;
    span.addEventListener('dragstart', (e) => {
      e.dataTransfer.setData('text/plain', span.dataset.tplPath);
      e.dataTransfer.effectAllowed = 'copyMove';
    });
    span.addEventListener('click', () => {
      copyToClipboard(span.dataset.tplPath, null);
      span.classList.add('wf-json-leaf-flash');
      setTimeout(() => span.classList.remove('wf-json-leaf-flash'), 400);
    });
    return span;
  }

  // lastInputPrefix + lastInputData track the current INPUT pane data
  // so refreshArgPreviews can render template results live as the
  // operator types `{{ }}` references into arg fields.
  let lastInputPrefix = null;
  let lastInputData = null;

  // refreshArgPreviews walks every visible arg input that has a
  // sibling `[data-arg-preview]` element and rewrites the preview
  // text with the rendered template, evaluated against
  // `lastInputData` plus the trigger event so the operator sees the
  // exact value that will land at runtime — without firing Execute
  // step.
  function refreshArgPreviews() {
    document.querySelectorAll('[data-arg-preview]').forEach((el) => {
      const input = el.previousElementSibling;
      if (!input || (input.tagName !== 'INPUT' && input.tagName !== 'TEXTAREA')) {
        el.textContent = '';
        return;
      }
      const tpl = input.value || '';
      const out = renderTemplatePreview(tpl);
      el.textContent = out;
    });
  }

  // renderTemplatePreview is a lightweight Go-template-ish evaluator
  // for the common shapes wick uses: `{{.A.B.C}}`. Walks
  // `lastInputData` rooted at `lastInputPrefix` for `.Event.…` refs,
  // and `lastRunOutputs[<id>]` for `.Node.<id>.…` refs. Doesn't
  // implement pipes, conditionals, or fancy stdlib funcs — those
  // still fire server-side at Execute step time. The preview is
  // diagnostic, not authoritative.
  function renderTemplatePreview(tpl) {
    if (typeof tpl !== 'string' || tpl === '') return '';
    return tpl.replace(/{{\s*([^}]+?)\s*}}/g, (_, raw) => {
      const val = evalTemplateExpr(raw.trim());
      if (val === undefined) return '⟨unresolved⟩';
      if (typeof val === 'object') return JSON.stringify(val);
      return String(val);
    });
  }

  // evalTemplateExpr handles the small set of expression shapes the
  // engine sees most often: dotted paths, pipe chains (`x | fromJson`,
  // `x | toJson`), and the parenthesized `(x | fromJson).field` form
  // where the operator pulls a field out of a parsed JSON string. Not
  // a full Go-template evaluator — anything else still renders as
  // ⟨unresolved⟩ until Execute step fires server-side.
  function evalTemplateExpr(expr) {
    // Strip outer parens and trailing `.field.path` for the
    // `(inner).field` pattern.
    let trailing = '';
    if (expr.startsWith('(')) {
      // Find matching closing paren at depth 0.
      let depth = 0, end = -1;
      for (let i = 0; i < expr.length; i++) {
        if (expr[i] === '(') depth++;
        else if (expr[i] === ')') {
          depth--;
          if (depth === 0) { end = i; break; }
        }
      }
      if (end < 0) return undefined;
      trailing = expr.slice(end + 1); // e.g. `.body_raw`
      expr = expr.slice(1, end);
    }
    // Split by pipe, evaluate left-to-right with simple filters.
    const segments = expr.split('|').map(s => s.trim());
    let val = resolveTemplatePath(segments[0]);
    for (let i = 1; i < segments.length; i++) {
      const fn = segments[i];
      if (val === undefined) return undefined;
      if (fn === 'fromJson' || fn === 'fromJSON' || fn === 'fromjson') {
        if (typeof val !== 'string') return val;
        try { val = JSON.parse(val); } catch (_) { return undefined; }
      } else if (fn === 'toJson' || fn === 'toJSON' || fn === 'tojson') {
        try { val = JSON.stringify(val); } catch (_) { return undefined; }
      } else {
        // Unknown filter — bail; engine will still try at runtime.
        return undefined;
      }
    }
    if (trailing && trailing.startsWith('.')) {
      const parts = trailing.slice(1).split('.');
      for (const k of parts) {
        if (val === null || val === undefined) return undefined;
        val = val[k];
      }
    }
    return val;
  }

  function resolveTemplatePath(path) {
    if (!path.startsWith('.')) return undefined;
    const parts = path.slice(1).split('.');
    let root;
    let lowercaseRoot = false;
    if (parts[0] === 'Event') {
      // Prefer lastInputData when parent is a trigger.
      // Fallback: find any trigger node output in lastRunOutputs.
      root = lastInputData;
      if (!root) {
        const live = editor.drawflow && editor.drawflow.drawflow.Home && editor.drawflow.drawflow.Home.data;
        if (live) {
          for (const k in live) {
            const n = live[k];
            if (n && n.data && n.data.type === 'trigger') {
              const nid = n.data.id || n.name;
              if (lastRunOutputs[nid]) { root = lastRunOutputs[nid]; break; }
            }
          }
        }
      }
      parts.shift();
      lowercaseRoot = true;
    } else if (parts[0] === 'Node') {
      const id = parts[1];
      root = lastRunOutputs[id];
      parts.splice(0, 2);
    } else {
      return undefined;
    }
    let cur = root;
    for (let i = 0; i < parts.length; i++) {
      if (cur === null || cur === undefined) return undefined;
      const key = (lowercaseRoot && i === 0) ? parts[i].toLowerCase() : parts[i];
      cur = cur[key];
    }
    return cur;
  }

  function findParentNodeID(node) {
    if (!node || !node.inputs) return null;
    const slots = Object.values(node.inputs);
    for (const slot of slots) {
      const conns = (slot && slot.connections) || [];
      for (const c of conns) {
        const live = editor.drawflow.drawflow.Home.data[c.node];
        if (live && live.data && live.data.id) return live.data.id;
      }
    }
    return null;
  }

  function updateNodeData(id) {
    const node = editor.getNodeFromId(id);
    if (!node) return;
    const d = node.data || {};
    const kind = d.type;
    const newLabel = f.label.value.trim() || (d.id || node.name);
    const oldLabel = (d.label || '').trim();
    if (node.html) {
      node.html = node.html.replace(/<div class="title">[^<]*<\/div>/, `<div class="title">${escapeHTML(newLabel)}</div>`);
      const el = document.querySelector(`#node-${id} .title`);
      if (el) el.textContent = newLabel;
    }
    d.label = newLabel;
    // Cascade label rename across the workflow when the new label is a
    // valid Go-template identifier and actually changed. Rewrites every
    // `{{...\.Node.<old>...}}` reference, `index .Node "<old>"` form,
    // graph edges (from/to), trigger entry_node, and session_from. ID
    // (d.id) stays stable so internal lookups don't break.
    if (oldLabel && oldLabel !== newLabel && isValidIdent(oldLabel) && isValidIdent(newLabel)) {
      cascadeRenameLabel(oldLabel, newLabel, id);
    }
    const inner = d.data || {};
    if (kind === 'classify' || kind === 'agent') {
      inner.prompt = f.prompt.value;
      inner.preset = f.preset.value;
      if (f.provider) inner.provider = f.provider.value;
      const pwrap = f.prompt.closest('.wf-arg-field');
      const mode = pwrap && pwrap.dataset.argMode;
      inner.__arg_modes = inner.__arg_modes || {};
      if (mode) inner.__arg_modes.prompt = mode;
      else delete inner.__arg_modes.prompt;
    }
    if (kind === 'agent') {
      const sel = document.getElementById('ins-agent-session');
      const from = document.getElementById('ins-agent-session-from');
      inner.session = sel ? sel.value : '';
      inner.session_from = from ? from.value.trim() : '';
    }
    // Mirror of showInspectorFor — delegate save to the per-node
    // module so legacy switch cases above stay scoped to the
    // not-yet-migrated node types.
    const mod = nodeModule(kind);
    if (mod && typeof mod.save === 'function') {
      mod.save(inner);
    }
    if (kind === 'shell') {
      inner.command = f.command.value.split('\n').filter(Boolean);
    }
    if (kind === 'transform') {
      const tEng = document.getElementById('ins-transform-engine');
      const tExpr = document.getElementById('ins-transform-expression');
      if (tEng) inner.engine = tEng.value;
      if (tExpr) inner.expression = tExpr.value;
    }
    if (kind === 'channel') {
      inner.channel = f.channel.value;
      inner.op = f.op.value;
      const jsonTA = document.getElementById('ins-channel-args-json');
      if (jsonTA) {
        try { inner.args = jsonTA.value.trim() ? JSON.parse(jsonTA.value) : {}; }
        catch (_) { /* leave previous args until JSON parses */ }
      } else if (chanArgsEl) {
        inner.args = collectArgs(chanArgsEl);
        inner.__arg_modes = collectArgModes(chanArgsEl);
      }
    }
    if (kind === 'connector') {
      inner.module = f.module.value;
      inner.op = f.connOp.value;
      if (connArgsEl) {
        inner.args = collectArgs(connArgsEl);
        inner.__arg_modes = collectArgModes(connArgsEl);
      }
    }
    if (kind === 'trigger') {
      const triggerKind = inner.triggerKind || 'manual';
      if (triggerKind === 'channel') {
        const chSel = document.getElementById('ins-trig-channel');
        const evSel = document.getElementById('ins-trig-event');
        const matchEnabled = document.getElementById('ins-trig-match-enabled');
        const matchPanel = document.getElementById('ins-trig-match');
        inner.channel = chSel?.value || '';
        inner.event = evSel?.value || '';
        const enabled = !!(matchEnabled && matchEnabled.checked);
        inner.match_enabled = enabled;
        if (matchPanel) {
          inner.match = collectArgs(matchPanel);
          inner.__match_modes = collectArgModes(matchPanel);
        } else {
          inner.match = {};
          inner.__match_modes = {};
        }
      } else if (triggerKind === 'cron') {
        inner.schedule = document.getElementById('ins-trig-schedule')?.value || '';
        inner.timezone = document.getElementById('ins-trig-timezone')?.value || '';
      } else if (triggerKind === 'webhook') {
        inner.path = document.getElementById('ins-trig-path')?.value || '';
        inner.method = document.getElementById('ins-trig-method')?.value || '';
      } else if (triggerKind === 'manual') {
        inner.label = document.getElementById('ins-trig-manual-label')?.value || '';
      }
    }
    editor.updateNodeDataFromId(id, { id: d.id, type: kind, data: inner });
    refreshOutputRefs();
  }

  function renderCaseRows(cases) {
    f.cases.innerHTML = '';
    (cases.length ? cases : ['default']).forEach((label) => appendCaseRow(label, ''));
  }
  function appendCaseRow(label, target) {
    const row = document.createElement('div');
    row.className = 'flex gap-1';
    row.innerHTML = `
      <input value="${escapeAttr(label)}" placeholder="case" class="flex-1 bg-white border border-slate-300 rounded px-2 py-1"/>
      <input value="${escapeAttr(target)}" placeholder="target" class="flex-1 bg-white border border-slate-300 rounded px-2 py-1 text-slate-600"/>
    `;
    row.querySelectorAll('input').forEach((inp) =>
      inp.addEventListener('input', () => persistCases(selectedID)));
    f.cases.appendChild(row);
  }
  function persistCases(id) {
    if (!id) return;
    const node = editor.getNodeFromId(id);
    if (!node) return;
    const labels = [];
    f.cases.querySelectorAll('div.flex').forEach((row) => {
      const ins = row.querySelectorAll('input');
      if (ins[0] && ins[0].value.trim()) labels.push(ins[0].value.trim());
    });
    const d = node.data || {};
    const inner = d.data || {};
    inner.cases = labels;
    editor.updateNodeDataFromId(id, { id: d.id, type: d.type, data: inner });
  }

  function refreshOutputRefs() {
    if (!f.refs) return;
    const refs = ['{{.Event.Payload.text}}', '{{.Event.Payload.user}}', '{{.Event.Payload.channel_id}}'];
    const all = editor.export();
    const nodes = all.drawflow.Home.data;
    Object.values(nodes).forEach((n) => {
      const nid = (n.data && n.data.id) || n.name;
      if (n.data && n.data.type === 'classify') refs.push(`{{.Node.${nid}.verdict}}`);
      else refs.push(`{{.Node.${nid}.result}}`);
    });
    f.refs.innerHTML = refs.map((r) => `<div>${escapeHTML(r)}</div>`).join('');
  }
  function escapeAttr(s) { return String(s).replace(/"/g, '&quot;'); }
  function escapeHTML(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }

  // ── Toolbar dropdowns ─────────────────────────────────────────
  // Publish + 3-dot menus open on click and close on outside click.
  // Single delegated outside-click listener keeps the wiring lean
  // even though there are two menus.
  function bindMenu(btnId, menuId) {
    const btn = document.getElementById(btnId);
    const menu = document.getElementById(menuId);
    if (!btn || !menu) return;
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      // Close any other open menu first.
      document.querySelectorAll('#wf-toolbar-actions [id$="-menu"]').forEach((m) => {
        if (m !== menu) m.classList.add('hidden');
      });
      menu.classList.toggle('hidden');
    });
  }
  bindMenu('wf-publish-menu-btn', 'wf-publish-menu');
  bindMenu('wf-more-btn', 'wf-more-menu');
  document.addEventListener('click', () => {
    document.querySelectorAll('#wf-toolbar-actions [id$="-menu"]').forEach((m) => m.classList.add('hidden'));
  });

  // History button toggles the bottom panel "Runs" tab so the user
  // can scan past executions without leaving the editor.
  document.getElementById('wf-history-btn')?.addEventListener('click', () => {
    const runsTab = document.querySelector('[data-bottom-tab="runs"]');
    if (runsTab) runsTab.click();
  });

  // Runs panel — click a row to reveal the full run ID + status
  // detail. The list itself is server-rendered with timestamps; the
  // detail line stays hidden until clicked so the panel doesn't
  // overwhelm the user with UUIDs.
  document.addEventListener('click', (e) => {
    const head = e.target.closest('[data-run-toggle]');
    if (!head) return;
    const row = head.closest('.wf-run-row');
    if (!row) return;
    row.querySelector('.wf-run-detail')?.classList.toggle('hidden');
  });

  // ── Run progress: flush-then-run + SSE per-node painting ─────
  // The server-side handler returns 202 {run_id} immediately after
  // enqueue (engine reads run_id from the Event payload so the
  // browser can subscribe in time to catch the first node_started
  // event). Flow on submit:
  //   1) cancel any pending autosave + flush save synchronously
  //   2) POST /run with Accept: application/json, expect 202 {run_id}
  //   3) open EventSource on /stream?session=wf:<id>, paint events
  //   4) close stream on workflow_completed | workflow_failed
  // Execute workflow pill — the run trigger lives on the canvas
  // floating button. `wf-execute-pill` opens a picker; the actual
  // run kick-off happens in startWorkflowRun() below.
  const runBtn = document.getElementById('wf-execute-pill');
  const logsList = document.getElementById('wf-logs-list');
  const logsEmpty = document.getElementById('wf-logs-empty');
  const logsCounter = document.getElementById('wf-logs-counter');
  let runEventSource = null;
  let currentRunID = null;
  let runStarted = 0;
  let logsByNode = {};
  // lastRunOutputs caches { node_id: output_map } from node_completed
  // events. The inspector's INPUT pane reads from this to render the
  // parent's last output as the upstream payload preview.
  const lastRunOutputs = {};
  let logCount = 0;
  // seenEventKeys dedups events that may arrive twice: once via live
  // SSE and once via the state-backfill fetch we kick off after POST
  // /run. Engine + broadcast hook share ev.TS, so the (ts|event|
  // node|case) tuple is identical across sources. Reset per run.
  let seenEventKeys = new Set();

  function clearAllNodeBadges() {
    document.querySelectorAll('.drawflow-node').forEach((el) => {
      el.classList.remove('wf-node-running', 'wf-node-success', 'wf-node-failed', 'wf-node-skipped');
      const badge = el.querySelector('.wf-node-badge');
      if (badge) badge.textContent = '';
    });
  }

  function setNodeBadge(domID, state, latencyMs) {
    const el = document.getElementById('node-' + domID);
    if (!el) return;
    el.classList.remove('wf-node-running', 'wf-node-success', 'wf-node-failed', 'wf-node-skipped');
    el.classList.add('wf-node-' + state);
    let badge = el.querySelector('.wf-status-badge');
    if (!badge) {
      badge = document.createElement('div');
      badge.className = 'wf-status-badge';
      el.appendChild(badge);
    }
    if (state === 'running') badge.textContent = '⟳';
    else if (state === 'success') badge.textContent = '✓';
    else if (state === 'failed') badge.textContent = '✕';
    else if (state === 'skipped') badge.textContent = '○';
    badge.dataset.state = state;
    if (typeof latencyMs === 'number') {
      let lat = el.querySelector('.wf-latency');
      if (!lat) {
        lat = document.createElement('div');
        lat.className = 'wf-latency';
        el.appendChild(lat);
      }
      lat.textContent = latencyMs + 'ms';
    }
  }

  function clearNodeBadges() {
    document.querySelectorAll('.drawflow-node').forEach((el) => {
      el.classList.remove('wf-node-running', 'wf-node-success', 'wf-node-failed', 'wf-node-skipped');
      el.querySelector('.wf-status-badge')?.remove();
      el.querySelector('.wf-latency')?.remove();
    });
  }

  // badgeFiringTrigger paints a success badge on the trigger node that
  // started the run. Engine sends `trigger_id` (exact match) and
  // `trigger_type` (fallback for legacy workflows with no IDs) on the
  // workflow_started event. Without one of these, multi-trigger
  // canvases can't tell which row fired — single-trigger canvases
  // still light up because the type matches the only candidate.
  function badgeFiringTrigger(data) {
    if (!data) return;
    const wantID = data.trigger_id || '';
    const wantEntry = data.entry_node || '';
    const wantType = data.trigger_type || data.trigger || '';
    const live = editor.drawflow && editor.drawflow.drawflow.Home && editor.drawflow.drawflow.Home.data;
    if (!live) return;
    let entryDomID = null;
    if (wantEntry) {
      for (const k in live) {
        if (live[k] && live[k].data && live[k].data.id === wantEntry) { entryDomID = String(k); break; }
      }
    }
    let matchKey = null;
    const typeMatches = [];
    for (const k in live) {
      const n = live[k];
      if (!n || !n.data || n.data.type !== 'trigger') continue;
      const nid = n.data.id || n.name;
      if (wantID && nid === wantID) { matchKey = k; break; }
      if (entryDomID) {
        const outs = (n.outputs && n.outputs.output_1 && n.outputs.output_1.connections) || [];
        if (outs.some((c) => String(c.node) === entryDomID)) { matchKey = k; break; }
      }
      const kind = (n.data.data && n.data.data.triggerKind) || '';
      if (wantType && kind === wantType) typeMatches.push(k);
    }
    if (!matchKey && typeMatches.length === 1) matchKey = typeMatches[0];
    if (matchKey) setNodeBadge(matchKey, 'success');
  }

  // Resolve workflow node id (data.id) → Drawflow numeric DOM id.
  function domIDFromNodeID(nodeID) {
    if (!nodeID) return null;
    const live = editor.drawflow && editor.drawflow.drawflow.Home && editor.drawflow.drawflow.Home.data;
    if (!live) return null;
    for (const k in live) {
      if (live[k].data && live[k].data.id === nodeID) return k;
      if (String(k) === nodeID) return k;
    }
    return null;
  }

  function pushLogEntry(ev) {
    if (!logsList) return;
    if (logsEmpty) logsEmpty.classList.add('hidden');
    const nodeID = ev.node || '(workflow)';
    const wrap = document.createElement('div');
    wrap.className = 'wf-log-row';
    wrap.dataset.nodeId = nodeID;
    const head = document.createElement('div');
    head.className = 'wf-log-head';
    const status = ev.event.replace('node_', '').replace('workflow_', '');
    const latency = ev.data && typeof ev.data.latency_ms === 'number' ? ` · ${ev.data.latency_ms}ms` : '';
    head.innerHTML = `<span class="wf-log-status wf-log-status-${status}">${status}</span> <span class="wf-log-node">${escapeHTML(nodeID)}</span><span class="wf-log-latency">${latency}</span>`;
    wrap.appendChild(head);
    if (ev.data && Object.keys(ev.data).length > 0) {
      const body = document.createElement('pre');
      body.className = 'wf-log-body hidden';
      body.textContent = JSON.stringify(ev.data, null, 2);
      head.style.cursor = 'pointer';
      head.addEventListener('click', () => body.classList.toggle('hidden'));
      // Copy button on the head so the user can grab the event payload
      // without selecting text. Stops propagation so it doesn't also
      // toggle the body open/closed.
      const copyBtn = document.createElement('button');
      copyBtn.type = 'button';
      copyBtn.className = 'wf-log-copy';
      copyBtn.textContent = 'copy';
      copyBtn.title = 'Copy this event\'s data as JSON';
      copyBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        copyToClipboard(JSON.stringify(ev, null, 2), copyBtn);
      });
      head.appendChild(copyBtn);
      wrap.appendChild(body);
    }
    logsList.appendChild(wrap);
    logsByNode[nodeID] = wrap;
    logCount++;
    if (logsCounter) logsCounter.textContent = `(${logCount})`;
    logsList.scrollTop = logsList.scrollHeight;
  }

  // copyToClipboard writes text to the system clipboard, flashing the
  // origin button's label on success/failure so the user gets visual
  // feedback. Uses the modern Clipboard API and falls back to a
  // hidden textarea + document.execCommand for older browsers.
  async function copyToClipboard(text, btn) {
    const origLabel = btn ? btn.textContent : '';
    let ok = false;
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text);
        ok = true;
      } else {
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        ok = document.execCommand('copy');
        ta.remove();
      }
    } catch (_) { ok = false; }
    if (!btn) return;
    btn.textContent = ok ? 'copied!' : 'failed';
    setTimeout(() => { btn.textContent = origLabel; }, 1200);
  }

  function openBottomLogs() {
    const tab = document.querySelector('[data-bottom-tab="logs"]');
    if (tab) tab.click();
  }

  function startRunStream(runID) {
    if (runEventSource) {
      try { runEventSource.close(); } catch (_) {}
    }
    const url = `${baseURL.replace(/\/workflows$/, '')}/stream?session=wf:${id}`;
    runEventSource = new EventSource(url);
    runEventSource.addEventListener('agent', (e) => {
      let payload;
      try { payload = JSON.parse(e.data); } catch (_) { return; }
      if (!payload || payload.type === undefined) return;
      if (!payload.type.startsWith('wf_')) return;
      let ev;
      try { ev = JSON.parse(payload.data); } catch (_) { return; }
      if (!ev || ev.run_id !== runID) return;
      handleRunEvent(ev);
    });
    runEventSource.onerror = () => {
      // EventSource auto-reconnects; rely on it. Surface a status only
      // if we never received the workflow_completed event before close.
      if (currentRunID === runID && runBtn?.dataset.state === 'running') {
        setStatus('warn', '⚠ SSE reconnecting…');
      }
    };
  }

  function handleRunEvent(ev) {
    // Dedup so the state-backfill replay (kicked off after POST /run)
    // doesn't double-paint events the live SSE already delivered.
    // Engine + WorkflowEventHook share ev.TS, so the tuple is stable
    // across both sources.
    const key = `${ev.ts || ''}|${ev.event || ''}|${ev.node || ''}|${ev.case || ''}`;
    if (seenEventKeys.has(key)) return;
    seenEventKeys.add(key);
    pushLogEntry(ev);
    // Trigger nodes never emit node_started/node_completed because the
    // engine begins walking at the entry node — without this branch the
    // canvas would show no indicator of which trigger row fired the run.
    // Engine attaches trigger_id (and trigger_type as a fallback for
    // legacy single-trigger workflows) to the workflow_started event;
    // resolve to a Drawflow DOM id and stamp a success badge so the
    // user sees the same green check that other nodes get.
    if (ev.event === 'workflow_started') badgeFiringTrigger(ev.data);
    const domID = domIDFromNodeID(ev.node);
    if (ev.event === 'node_started' && domID) setNodeBadge(domID, 'running');
    if (ev.event === 'node_completed' && domID) {
      setNodeBadge(domID, 'success', ev.data && ev.data.latency_ms);
      // Cache the output so the inspector's INPUT pane can render
      // the upstream payload preview when the user selects a child
      // node after the run completes.
      if (ev.node && ev.data && ev.data.output) lastRunOutputs[ev.node] = ev.data.output;
      // If the user is currently inspecting a node that just finished,
      // refresh the input + output panes so the modal reflects the new
      // data without requiring a re-click.
      if (selectedID) {
        const node = editor.getNodeFromId(selectedID);
        if (node) {
          hydrateInputPane(node);
          hydrateOutputPane(node);
        }
      }
      // Refresh input pane dropdown if inspector is open.
      if (selectedID) {
        const selEl = document.getElementById('ins-allnodes-select');
        if (selEl) hydrateInputPane(editor.getNodeFromId(selectedID));
      }
    }
    if (ev.event === 'node_failed' && domID) {
      setNodeBadge(domID, 'failed');
      // Cache the error so hydrateOutputPane can show it.
      if (ev.node && ev.data && ev.data.error) {
        lastRunOutputs[ev.node] = { __error: ev.data.error };
      }
      if (selectedID) {
        const node = editor.getNodeFromId(selectedID);
        if (node) hydrateOutputPane(node);
      }
    }
    if (ev.event === 'node_skipped' && domID) setNodeBadge(domID, 'skipped');
    if (ev.event === 'workflow_completed' || ev.event === 'workflow_failed') {
      finishRun(ev.event === 'workflow_completed');
      // Fetch the final state for the run that just finished and
      // prepend a fresh row to the Runs panel so the count + ordering
      // tracks reality without forcing the user to reload the page.
      prependRunRow(ev.run_id);
    }
  }

  // prependRunRow fetches /runs/<id>/state and pushes a new row at
  // the top of the Runs list. Status badge + timestamp + duration
  // mirror the server-side template so the panel looks consistent
  // whether the row came from initial page load or a live append.
  async function prependRunRow(rid) {
    if (!rid) return;
    // Only prepend when the user is viewing page 1 — otherwise the
    // row would interleave into an older page and break the sort.
    // Page 1 is the default; the URL only carries `runs_page` when
    // navigating older pages.
    const params = new URLSearchParams(window.location.search);
    const page = parseInt(params.get('runs_page') || '1', 10);
    if (page > 1) return;
    try {
      const resp = await fetch(`${baseURL}/edit/${id}/runs/${rid}/state`, {
        headers: { 'Accept': 'application/json' },
      });
      if (!resp.ok) return;
      const body = await resp.json();
      const st = body.state;
      if (!st) return;
      let list = document.getElementById('wf-runs-list');
      const tab = document.querySelector('[data-bottom-tab="runs"]');
      // First run on a workflow renders "No runs yet" placeholder
      // instead of the <ul>. Swap the placeholder for an empty list
      // we can prepend into.
      if (!list) {
        const panel = document.querySelector('[data-bottom-panel="runs"]');
        if (panel) {
          panel.innerHTML = '<ul class="space-y-1 text-xs" id="wf-runs-list"></ul>';
          list = document.getElementById('wf-runs-list');
        }
        if (!list) {
          if (tab) tab.textContent = `Runs (1) `;
          return;
        }
      }
      // Skip if a row for this id is already there (shouldn't happen
      // but cheap to guard).
      if (list.querySelector(`[data-run-id="${rid}"]`)) return;
      const li = document.createElement('li');
      li.className = 'wf-run-row';
      li.dataset.runId = rid;
      const started = st.started_at ? new Date(st.started_at) : null;
      const ended = st.ended_at ? new Date(st.ended_at) : null;
      const status = st.status || 'unknown';
      const tsLabel = started
        ? started.toLocaleString('sv-SE', { hour12: false }).replace('T', ' ')
        : '—';
      const dur = (started && ended) ? formatDur(ended - started) : '';
      li.innerHTML = `
        <button type="button" class="wf-run-head" data-run-toggle>
          <span class="wf-run-status wf-run-status-${escapeAttr(status)}">${escapeHTML(status)}</span>
          <span class="wf-run-time">${escapeHTML(tsLabel)}</span>
          <span class="wf-run-dur">${escapeHTML(dur)}</span>
        </button>
        <div class="wf-run-detail hidden">
          <div class="font-mono text-[11px] text-black-700 dark:text-black-600 break-all">${escapeHTML(rid)}</div>
          <a href="${baseURL}/edit/${id}/runs/${rid}" class="mt-1 inline-block text-green-600 dark:text-green-400 hover:text-green-700 dark:hover:text-green-300">Open run detail →</a>
        </div>`;
      list.insertBefore(li, list.firstChild);
      // Bump the tab counter — pull the integer out of the existing
      // "Runs (N)" label and increment.
      if (tab) {
        const m = tab.textContent.match(/Runs \((\d+)\)/);
        const next = m ? parseInt(m[1], 10) + 1 : list.children.length;
        tab.firstChild.nodeValue = `Runs (${next}) `;
      }
    } catch (_) {
      // Silent — Runs panel staleness is non-fatal; user can reload
      // the page to recover.
    }
  }

  function formatDur(ms) {
    if (ms < 1000) return `${ms}ms`;
    const s = Math.floor(ms / 1000);
    return `${s}s`;
  }

  function setPillState(state, label) {
    if (!runBtn) return;
    runBtn.dataset.state = state;
    runBtn.disabled = state === 'running' || state === 'disabled';
    const caret = runBtn.querySelector('.wf-execute-caret');
    runBtn.textContent = '';
    const icon = document.createElement('span');
    icon.innerHTML = state === 'running'
      ? '⟳ '
      : '<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" style="vertical-align:-2px;margin-right:6px;"><path d="M10 2v7.31"/><path d="M14 9.3V1.99"/><path d="M8.5 2h7"/><path d="M14 9.3a6.5 6.5 0 1 1-4 0"/></svg>';
    runBtn.appendChild(icon);
    runBtn.appendChild(document.createTextNode(label));
    if (caret && state !== 'running') {
      const c = document.createElement('span');
      c.className = 'wf-execute-caret';
      c.textContent = '▾';
      runBtn.appendChild(c);
    }
  }

  function finishRun(ok) {
    if (runEventSource) { try { runEventSource.close(); } catch (_) {} runEventSource = null; }
    setPillState('idle', 'Execute workflow');
    const ms = Date.now() - runStarted;
    setStatus(ok ? 'ok' : 'error', ok ? `✓ Run completed in ${ms}ms` : `✕ Run failed (${ms}ms)`);
  }

  async function flushAutosave() {
    if (saveTimer) {
      clearTimeout(saveTimer);
      saveTimer = null;
      await autoSave();
    }
  }

  // startWorkflowRun fires one trigger from the canvas. Caller must
  // pass a Drawflow trigger node — its data carries the kind +
  // outgoing wiring needed to drive the server-side run handler.
  async function startWorkflowRun(triggerNode) {
    if (!triggerNode) return;
    setPillState('running', 'Running…');
    setStatus('saving', '⟳ Saving + running…');
    clearNodeBadges();
    logsByNode = {};
    logCount = 0;
    seenEventKeys = new Set();
    if (logsList) logsList.innerHTML = '';
    if (logsCounter) logsCounter.textContent = '(0)';
    if (logsEmpty) logsEmpty.classList.add('hidden');
    openBottomLogs();
    runStarted = Date.now();
    await flushAutosave();
    try {
      const tid = (triggerNode.data && triggerNode.data.id) || triggerNode.name;
      const resp = await fetch(`${baseURL}/edit/${id}/run`, {
        method: 'POST',
        headers: { 'Accept': 'application/json', 'Content-Type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams({ trigger_id: tid }).toString(),
      });
      let data = null;
      try { data = await resp.json(); } catch (_) {}
      if (!resp.ok || !data || !data.ok) {
        const msg = (data && data.error) || `HTTP ${resp.status}`;
        setStatus('error', `✕ Run rejected: ${msg}`);
        setPillState('idle', 'Execute workflow');
        return;
      }
      currentRunID = data.run_id;
      setStatus('saving', `⟳ Running ${currentRunID.slice(0, 8)}…`);
      clearAllNodeBadges();
      for (const k in lastRunOutputs) delete lastRunOutputs[k];
      startRunStream(currentRunID);
      // Backfill events that fired between enqueue and SSE subscribe.
      // Broadcaster has no replay buffer, so a fast first node can
      // emit workflow_started + early node_started before EventSource
      // hands shakes. State.events.jsonl is the source of truth —
      // fetch + replay through handleRunEvent (dedup handles overlap
      // with any SSE events that did arrive).
      backfillRunEvents(currentRunID);
    } catch (err) {
      setStatus('error', `✕ Run failed: ${err.message || err}`);
      setPillState('idle', 'Execute workflow');
    }
  }

  // ── Trigger picker menu ──────────────────────────────────────
  const executeMenu = document.getElementById('wf-execute-menu');

  function listCanvasTriggers() {
    const live = editor.drawflow.drawflow.Home.data;
    const out = [];
    for (const k in live) {
      const n = live[k];
      if (!n || !n.data) continue;
      if (n.data.type !== 'trigger') continue;
      const inner = n.data.data || {};
      const kind = inner.triggerKind || 'manual';
      // Find first outgoing connection target node id (data.id).
      let target = '';
      const outs = n.outputs || {};
      const slot = outs.output_1;
      const conns = (slot && slot.connections) || [];
      if (conns.length > 0) {
        const child = live[conns[0].node];
        if (child && child.data) target = child.data.id || child.name;
      }
      out.push({ node: n, id: n.data.id || n.name, kind, target });
    }
    return out;
  }

  function renderExecuteMenu() {
    if (!executeMenu) return;
    const trigs = listCanvasTriggers();
    executeMenu.innerHTML = '';
    if (trigs.length === 0) {
      const e = document.createElement('div');
      e.className = 'wf-execute-menu-empty';
      e.textContent = 'No trigger on canvas. Drag a trigger from the palette to wire it up.';
      executeMenu.appendChild(e);
      return;
    }
    const header = document.createElement('div');
    header.className = 'wf-execute-menu-header';
    header.textContent = 'Pick trigger to fire';
    executeMenu.appendChild(header);
    trigs.forEach((t) => {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'wf-execute-menu-item';
      if (!t.target) btn.disabled = true;
      const title = document.createElement('div');
      title.className = 'wf-execute-menu-title';
      title.innerHTML = `<span class="wf-run-status wf-run-status-${escapeAttr(t.kind === 'manual' ? 'queued' : 'success')}" style="min-width:auto;">${escapeHTML(t.kind)}</span> ${escapeHTML(t.id)}`;
      const meta = document.createElement('div');
      meta.className = 'wf-execute-menu-meta';
      meta.textContent = t.target ? `→ ${t.target}` : 'not wired to any node — drag a line from this trigger first';
      btn.appendChild(title);
      btn.appendChild(meta);
      btn.addEventListener('click', () => {
        executeMenu.classList.add('hidden');
        startWorkflowRun(t.node);
      });
      executeMenu.appendChild(btn);
    });
  }

  function toggleExecuteMenu() {
    if (!executeMenu) return;
    const isHidden = executeMenu.classList.contains('hidden');
    if (isHidden) renderExecuteMenu();
    executeMenu.classList.toggle('hidden');
  }

  runBtn?.addEventListener('click', (e) => {
    e.stopPropagation();
    if (runBtn.dataset.state === 'running') return;
    toggleExecuteMenu();
  });
  document.addEventListener('click', (e) => {
    if (!executeMenu || executeMenu.classList.contains('hidden')) return;
    if (executeMenu.contains(e.target) || runBtn.contains(e.target)) return;
    executeMenu.classList.add('hidden');
  });

  // ── Execute step (single-node iteration) ─────────────────────
  const execBtn = document.getElementById('ins-exec-btn');
  const execInput = document.getElementById('ins-exec-input');
  const execStatus = document.getElementById('ins-exec-status');
  const execOutput = document.getElementById('ins-exec-output');
  const execJSON = document.getElementById('ins-exec-json');
  const execSchema = document.getElementById('ins-exec-schema');
  const execLatency = document.getElementById('ins-exec-latency');

  document.querySelectorAll('[data-exec-tab]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const key = btn.dataset.execTab;
      document.querySelectorAll('[data-exec-tab]').forEach((b) => {
        const on = b === btn;
        b.classList.toggle('bg-green-500/15', on);
        b.classList.toggle('text-green-700', on);
        b.classList.toggle('font-medium', on);
      });
      if (execJSON) execJSON.classList.toggle('hidden', key !== 'json');
      if (execSchema) execSchema.classList.toggle('hidden', key !== 'schema');
    });
  });

  function inferSchema(v, depth) {
    depth = depth || 0;
    if (depth > 6) return '...';
    if (v === null) return 'null';
    if (Array.isArray(v)) {
      if (v.length === 0) return 'array<unknown>';
      return 'array<' + inferSchema(v[0], depth + 1) + '>';
    }
    if (typeof v === 'object') {
      return '{\n' + Object.entries(v).map(([k, val]) => `  ${'  '.repeat(depth)}${k}: ${inferSchema(val, depth + 1)}`).join(',\n') + '\n' + '  '.repeat(depth) + '}';
    }
    return typeof v;
  }

  execBtn?.addEventListener('click', async () => {
    if (!selectedID) return;
    const live = editor.drawflow.drawflow.Home.data[selectedID];
    if (!live) return;
    let input = {};
    let event = null;
    const raw = (execInput?.value || '').trim();
    if (raw) {
      try { input = JSON.parse(raw); }
      catch (err) {
        if (execStatus) execStatus.textContent = '✕ Mock input is not valid JSON';
        return;
      }
    } else if (lastInputData) {
      // Auto-seed the step's run context from whatever's cached in the
      // INPUT pane: a trigger parent caches the full workflow.Event so
      // we send it as `event` to restore Type/Subtype/Channel; a
      // regular parent caches a NodeOutput so we send it as `input`.
      const looksLikeEvent = lastInputData && typeof lastInputData === 'object' && 'payload' in lastInputData;
      if (looksLikeEvent) {
        event = lastInputData;
        input = lastInputData.payload || {};
      } else {
        input = lastInputData;
      }
    }
    if (execStatus) execStatus.textContent = '⟳ Executing…';
    execBtn.disabled = true;
    try {
      const body = { node: live, input: input };
      if (event) body.event = event;
      const resp = await fetch(`${baseURL}/edit/${id}/exec-node`, {
        method: 'POST',
        headers: { 'Accept': 'application/json', 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      const data = await resp.json();
      // Both branches dump JSON into the OUTPUT pane and the empty
      // placeholder has to hide either way — leaving it visible was
      // the "error message + No output data + JSON" triple-stack bug.
      document.getElementById('ins-output-empty')?.classList.add('hidden');
      if (execOutput) execOutput.classList.remove('hidden');
      if (!resp.ok || !data.ok) {
        if (execStatus) execStatus.textContent = '✕ ' + (data.error || `HTTP ${resp.status}`);
        if (execJSON) execJSON.textContent = JSON.stringify(data, null, 2);
        if (execSchema) execSchema.textContent = inferSchema(data);
        return;
      }
      if (execStatus) execStatus.textContent = '✓ Step completed';
      if (execLatency) execLatency.textContent = `${data.latency_ms || 0}ms`;
      if (execJSON) execJSON.textContent = JSON.stringify(data.output || {}, null, 2);
      if (execSchema) execSchema.textContent = inferSchema(data.output || {});
    } catch (err) {
      if (execStatus) execStatus.textContent = '✕ ' + (err.message || err);
    } finally {
      execBtn.disabled = false;
    }
  });

  // ── Inspector modal: close / ESC / tabs / shortcuts ───────────
  const inspectorModal = document.getElementById('wf-inspector');
  document.getElementById('ins-close')?.addEventListener('click', hideInspector);
  document.querySelector('.wf-inspector-backdrop')?.addEventListener('click', hideInspector);
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && inspectorModal && !inspectorModal.classList.contains('hidden')) {
      hideInspector();
    }
  });

  // Parameters / Settings tab switch inside the modal centre column.
  document.querySelectorAll('[data-param-tab]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const key = btn.dataset.paramTab;
      document.querySelectorAll('[data-param-tab]').forEach((b) => b.classList.toggle('is-on', b === btn));
      document.querySelectorAll('[data-param-panel]').forEach((p) => p.classList.toggle('hidden', p.dataset.paramPanel !== key));
    });
  });

  // Input pane JSON / Schema toggle (within parent tab).
  document.querySelectorAll('[data-input-view]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const key = btn.dataset.inputView;
      document.querySelectorAll('[data-input-view]').forEach((b) => {
        const on = b === btn;
        b.classList.toggle('bg-rose-500/20', on);
        b.classList.toggle('text-rose-300', on);
        b.classList.toggle('font-medium', on);
      });
      document.getElementById('ins-input-json')?.classList.toggle('hidden', key !== 'json');
      document.getElementById('ins-input-schema')?.classList.toggle('hidden', key !== 'schema');
    });
  });

  // Dropdown node selector in INPUT pane — re-render when user picks a node.
  document.getElementById('ins-allnodes-select')?.addEventListener('change', renderInputFromSelected);

  // Output-pane buttons in the empty state — "Execute step" mirrors
  // the header button (single-node exec); "set mock data" jumps to
  // the Settings tab so the user can paste a JSON payload.
  document.getElementById('ins-output-exec')?.addEventListener('click', () => execBtn?.click());
  document.getElementById('ins-output-mock')?.addEventListener('click', () => {
    const settingsTab = document.querySelector('[data-param-tab="settings"]');
    if (settingsTab) settingsTab.click();
    document.getElementById('ins-exec-input')?.focus();
  });
  // "Execute previous nodes" — opens the trigger picker so the
  // user explicitly chooses which trigger to fire. We can't just
  // auto-pick because per-trigger routing means cron / slack /
  // manual all start from different nodes; only one is the
  // right answer for the current inspector context.
  document.getElementById('ins-input-from-parent')?.addEventListener('click', () => {
    runBtn?.click();
  });

  // ── Palette drawer: open / close / drill / search filter ─────
  const paletteDrawer = document.getElementById('wf-palette');
  const paletteTitle = paletteDrawer?.querySelector('[data-palette-title]');
  const paletteBack = paletteDrawer?.querySelector('[data-palette-back]');
  const paletteLevel1 = paletteDrawer?.querySelector('[data-palette-level="1"]');
  const paletteLevel2s = paletteDrawer?.querySelectorAll('[data-palette-level="2"]') || [];
  const paletteSearch = document.getElementById('wf-palette-search');

  function showLevel1() {
    if (!paletteDrawer) return;
    paletteLevel1?.classList.remove('hidden');
    paletteLevel2s.forEach((el) => el.classList.add('hidden'));
    paletteBack?.classList.add('hidden');
    if (paletteTitle) paletteTitle.textContent = 'Add node';
    if (paletteSearch) { paletteSearch.value = ''; }
    filterPalette('');
  }
  function showLevel2(group) {
    if (!paletteDrawer) return;
    const panel = paletteDrawer.querySelector(`[data-palette-level="2"][data-palette-group="${CSS.escape(group)}"]`);
    if (!panel) return;
    paletteLevel1?.classList.add('hidden');
    paletteLevel2s.forEach((el) => el.classList.add('hidden'));
    panel.classList.remove('hidden');
    paletteBack?.classList.remove('hidden');
    if (paletteTitle) paletteTitle.textContent = panel.dataset.paletteTitleText || 'Add node';
    if (paletteSearch) { paletteSearch.value = ''; }
    filterPalette('');
  }

  // Palette open/close toggles a custom `wf-palette-drawer-closed`
  // class — NOT Tailwind's `hidden`. Tailwind hidden sets display:none
  // which would unmount the drawer from layout and reflow the canvas
  // on every toggle; we want the drawer to stay mounted and slide via
  // CSS transform.
  function openPalette() {
    paletteDrawer?.classList.remove('wf-palette-drawer-closed');
    showLevel1();
    paletteSearch?.focus();
  }
  function closePalette() {
    paletteDrawer?.classList.add('wf-palette-drawer-closed');
    // Reset to level-1 so the next open starts fresh — otherwise the
    // user re-opens the drawer mid-drill and gets a confusing context.
    showLevel1();
  }
  document.getElementById('wf-palette-open')?.addEventListener('click', openPalette);
  document.querySelectorAll('[data-palette-close]').forEach((el) => {
    el.addEventListener('click', closePalette);
  });
  paletteBack?.addEventListener('click', showLevel1);
  paletteDrawer?.querySelectorAll('[data-palette-drill]').forEach((el) => {
    el.addEventListener('click', () => showLevel2(el.dataset.paletteDrill));
  });
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || !paletteDrawer) return;
    if (paletteDrawer.classList.contains('wf-palette-drawer-closed')) return;
    // ESC backs out of level-2 first, closes the drawer at level-1.
    if (paletteBack && !paletteBack.classList.contains('hidden')) {
      showLevel1();
    } else {
      closePalette();
    }
  });
  // After dragging a palette item to the canvas the drawer should
  // get out of the way. Drawflow fires nodeCreated synchronously
  // after the drop handler, so close in that hook.
  editor.on('nodeCreated', () => closePalette());

  // Live filter — match palette items by text content. Section
  // headings hide when every item underneath is filtered out so the
  // list stays tidy. Runs against whichever level is currently visible.
  function filterPalette(q) {
    const query = q.trim().toLowerCase();
    const activeList = paletteDrawer?.querySelector('[data-palette-level]:not(.hidden)');
    if (!activeList) return;
    const sections = activeList.querySelectorAll('[data-palette-section]');
    sections.forEach((sec) => {
      const group = sec.nextElementSibling;
      if (!group) return;
      let visible = 0;
      group.querySelectorAll('.wf-palette-item').forEach((row) => {
        const text = row.textContent.toLowerCase();
        const match = query === '' || text.includes(query);
        row.style.display = match ? '' : 'none';
        if (match) visible++;
      });
      sec.style.display = visible === 0 ? 'none' : '';
      group.style.display = visible === 0 ? 'none' : '';
    });
  }
  paletteSearch?.addEventListener('input', () => filterPalette(paletteSearch.value));

  // ── Toast + background publish/discard ────────────────────────────
  // Replaces the full-page reload pattern: Publish / Discard / Toggle /
  // Unpublish forms POST via fetch, surface server response inline via
  // toast, and update the toolbar button states locally. The editor
  // never reloads; only when the server response carries fresh state
  // (e.g. validation report) do we re-paint affected widgets.
  const toastHost = document.getElementById('wf-toast-host');
  function showToast(kind, title, body) {
    if (!toastHost) return;
    const el = document.createElement('div');
    el.className = `wf-toast wf-toast-${kind}`;
    const t = document.createElement('div');
    t.className = 'wf-toast-title';
    t.textContent = title;
    el.appendChild(t);
    if (body) {
      const b = document.createElement('div');
      b.className = 'wf-toast-body';
      b.textContent = body;
      el.appendChild(b);
    }
    el.addEventListener('click', () => el.remove());
    toastHost.appendChild(el);
    setTimeout(() => el.remove(), kind === 'error' ? 8000 : 4000);
  }

  // bindBackgroundForm intercepts a form submit, POSTs via fetch, and
  // routes the response through showToast. okMessage shows on 2xx /
  // 3xx (server typically 303s after success); errors show the
  // response body text. onOK runs after a successful response so the
  // caller can flip local UI state (e.g. disable the Publish button
  // once the draft has been promoted).
  function bindBackgroundForm(form, opts) {
    if (!form) return;
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      if (opts.confirmText) {
        const ok = await wickConfirm(opts.confirmText, { title: opts.confirmTitle || 'Confirm', danger: !!opts.confirmDanger, ok: opts.confirmOK });
        if (!ok) return;
      }
      try {
        const resp = await fetch(form.action, {
          method: form.method || 'POST',
          headers: { 'Accept': 'text/plain' },
          body: new URLSearchParams(new FormData(form)),
          redirect: 'manual',
        });
        // 0 = opaque redirect (manual), 2xx = direct OK, 3xx not
        // followed = also OK.
        if (resp.status === 0 || resp.type === 'opaqueredirect' || (resp.status >= 200 && resp.status < 400)) {
          showToast('ok', opts.okTitle, opts.okBody || '');
          opts.onOK?.();
          return;
        }
        const text = await resp.text();
        showToast('error', opts.errTitle || 'Request failed', text || `HTTP ${resp.status}`);
      } catch (err) {
        showToast('error', opts.errTitle || 'Request failed', String(err));
      }
    });
  }

  bindBackgroundForm(document.querySelector(`form[action$="/edit/${id}/publish"]`), {
    okTitle: 'Published',
    okBody: 'Draft promoted to workflow.yaml.',
    errTitle: 'Cannot publish',
    onOK: () => {
      // Draft is now the published copy — hide the Publish button until
      // the next save creates a new draft. Easier than re-rendering the
      // toolbar: disable in place, swap the title.
      document.querySelectorAll(`form[action$="/edit/${id}/publish"] button[type=submit]`).forEach((btn) => {
        btn.disabled = true;
        btn.title = 'No draft to publish';
        btn.classList.add('cursor-not-allowed');
        btn.classList.remove('hover:bg-blue-600', 'active:bg-blue-700');
        btn.classList.add('bg-blue-500/40');
        btn.classList.remove('bg-blue-500');
      });
      document.querySelectorAll(`form[action$="/edit/${id}/discard"] button[type=submit]`).forEach((btn) => {
        btn.disabled = true;
        btn.classList.add('opacity-50', 'cursor-not-allowed');
      });
    },
  });
  // ── Replay a historical run inside the editor ─────────────────────
  // Picks a run from the Runs panel, fetches its state.json + events
  // from the server, and applies them locally as if the run just
  // executed: node status badges paint, log entries populate, and the
  // outputs cache primes so the inspector's INPUT pane shows the same
  // upstream payload the node received. Lets the operator debug a
  // failed run side-by-side with the live graph without leaving the
  // editor.
  // backfillRunEvents fetches state.events.jsonl for a live run and
  // feeds each event through handleRunEvent. Dedup in handleRunEvent
  // means events already painted via SSE get skipped — so this safely
  // closes the race window between POST /run returning and the
  // EventSource subscription being live. Runs once at run start.
  async function backfillRunEvents(runID) {
    if (!runID) return;
    try {
      const resp = await fetch(`${baseURL}/edit/${id}/runs/${encodeURIComponent(runID)}/state`, {
        headers: { 'Accept': 'application/json' },
      });
      if (!resp.ok) return;
      const payload = await resp.json();
      // Drop if user kicked off a different run while this was in flight.
      if (currentRunID !== runID) return;
      const events = Array.isArray(payload.events) ? payload.events : [];
      events.forEach((ev) => handleRunEvent(ev));
    } catch (_) {
      // Silent — SSE will still drive the UI if backfill is unavailable.
    }
  }

  async function replayRun(runID) {
    if (!runID) return;
    const url = `${baseURL}/edit/${id}/runs/${encodeURIComponent(runID)}/state`;
    try {
      const resp = await fetch(url, { headers: { 'Accept': 'application/json' } });
      if (!resp.ok) {
        const text = await resp.text();
        showToast('error', 'Failed to load run', text || `HTTP ${resp.status}`);
        return;
      }
      const payload = await resp.json();
      const events = Array.isArray(payload.events) ? payload.events : [];
      // Reset live editor state so the replay paints from scratch —
      // otherwise old badges from a previous live run or replay would
      // overlay the historical one.
      clearNodeBadges();
      logsList.innerHTML = '';
      logsByNode = {};
      logCount = 0;
      seenEventKeys = new Set();
      if (logsCounter) logsCounter.textContent = '(0)';
      if (logsEmpty) logsEmpty.classList.add('hidden');
      for (const k in lastRunOutputs) delete lastRunOutputs[k];
      currentRunID = runID;
      // Replay events in order. handleRunEvent already knows how to
      // paint badges + cache outputs, so we just feed it back.
      events.forEach((ev) => handleRunEvent(ev));
      // Trigger nodes don't emit node_completed (they're the workflow
      // origin), but operators still want to inspect the event payload
      // that fired the run. Cache the run's trigger event under every
      // canvas trigger node id so clicking one populates the OUTPUT
      // pane with the real payload (channel_id, user, text, …) instead
      // of "No output data".
      const triggerEvent = payload.state && payload.state.event;
      if (triggerEvent) {
        // Only cache the event under the trigger that actually fired —
        // workflow_started carries trigger_id which matches the node id.
        // Caching under ALL trigger nodes was wrong: it made trigger-2
        // appear in All nodes even when only trigger-1 ran.
        const startedEv = payload.events && payload.events.find(e => e.event === 'workflow_started');
        const firingTriggerID = (startedEv && startedEv.data && startedEv.data.trigger_id) || null;
        const live = editor.drawflow && editor.drawflow.drawflow.Home && editor.drawflow.drawflow.Home.data;
        if (live) {
          for (const k in live) {
            const n = live[k];
            if (!n || !n.data || n.data.type !== 'trigger') continue;
            const nid = n.data.id || n.name;
            // Only set the firing trigger; skip others.
            if (firingTriggerID && nid !== firingTriggerID) continue;
            lastRunOutputs[nid] = triggerEvent;
          }
        }
      }
      // If the inspector is currently showing a node, refresh its
      // INPUT + OUTPUT panes so the panel reflects the replay.
      if (selectedID) {
        const sel = editor.getNodeFromId(selectedID);
        if (sel) {
          hydrateInputPane(sel);
          hydrateOutputPane(sel);
        }
      }
      openBottomLogs();
      showToast('ok', 'Replaying run', `${events.length} event${events.length === 1 ? '' : 's'} loaded into the editor.`);
    } catch (err) {
      showToast('error', 'Failed to load run', String(err));
    }
  }

  async function exportRun(runID) {
    if (!runID) return;
    const url = `${baseURL}/edit/${id}/runs/${encodeURIComponent(runID)}/state`;
    try {
      const resp = await fetch(url, { headers: { 'Accept': 'application/json' } });
      if (!resp.ok) {
        showToast('error', 'Failed to export run', `HTTP ${resp.status}`);
        return;
      }
      const text = await resp.text();
      await copyToClipboard(text, null);
      showToast('ok', 'Run JSON copied', `${(text.length / 1024).toFixed(1)} KB on the clipboard.`);
    } catch (err) {
      showToast('error', 'Failed to export run', String(err));
    }
  }

  document.querySelectorAll('[data-run-replay]').forEach((btn) => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      replayRun(btn.dataset.runReplay);
    });
  });
  document.querySelectorAll('[data-run-export]').forEach((btn) => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      exportRun(btn.dataset.runExport);
    });
  });
  document.querySelectorAll('[data-run-copy-id]').forEach((btn) => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      copyToClipboard(btn.dataset.runCopyId, btn);
    });
  });

  // ── Copy to editor ────────────────────────────────────────────────
  // Restores the published workflow graph as a new draft. Works from
  // the bottom-panel Runs tab and the run detail page.
  document.addEventListener('click', async (e) => {
    const btn = e.target.closest('[data-run-copy-to-editor]');
    if (!btn) return;
    e.stopPropagation();
    const url = btn.dataset.copyUrl;
    if (!url) return;
    const hasDraftBadge = document.querySelector('[data-has-draft]');
    if (hasDraftBadge) {
      const ok = await wickConfirm(
        'This will replace your current unsaved draft with the workflow graph from this run. Continue?',
        { title: 'Replace draft?', ok: 'Replace draft', danger: true }
      );
      if (!ok) return;
    }
    const label = btn.textContent.trim();
    btn.textContent = 'Copying…';
    btn.disabled = true;
    try {
      const resp = await fetch(url, { method: 'POST', headers: { 'Accept': 'application/json' } });
      const data = await resp.json();
      if (!resp.ok || !data.ok) { await wickAlert(data.error || `HTTP ${resp.status}`); return; }
      const runShort = (btn.dataset.runCopyToEditor || '').slice(0, 8);
      showToast('ok', 'Draft restored', `From run ${runShort} — reloading editor…`);
      setTimeout(() => window.location.reload(), 900);
    } catch (err) {
      await wickAlert('Copy failed: ' + err.message);
    } finally {
      btn.textContent = label;
      btn.disabled = false;
    }
  });

  bindBackgroundForm(document.querySelector(`form[action$="/edit/${id}/discard"]`), {
    confirmText: 'Rollback to last published version? All draft changes will be lost.',
    okTitle: 'Draft discarded',
    okBody: 'Editor will refresh to the last published version.',
    errTitle: 'Cannot discard',
    onOK: () => {
      // Discard rolls graph state back to the published copy — easiest
      // way to sync the canvas with the new server state is a full
      // reload. This is the one path that still nav, by necessity.
      setTimeout(() => window.location.reload(), 500);
    },
  });

  // ── Workflow name click-to-edit ────────────────────────────────────
  const nameDisplay = document.getElementById('wf-name-display');
  const nameInput = document.getElementById('wf-name-input');
  if (nameDisplay && nameInput) {
    const renameURL = nameDisplay.dataset.renameUrl || '';
    const enterEdit = () => {
      nameInput.value = nameDisplay.textContent.trim();
      nameDisplay.classList.add('hidden');
      nameInput.classList.remove('hidden');
      nameInput.focus();
      nameInput.select();
    };
    const exitEdit = (newName) => {
      if (newName) nameDisplay.textContent = newName;
      nameInput.classList.add('hidden');
      nameDisplay.classList.remove('hidden');
    };
    const commit = async () => {
      const next = nameInput.value.trim();
      const prev = nameDisplay.textContent.trim();
      if (!next || next === prev) { exitEdit(''); return; }
      try {
        const form = new URLSearchParams();
        form.set('name', next);
        const resp = await fetch(renameURL, {
          method: 'POST',
          headers: { 'Accept': 'application/json', 'Content-Type': 'application/x-www-form-urlencoded' },
          body: form.toString(),
        });
        if (!resp.ok) { alert('Rename failed: ' + await resp.text()); exitEdit(''); return; }
        exitEdit(next);
      } catch (err) {
        alert('Rename failed: ' + err.message);
        exitEdit('');
      }
    };
    nameDisplay.addEventListener('click', enterEdit);
    nameInput.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); commit(); }
      if (e.key === 'Escape') { e.preventDefault(); exitEdit(''); }
    });
    nameInput.addEventListener('blur', commit);
  }

  // ── Executions panel ──────────────────────────────────────────────
  // Top-level tab switching: Editor ↔ Executions (lazy-loaded).
  const viewEditorBtn     = document.getElementById('wf-view-editor');
  const viewExecutionsBtn = document.getElementById('wf-view-executions');
  const viewEditorBody    = document.getElementById('wf-view-editor-body');
  const viewEditorBottom  = document.getElementById('wf-editor-bottom-wrap');
  const viewExBody        = document.getElementById('wf-view-executions-body');
  const exContent         = document.getElementById('wf-executions-content');
  let exAutoTimer         = null;

  const TAB_ON  = ['bg-white-100','dark:bg-navy-700','text-black-900','dark:text-white-100'];
  const TAB_OFF = ['text-black-600','dark:text-black-500'];

  function activateTab(onBtn, offBtn) {
    TAB_ON.forEach(c => { onBtn?.classList.add(c); offBtn?.classList.remove(c); });
    TAB_OFF.forEach(c => { offBtn?.classList.add(c); onBtn?.classList.remove(c); });
  }

  function showEditorView() {
    viewEditorBody?.classList.remove('hidden');
    viewEditorBottom?.classList.remove('hidden');
    viewExBody?.classList.add('hidden');
    activateTab(viewEditorBtn, viewExecutionsBtn);
    clearInterval(exAutoTimer);
  }

  function showExecutionsView() {
    viewEditorBody?.classList.add('hidden');
    viewEditorBottom?.classList.add('hidden');
    viewExBody?.classList.remove('hidden');
    activateTab(viewExecutionsBtn, viewEditorBtn);
    loadExPanel();
  }

  function loadExPanel(url) {
    const target = url || viewExecutionsBtn?.dataset.exUrl;
    if (!target || !exContent) return;
    exContent.innerHTML = '<div style="height:100%;display:flex;align-items:center;justify-content:center;font-size:12px;color:#9ca3af;font-style:italic;">Loading…</div>';
    fetch(target)
      .then(r => r.text())
      .then(html => { exContent.innerHTML = html; bindExEvents(); startExAutoRefresh(); })
      .catch(err => { exContent.innerHTML = `<p class="p-6 text-xs text-red-600">${err.message}</p>`; });
  }

  function startExAutoRefresh() {
    clearInterval(exAutoTimer);
    if (!document.getElementById('wf-ex-autorefresh')?.checked) return;
    exAutoTimer = setInterval(() => {
      if (!document.getElementById('wf-ex-autorefresh')?.checked) { clearInterval(exAutoTimer); return; }
      fetch(viewExecutionsBtn?.dataset.exUrl || '')
        .then(r => r.text())
        .then(html => { if (exContent) { exContent.innerHTML = html; bindExEvents(); } })
        .catch(() => {});
    }, 5000);
  }

  function bindExEvents() {
    // Row click → load run detail in right pane.
    exContent?.querySelectorAll('[data-ex-load]').forEach(btn => {
      btn.addEventListener('click', () => {
        exContent.querySelectorAll('.wf-ex-row').forEach(r => r.classList.remove('border-l-green-500','border-l-4'));
        btn.classList.add('border-l-green-500','border-l-4');
        const det = document.getElementById('wf-ex-detail');
        if (!det) return;
        det.innerHTML = '<div class="flex items-center justify-center h-full italic text-xs text-black-600 dark:text-black-700">Loading…</div>';
        fetch(btn.dataset.exLoad)
          .then(r => r.text())
          .then(html => { det.innerHTML = html; bindDetailEvents(det); })
          .catch(err => { det.innerHTML = `<p class="p-4 text-xs text-red-600">${err.message}</p>`; });
      });
    });
    // Refresh button.
    document.getElementById('wf-ex-refresh')?.addEventListener('click', () => loadExPanel());
    // Auto-refresh toggle.
    document.getElementById('wf-ex-autorefresh')?.addEventListener('change', startExAutoRefresh);
    // Load more.
    document.getElementById('wf-ex-load-more')?.addEventListener('click', e => {
      loadExPanel(e.currentTarget.dataset.exMore);
    });
  }

  function bindDetailEvents(container) {
    // Node row click → show output in the collapsible output panel.
    container.querySelectorAll('.wf-ex-node-row').forEach(row => {
      row.addEventListener('click', () => {
        const panel = container.querySelector('#wf-ex-output');
        const label = container.querySelector('#wf-ex-output-label');
        const pre   = container.querySelector('#wf-ex-output-json');
        if (!panel || !pre) return;
        const out = row.dataset.output;
        if (label) label.textContent = `Output — ${row.dataset.nodeId}`;
        try { pre.textContent = out ? JSON.stringify(JSON.parse(out), null, 2) : '(no output data)'; }
        catch (_) { pre.textContent = out || '(no output data)'; }
        panel.classList.remove('hidden');
      });
    });
    container.querySelector('#wf-ex-output-close')?.addEventListener('click', () => {
      container.querySelector('#wf-ex-output')?.classList.add('hidden');
    });
  }

  viewEditorBtn?.addEventListener('click', showEditorView);
  viewExecutionsBtn?.addEventListener('click', showExecutionsView);

  // ── Node search (Ctrl+K) ──────────────────────────────────────────
  (function initNodeSearch() {
    const overlay  = document.getElementById('wf-node-search');
    const backdrop = overlay?.querySelector('.wf-node-search-backdrop');
    const input    = document.getElementById('wf-node-search-input');
    const results  = document.getElementById('wf-node-search-results');
    const searchBtn = document.getElementById('wf-node-search-btn');
    if (!overlay || !input || !results) return;

    let activeIdx = -1;

    function openSearch() {
      overlay.classList.remove('hidden');
      input.value = '';
      activeIdx = -1;
      renderResults('');
      requestAnimationFrame(() => input.focus());
    }

    function closeSearch() {
      overlay.classList.add('hidden');
      input.value = '';
    }

    // Build a flat list of nodes from the editor graph.
    function getNodes() {
      if (!editor || !editor.drawflow) return [];
      const data = editor.drawflow.drawflow.Home.data;
      return Object.entries(data).map(([dfId, n]) => {
        const d = n.data || {};
        return {
          dfId,
          id: d.id || n.name || dfId,
          type: d.type || n.name || 'shell',
          label: n.name || d.id || dfId,
        };
      });
    }

    function renderResults(query) {
      const all = getNodes();
      const q = query.trim().toLowerCase();
      const filtered = q === ''
        ? all
        : all.filter(n =>
            n.id.toLowerCase().includes(q) ||
            n.type.toLowerCase().includes(q) ||
            n.label.toLowerCase().includes(q)
          );

      if (filtered.length === 0) {
        results.innerHTML = `<li class="wf-node-search-empty">${q ? 'No matching nodes' : 'No nodes in workflow'}</li>`;
        return;
      }

      results.innerHTML = filtered.map((n, i) =>
        `<li class="wf-node-search-item" role="option" aria-selected="${i === activeIdx}"
             data-df-id="${n.dfId}" data-idx="${i}" tabindex="-1">
          <span class="wf-node-search-type">${esc(n.type)}</span>
          <span class="wf-node-search-name">${esc(n.label)}</span>
          <span class="wf-node-search-id">${esc(n.id)}</span>
        </li>`
      ).join('');

      results.querySelectorAll('.wf-node-search-item').forEach(el => {
        el.addEventListener('click', () => selectItem(el));
        el.addEventListener('mouseenter', () => {
          setActive(parseInt(el.dataset.idx, 10));
        });
      });
    }

    function esc(s) {
      return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
    }

    function setActive(idx) {
      activeIdx = idx;
      results.querySelectorAll('.wf-node-search-item').forEach((el, i) => {
        el.setAttribute('aria-selected', String(i === idx));
      });
    }

    function selectItem(el) {
      const dfId = el?.dataset.dfId;
      if (!dfId) return;
      closeSearch();
      panToNode(dfId);
      showInspectorFor(dfId);
    }

    function panToNode(dfId) {
      if (!editor || !editor.precanvas) return;
      const data = editor.drawflow.drawflow.Home.data;
      const n = data[dfId];
      if (!n) return;
      const vw = canvasEl.clientWidth || 800;
      const vh = canvasEl.clientHeight || 600;
      const z = editor.zoom || 1;
      editor.canvas_x = vw / 2 - n.pos_x * z - 100;
      editor.canvas_y = vh / 2 - n.pos_y * z - 60;
      editor.precanvas.style.transform =
        `translate(${editor.canvas_x}px, ${editor.canvas_y}px) scale(${z})`;
    }

    input.addEventListener('input', () => {
      activeIdx = -1;
      renderResults(input.value);
    });

    input.addEventListener('keydown', (e) => {
      const items = results.querySelectorAll('.wf-node-search-item[data-df-id]');
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        const next = Math.min(activeIdx + 1, items.length - 1);
        setActive(next);
        items[next]?.scrollIntoView({ block: 'nearest' });
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        const prev = Math.max(activeIdx - 1, 0);
        setActive(prev);
        items[prev]?.scrollIntoView({ block: 'nearest' });
      } else if (e.key === 'Enter') {
        e.preventDefault();
        selectItem(items[activeIdx < 0 ? 0 : activeIdx]);
      } else if (e.key === 'Escape') {
        e.preventDefault();
        closeSearch();
      }
    });

    backdrop?.addEventListener('click', closeSearch);
    searchBtn?.addEventListener('click', openSearch);

    document.addEventListener('keydown', (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'k') {
        e.preventDefault();
        if (overlay.classList.contains('hidden')) {
          openSearch();
        } else {
          closeSearch();
        }
      }
    });
  })();

})();
