// MCP Access page behaviors: copy-to-clipboard buttons + create-form toggle.
(function () {
  function flashCopied(btn) {
    var orig = btn.textContent;
    btn.textContent = 'Copied';
    btn.disabled = true;
    setTimeout(function () {
      btn.textContent = orig;
      btn.disabled = false;
    }, 1200);
  }

  function copyText(text, btn) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () {
        flashCopied(btn);
      });
      return;
    }
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); flashCopied(btn); } catch (_) {}
    document.body.removeChild(ta);
  }

  // Copy just-issued plaintext token.
  document.querySelectorAll('[data-copy-token]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var el = document.querySelector('[data-token-value]');
      if (el) copyText(el.textContent.trim(), btn);
    });
  });

  // Copy endpoint URL.
  document.querySelectorAll('[data-copy-endpoint]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var el = document.querySelector('[data-endpoint-value]');
      if (el) copyText(el.textContent.trim(), btn);
    });
  });

  // Copy install snippet (button is positioned inside its container).
  document.querySelectorAll('[data-copy-snippet]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var pre = btn.parentElement.querySelector('[data-snippet]');
      if (pre) copyText(pre.textContent, btn);
    });
  });

  // Toggle inline create form.
  var openBtn = document.querySelector('[data-open-create]');
  var form = document.querySelector('[data-create-form]');
  var cancelBtn = document.querySelector('[data-cancel-create]');
  if (openBtn && form) {
    openBtn.addEventListener('click', function () {
      form.classList.remove('hidden');
      var input = form.querySelector('input[name="name"]');
      if (input) input.focus();
    });
  }
  if (cancelBtn && form) {
    cancelBtn.addEventListener('click', function () {
      form.classList.add('hidden');
      var input = form.querySelector('input[name="name"]');
      if (input) input.value = '';
    });
  }
})();
