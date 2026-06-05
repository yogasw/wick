(function () {
  function supportsPush() {
    return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;
  }

  function escapeHTML(value) {
    return String(value || '').replace(/[&<>"']/g, function (ch) {
      return ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[ch];
    });
  }

  function urlBase64ToUint8Array(base64String) {
    var padding = '='.repeat((4 - (base64String.length % 4)) % 4);
    var base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
    var rawData = atob(base64);
    var outputArray = new Uint8Array(rawData.length);
    for (var i = 0; i < rawData.length; i++) outputArray[i] = rawData.charCodeAt(i);
    return outputArray;
  }

  function shortEndpoint(endpoint) {
    if (!endpoint) return '';
    if (endpoint.length <= 34) return endpoint;
    return endpoint.slice(0, 18) + '...' + endpoint.slice(-12);
  }

  function deviceLabel() {
    var ua = navigator.userAgent || '';
    if (/iPhone|iPad|iPod/.test(ua)) return 'Safari iOS PWA';
    if (/Android/.test(ua) && /Chrome/.test(ua)) return 'Chrome Android';
    if (/Edg\//.test(ua)) return 'Microsoft Edge';
    if (/Firefox\//.test(ua)) return 'Firefox';
    if (/Chrome\//.test(ua)) return 'Chrome';
    if (/Safari\//.test(ua)) return 'Safari';
    return 'This browser';
  }

  async function currentSubscription() {
    if (!supportsPush()) return null;
    var registration = await navigator.serviceWorker.ready;
    return registration.pushManager.getSubscription();
  }

  async function subscribeCurrent() {
    if (!supportsPush()) throw new Error('Notifications are not supported by this browser.');
    var permission = await Notification.requestPermission();
    if (permission !== 'granted') throw new Error('Notification permission was not granted.');
    var keyRes = await fetch('/api/push/vapid-public-key');
    if (!keyRes.ok) throw new Error('Failed to load push public key.');
    var keyData = await keyRes.json();
    if (!keyData.publicKey) throw new Error('Push public key is not configured.');
    var registration = await navigator.serviceWorker.ready;
    var sub = await registration.pushManager.getSubscription();
    if (!sub) {
      sub = await registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(keyData.publicKey),
      });
    }
    var payload = sub.toJSON();
    payload.deviceLabel = deviceLabel();
    var res = await fetch('/api/push/subscribe', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    if (!res.ok) throw new Error(await res.text() || 'Failed to save push subscription.');
    return sub;
  }

  async function recordPermission(permission) {
    if (!permission) return;
    await fetch('/api/push/permission', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ permission: permission }),
    }).catch(function () {});
  }

  async function unsubscribeEndpoint(endpoint, browserSub) {
    if (!endpoint) return;
    await fetch('/api/push/unsubscribe', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ endpoint: endpoint }),
    });
    if (browserSub) await browserSub.unsubscribe().catch(function () {});
  }

  async function loadDevices() {
    var res = await fetch('/api/push/subscriptions');
    if (!res.ok) throw new Error('Failed to load notification devices.');
    return res.json();
  }

  function setStatus(text, tone, help) {
    var el = document.getElementById('push-current-status');
    if (el) {
      el.textContent = text;
      el.classList.remove('border-pos-200', 'bg-pos-100', 'text-pos-400', 'border-neg-200', 'bg-neg-100', 'text-neg-400');
      if (tone === 'ok') el.classList.add('border-pos-200', 'bg-pos-100', 'text-pos-400');
      if (tone === 'bad') el.classList.add('border-neg-200', 'bg-neg-100', 'text-neg-400');
    }
    var helper = document.getElementById('push-current-help');
    if (helper && help) helper.textContent = help;
  }

  // setBellState renders the chat composer bell across four states:
  //
  //   unsupported — browser can't deliver push; bell hidden entirely.
  //   setup       — push not enabled for this browser (no subscription
  //                 in the browser OR permission still default).
  //                 Outline bell, click jumps to /profile so the user
  //                 can flip the master switch + accept the permission
  //                 prompt in one place.
  //   off         — push on, but THIS session is not subscribed for the
  //                 calling user. Outline bell, click POSTs subscribe.
  //   on          — push on AND this session subscribed for the calling
  //                 user (bell turns green + dot lights). Click POSTs
  //                 unsubscribe.
  //   blocked     — browser permission denied. Bell with slash; click
  //                 toasts the site-settings hint (we can't re-prompt).
  //
  // Per-session state means the bell talks to the server, not just the
  // browser PushManager — see hydrateBell for the fetch.
  function setBellState(state) {
    var btn = document.getElementById('push-bell-btn');
    if (!btn) return;
    if (state === 'unsupported') {
      btn.className = 'hidden';
      return;
    }
    btn.dataset.state = state;
    var dot = btn.querySelector('[data-push-bell-dot]');
    var slash = btn.querySelector('[data-push-bell-slash]');
    // Bell sits absolute-positioned in the composer's top-right corner
    // (see chatComposer in sessions.templ). Keep those position
    // classes when rebuilding className — only swap the colour set
    // that conveys current state.
    var baseCls = 'absolute top-2 right-2 z-10 inline-flex items-center justify-center h-7 w-7 rounded-lg transition-colors hover:bg-white-200 dark:hover:bg-navy-700';
    if (state === 'on') {
      btn.className = baseCls + ' text-green-600 dark:text-green-400';
      btn.setAttribute('title', 'Subscribed — click to stop notifications for this session');
      if (dot) dot.classList.remove('hidden');
      if (slash) slash.classList.add('hidden');
    } else if (state === 'blocked') {
      btn.className = baseCls + ' text-neg-400';
      btn.setAttribute('title', 'Notifications blocked — unblock in site settings');
      if (dot) dot.classList.add('hidden');
      if (slash) slash.classList.remove('hidden');
    } else if (state === 'setup') {
      btn.className = baseCls + ' text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100';
      btn.setAttribute('title', 'Set up notifications in Account first');
      if (dot) dot.classList.add('hidden');
      if (slash) slash.classList.add('hidden');
    } else {
      // off — push on but this session not subscribed yet
      btn.className = baseCls + ' text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100';
      btn.setAttribute('title', 'Click to get notified about this session');
      if (dot) dot.classList.add('hidden');
      if (slash) slash.classList.add('hidden');
    }
  }

  // sessionIDForBell walks up from the bell to the closest element
  // carrying a session id. sessions.templ wraps the chat layout with
  // data-session-id so the bell stays generic and reusable.
  function sessionIDForBell(btn) {
    if (!btn) return '';
    var holder = btn.closest('[data-session-id]');
    return holder ? holder.getAttribute('data-session-id') : '';
  }

  // serverSubscriptionForSession fetches the calling user's per-session
  // subscribe state. Returns false on any error so the bell defaults
  // to off rather than getting stuck.
  async function serverSubscriptionForSession(sessionID) {
    if (!sessionID) return false;
    try {
      var res = await fetch('/tools/agents/sessions/' + encodeURIComponent(sessionID) + '/subscription');
      if (!res.ok) return false;
      var data = await res.json();
      return !!data.subscribed;
    } catch (_) {
      return false;
    }
  }

  // ensureToastStack lazily mounts the floating toast container.
  // Bottom-right, fixed, pointer-events-none so the layer never blocks
  // clicks unless a toast is actually present.
  function ensureToastStack() {
    var el = document.getElementById('push-toast-stack');
    if (el) return el;
    el = document.createElement('div');
    el.id = 'push-toast-stack';
    el.className = 'pointer-events-none fixed bottom-4 right-4 z-50 flex flex-col gap-2';
    document.body.appendChild(el);
    return el;
  }

  // showToast surfaces transient feedback ("Enabled.", "Test sent.")
  // that doesn't need to persist. Auto-dismiss after 3s.
  function showToast(text, tone) {
    var stack = ensureToastStack();
    var toast = document.createElement('div');
    var toneCls = tone === 'ok'
      ? 'border-pos-200 bg-pos-100 text-pos-400'
      : tone === 'bad'
        ? 'border-neg-200 bg-neg-100 text-neg-400'
        : 'border-white-300 bg-white-100 text-black-800 dark:border-navy-600 dark:bg-navy-700 dark:text-black-600';
    toast.className = 'pointer-events-auto rounded-lg border px-4 py-2 text-sm shadow-md transition-opacity duration-200 ' + toneCls;
    toast.textContent = text;
    stack.appendChild(toast);
    window.setTimeout(function () {
      toast.style.opacity = '0';
      window.setTimeout(function () { toast.remove(); }, 220);
    }, 3000);
  }

  // showLifecycleCard renders a rich, clickable in-app toast when the
  // service worker relays a lifecycle push back to the page (wick was
  // open, so we skipped the OS notification surface). Bigger than the
  // small status toast — title + body preview + a footer hint —
  // because the content here is the actual agent output the user
  // wants to read at a glance.
  //
  // Click anywhere on the card navigates to the session URL. Auto-
  // dismisses after 8s (longer than a status toast since users may
  // need to skim the body) but the user can also click the × to
  // dismiss early.
  function showLifecycleCard(payload) {
    var stack = ensureToastStack();
    var card = document.createElement('div');
    card.className = 'pointer-events-auto w-80 max-w-[calc(100vw-2rem)] cursor-pointer rounded-xl border border-white-300 bg-white-100 text-black-900 shadow-lg transition-opacity duration-200 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100';
    var title = payload && payload.title ? String(payload.title) : 'Wick notification';
    var body = payload && payload.body ? String(payload.body) : '';
    var url = payload && payload.url ? String(payload.url) : '/';
    card.innerHTML =
      '<div class="flex items-start gap-3 px-4 py-3">' +
        '<div class="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-green-500/15 text-green-600 dark:text-green-400">' +
          '<svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">' +
            '<path d="M8 2.25c-2.07 0-3.75 1.68-3.75 3.75v2.25L3 9.75v.75h10v-0.75L11.75 8.25V6c0-2.07-1.68-3.75-3.75-3.75z" stroke-linejoin="round"></path>' +
            '<path d="M6.5 12a1.5 1.5 0 0 0 3 0" stroke-linecap="round"></path>' +
          '</svg>' +
        '</div>' +
        '<div class="min-w-0 flex-1">' +
          '<div class="text-sm font-medium leading-tight">' + escapeHTML(title) + '</div>' +
          (body ? '<div class="mt-1 line-clamp-3 text-xs text-black-700 dark:text-black-600">' + escapeHTML(body) + '</div>' : '') +
          '<div class="mt-2 text-[11px] text-green-600 dark:text-green-400">Click to open session →</div>' +
        '</div>' +
        '<button type="button" data-lifecycle-card-dismiss class="-mr-1 -mt-1 shrink-0 rounded-md p-1 text-black-600 opacity-60 transition-opacity hover:opacity-100 dark:text-black-700" aria-label="Dismiss">' +
          '<svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M3 3l6 6M9 3l-6 6" stroke-linecap="round"></path></svg>' +
        '</button>' +
      '</div>';
    var dismissed = false;
    function dismiss() {
      if (dismissed) return;
      dismissed = true;
      card.style.opacity = '0';
      window.setTimeout(function () { card.remove(); }, 220);
    }
    card.addEventListener('click', function (e) {
      if (e.target.closest('[data-lifecycle-card-dismiss]')) {
        e.stopPropagation();
        dismiss();
        return;
      }
      dismiss();
      // Same-origin navigation keeps the SPA / tab state intact.
      window.location.assign(url);
    });
    stack.appendChild(card);
    window.setTimeout(dismiss, 8000);
  }

  // Service worker → page bridge for lifecycle pushes. When wick is
  // open anywhere, sw.js routes the push via postMessage instead of
  // (or alongside, silently) a real OS notification so the page can
  // render a click-to-navigate in-app card. See sw.js push handler.
  if ('serviceWorker' in navigator) {
    navigator.serviceWorker.addEventListener('message', function (event) {
      var data = event.data || {};
      if (data.type === 'wick:lifecycle_push') {
        showLifecycleCard(data);
      }
    });
  }

  function renderDeviceList(devices, currentEndpoint) {
    var list = document.getElementById('push-device-list');
    if (!list) return;
    if (!devices.length) {
      list.innerHTML = '<div class="flex flex-col gap-2 bg-white-200 dark:bg-navy-800 px-4 py-5 text-sm text-black-800 dark:text-black-600"><span class="font-medium text-black-900 dark:text-white-100">No notification devices yet.</span><span class="text-xs text-black-700 dark:text-black-600">Enable notifications to add this browser.</span></div>';
      return;
    }
    list.innerHTML = devices.map(function (d) {
      var isCurrent = d.endpoint === currentEndpoint;
      var label = escapeHTML(d.deviceLabel || 'Browser device');
      var seen = d.lastSeenAt ? new Date(d.lastSeenAt).toLocaleString() : 'Never';
      return '<div class="flex flex-col gap-3 border-b border-white-300 bg-white-100 px-4 py-4 last:border-b-0 dark:border-navy-600 dark:bg-navy-700 sm:flex-row sm:items-center sm:justify-between">' +
        '<div class="min-w-0">' +
        '<div class="flex flex-wrap items-center gap-2"><span class="text-sm font-medium text-black-900 dark:text-white-100">' + label + '</span>' +
        (isCurrent ? '<span class="rounded-full bg-pos-100 px-2 py-0.5 text-xs font-medium text-pos-400">This browser</span>' : '') +
        '</div>' +
        '<div class="mt-1 truncate font-mono text-xs text-black-700 dark:text-black-600">' + escapeHTML(shortEndpoint(d.endpoint)) + '</div>' +
        '<div class="mt-1 text-xs text-black-700 dark:text-black-600">Last seen ' + escapeHTML(seen) + '</div>' +
        '</div>' +
        '<button type="button" data-push-remove="' + escapeHTML(d.endpoint) + '" data-current="' + (isCurrent ? '1' : '0') + '" class="inline-flex items-center justify-center rounded-lg border border-white-400 bg-white-100 px-3 py-2 text-sm font-medium text-neg-400 transition-colors hover:bg-neg-100 dark:border-navy-600 dark:bg-navy-800">Remove</button>' +
        '</div>';
    }).join('');
  }

  function renderPushID(pushID) {
    var value = document.getElementById('push-id-value');
    var copy = document.getElementById('push-copy-id-btn');
    if (!value) return;
    value.textContent = pushID || 'Unavailable';
    value.title = pushID || '';
    if (copy) {
      copy.dataset.pushId = pushID || '';
      copy.disabled = !pushID;
    }
  }

  async function refreshProfile() {
    var root = document.getElementById('push-device-list');
    if (!root) return;
    if (!supportsPush()) {
      setStatus('Unsupported', 'bad', 'This browser cannot receive notifications from Wick.');
      renderDeviceList([], '');
      return;
    }
    var sub = await currentSubscription();
    var currentEndpoint = sub ? sub.endpoint : '';
    if (Notification.permission === 'denied') {
      setStatus('Blocked', 'bad', 'Browser notifications are blocked in site settings. Unblock them before enabling this browser.');
    } else if (sub) {
      setStatus('Enabled', 'ok', 'This browser is subscribed and can receive notifications.');
    } else {
      setStatus('Disabled', '', 'This browser is not subscribed. Enable notifications to add it as a delivery device.');
    }
    var data = await loadDevices();
    renderPushID(data.push_id || '');
    renderDeviceList(data.devices || [], currentEndpoint);

    var actions = document.getElementById('push-device-actions');
    if (actions) {
      actions.innerHTML = '<button type="button" id="push-enable-btn" class="rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 transition-colors hover:bg-green-600">' + (sub ? 'Refresh device' : 'Enable notifications') + '</button>' +
        '<button type="button" id="push-test-btn" class="rounded-lg border border-white-400 bg-white-100 px-4 py-2 text-sm font-medium text-black-800 transition-colors hover:border-green-400 dark:border-navy-600 dark:bg-navy-800 dark:text-black-600">Send test</button>';
    }
  }

  // browserPushReady returns "ready" when the browser has both an
  // active service-worker subscription AND granted permission, "blocked"
  // when permission is denied (we can't recover from this in-app),
  // "setup" otherwise (default permission or missing browser sub),
  // and "unsupported" when the browser can't do push at all.
  async function browserPushReady() {
    if (!supportsPush()) return 'unsupported';
    if (Notification.permission === 'denied') return 'blocked';
    var sub = await currentSubscription().catch(function () { return null; });
    if (sub && Notification.permission === 'granted') return 'ready';
    return 'setup';
  }

  // hydrateBell drives the composer bell's initial render. Bell is
  // mounted by chatComposer (session detail pages only), so calling
  // this elsewhere is a no-op via the early return.
  async function hydrateBell() {
    var btn = document.getElementById('push-bell-btn');
    if (!btn) return;
    var ready = await browserPushReady();
    if (ready === 'unsupported') {
      setBellState('unsupported');
      return;
    }
    if (ready === 'blocked') {
      await recordPermission('denied');
      setBellState('blocked');
      return;
    }
    if (ready === 'setup') {
      setBellState('setup');
      return;
    }
    var sessionID = sessionIDForBell(btn);
    var subscribed = await serverSubscriptionForSession(sessionID);
    setBellState(subscribed ? 'on' : 'off');
  }

  // handleBellClick: state machine for the chat composer bell.
  //
  //   setup       → /profile (single place to set up the master push
  //                 switch). We don't pop the permission prompt from
  //                 the chat composer because a reflex Block here
  //                 permanently kills push for this origin.
  //   blocked     → toast pointing at site settings.
  //   off         → POST /sessions/<id>/subscribe, promote to on.
  //   on          → POST /sessions/<id>/unsubscribe, drop to off.
  async function handleBellClick(btn) {
    var state = btn.dataset.state || 'off';
    if (state === 'setup') {
      window.location.assign('/profile');
      return;
    }
    if (state === 'blocked') {
      showToast('Notifications are blocked. Unblock in site settings to enable.', 'bad');
      return;
    }
    var sessionID = sessionIDForBell(btn);
    if (!sessionID) {
      showToast('Cannot resolve session id for this bell.', 'bad');
      return;
    }
    btn.disabled = true;
    var target = state === 'on' ? 'unsubscribe' : 'subscribe';
    try {
      var res = await fetch('/tools/agents/sessions/' + encodeURIComponent(sessionID) + '/' + target, {
        method: 'POST',
      });
      if (!res.ok) throw new Error(await res.text() || 'Request failed');
      var data = await res.json();
      setBellState(data.subscribed ? 'on' : 'off');
      showToast(
        data.subscribed
          ? 'Subscribed — you’ll get a push when this session changes state.'
          : 'Unsubscribed — no more pushes for this session.',
        data.subscribed ? 'ok' : ''
      );
      // Queue rows show the same session; keep them in sync.
      await refreshQueueBells();
    } catch (err) {
      showToast(err.message || 'Could not change subscription.', 'bad');
    } finally {
      btn.disabled = false;
    }
  }

  // setQueueBellState mirrors setBellState's logic for the queue-row
  // bell variant. State is per-row (each row has its own session id),
  // so this is called once per bell element.
  function setQueueBellState(btn, state) {
    if (!btn) return;
    if (state === 'unsupported') {
      btn.classList.add('hidden');
      return;
    }
    btn.classList.remove('hidden');
    btn.dataset.state = state;
    var dot = btn.querySelector('[data-queue-notify-dot]');
    if (state === 'on') {
      btn.setAttribute('title', 'Subscribed — click to stop notifications');
      btn.classList.add('text-green-600', 'dark:text-green-400');
      btn.classList.remove('text-amber-700', 'dark:text-amber-400');
      btn.classList.remove('text-neg-400');
      if (dot) dot.classList.remove('hidden');
    } else if (state === 'blocked') {
      btn.setAttribute('title', 'Notifications blocked — unblock in site settings');
      btn.classList.add('text-neg-400');
      btn.classList.remove('text-amber-700', 'dark:text-amber-400', 'text-green-600', 'dark:text-green-400');
      if (dot) dot.classList.add('hidden');
    } else if (state === 'setup') {
      btn.setAttribute('title', 'Set up notifications in Account first');
      btn.classList.add('text-amber-700', 'dark:text-amber-400');
      btn.classList.remove('text-green-600', 'dark:text-green-400', 'text-neg-400');
      if (dot) dot.classList.add('hidden');
    } else {
      btn.setAttribute('title', 'Notify me when this session starts');
      btn.classList.add('text-amber-700', 'dark:text-amber-400');
      btn.classList.remove('text-green-600', 'dark:text-green-400', 'text-neg-400');
      if (dot) dot.classList.add('hidden');
    }
  }

  // refreshQueueBells hydrates every per-row bell on the overview
  // queue panel. One subscription-status fetch per row — small N so
  // no batching needed today. Called on load and after the chat
  // composer bell flips state (since the user may be subscribed via
  // either path).
  async function refreshQueueBells() {
    var bells = document.querySelectorAll('[data-queue-notify]');
    if (!bells.length) return;
    var ready = await browserPushReady();
    if (ready === 'unsupported') {
      bells.forEach(function (b) { setQueueBellState(b, 'unsupported'); });
      return;
    }
    if (ready === 'blocked') {
      bells.forEach(function (b) { setQueueBellState(b, 'blocked'); });
      return;
    }
    if (ready === 'setup') {
      bells.forEach(function (b) { setQueueBellState(b, 'setup'); });
      return;
    }
    // ready — fetch per-row subscription state in parallel
    var rows = Array.prototype.map.call(bells, function (b) {
      var row = b.closest('[data-queue-id]');
      return { btn: b, sessionID: row ? row.getAttribute('data-queue-id') : '' };
    });
    await Promise.all(rows.map(async function (r) {
      var subscribed = await serverSubscriptionForSession(r.sessionID);
      setQueueBellState(r.btn, subscribed ? 'on' : 'off');
    }));
  }

  document.addEventListener('click', async function (e) {
    var bell = e.target.closest('#push-bell-btn');
    var enable = e.target.closest('#push-enable-btn');
    var test = e.target.closest('#push-test-btn');
    var remove = e.target.closest('[data-push-remove]');
    var copyID = e.target.closest('#push-copy-id-btn');
    var queueBell = e.target.closest('[data-queue-notify]');
    try {
      if (bell) {
        await handleBellClick(bell);
        return;
      }
      // Queue row bell — per-row subscribe toggle. Same state machine
      // as the chat composer bell but scoped to one queue session.
      if (queueBell) {
        e.preventDefault();
        e.stopPropagation();
        var qstate = queueBell.dataset.state || 'off';
        if (qstate === 'unsupported') {
          showToast('Notifications are not supported by this browser.', 'bad');
          return;
        }
        if (qstate === 'setup') {
          window.location.assign('/profile');
          return;
        }
        if (qstate === 'blocked') {
          showToast('Notifications are blocked. Unblock in site settings to enable.', 'bad');
          return;
        }
        var row = queueBell.closest('[data-queue-id]');
        var sessionID = row ? row.getAttribute('data-queue-id') : '';
        if (!sessionID) {
          showToast('Cannot resolve session id for this row.', 'bad');
          return;
        }
        queueBell.disabled = true;
        var target = qstate === 'on' ? 'unsubscribe' : 'subscribe';
        try {
          var res = await fetch('/tools/agents/sessions/' + encodeURIComponent(sessionID) + '/' + target, {
            method: 'POST',
          });
          if (!res.ok) throw new Error(await res.text() || 'Request failed');
          var data = await res.json();
          setQueueBellState(queueBell, data.subscribed ? 'on' : 'off');
          showToast(
            data.subscribed
              ? 'You’ll get a notification when this session starts.'
              : 'Unsubscribed.',
            data.subscribed ? 'ok' : ''
          );
          // If the composer bell happens to be on the page (rare —
          // queue is overview, composer is session detail), reflect
          // the change there too.
          await hydrateBell();
        } catch (err) {
          showToast(err.message || 'Could not change subscription.', 'bad');
        } finally {
          queueBell.disabled = false;
        }
        return;
      }
      if (copyID) {
        var pushID = copyID.dataset.pushId || '';
        if (pushID && navigator.clipboard) {
          await navigator.clipboard.writeText(pushID);
          copyID.textContent = 'Copied';
          window.setTimeout(function () { copyID.textContent = 'Copy'; }, 1200);
        }
      }
      if (enable) {
        enable.disabled = true;
        await subscribeCurrent();
        await recordPermission(Notification.permission);
        showToast('Notifications enabled for this browser.', 'ok');
        await refreshProfile();
        await hydrateBell();
      }
      if (test) {
        test.disabled = true;
        var sub = await currentSubscription();
        await fetch('/api/push/test', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ endpoint: sub ? sub.endpoint : '' }),
        });
        test.disabled = false;
        showToast('Test notification sent.', 'ok');
      }
      if (remove) {
        remove.disabled = true;
        var endpoint = remove.getAttribute('data-push-remove');
        var subForRemove = remove.getAttribute('data-current') === '1' ? await currentSubscription() : null;
        await unsubscribeEndpoint(endpoint, subForRemove);
        await refreshProfile();
        // Removing the current browser's row also clears its push state,
        // so re-hydrate the bell to drop the green dot.
        if (subForRemove) await hydrateBell();
        showToast('Device removed.', '');
      }
    } catch (err) {
      // Profile page has its own status pill; everywhere else falls
      // back to a toast so the failure doesn't get lost.
      if (document.getElementById('push-current-status')) {
        setStatus(err.message || 'Failed', 'bad');
      } else {
        showToast(err.message || 'Failed', 'bad');
      }
      if (enable) enable.disabled = false;
      if (test) test.disabled = false;
      if (remove) remove.disabled = false;
    }
  });

  document.addEventListener('submit', async function (e) {
    var form = e.target;
    if (!form || form.getAttribute('action') !== '/auth/logout' || form.dataset.pushLogoutDone === '1') return;
    if (!supportsPush()) return;
    e.preventDefault();
    try {
      var sub = await currentSubscription();
      if (sub) await unsubscribeEndpoint(sub.endpoint, sub);
    } catch (_) {
    } finally {
      form.dataset.pushLogoutDone = '1';
      form.submit();
    }
  });

  window.addEventListener('load', function () {
    hydrateBell().catch(function () {});
    refreshQueueBells().catch(function () {});
    refreshProfile().catch(function (err) {
      setStatus(err.message || 'Failed', 'bad');
    });
  });
})();
