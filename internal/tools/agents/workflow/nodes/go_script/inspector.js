// go_script inspector module. Upgrades the `code` textarea row to an
// Ace Editor instance (Go mode) so users get syntax highlighting,
// gutter, and line numbers instead of staring at plain text. Ace
// loads lazily from CDN on first inspector open — keeps boot fast for
// workflows that never use this node.
//
// The Ace instance writes back into the underlying textarea on every
// keystroke so existing collectArgs / hydrateArgsForm helpers see the
// same shape they do for any other textarea field.
(function () {
  'use strict';

  const ARG_KEYS = ['code', 'timeout_sec'];
  const ACE_BASE = 'https://cdn.jsdelivr.net/npm/ace-builds@1.32.7/src-min-noconflict';
  let aceLoading = null;

  function container() { return document.getElementById('ins-goscript-args'); }

  function loadAce() {
    if (window.ace) return Promise.resolve(window.ace);
    if (aceLoading) return aceLoading;
    aceLoading = new Promise((resolve, reject) => {
      const s = document.createElement('script');
      s.src = ACE_BASE + '/ace.js';
      s.async = true;
      s.onload = () => {
        try {
          window.ace.config.set('basePath', ACE_BASE);
          window.ace.config.set('modePath', ACE_BASE);
          window.ace.config.set('themePath', ACE_BASE);
          window.ace.config.set('workerPath', ACE_BASE);
          resolve(window.ace);
        } catch (e) {
          reject(e);
        }
      };
      s.onerror = () => reject(new Error('failed to load ace from ' + ACE_BASE));
      document.head.appendChild(s);
    });
    return aceLoading;
  }

  function isDark() {
    return document.documentElement.classList.contains('dark') ||
      document.body.classList.contains('dark');
  }

  function upgradeCodeField(wrap) {
    if (!wrap || wrap.dataset.aceReady === '1') return;
    const ta = wrap.querySelector('textarea');
    if (!ta) return;
    return loadAce().then((ace) => {
      const host = document.createElement('div');
      host.className = 'wf-goscript-editor';
      host.style.cssText = 'min-height:240px;border:1px solid var(--ace-border,#3b4252);border-radius:6px;overflow:hidden;font-family:ui-monospace,Menlo,monospace;font-size:12px;';
      ta.style.display = 'none';
      ta.parentNode.insertBefore(host, ta);
      const editor = ace.edit(host);
      editor.session.setMode('ace/mode/golang');
      editor.setTheme(isDark() ? 'ace/theme/tomorrow_night_bright' : 'ace/theme/chrome');
      editor.session.setUseWorker(true);
      editor.session.setTabSize(2);
      editor.session.setUseSoftTabs(true);
      editor.setOptions({
        showPrintMargin: false,
        highlightActiveLine: true,
        fontSize: 12,
        minLines: 12,
        maxLines: 36,
        scrollPastEnd: 0.5,
      });
      editor.session.setValue(ta.value || '', -1);
      editor.session.on('change', () => {
        ta.value = editor.session.getValue();
        ta.dispatchEvent(new Event('input', { bubbles: true }));
      });
      wrap._aceEditor = editor;
      wrap.dataset.aceReady = '1';

      const themeObserver = new MutationObserver(() => {
        editor.setTheme(isDark() ? 'ace/theme/tomorrow_night_bright' : 'ace/theme/chrome');
      });
      themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
    }).catch((e) => {
      console.warn('go_script: ace editor failed to load, falling back to textarea', e);
    });
  }

  function findCodeWrap(c) {
    return c ? c.querySelector('.wf-arg-field[data-field-key="code"]') : null;
  }

  // Default starter program. Shows the stdin → stdout JSON contract so
  // new users see the shape immediately instead of a blank editor.
  const DEFAULT_CODE = [
    'package main',
    '',
    'import (',
    '\t"encoding/json"',
    '\t"os"',
    ')',
    '',
    'func main() {',
    '\tvar ctx map[string]any',
    '\tjson.NewDecoder(os.Stdin).Decode(&ctx)',
    '\t// ev := ctx["Event"].(map[string]any)["Payload"].(map[string]any)',
    '\tjson.NewEncoder(os.Stdout).Encode(map[string]any{',
    '\t\t"ok": true,',
    '\t})',
    '}',
    '',
  ].join('\n');

  function buildArgsFromInner(inner) {
    return {
      code: inner.code || '',
      timeout_sec: inner.timeout_sec ? String(inner.timeout_sec) : '',
    };
  }

  function syncEditorFrom(wrap, value) {
    const editor = wrap && wrap._aceEditor;
    if (!editor) return;
    if (editor.session.getValue() !== value) {
      const cursor = editor.getCursorPosition();
      editor.session.setValue(value, -1);
      try { editor.moveCursorToPosition(cursor); } catch (_) {}
    }
  }

  const mod = {
    meta: {
      kind: 'go_script',
      head: 'go_script',
      hint: 'stdin → stdout JSON',
      cssType: 'go_script',
      inputs: 1,
      outputs: 1,
      defaults: { code: DEFAULT_CODE },
    },

    onDrop(data) {
      if (!('code' in data) || !data.code) data.code = DEFAULT_CODE;
    },

    hydrate(inner) {
      const helpers = window.wickEditorHelpers;
      const c = container();
      if (!helpers || !c) return;
      const args = buildArgsFromInner(inner);
      const modes = inner.__arg_modes || {};
      if (!c.dataset.hydrated) {
        const html = c.innerHTML;
        helpers.hydrateArgsForm(c, html, args, modes, '');
        c.dataset.hydrated = '1';
        setTimeout(() => upgradeCodeField(findCodeWrap(c)), 0);
      } else {
        c.querySelectorAll('.wf-arg-field').forEach((wrap) => {
          const k = wrap.dataset.fieldKey;
          if (k === 'code') {
            const ta = wrap.querySelector('textarea');
            if (ta) ta.value = args.code || '';
            syncEditorFrom(wrap, args.code || '');
          } else {
            const editable = wrap.querySelector('input:not([type="hidden"]), select, textarea');
            if (editable && k in args) editable.value = args[k];
          }
          helpers.setArgFieldMode(wrap, modes[k] || 'fixed', false);
        });
      }
    },

    save(inner) {
      const helpers = window.wickEditorHelpers;
      const c = container();
      if (!helpers || !c) return;
      const codeWrap = findCodeWrap(c);
      if (codeWrap && codeWrap._aceEditor) {
        const ta = codeWrap.querySelector('textarea');
        if (ta) ta.value = codeWrap._aceEditor.session.getValue();
      }
      const args = helpers.collectArgs(c);
      const modes = helpers.collectArgModes(c);
      inner.code = args.code || '';
      const t = parseInt(args.timeout_sec || '', 10);
      inner.timeout_sec = Number.isFinite(t) && t > 0 ? t : 0;
      const trimmed = {};
      for (const k of ARG_KEYS) {
        if (modes[k] && modes[k] !== 'fixed') trimmed[k] = modes[k];
      }
      if (Object.keys(trimmed).length > 0) inner.__arg_modes = trimmed;
      else delete inner.__arg_modes;
    },

    attach({ requestUpdate }) {
      void requestUpdate;
    },
  };

  window.WickNodes = window.WickNodes || {};
  window.WickNodes.go_script = mod;
})();
