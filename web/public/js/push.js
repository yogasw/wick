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

  // setBellState updates the floating notification bell to reflect the
  // current subscription + permission state. Three visual variants:
  //
  //   on       — subscribed + permission granted. Solid bell color +
  //              green dot. Click unsubscribes.
  //   off      — not subscribed, permission default (never asked) or
  //              granted-but-no-sub. Outline bell. Click subscribes
  //              (which pops the browser permission prompt if needed).
  //   blocked  — site permission denied. Bell with a diagonal slash.
  //              Click surfaces a toast pointing at site settings; the
  //              user must unblock manually.
  //
  // Hidden entirely when the browser doesn't support push (Safari < 16,
  // etc.) — no point teasing a feature the platform can't deliver.
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
    var baseCls = 'fixed top-3 right-3 z-20 flex h-9 w-9 items-center justify-center rounded-lg border border-white-300 dark:border-navy-600 bg-white-100/90 dark:bg-navy-700/90 backdrop-blur-sm shadow-sm transition-colors';
    if (state === 'on') {
      btn.className = baseCls + ' text-green-600 dark:text-green-400 hover:text-green-700 dark:hover:text-green-300';
      btn.setAttribute('title', 'Notifications enabled — click to disable for this browser');
      if (dot) dot.classList.remove('hidden');
      if (slash) slash.classList.add('hidden');
    } else if (state === 'blocked') {
      btn.className = baseCls + ' text-neg-400 hover:opacity-80';
      btn.setAttribute('title', 'Notifications blocked — unblock in site settings');
      if (dot) dot.classList.add('hidden');
      if (slash) slash.classList.remove('hidden');
    } else {
      // off (default / never asked)
      btn.className = baseCls + ' text-black-800 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100';
      btn.setAttribute('title', 'Enable notifications for this browser');
      if (dot) dot.classList.add('hidden');
      if (slash) slash.classList.add('hidden');
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

  // hydrateBell decides the bell icon state on initial page load and
  // any time the subscription state may have changed. Only runs on
  // /tools/agents/* — the bell is mounted by the agents layout shell,
  // so calling this elsewhere is a no-op via the early-return in
  // setBellState (missing #push-bell-btn).
  //
  // No auto-popup: we never call Notification.requestPermission() here.
  // The bell waits for an explicit click before triggering the browser
  // prompt — users who reflex-blocked a popup can't be re-asked, so we
  // make the ask deliberate.
  async function hydrateBell() {
    if (!document.getElementById('push-bell-btn')) return;
    if (!supportsPush()) {
      setBellState('unsupported');
      return;
    }
    var sub = await currentSubscription().catch(function () { return null; });
    if (Notification.permission === 'denied') {
      await recordPermission('denied');
      setBellState('blocked');
      return;
    }
    if (sub && Notification.permission === 'granted') {
      setBellState('on');
      return;
    }
    setBellState('off');
  }

  // handleBellClick implements the bell's state machine:
  //   on      → unsubscribe current browser, drop to off
  //   off     → subscribe (browser may prompt), promote to on
  //   blocked → no-op except toast pointing at site settings
  // Each transition surfaces a toast so the click feels confirmed.
  async function handleBellClick(btn) {
    var state = btn.dataset.state || 'off';
    if (state === 'blocked') {
      showToast('Notifications are blocked. Unblock in site settings to enable.', 'bad');
      return;
    }
    btn.disabled = true;
    try {
      if (state === 'on') {
        var sub = await currentSubscription();
        if (sub) await unsubscribeEndpoint(sub.endpoint, sub);
        setBellState('off');
        showToast('Notifications disabled for this browser.', '');
      } else {
        await subscribeCurrent();
        await recordPermission(Notification.permission);
        setBellState('on');
        showToast('Notifications enabled for this browser.', 'ok');
      }
      // Refresh the profile page device list if we're on it — the
      // bell toggle directly mutates a row there.
      await refreshProfile().catch(function () {});
    } catch (err) {
      // Subscribe can fail because the user clicked Block in the
      // browser prompt, or because the platform refused. Re-hydrate
      // so the bell reflects whatever the browser ended up doing
      // (typically: 'denied' → blocked state, dot → slash).
      await hydrateBell();
      showToast(err.message || 'Could not change notification state.', 'bad');
    } finally {
      btn.disabled = false;
    }
  }

  document.addEventListener('click', async function (e) {
    var bell = e.target.closest('#push-bell-btn');
    var enable = e.target.closest('#push-enable-btn');
    var test = e.target.closest('#push-test-btn');
    var remove = e.target.closest('[data-push-remove]');
    var copyID = e.target.closest('#push-copy-id-btn');
    try {
      if (bell) {
        await handleBellClick(bell);
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
    refreshProfile().catch(function (err) {
      setStatus(err.message || 'Failed', 'bad');
    });
  });
})();
