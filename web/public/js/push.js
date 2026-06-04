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

  function setAgentStatus(text, tone) {
    var el = document.getElementById('push-agent-status');
    if (!el) return;
    if (!text) {
      el.classList.add('hidden');
      el.textContent = '';
      return;
    }
    el.classList.remove('hidden', 'text-pos-400', 'text-neg-400');
    if (tone === 'ok') el.classList.add('text-pos-400');
    if (tone === 'bad') el.classList.add('text-neg-400');
    el.textContent = text;
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

  async function autoEnableForAgents() {
    if (!/^\/tools\/agents(?:\/|$)/.test(window.location.pathname)) return;
    if (!supportsPush()) {
      setAgentStatus('Notifications are not supported in this browser.', 'bad');
      return;
    }
    if (Notification.permission === 'denied') {
      await recordPermission('denied');
      setAgentStatus('Browser notifications are blocked. You can unblock them from site settings; manual controls remain in Account.', 'bad');
      return;
    }
    try {
      var sub = await currentSubscription();
      if (sub) {
        setAgentStatus('Notifications are enabled for this browser.', 'ok');
        return;
      }
      setAgentStatus('Wick wants to enable notifications for agent updates.', '');
      await subscribeCurrent();
      await recordPermission(Notification.permission);
      setAgentStatus('Notifications are enabled for agent updates.', 'ok');
    } catch (err) {
      if (Notification.permission === 'denied') {
        await recordPermission('denied');
        setAgentStatus('Browser notifications are blocked. You can unblock them from site settings; manual controls remain in Account.', 'bad');
      } else {
        setAgentStatus(err.message || 'Notifications were not enabled.', 'bad');
      }
    }
  }

  document.addEventListener('click', async function (e) {
    var enable = e.target.closest('#push-enable-btn');
    var test = e.target.closest('#push-test-btn');
    var remove = e.target.closest('[data-push-remove]');
    var copyID = e.target.closest('#push-copy-id-btn');
    try {
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
        await refreshProfile();
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
      }
      if (remove) {
        remove.disabled = true;
        var endpoint = remove.getAttribute('data-push-remove');
        var subForRemove = remove.getAttribute('data-current') === '1' ? await currentSubscription() : null;
        await unsubscribeEndpoint(endpoint, subForRemove);
        await refreshProfile();
      }
    } catch (err) {
      setStatus(err.message || 'Failed', 'bad');
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
    autoEnableForAgents().catch(function () {});
    refreshProfile().catch(function (err) {
      setStatus(err.message || 'Failed', 'bad');
    });
  });
})();
