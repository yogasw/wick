"use strict";
// process.js — drives the Process tab inside the shared context panel.
// Renders active spawns for this session, updates via SSE pool_stats.
// Depends on context.js being loaded first (shares the same panel).

(function () {
  var panel     = document.querySelector('[data-context-panel]');
  var list      = document.querySelector('[data-process-list]');
  var fabCount  = document.querySelector('[data-process-fab-count]');
  var statusDot = document.querySelector('[data-process-status-dot]');

  if (!panel || !list) return;

  var sessionID = panel.dataset.sessionId || '';
  var base      = panel.dataset.base || '';
  var pollTimer = null;

  // ── fetch from REST endpoint ──────────────────────────────────────────
  function fetchProcesses() {
    if (!sessionID) return;
    fetch(base + '/sessions/' + encodeURIComponent(sessionID) + '/processes', { credentials: 'include' })
      .then(function (r) { return r.ok ? r.json() : []; })
      .then(function (procs) { renderList(procs); })
      .catch(function () {});
  }

  // Poll every 5s while the Process tab is visible. Guarded so repeated
  // class mutations (tab switching toggles several classes per change)
  // don't spawn duplicate timers or fire a burst of fetches.
  function startPoll() {
    if (pollTimer) return;       // already polling — no duplicate timer/fetch
    pollTimer = setInterval(fetchProcesses, 5000);
    fetchProcesses();            // immediate first load on open
  }
  function stopPoll() {
    if (!pollTimer) return;
    clearInterval(pollTimer);
    pollTimer = null;
  }

  var panelContent = document.querySelector('[data-panel-content="process"]');
  if (panelContent) {
    var wasHidden = panelContent.classList.contains('hidden');
    new MutationObserver(function () {
      var hidden = panelContent.classList.contains('hidden');
      if (hidden === wasHidden) return; // class changed but visibility didn't
      wasHidden = hidden;
      if (hidden) stopPoll();
      else startPoll();
    }).observe(panelContent, { attributes: true, attributeFilter: ['class'] });
  }

  // ── SSE pool_stats (realtime update) ─────────────────────────────────
  var worker;
  try {
    worker = new SharedWorker(base + '/static/js/sse-worker.js');
    worker.port.start();
    worker.port.postMessage({ type: 'subscribe', sessionID: '', base: base });
    worker.port.onmessage = function (msg) {
      var d = msg.data;
      if (!d || d.type !== 'event') return;
      var ev = d.event;
      if (!ev || ev.type !== 'pool_stats') return;
      var stats;
      try { stats = JSON.parse(ev.data); } catch (_) { return; }
      renderStats(stats);
    };
  } catch (_) {}

  // ── kill ─────────────────────────────────────────────────────────────
  document.addEventListener('click', function (e) {
    var btn = e.target.closest('[data-process-list] .kill-process-btn');
    if (!btn) return;
    var sid = btn.dataset.killSession;
    var queued = btn.dataset.queued === '1';
    if (!sid || !confirm(queued ? 'Cancel this queued request?' : 'Kill this process?')) return;
    btn.disabled = true;
    btn.textContent = queued ? 'Cancelling…' : 'Killing…';
    // Queued requests have no process to signal — dequeue them instead of
    // killing a PID that doesn't exist yet.
    var path = queued ? '/dequeue' : '/kill';
    fetch(base + '/sessions/' + encodeURIComponent(sid) + path, {
      method: 'POST', credentials: 'include'
    }).then(function () { fetchProcesses(); });
  });

  // ── render ───────────────────────────────────────────────────────────
  function renderStats(stats) {
    // Per-session panel: the global pool_stats event carries every spawn
    // (the admin Providers page needs the full picture), so filter down to
    // this session here. Queued entries aren't in this payload — the 5s REST
    // poll covers those; this path only keeps the active rows live.
    var procs = (stats.live_processes || []).filter(function (p) {
      return p.session_id === sessionID;
    });
    renderList(procs);
  }

  function renderList(procs) {

    var n = procs.length;
    if (fabCount) {
      if (n > 0) { fabCount.textContent = n; fabCount.classList.remove('hidden'); fabCount.classList.add('inline-flex'); }
      else        { fabCount.classList.add('hidden'); fabCount.classList.remove('inline-flex'); }
    }
    if (statusDot) {
      var hasWorking = procs.some(function(p) { return p.lifecycle === 'working'; });
      var hasIdle    = procs.some(function(p) { return p.lifecycle === 'idle'; });
      statusDot.className = 'inline-block w-2 h-2 rounded-full ' + (
        n === 0        ? 'bg-white-400 dark:bg-navy-500' :
        hasWorking     ? 'bg-green-500 animate-pulse' :
        hasIdle        ? 'bg-amber-500' :
                         'bg-blue-500 animate-pulse'
      );
    }

    if (n === 0) {
      list.innerHTML = '<p class="text-xs text-black-700 dark:text-black-600 py-4 px-2">No active processes for this session.</p>';
      return;
    }

    list.innerHTML = procs.map(function (p) {
      var sid  = p.session_id || '';
      var pid  = p.pid > 0 ? p.pid : '—';
      // A queued request has no process yet — never flip it to "dead" just
      // because there's no live PID; the "queued" lifecycle is the truth.
      var dead = p.alive === false && p.lifecycle !== 'queued';
      var lc   = dead ? 'dead' : (p.lifecycle || '—');
      var lcCls = dead
        ? 'bg-red-100 dark:bg-red-900 text-red-700 dark:text-red-300'
        : ({
            working:  'bg-green-100 dark:bg-green-900 text-green-700 dark:text-green-300',
            idle:     'bg-amber-100 dark:bg-amber-900 text-amber-700 dark:text-amber-300',
            spawning: 'bg-blue-100 dark:bg-blue-900 text-blue-700 dark:text-blue-300',
            queued:   'bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600',
            killed:   'bg-red-100 dark:bg-red-900 text-red-700 dark:text-red-300',
          })[lc] || 'bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600';

      return [
        '<div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-3 space-y-2">',
          '<div class="flex items-center justify-between gap-2">',
            '<div class="flex items-center gap-2 min-w-0">',
              '<span class="text-xs font-semibold text-black-900 dark:text-white-100 truncate">', esc(p.agent_name || '—'), '</span>',
              '<span class="rounded px-1.5 py-0.5 text-[10px] font-medium ', lcCls, '">', esc(lc), '</span>',
            '</div>',
            '<button data-kill-session="', esc(sid), '"', (lc === 'queued' ? ' data-queued="1"' : ''), ' class="kill-process-btn shrink-0 rounded px-2 py-1 text-[10px] font-medium bg-red-100 dark:bg-red-900 text-red-700 dark:text-red-300 hover:bg-red-200 dark:hover:bg-red-800 transition-colors">', (lc === 'queued' ? 'Cancel' : 'Kill'), '</button>',
          '</div>',
          '<dl class="grid grid-cols-2 gap-x-3 gap-y-1 text-[11px]">',
            '<dt class="text-black-700 dark:text-black-600">Provider</dt>',
            '<dd class="font-mono text-black-900 dark:text-white-100">', esc(p.provider || '—'), '</dd>',
            '<dt class="text-black-700 dark:text-black-600">PID</dt>',
            '<dd class="font-mono text-black-900 dark:text-white-100">', esc(String(pid)), '</dd>',
            '<dt class="text-black-700 dark:text-black-600">Session</dt>',
            '<dd class="font-mono text-black-900 dark:text-white-100">', esc(sid.slice(0, 8)), '</dd>',
            p.substate
              ? '<dt class="text-black-700 dark:text-black-600">Substate</dt><dd class="font-mono text-black-900 dark:text-white-100">' + esc(p.substate) + '</dd>'
              : '',
            (p.queued > 0)
              ? '<dt class="text-black-700 dark:text-black-600">Queued</dt><dd class="font-mono text-amber-600 dark:text-amber-400">' + esc(String(p.queued)) + ' waiting</dd>'
              : '',
          '</dl>',
        '</div>',
      ].join('');
    }).join('');
  }

  function esc(s) {
    return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
  }
})();
