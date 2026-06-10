// Admin Users page: "Add user" form toggle + copy the one-time
// generated password. Mirrors the access-token create flow.
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

  // Copy the just-generated plaintext password.
  document.querySelectorAll('[data-copy-password]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var el = document.querySelector('[data-password-value]');
      if (el) copyText(el.textContent.trim(), btn);
    });
  });

  // Toggle the inline create form.
  var openBtn = document.querySelector('[data-open-create]');
  var form = document.querySelector('[data-create-form]');
  var cancelBtn = document.querySelector('[data-cancel-create]');
  if (openBtn && form) {
    openBtn.addEventListener('click', function () {
      form.classList.remove('hidden');
      var input = form.querySelector('input[name="email"]');
      if (input) input.focus();
    });
  }
  if (cancelBtn && form) {
    cancelBtn.addEventListener('click', function () {
      form.classList.add('hidden');
      form.querySelectorAll('input').forEach(function (i) { i.value = ''; });
    });
  }
})();
