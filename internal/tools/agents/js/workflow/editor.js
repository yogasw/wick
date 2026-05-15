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
  if (initialGraph && initialGraph.drawflow) {
    editor.import(initialGraph);
  } else {
    seedEmptyGraph();
  }

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
  document.querySelectorAll('.wf-palette-item').forEach((el) => {
    el.addEventListener('dragstart', (e) => {
      e.dataTransfer.setData('node-type', el.dataset.nodeType);
      e.dataTransfer.effectAllowed = 'copy';
    });
  });
  canvasEl.addEventListener('dragover', (e) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'copy';
  });
  canvasEl.addEventListener('drop', (e) => {
    e.preventDefault();
    const type = e.dataTransfer.getData('node-type');
    if (!type) return;
    const rect = canvasEl.getBoundingClientRect();
    const pos = canvasToFlow(e.clientX - rect.left, e.clientY - rect.top);
    addNodeOfType(type, pos.x, pos.y);
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
    url: document.getElementById('ins-url'),
    method: document.getElementById('ins-method'),
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

  // Cascade refresh when parent picker changes.
  f.channel?.addEventListener('change', () => { hydrateChannelOps(); if (selectedID) updateNodeData(selectedID); });
  f.module?.addEventListener('change', () => { hydrateConnectorOps(); if (selectedID) updateNodeData(selectedID); });
  const panels = {
    prompt: document.getElementById('ins-prompt-panel'),
    cases: document.getElementById('ins-cases-panel'),
    preset: document.getElementById('ins-preset-panel'),
    command: document.getElementById('ins-command-panel'),
    url: document.getElementById('ins-url-panel'),
    channel: document.getElementById('ins-channel-panel'),
    connector: document.getElementById('ins-connector-panel'),
  };
  let selectedID = null;

  editor.on('nodeSelected', (id) => { selectedID = id; showInspectorFor(id); });
  editor.on('nodeUnselected', () => { selectedID = null; hideInspector(); });
  editor.on('nodeRemoved', () => { selectedID = null; hideInspector(); refreshOutputRefs(); });
  editor.on('connectionCreated', () => refreshOutputRefs());

  Object.values(f).forEach((el) => {
    if (!el || el === f.cases || el === f.refs) return;
    el.addEventListener('input', () => { if (selectedID) updateNodeData(selectedID); });
  });

  document.getElementById('ins-add-case').addEventListener('click', () => {
    if (!selectedID) return;
    appendCaseRow('', '');
    persistCases(selectedID);
  });
  document.getElementById('ins-delete').addEventListener('click', () => {
    if (!selectedID) return;
    if (!confirm('Delete this node?')) return;
    editor.removeNodeId('node-' + selectedID);
  });

  // ── Zoom controls ──────────────────────────────────────────────
  document.getElementById('wf-zoom-in').addEventListener('click', () => editor.zoom_in());
  document.getElementById('wf-zoom-out').addEventListener('click', () => editor.zoom_out());
  document.getElementById('wf-zoom-reset').addEventListener('click', () => editor.zoom_reset());

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
  // baseURL = `<base>/workflows` (from data-wf-base). So the slug-bound
  // path is `${baseURL}/edit/${slug}/save`. The registry catalog lives
  // at `${baseURL}/api/registry`.
  const slug = window.location.pathname.split('/').filter(Boolean).pop();
  const saveURL = `${baseURL}/edit/${slug}/save`;
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

  function addNodeOfType(type, x, y) {
    const id = uniqueID(type);
    const meta = nodeMeta(type);
    const html = nodeHTML(meta.head, id, meta.hint);
    editor.addNode(id, meta.inputs, meta.outputs, x, y, 'node-' + meta.cssType, {
      id, type: meta.kind, data: meta.defaults,
    }, html);
    refreshOutputRefs();
  }

  function uniqueID(prefix) {
    let i = 1, id = prefix;
    while (idTaken(id)) { i++; id = `${prefix}-${i}`; }
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
    return fixtures[t] || fixtures.shell;
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
    const node = editor.getNodeFromId(id);
    if (!node) return;
    const d = node.data || {};
    const kind = d.type || 'shell';
    f.id.textContent = d.id || node.name;
    f.type.textContent = kind;
    f.label.value = node.name || '';
    Object.values(panels).forEach((p) => p.classList.add('hidden'));
    const inner = d.data || {};
    if (kind === 'classify' || kind === 'agent') {
      panels.prompt.classList.remove('hidden');
      panels.preset.classList.remove('hidden');
      f.prompt.value = inner.prompt || '';
      f.preset.value = inner.preset || '';
      if (f.provider) f.provider.value = inner.provider || '';
    }
    if (kind === 'classify') {
      panels.cases.classList.remove('hidden');
      renderCaseRows(inner.cases || []);
    }
    if (kind === 'shell') {
      panels.command.classList.remove('hidden');
      f.command.value = (inner.command || []).join('\n');
    }
    if (kind === 'http') {
      panels.url.classList.remove('hidden');
      f.url.value = inner.url || '';
      f.method.value = inner.method || 'GET';
    }
    if (kind === 'channel') {
      panels.channel.classList.remove('hidden');
      f.channel.value = inner.channel || '';
      hydrateChannelOps();
      f.op.value = inner.op || '';
    }
    if (kind === 'connector') {
      panels.connector.classList.remove('hidden');
      f.module.value = inner.module || '';
      hydrateConnectorOps();
      f.connOp.value = inner.op || '';
    }
    refreshOutputRefs();
  }
  function hideInspector() {
    insEmpty.classList.remove('hidden');
    insNode.classList.add('hidden');
  }

  function updateNodeData(id) {
    const node = editor.getNodeFromId(id);
    if (!node) return;
    const d = node.data || {};
    const kind = d.type;
    const newLabel = f.label.value.trim() || (d.id || node.name);
    if (node.html) {
      node.html = node.html.replace(/<div class="title">[^<]*<\/div>/, `<div class="title">${escapeHTML(newLabel)}</div>`);
      const el = document.querySelector(`#node-${id} .title`);
      if (el) el.textContent = newLabel;
    }
    const inner = d.data || {};
    if (kind === 'classify' || kind === 'agent') {
      inner.prompt = f.prompt.value;
      inner.preset = f.preset.value;
      if (f.provider) inner.provider = f.provider.value;
    }
    if (kind === 'shell') {
      inner.command = f.command.value.split('\n').filter(Boolean);
    }
    if (kind === 'http') {
      inner.url = f.url.value;
      inner.method = f.method.value;
    }
    if (kind === 'channel') {
      inner.channel = f.channel.value;
      inner.op = f.op.value;
    }
    if (kind === 'connector') {
      inner.module = f.module.value;
      inner.op = f.connOp.value;
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
    const refs = ['{{.Event.Text}}', '{{.Event.User}}', '{{.Event.Channel}}'];
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
})();
