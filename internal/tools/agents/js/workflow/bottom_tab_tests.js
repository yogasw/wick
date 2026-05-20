// Tests tab — manages workflow test fixtures (__tests__/<name>.yaml).
// Owns the tab's lazy-load trigger, the per-row delegated handlers
// (run/edit/delete/run-all), and the test-case editor modal.
//
// Self-contained: reads base/id from #wf-tc-modal's dataset (server
// renders them into the modal shell at page load), so nothing here
// depends on the WfEditor bus. That isolation is intentional — the
// Tests panel works the same whether the editor JS is healthy or not.
(function () {
  const testsBtn = document.querySelector('[data-bottom-tab="tests"]');
  if (!testsBtn) return;

  // Tests tab click → load manager panel (GET /test-cases)
  testsBtn.addEventListener('click', () => {
    const target = document.getElementById('wf-test-results');
    if (!target) return;
    const base = document.getElementById('wf-tc-modal')?.dataset.base || '';
    const id = document.getElementById('wf-tc-modal')?.dataset.id || '';
    if (!base || !id) return;
    target.innerHTML = '<span class="p-3 italic text-xs text-black-600 dark:text-black-700">Loading…</span>';
    fetch(`${base}/workflows/edit/${id}/test-cases`)
      .then(r => r.text())
      .then(html => { target.innerHTML = html; bindTestManager(base, id); })
      .catch(err => { target.innerHTML = `<span class="text-red-600 text-xs p-3">${err.message}</span>`; });
  });

  // Run All (delegated — button is injected dynamically with the manager HTML)
  document.getElementById('wf-test-results')?.addEventListener('click', (e) => {
    const runAll = e.target.closest('[data-wf-tc-run-all]');
    if (runAll) {
      const url = runAll.dataset.wfTcRunAll;
      const target = document.getElementById('wf-test-results');
      target.innerHTML = '<span class="p-3 italic text-xs text-black-600 dark:text-black-700">Running all tests…</span>';
      fetch(url, { method: 'POST' })
        .then(r => r.text())
        .then(html => { target.innerHTML = html; })
        .catch(err => { target.innerHTML = `<span class="text-red-600 text-xs p-3">${err.message}</span>`; });
    }
  });

  function bindTestManager(base, id) {
    const panel = document.getElementById('wf-test-results');
    if (!panel) return;

    // + New button
    panel.querySelector('[data-wf-tc-new]')?.addEventListener('click', () => openTCModal());

    // Run single (delegated)
    panel.addEventListener('click', (e) => {
      const btn = e.target.closest('[data-wf-tc-run]');
      if (!btn) return;
      const url = btn.dataset.wfTcRun;
      const rowKey = btn.dataset.wfTcRow;
      const row = document.getElementById('wf-tc-row-' + rowKey);
      if (row) row.style.opacity = '0.5';
      fetch(url, { method: 'POST' })
        .then(r => r.text())
        .then(html => {
          if (row) row.outerHTML = html;
        })
        .catch(err => { if (row) row.style.opacity = '1'; console.warn(err); });
    });

    // Edit (delegated)
    panel.addEventListener('click', (e) => {
      const btn = e.target.closest('[data-wf-tc-edit]');
      if (!btn) return;
      const name = btn.dataset.wfTcEdit;
      let tc = {};
      try { tc = JSON.parse(btn.dataset.wfTcJson || '{}'); } catch (_) {}
      openTCModal(name, tc);
    });

    // Delete (delegated)
    panel.addEventListener('click', (e) => {
      const btn = e.target.closest('[data-wf-tc-delete]');
      if (!btn) return;
      const rowKey = btn.dataset.wfTcRow;
      if (!confirm(`Delete test case "${btn.dataset.wfTcDelete.split('/').pop()}"?`)) return;
      fetch(btn.dataset.wfTcDelete, { method: 'DELETE' })
        .then(r => r.json())
        .then(d => {
          if (d.ok) {
            const row = document.getElementById('wf-tc-row-' + rowKey);
            if (row) row.remove();
          }
        });
    });
  }

  // ── Test Case Modal ───────────────────────────────────────────────
  const tcModal   = document.getElementById('wf-tc-modal');
  const tcSaveBtn = document.getElementById('wf-tc-save');
  const tcAddAsrt = document.getElementById('wf-tc-add-assertion');
  const tcAsrtBox = document.getElementById('wf-tc-assertions');
  const tcEvtType = document.getElementById('wf-tc-evt-type');
  const tcError   = document.getElementById('wf-tc-error');

  function openTCModal(editName, tc) {
    if (!tcModal) return;
    const isEdit = !!editName;
    document.getElementById('wf-tc-modal-title').textContent = isEdit ? 'Edit Test Case' : 'New Test Case';
    document.getElementById('wf-tc-edit-name').value = editName || '';
    document.getElementById('wf-tc-name').value = editName || '';
    document.getElementById('wf-tc-name').disabled = isEdit;

    // Fill event fields
    const evt = tc?.input?.Event || {};
    if (tcEvtType) tcEvtType.value = evt.type || 'manual';
    const ch = document.getElementById('wf-tc-channel');
    const st = document.getElementById('wf-tc-subtype');
    if (ch) ch.value = evt.channel || 'slack';
    if (st) st.value = evt.subtype || 'message';
    tcUpdateChannelVis();
    const payload = evt.payload || {};
    const ta = document.getElementById('wf-tc-payload');
    if (ta) ta.value = Object.keys(payload).length ? JSON.stringify(payload, null, 2) : '';

    // Fill assertions
    if (tcAsrtBox) {
      tcAsrtBox.innerHTML = '';
      (tc?.assertions || [{ subject: 'status', operator: '==', value: 'completed' }])
        .forEach(a => tcAddAssertionRow(a));
    }
    if (tcError) { tcError.textContent = ''; tcError.classList.add('hidden'); }
    tcModal.classList.remove('hidden');
    document.getElementById('wf-tc-name')?.focus();
  }

  function closeTCModal() { tcModal?.classList.add('hidden'); }

  tcModal?.querySelectorAll('[data-wf-tc-cancel]').forEach(el =>
    el.addEventListener('click', closeTCModal)
  );
  document.addEventListener('keydown', e => { if (e.key === 'Escape') closeTCModal(); });

  function tcUpdateChannelVis() {
    const isChannel = tcEvtType?.value === 'channel';
    document.getElementById('wf-tc-channel-col')?.classList.toggle('hidden', !isChannel);
    document.getElementById('wf-tc-subtype-col')?.classList.toggle('hidden', !isChannel);
  }
  tcEvtType?.addEventListener('change', tcUpdateChannelVis);
  tcUpdateChannelVis();

  function tcAddAssertionRow(a) {
    if (!tcAsrtBox) return;
    const row = document.createElement('div');
    row.className = 'wf-tc-assertion-row';
    const ops = ['==','!=','contains','case_fired','node_skipped','path_taken','edge_traversed'];
    const sel = ops.map(o => `<option value="${o}"${a?.operator===o?' selected':''}>${o}</option>`).join('');
    row.innerHTML = `
      <input class="wf-input text-xs" placeholder="status or node.id.field" value="${a?.subject||''}"/>
      <select class="wf-input text-xs">${sel}</select>
      <input class="wf-input text-xs" placeholder="completed" value="${a?.value||''}"/>
      <button type="button" class="text-red-500 hover:text-red-700 text-sm font-bold" data-remove-row>✕</button>`;
    row.querySelector('[data-remove-row]').addEventListener('click', () => row.remove());
    tcAsrtBox.appendChild(row);
  }
  tcAddAsrt?.addEventListener('click', () => tcAddAssertionRow(null));

  tcSaveBtn?.addEventListener('click', async () => {
    const base = tcModal.dataset.base;
    const id   = tcModal.dataset.id;
    const name = document.getElementById('wf-tc-name').value.trim();
    if (!name) { showTCError('Name is required'); return; }

    let payload = {};
    const rawPayload = document.getElementById('wf-tc-payload').value.trim();
    if (rawPayload) {
      try { payload = JSON.parse(rawPayload); }
      catch (_) { showTCError('Payload is not valid JSON'); return; }
    }

    const assertions = [];
    tcAsrtBox?.querySelectorAll('.wf-tc-assertion-row').forEach(row => {
      const [subj, op, val] = row.querySelectorAll('input, select');
      if (subj.value.trim()) assertions.push({ subject: subj.value.trim(), operator: op.value, value: val.value.trim() });
    });

    const evt = {
      type: tcEvtType?.value || 'manual',
      payload,
    };
    if (evt.type === 'channel') {
      evt.channel = document.getElementById('wf-tc-channel')?.value || 'slack';
      evt.subtype = document.getElementById('wf-tc-subtype')?.value || 'message';
    }

    tcSaveBtn.disabled = true;
    tcSaveBtn.textContent = 'Saving…';
    try {
      const resp = await fetch(`${base}/workflows/edit/${id}/test-cases`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, input: { Event: evt }, assertions }),
      });
      const data = await resp.json();
      if (!resp.ok) { showTCError(data.error || `HTTP ${resp.status}`); return; }
      closeTCModal();
      const target = document.getElementById('wf-test-results');
      fetch(`${base}/workflows/edit/${id}/test-cases`)
        .then(r => r.text())
        .then(html => { if (target) { target.innerHTML = html; bindTestManager(base, id); } });
    } catch (err) {
      showTCError(err.message);
    } finally {
      tcSaveBtn.disabled = false;
      tcSaveBtn.textContent = 'Save Test Case';
    }
  });

  function showTCError(msg) {
    if (!tcError) return;
    tcError.textContent = msg;
    tcError.classList.remove('hidden');
  }
})();
