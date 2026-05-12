(function () {
  'use strict';

  const MAX_RETRIES = 3;
  let retries = 0;
  let iframe = null;
  let base = '';

  function init() {
    const root = document.getElementById('webtty-root');
    if (!root) return;
    base = root.dataset.base || '';

    iframe = document.getElementById('webtty-frame');
    if (!iframe) return;

    iframe.addEventListener('load', onFrameLoad);

    const startBtn = document.getElementById('webtty-start');
    const stopBtn = document.getElementById('webtty-stop');
    if (startBtn) startBtn.addEventListener('click', startSession);
    if (stopBtn) stopBtn.addEventListener('click', stopSession);
  }

  function onFrameLoad() {
    retries++;
    if (retries >= MAX_RETRIES) {
      setStatus('error', 'Terminal failed to connect after ' + MAX_RETRIES + ' attempts. Check server logs.');
      iframe.style.display = 'none';
    }
  }

  function setStatus(state, msg) {
    const el = document.getElementById('webtty-status');
    if (!el) return;
    el.textContent = msg;
    el.className = el.className.replace(/\bstatus-\S+/g, '');
    el.classList.add('status-' + state);
  }

  function setPlaceholder(visible) {
    const el = document.getElementById('webtty-placeholder');
    if (el) el.style.display = visible ? '' : 'none';
  }

  function startSession() {
    retries = 0;
    fetch(base + '/tty/start', { method: 'POST' })
      .then(function (r) {
        if (!r.ok) throw new Error('start failed: ' + r.status);
        setStatus('running', 'Running');
        setPlaceholder(false);
        if (iframe) {
          iframe.style.display = '';
          iframe.src = iframe.src; // reload
        }
      })
      .catch(function (e) {
        setStatus('error', e.message);
      });
  }

  function stopSession() {
    fetch(base + '/tty/stop', { method: 'POST' })
      .then(function () {
        setStatus('stopped', 'Stopped');
        setPlaceholder(true);
        if (iframe) iframe.style.display = 'none';
      })
      .catch(function (e) {
        setStatus('error', e.message);
      });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
