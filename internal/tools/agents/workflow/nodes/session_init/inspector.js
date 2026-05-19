// session_init inspector module. Registered into the global
// window.WickNodes registry; editor.js dispatches hydrate/save/onDrop
// to the entry matching the selected node's type. Pure vanilla — no
// build step, no imports.
(function () {
  'use strict';

  function uuid() {
    if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
      return crypto.randomUUID();
    }
    return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
      const r = (Math.random() * 16) | 0;
      const v = c === 'x' ? r : (r & 0x3) | 0x8;
      return v.toString(16);
    });
  }

  function toggleCustomWrap(mode) {
    const wrap = document.getElementById('ins-session-custom-wrap');
    if (wrap) wrap.classList.toggle('hidden', mode !== 'custom');
  }

  const mod = {
    // meta is consumed by editor.js nodeMeta() when a fresh node is
    // dropped from the palette. Matches the Go-side registry's
    // Render() output so canvas + server agree on port counts and
    // CSS class.
    meta: {
      kind: 'session_init',
      head: 'session',
      hint: 'default ID',
      cssType: 'session_init',
      inputs: 1,
      outputs: 1,
      defaults: { preset: 'workflow_run', session_id: '' },
    },

    // onDrop seeds default fields when the node is first dragged
    // onto the canvas. workflow_run preset means "per-run sessionID,
    // shared across agent nodes in same run" — the common case that
    // needs zero typing.
    onDrop(data) {
      if (!data.preset) data.preset = 'workflow_run';
      if (!('session_id' in data)) data.session_id = '';
    },

    // hydrate populates the inspector controls from saved node data.
    // The mode dropdown derives from (preset, session_id): a populated
    // session_id means the user picked "custom" and typed an override.
    hydrate(inner) {
      const hasCustom = !!(inner.session_id && String(inner.session_id).length);
      const mode = hasCustom ? 'custom' : (inner.preset || 'workflow_run');
      const modeEl = document.getElementById('ins-session-mode');
      const idEl = document.getElementById('ins-session-id');
      if (modeEl) modeEl.value = mode;
      if (idEl) idEl.value = inner.session_id || '';
      toggleCustomWrap(mode);
    },

    // save reads the inspector controls back into the node data.
    // The YAML carries either preset OR session_id (mutually
    // exclusive); resolver in Go gives session_id priority when both
    // are set, but the UI keeps them clean.
    save(inner) {
      const modeEl = document.getElementById('ins-session-mode');
      const idEl = document.getElementById('ins-session-id');
      const mode = modeEl ? modeEl.value : 'workflow_run';
      if (mode === 'custom') {
        inner.session_id = idEl ? idEl.value.trim() : '';
        inner.preset = '';
      } else {
        inner.session_id = '';
        inner.preset = mode;
      }
    },

    // attach is called once per page load — wire DOM listeners for
    // controls that need to react beyond input/change (the regen
    // button + mode toggle).
    attach({ requestUpdate }) {
      document.getElementById('ins-session-regen')?.addEventListener('click', () => {
        const idEl = document.getElementById('ins-session-id');
        if (idEl) {
          idEl.value = uuid();
          requestUpdate();
        }
      });
      document.getElementById('ins-session-mode')?.addEventListener('change', (e) => {
        const mode = e.target.value;
        toggleCustomWrap(mode);
        // Auto-seed a UUID when switching INTO custom so the input
        // has something to edit. User can replace with a template.
        const idEl = document.getElementById('ins-session-id');
        if (mode === 'custom' && idEl && !idEl.value) {
          idEl.value = uuid();
        }
        requestUpdate();
      });
    },
  };

  window.WickNodes = window.WickNodes || {};
  window.WickNodes.session_init = mod;
})();
