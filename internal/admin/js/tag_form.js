// Background save for the admin tag forms.
//
// Every tag editor on these pages used to be a plain POST + 302: saving
// one row reloaded the whole page and threw away your scroll position.
// Tagging twenty users meant twenty reloads, and the reload is also what
// made it impossible to tell which rows you had already saved — the page
// came back looking exactly the same either way.
//
// So: post in the background, and let the Save button carry the state.
// It is HIDDEN while a row matches what the server has, appears the
// moment you change something, and disappears again once the save lands.
// A visible Save button therefore means exactly one thing — this row has
// unsaved changes — which is the signal you actually want when working
// down a long list.
//
// Progressive enhancement: with JS off (or this file failing to load)
// every form is still a normal POST that redirects, exactly as before.
(function () {
  const ASYNC_HEADER = 'X-Wick-Async';

  // formState serializes the parts of a form a human can change, so
  // "dirty" is a comparison rather than a pile of per-widget listeners.
  function formState(form) {
    const parts = [];
    const picker = form.querySelector('.tag-picker');
    if (picker) {
      const ids = Array.from(form.querySelectorAll('input[name="tag_ids[]"]'))
        .map(i => i.value).filter(Boolean).sort();
      parts.push('tags:' + ids.join(','));
    }
    form.querySelectorAll('input, select, textarea').forEach(el => {
      if (!el.name || el.name === 'tag_ids[]') return;
      if (el.type === 'checkbox' || el.type === 'radio') {
        parts.push(el.name + '=' + (el.checked ? '1' : '0'));
      } else {
        parts.push(el.name + '=' + el.value);
      }
    });
    return parts.join('|');
  }

  // baselineState is what the SERVER currently holds, read from the
  // markup it rendered — not from the live inputs, which the picker
  // rewrites asynchronously as it initialises.
  function baselineState(form) {
    const parts = [];
    const picker = form.querySelector('.tag-picker');
    if (picker) {
      const ids = (picker.dataset.selected || '')
        .split(',').map(s => s.trim()).filter(Boolean).sort();
      parts.push('tags:' + ids.join(','));
    }
    form.querySelectorAll('input, select, textarea').forEach(el => {
      if (!el.name || el.name === 'tag_ids[]') return;
      if (el.type === 'checkbox' || el.type === 'radio') {
        parts.push(el.name + '=' + (el.defaultChecked ? '1' : '0'));
      } else if (el.tagName === 'SELECT') {
        const def = Array.from(el.options).find(o => o.defaultSelected) || el.options[0];
        parts.push(el.name + '=' + (def ? def.value : ''));
      } else {
        parts.push(el.name + '=' + el.defaultValue);
      }
    });
    return parts.join('|');
  }

  function enhance(form) {
    if (form.dataset.tagFormInit === '1') return;
    form.dataset.tagFormInit = '1';

    const btn = form.querySelector('button[type="submit"], input[type="submit"]');
    if (!btn) return;

    let baseline = baselineState(form);
    const status = document.createElement('span');
    status.className = 'ml-2 text-xs text-black-700 dark:text-black-600';
    btn.insertAdjacentElement('afterend', status);

    function refresh() {
      const dirty = formState(form) !== baseline;
      btn.hidden = !dirty;
      if (dirty) status.textContent = '';
    }

    // The picker rewrites its hidden inputs on every chip change and
    // emits no event, so watch the DOM rather than asking it to.
    const observer = new MutationObserver(refresh);
    observer.observe(form, { childList: true, subtree: true, attributes: true, attributeFilter: ['value'] });
    form.addEventListener('input', refresh);
    form.addEventListener('change', refresh);
    refresh();

    form.addEventListener('submit', function (e) {
      e.preventDefault();
      btn.disabled = true;
      status.textContent = 'saving…';
      fetch(form.action, {
        method: form.method || 'POST',
        body: new FormData(form),
        headers: { [ASYNC_HEADER]: '1' },
        credentials: 'same-origin',
      }).then(function (r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        // The row now matches the server, so the button goes away and
        // the new state becomes the thing we compare against.
        const picker = form.querySelector('.tag-picker');
        if (picker) {
          picker.dataset.selected = Array.from(form.querySelectorAll('input[name="tag_ids[]"]'))
            .map(i => i.value).filter(Boolean).join(',');
        }
        form.querySelectorAll('input, select, textarea').forEach(el => {
          if (!el.name || el.name === 'tag_ids[]') return;
          if (el.type === 'checkbox' || el.type === 'radio') {
            el.defaultChecked = el.checked;
          } else if (el.tagName === 'SELECT') {
            Array.from(el.options).forEach(o => { o.defaultSelected = o.selected; });
          } else {
            el.defaultValue = el.value;
          }
        });
        baseline = baselineState(form);
        status.textContent = 'saved';
        setTimeout(function () { if (status.textContent === 'saved') status.textContent = ''; }, 2000);
      }).catch(function (err) {
        // Keep the button: the row still differs from the server, which
        // is exactly what the button is there to say.
        status.textContent = 'not saved (' + err.message + ')';
      }).finally(function () {
        btn.disabled = false;
        refresh();
      });
    });
  }

  function init() {
    document.querySelectorAll('.tag-picker').forEach(function (p) {
      const form = p.closest('form');
      if (form) enhance(form);
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
  // Rows can be added after load (filters, lazy sections); re-scan cheaply.
  document.addEventListener('wick:rows-updated', init);
})();
