// Validation tab — renders the parse.Validate report into the bottom
// panel with human-readable explanations + per-node anchors. Hydrates
// from `window.__wfInitialValidation` on page load and refreshes via
// `window.WfEditor.on('validation', payload)` after every auto-save.
//
// Draft save is intentionally lenient on the server, so this tab is
// where the operator learns *what* the publish gate will reject and
// *why*, ahead of pressing Publish.
//
// Each tab JS is self-contained: a single IIFE wires its own DOM refs
// and never assumes the existence of state owned by another tab.
(function () {
  const root = document.querySelector('[data-bottom-panel="validation"]');
  if (!root) return;

  const empty = document.getElementById('wf-validation-empty');
  const errList = document.getElementById('wf-validation-errors');
  const warnWrap = document.getElementById('wf-validation-warnings-wrap');
  const warnList = document.getElementById('wf-validation-warnings');
  const summary = document.getElementById('wf-validation-summary');
  const headline = summary && summary.querySelector('[data-wf-validation-headline]');
  const subtext = summary && summary.querySelector('[data-wf-validation-subtext]');
  const tabBtn = document.querySelector('[data-bottom-tab="validation"]');
  const counter = document.getElementById('wf-validation-counter');

  // explain maps cryptic validator paths/messages to a one-line hint
  // the operator can act on. Keep entries short — the full message is
  // already shown verbatim; this is the *why*, not the *what*.
  const HINTS = [
    { match: /label/i, hint: 'Labels must be lowercase a-z, digits, or underscore. Rename via the inspector.' },
    { match: /entry_node/i, hint: 'Every trigger needs an entry_node — drag a wire from the trigger card to the first node.' },
    { match: /cycle/i, hint: 'The graph loops back on itself. Break the cycle by removing one of the edges in the loop.' },
    { match: /unknown node|missing node/i, hint: 'An edge points to a node id that no longer exists. Delete the dangling edge.' },
    { match: /channel.*required|channel is required/i, hint: 'Channel trigger needs the channel name + event filled in via the trigger inspector.' },
    { match: /schedule|cron/i, hint: 'Cron schedule is missing or malformed. Use the 6-field form (sec min hour dom mon dow).' },
    { match: /preset|prompt/i, hint: 'Agent/classify node needs either a preset or a prompt to run.' },
    { match: /unknown type|invalid type/i, hint: 'Node type is not registered. Re-pick from the palette.' },
  ];
  function hintFor(message, path) {
    const text = `${path || ''} ${message || ''}`;
    for (const h of HINTS) if (h.match.test(text)) return h.hint;
    return '';
  }

  function liFor(item) {
    const li = document.createElement('li');
    li.className = 'rounded border border-rose-200 dark:border-rose-900/60 bg-rose-50/60 dark:bg-rose-900/20 p-2';
    const nodeID = nodeIDFromPath(item.Path || item.path || '');
    const msg = item.Message || item.message || '';
    const path = item.Path || item.path || '';
    li.innerHTML = `
      <div class="flex items-start gap-2">
        <span class="font-mono text-[11px] text-rose-700 dark:text-rose-300 shrink-0">${escapeHTML(path)}</span>
        <span class="flex-1 text-black-900 dark:text-white-100">${escapeHTML(msg)}</span>
        ${nodeID ? `<button type="button" data-wf-validation-focus="${escapeAttr(nodeID)}" class="text-xs text-indigo-600 dark:text-indigo-400 hover:underline">focus</button>` : ''}
      </div>
      ${(() => { const h = hintFor(msg, path); return h ? `<div class="mt-1 text-xs text-black-700 dark:text-black-600">${escapeHTML(h)}</div>` : ''; })()}
    `;
    return li;
  }

  function nodeIDFromPath(p) {
    // Matches `graph.nodes[<id>].field`, `triggers[<idx>].field`,
    // `nodes[<id>]` — we only attempt anchoring on the graph.nodes
    // form since trigger indexes aren't stable handles.
    const m = /graph\.nodes\[([^\]]+)\]/.exec(p || '');
    return m ? m[1] : '';
  }

  function setCounter(n) {
    if (!counter) return;
    if (n > 0) {
      counter.textContent = `(${n})`;
      counter.classList.remove('hidden');
      counter.classList.add('text-rose-600', 'dark:text-rose-400', 'font-semibold');
    } else {
      counter.classList.add('hidden');
      counter.classList.remove('text-rose-600', 'dark:text-rose-400', 'font-semibold');
    }
  }

  function render(payload) {
    // Empty payload (no save yet) → show the "no issues" placeholder.
    if (!payload) payload = { ok: true, errors: [], warnings: [] };
    const errors = payload.errors || payload.Errors || [];
    const warnings = payload.warnings || payload.Warnings || [];
    setCounter(errors.length);
    if (summary) {
      summary.classList.toggle('hidden', errors.length === 0 && warnings.length === 0);
      if (headline) {
        headline.textContent = errors.length > 0
          ? `✕ ${errors.length} error${errors.length === 1 ? '' : 's'} — Publish blocked`
          : warnings.length > 0
            ? `⚠ ${warnings.length} warning${warnings.length === 1 ? '' : 's'} — Publish allowed`
            : '';
        headline.className = 'font-semibold ' + (errors.length > 0 ? 'text-rose-700 dark:text-rose-400' : 'text-amber-700 dark:text-amber-400');
      }
      if (subtext) {
        subtext.textContent = errors.length > 0
          ? 'Draft is saved — fix the errors below before publishing.'
          : warnings.length > 0
            ? 'Warnings do not block publish but are worth a look.'
            : '';
      }
    }
    if (errors.length === 0 && warnings.length === 0) {
      empty.classList.remove('hidden');
      errList.classList.add('hidden');
      warnWrap.classList.add('hidden');
      return;
    }
    empty.classList.add('hidden');
    errList.innerHTML = '';
    errors.forEach((e) => errList.appendChild(liFor(e)));
    errList.classList.toggle('hidden', errors.length === 0);
    warnList.innerHTML = '';
    warnings.forEach((w) => warnList.appendChild(liFor(w)));
    warnWrap.classList.toggle('hidden', warnings.length === 0);
  }

  function escapeHTML(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }
  function escapeAttr(s) { return String(s == null ? '' : s).replace(/"/g, '&quot;'); }

  // Focus button: jump to the offending node + pulse it. Routes
  // through WfEditor.focusNode — the bus is fully populated by the
  // time editor.js finishes its IIFE.
  root.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-wf-validation-focus]');
    if (!btn) return;
    if (window.WfEditor && typeof window.WfEditor.focusNode === 'function') {
      window.WfEditor.focusNode(btn.dataset.wfValidationFocus);
    }
  });

  // Initial paint from server-rendered validation (same payload the
  // canvas badge code already consumes).
  const initial = window.__wfInitialValidation || null;
  render(initial);

  // Subscribe to the shared bus for live updates. The bus is set up
  // in editor.js (Phase A) and stable across the page lifetime.
  function attach() {
    if (!window.WfEditor || typeof window.WfEditor.on !== 'function') return false;
    window.WfEditor.on('validation', render);
    return true;
  }
  if (!attach()) {
    // editor.js loads after this file; retry on DOMContentLoaded and
    // again on the next animation frame as a belt-and-braces fallback.
    document.addEventListener('DOMContentLoaded', attach, { once: true });
    requestAnimationFrame(attach);
  }
})();
