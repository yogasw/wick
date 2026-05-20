// Guard tab — passive list of guard violations is server-rendered
// into the tab's panel. The active part is painting per-node error
// badges on the canvas whenever validation arrives, so the operator
// sees red rings on the offending nodes without opening this tab.
//
// Subscribes to `validation` on window.WfEditor — emitted by editor.js
// after every auto-save, on initial page load, and on a failed Publish.
//
// Phase B note: badge painting used to live in editor.js as
// applyValidation(); the responsibility moved here so the canvas
// reaction to validation is owned by a single file.
(function () {
  function attach() {
    if (!window.WfEditor || typeof window.WfEditor.on !== 'function') return false;
    window.WfEditor.on('validation', paintBadges);
    return true;
  }
  if (!attach()) {
    document.addEventListener('DOMContentLoaded', attach, { once: true });
    requestAnimationFrame(attach);
  }

  // paintBadges walks every canvas node, strips the previous error
  // ring + badge, then re-paints from the per-node error map. Null
  // payload (or an empty by_node map) means "clean slate" — used by
  // the save-error path so a stale badge can never linger.
  function paintBadges(v) {
    document.querySelectorAll('.drawflow-node').forEach((el) => {
      el.classList.remove('wf-node-error');
      const old = el.querySelector('.wf-error-badge');
      if (old) old.remove();
    });
    if (!v || !v.by_node) return;
    const bus = window.WfEditor;
    Object.entries(v.by_node).forEach(([nodeID, msgs]) => {
      const domID = bus.domIDFromWorkflowID(nodeID);
      if (!domID) return;
      const el = document.getElementById('node-' + domID);
      if (!el) return;
      el.classList.add('wf-node-error');
      const badge = document.createElement('div');
      badge.className = 'wf-error-badge';
      badge.title = msgs.join('\n');
      badge.textContent = '!';
      el.appendChild(badge);
    });
  }
})();
