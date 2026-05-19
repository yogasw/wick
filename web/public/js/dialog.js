// dialog.js — global confirm/alert modal driver.
//
// Exposes window.wickConfirm(message, opts) and window.wickAlert(message, opts)
// as Promise-returning replacements for the browser's native confirm()/alert().
// The DOM shell is rendered once per page by @ui.Dialog (see
// internal/pkg/ui/dialog.templ); this script slots text and resolves
// the promise on OK / Cancel / ESC / backdrop click.
//
// Usage:
//   const ok = await wickConfirm('Delete 3 nodes?', { title: 'Delete nodes', danger: true });
//   if (!ok) return;
//   await wickAlert('Saved.');
//
// Options:
//   title:   string  — header text (defaults to "Confirm" / "Notice")
//   ok:      string  — primary button label (defaults to "OK")
//   cancel:  string  — cancel label (defaults to "Cancel"; pass empty
//                      to hide for alert-style dialogs)
//   danger:  bool    — primary button gets red instead of green; use
//                      for destructive confirmations (delete, discard).
(function () {
  'use strict';

  // Resolve state lives in module scope rather than dataset attrs so
  // overlapping calls (rare but possible from async code) reject the
  // older promise before opening the new dialog. Without this an
  // earlier awaiter would hang forever after the second open.
  let active = null;

  function el() { return document.getElementById('wickdlg'); }

  function close(result) {
    const dlg = el();
    if (!dlg) return;
    dlg.classList.add('hidden');
    document.removeEventListener('keydown', onKey, true);
    if (active) {
      const { resolve } = active;
      active = null;
      resolve(result);
    }
  }

  function onKey(e) {
    if (e.key === 'Escape') {
      e.stopPropagation();
      close(false);
    } else if (e.key === 'Enter') {
      // Enter activates the primary action — matches native confirm()
      // behaviour where the OK button takes default focus.
      e.stopPropagation();
      e.preventDefault();
      close(true);
    }
  }

  function open(message, opts, expectCancel) {
    const dlg = el();
    if (!dlg) {
      // No dialog shell on this page — fall back to native to avoid
      // silently swallowing the prompt. Devs see the same UX they had
      // before mounting <Dialog> in their layout.
      return Promise.resolve(expectCancel ? window.confirm(message) : (window.alert(message), true));
    }
    if (active) {
      // Reject the prior awaiter so we don't leak the promise.
      const prior = active;
      active = null;
      prior.resolve(false);
    }
    opts = opts || {};
    const title = opts.title || (expectCancel ? 'Confirm' : 'Notice');
    const okLabel = opts.ok || 'OK';
    const cancelLabel = opts.cancel === '' ? '' : (opts.cancel || 'Cancel');

    dlg.querySelector('[data-wickdlg-title]').textContent = title;
    dlg.querySelector('[data-wickdlg-body]').textContent = message;

    const okBtn = dlg.querySelector('[data-wickdlg-confirm]');
    const cancelBtn = dlg.querySelector('[data-wickdlg-cancel][type="button"]');
    okBtn.textContent = okLabel;
    // Danger variant — red primary so destructive confirms read as
    // a stop sign. Tailwind utilities applied directly so the rule
    // doesn't depend on a global stylesheet edit.
    okBtn.classList.remove('bg-green-500', 'hover:bg-green-600', 'bg-red-600', 'hover:bg-red-700');
    if (opts.danger) {
      okBtn.classList.add('bg-red-600', 'hover:bg-red-700');
    } else {
      okBtn.classList.add('bg-green-500', 'hover:bg-green-600');
    }
    if (expectCancel && cancelLabel) {
      cancelBtn.textContent = cancelLabel;
      cancelBtn.classList.remove('hidden');
    } else {
      // Alert dialog — single OK button. Hide the cancel button but
      // keep the backdrop's data-wickdlg-cancel binding so ESC /
      // outside-click still dismiss with `false` (callers ignore it).
      cancelBtn.classList.add('hidden');
    }

    dlg.classList.remove('hidden');
    document.addEventListener('keydown', onKey, true);
    // Defer focus so the dialog has time to lay out before we steal
    // focus from the trigger element.
    setTimeout(() => { try { okBtn.focus(); } catch (_) {} }, 0);

    return new Promise((resolve) => {
      active = { resolve };
    });
  }

  function ensureWiring() {
    const dlg = el();
    if (!dlg || dlg.dataset.wired === '1') return;
    dlg.dataset.wired = '1';
    dlg.querySelectorAll('[data-wickdlg-cancel]').forEach((b) => {
      b.addEventListener('click', () => close(false));
    });
    dlg.querySelector('[data-wickdlg-confirm]').addEventListener('click', () => close(true));
  }
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', ensureWiring);
  } else {
    ensureWiring();
  }

  window.wickConfirm = function (message, opts) { return open(message, opts, true); };
  window.wickAlert = function (message, opts) { return open(message, opts, false); };
})();
