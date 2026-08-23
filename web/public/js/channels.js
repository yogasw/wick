// Channel connections panel on the account page: which chat accounts this
// wick account is reachable on, and a pause switch per connection.
//
// Pausing is enforced server-side at send time, not here — this only drives the
// switch. A button that merely greyed the row would be lying about delivery.
(function () {
  var listEl = document.getElementById('channel-conn-list');
  var statusEl = document.getElementById('channel-conn-status');
  if (!listEl) return;

  // Channel labels. An unknown type falls back to its raw key rather than
  // being hidden, so a newly added transport still shows up.
  var LABELS = { slack: 'Slack', telegram: 'Telegram', discord: 'Discord' };

  function label(type) {
    return LABELS[type] || type;
  }

  // instanceKey carries the registry's internal shape (e.g. "slack:__owner__"
  // or "slack:<userID>"). Show the readable part: the workspace/owner segment
  // matters to a person with two workspaces, the prefix does not.
  function instanceLabel(type, key) {
    if (!key) return '';
    var s = String(key);
    if (s === type || s === 'default') return '';
    if (s.indexOf(':') === 0) s = s.slice(1);
    if (s.indexOf(type + ':') === 0) s = s.slice(type.length + 1);
    s = s.replace(/^__|__$/g, '');
    return s;
  }

  function relative(iso) {
    if (!iso) return 'never';
    var then = new Date(iso).getTime();
    if (isNaN(then)) return 'unknown';
    var mins = Math.floor((Date.now() - then) / 60000);
    if (mins < 1) return 'just now';
    if (mins < 60) return mins + 'm ago';
    var hrs = Math.floor(mins / 60);
    if (hrs < 24) return hrs + 'h ago';
    return Math.floor(hrs / 24) + 'd ago';
  }

  function esc(v) {
    var d = document.createElement('div');
    d.textContent = v == null ? '' : String(v);
    return d.innerHTML;
  }

  function setStatus(text) {
    if (statusEl) statusEl.textContent = text;
  }

  function emptyState() {
    return (
      '<div class="flex flex-col gap-2 bg-white-200 dark:bg-navy-800 px-4 py-5 text-sm text-black-800 dark:text-black-600">' +
      '<span class="font-medium text-black-900 dark:text-white-100">No channel connections yet.</span>' +
      '<span class="text-xs text-black-700 dark:text-black-600">Message wick from Slack or another connected chat channel and it will appear here.</span>' +
      '</div>'
    );
  }

  function row(c) {
    var inst = instanceLabel(c.channelType, c.instanceKey);
    var title = esc(label(c.channelType)) + (inst ? ' <span class="text-black-700 dark:text-black-600">· ' + esc(inst) + '</span>' : '');
    var badge = c.paused
      ? '<span class="inline-flex items-center rounded-full bg-cau-100 px-3 py-1 text-xs font-medium text-cau-400">Paused</span>'
      : '<span class="inline-flex items-center rounded-full bg-pos-100 px-3 py-1 text-xs font-medium text-pos-400">Active</span>';
    var action = c.paused ? 'resume' : 'pause';
    var actionLabel = c.paused ? 'Resume' : 'Pause';

    return (
      '<div class="flex flex-col gap-3 border-b border-white-300 px-4 py-4 last:border-b-0 dark:border-navy-600 sm:flex-row sm:items-center sm:justify-between">' +
      '<div class="min-w-0">' +
      '<div class="text-sm font-medium text-black-900 dark:text-white-100">' + title + '</div>' +
      '<div class="mt-1 truncate text-xs text-black-800 dark:text-black-600">' +
      (c.displayName ? esc(c.displayName) : 'Unknown account') +
      (c.email ? ' · ' + esc(c.email) : '') +
      '</div>' +
      '<div class="mt-1 text-xs text-black-700 dark:text-black-600">Last active ' + esc(relative(c.lastSeenAt)) + '</div>' +
      '</div>' +
      '<div class="flex shrink-0 items-center gap-3">' +
      badge +
      '<button type="button" data-conn-id="' + esc(c.id) + '" data-conn-action="' + action + '"' +
      ' class="inline-flex items-center justify-center rounded-lg border border-white-400 bg-white-100 px-3 py-2 text-sm font-medium text-black-800 transition-colors hover:border-green-400 dark:border-navy-600 dark:bg-navy-700 dark:text-black-600">' +
      actionLabel +
      '</button>' +
      '</div>' +
      '</div>'
    );
  }

  function render(list) {
    if (!list || !list.length) {
      listEl.innerHTML = emptyState();
      setStatus('None');
      return;
    }
    var active = list.filter(function (c) { return !c.paused; }).length;
    setStatus(active + ' of ' + list.length + ' active');
    listEl.innerHTML = list.map(row).join('');
  }

  function load() {
    return fetch('/api/channel-connections', { credentials: 'same-origin' })
      .then(function (res) {
        if (!res.ok) throw new Error('failed to load connections');
        return res.json();
      })
      .then(render)
      .catch(function () {
        listEl.innerHTML =
          '<div class="bg-white-200 px-4 py-5 text-sm text-neg-400 dark:bg-navy-800">Could not load channel connections.</div>';
        setStatus('Unavailable');
      });
  }

  // Delegated so the handler survives every re-render.
  listEl.addEventListener('click', function (e) {
    var btn = e.target.closest('[data-conn-action]');
    if (!btn) return;
    var id = btn.getAttribute('data-conn-id');
    var action = btn.getAttribute('data-conn-action');
    if (!id || !action) return;

    btn.disabled = true;
    fetch('/api/channel-connections/' + encodeURIComponent(id) + '/' + action, {
      method: 'POST',
      credentials: 'same-origin',
    })
      .then(function (res) {
        if (!res.ok) throw new Error('failed');
        return load();
      })
      .catch(function () {
        btn.disabled = false;
      });
  });

  load();
})();
