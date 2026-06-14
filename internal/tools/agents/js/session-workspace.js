// session-workspace.js — the Session Workspace tab. A USER-initiated way
// to spin up ephemeral connector instances scoped to this session (clone a
// base connector, point it at staging, use a different key). Renders into
// the Workspace panel of the Context slide-over (see context_panel.templ).
// Instances POST to /sessions/{id}/workspace[/{cid}]; secrets are encrypted
// server-side, never echoed back, never seen by the agent. Cards are
// collapsible so a session with many instances stays tidy. The Workspace
// rail tab shows a count badge of how many connectors live in it.
(function () {
  "use strict";

  function panel() { return document.querySelector("[data-context-panel]"); }
  function base() { var p = panel(); return p ? (p.dataset.base || "") : ""; }
  function sessionID() { var p = panel(); return p ? (p.dataset.sessionId || "") : ""; }
  function listEl() { return document.querySelector("[data-config-list]"); }
  function wsURL(suffix) {
    return base() + "/sessions/" + encodeURIComponent(sessionID()) + "/workspace" + (suffix || "");
  }

  var INPUT_CLASS =
    "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none";

  // Remember which cards the user expanded so a reload (after save/test)
  // doesn't collapse the one they're working in.
  var expanded = {};

  function load() {
    var box = listEl();
    if (!box) return;
    if (!base() || !sessionID()) return;
    box.innerHTML = '<div class="flex items-center justify-center py-12 text-xs text-black-700 dark:text-black-600">Loading…</div>';
    fetch(wsURL(""))
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (data) { render(data || {}); })
      .catch(function () { box.innerHTML = errorLine("Failed to load."); });
  }

  function errorLine(msg) {
    return '<p class="text-xs text-neg-400">' + escapeHTML(msg) + "</p>";
  }
  function escapeHTML(s) {
    return String(s || "").replace(/[&<>"']/g, function (ch) {
      return ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[ch];
    });
  }

  // updateCount sets the Workspace rail-tab badge to the number of session
  // connectors, mirroring the Context / Process / Source badges. Hidden at
  // zero so an empty workspace shows no chip.
  function updateCount(n) {
    var badge = document.querySelector("[data-config-fab-count]");
    if (!badge) return;
    if (n > 0) {
      badge.textContent = n;
      badge.classList.remove("hidden");
      badge.classList.add("inline-flex");
    } else {
      badge.classList.add("hidden");
      badge.classList.remove("inline-flex");
    }
  }

  function render(data) {
    var box = listEl();
    if (!box) return;
    box.innerHTML = "";
    var instances = data.instances || [];
    var bases = data.bases || [];

    updateCount(instances.length);

    instances.forEach(function (inst) { box.appendChild(instanceCard(inst)); });

    if (!instances.length) {
      var empty = document.createElement("p");
      empty.className = "text-xs text-black-700 dark:text-black-600";
      empty.textContent = "No session connectors yet.";
      box.appendChild(empty);
    }

    if (bases.length) {
      box.appendChild(addPicker(bases));
    } else if (!instances.length) {
      var none = document.createElement("p");
      none.className = "text-[11px] text-black-700 dark:text-black-600";
      none.textContent = "No connector here is enabled for session instances. An admin turns this on per connector.";
      box.appendChild(none);
    }
  }

  // addPicker: pick a base connector, create a blank instance, reload
  // with its card expanded so the user fills it in immediately.
  function addPicker(bases) {
    var wrap = document.createElement("div");
    wrap.className = "pt-2 border-t border-white-300 dark:border-navy-600";
    var sel = document.createElement("select");
    sel.className = INPUT_CLASS;
    var ph = document.createElement("option");
    ph.value = "";
    ph.textContent = "+ Add a session connector…";
    sel.appendChild(ph);
    bases.forEach(function (b) {
      var o = document.createElement("option");
      o.value = b.base_key;
      o.textContent = b.label || b.base_key;
      sel.appendChild(o);
    });
    sel.addEventListener("change", function () {
      var key = sel.value;
      sel.value = "";
      if (!key) return;
      sel.disabled = true;
      fetch(wsURL(""), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ base_key: key }),
      })
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (inst) {
          if (inst && inst.id) expanded[inst.id] = true;
          load();
        })
        .catch(function () { sel.disabled = false; });
    });
    wrap.appendChild(sel);
    return wrap;
  }

  function statusBadge(status) {
    var b = document.createElement("span");
    var ok = status === "ready";
    b.className =
      "shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium " +
      (ok
        ? "bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300"
        : "bg-cau-100 text-cau-700 dark:bg-cau-900 dark:text-cau-300");
    b.textContent = ok ? "ready" : "needs setup";
    return b;
  }

  function instanceCard(inst) {
    var card = document.createElement("div");
    card.dataset.cid = inst.id;
    card.className = "rounded-xl border border-white-300 dark:border-navy-600 overflow-hidden";

    var isOpen = !!expanded[inst.id];

    // Header: chevron + label + status, click toggles body.
    var head = document.createElement("button");
    head.type = "button";
    head.className =
      "w-full flex items-center gap-2 px-3 py-2 bg-white-200 dark:bg-navy-800 text-left hover:bg-white-300 dark:hover:bg-navy-700 transition-colors";
    var chevron = document.createElement("span");
    chevron.className = "shrink-0 text-black-600 dark:text-black-700 transition-transform";
    chevron.style.transform = isOpen ? "rotate(90deg)" : "";
    chevron.innerHTML = '<svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"></path></svg>';
    head.appendChild(chevron);

    // Title group: label text + pencil. Clicking either swaps to an input
    // (rename inline); the header's own toggle is suppressed while editing.
    var titleWrap = document.createElement("span");
    titleWrap.className = "flex-1 min-w-0 flex items-center gap-1.5";
    var title = document.createElement("span");
    title.className = "min-w-0 truncate text-sm font-medium text-black-900 dark:text-white-100";
    title.textContent = inst.label || inst.id;
    titleWrap.appendChild(title);
    var pencil = document.createElement("span");
    pencil.className = "shrink-0 text-black-600 dark:text-black-700 hover:text-green-600 dark:hover:text-green-400 transition-colors cursor-pointer";
    pencil.setAttribute("title", "Rename");
    pencil.innerHTML = '<svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2"><path d="M11.5 2.5l2 2L6 12l-2.5.5.5-2.5 7.5-7.5z" stroke-linecap="round" stroke-linejoin="round"></path></svg>';
    titleWrap.appendChild(pencil);
    head.appendChild(titleWrap);
    head.appendChild(statusBadge(inst.status));
    card.appendChild(head);

    function beginRename(ev) {
      if (ev) { ev.preventDefault(); ev.stopPropagation(); }
      if (titleWrap.querySelector("input")) return;
      var cur = inst.label || "";
      var inp = document.createElement("input");
      inp.type = "text";
      inp.value = cur;
      inp.className =
        "flex-1 min-w-0 rounded-md border border-green-500 bg-white-100 dark:bg-navy-800 px-2 py-0.5 text-sm font-medium text-black-900 dark:text-white-100 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none";
      // Don't let clicks inside the input toggle the card.
      inp.addEventListener("click", function (e) { e.stopPropagation(); });
      title.classList.add("hidden");
      pencil.classList.add("hidden");
      titleWrap.appendChild(inp);
      inp.focus();
      inp.select();

      var done = false;
      function finish(save) {
        if (done) return;
        done = true;
        var next = (inp.value || "").trim();
        if (save && next && next !== cur) {
          postJSON(wsURL("/" + encodeURIComponent(inst.id) + "/rename"), { label: next }, function (ok, msg) {
            if (ok) { inst.label = next; title.textContent = next; }
            restore();
          });
        } else {
          restore();
        }
      }
      function restore() {
        if (inp.parentNode) inp.parentNode.removeChild(inp);
        title.classList.remove("hidden");
        pencil.classList.remove("hidden");
      }
      inp.addEventListener("keydown", function (e) {
        if (e.key === "Enter") { e.preventDefault(); finish(true); }
        else if (e.key === "Escape") { e.preventDefault(); finish(false); }
      });
      inp.addEventListener("blur", function () { finish(true); });
    }
    pencil.addEventListener("click", beginRename);

    var body = document.createElement("div");
    body.className = "p-3 space-y-3" + (isOpen ? "" : " hidden");
    head.addEventListener("click", function () {
      var nowOpen = body.classList.toggle("hidden") === false;
      expanded[inst.id] = nowOpen;
      chevron.style.transform = nowOpen ? "rotate(90deg)" : "";
    });

    var inputs = {};
    // onDirty repaints the action row (shows/hides Reset) the moment a
    // field's value first diverges from what was loaded.
    var onDirty = function () { updateDirtyUI(); };
    (inst.fields || []).forEach(function (f) {
      body.appendChild(fieldRow(f, inputs, onDirty));
    });

    var err = document.createElement("p");
    err.className = "hidden text-xs font-medium text-neg-400";
    body.appendChild(err);

    var testOut = document.createElement("p");
    testOut.className = "hidden text-xs font-medium";
    body.appendChild(testOut);

    // dirtyValues returns only the fields the user actually edited. A
    // field is "dirty" when its current value differs from the value it
    // loaded with (tracked in fieldRow). This is the single source of
    // truth for Save, Test, and Reset visibility — so a value the browser
    // autofilled but the user never touched is never sent anywhere, and
    // an unedited Test posts an empty config (server keeps the stored one).
    function dirtyValues() {
      var values = {};
      Object.keys(inputs).forEach(function (k) {
        var input = inputs[k];
        if (input.dataset.dirty !== "1") return;
        values[k] = (input.value || "").trim();
      });
      return values;
    }
    function anyDirty() {
      return Object.keys(inputs).some(function (k) { return inputs[k].dataset.dirty === "1"; });
    }

    // Action row: Save / Reset / Test / Duplicate / Delete.
    var actions = document.createElement("div");
    actions.className = "flex flex-wrap items-center gap-2 pt-1";

    var save = primaryBtn("Save");
    save.addEventListener("click", function () {
      var values = dirtyValues();
      if (!Object.keys(values).length) {
        showErr(err, "Nothing to save — edit a field first.");
        return;
      }
      err.classList.add("hidden");
      save.disabled = true;
      postJSON(wsURL("/" + encodeURIComponent(inst.id)), { values: values }, function (ok, msg) {
        save.disabled = false;
        if (ok) { expanded[inst.id] = true; load(); }
        else { showErr(err, msg || "Save failed."); }
      });
    });
    actions.appendChild(save);

    // Reset reverts every field to the value it loaded with and clears the
    // dirty flags. Only shown while something is dirty (updateDirtyUI).
    var reset = ghostBtn("Reset");
    reset.addEventListener("click", function () {
      Object.keys(inputs).forEach(function (k) {
        var input = inputs[k];
        input.value = input.dataset.orig || "";
        input.dataset.dirty = "0";
      });
      err.classList.add("hidden");
      testOut.classList.add("hidden");
      updateDirtyUI();
    });
    actions.appendChild(reset);

    // runTest probes the connector with the config CURRENTLY on screen.
    // Only edited fields are sent inline and overlaid on the saved config
    // for this probe alone — never persisted. So Test always exercises
    // what you see, with no Save-first dance and no autofill bleed-through.
    var test = ghostBtn("Test");
    test.addEventListener("click", function () {
      test.disabled = true;
      testOut.classList.remove("hidden");
      testOut.className = "text-xs font-medium text-black-700 dark:text-black-600";
      testOut.textContent = "Testing…";
      postJSONRaw(wsURL("/" + encodeURIComponent(inst.id) + "/test"), { config: dirtyValues() }, function (res) {
        test.disabled = false;
        if (!res) { testFeedback(testOut, false, "Test failed."); return; }
        if (res.ok) { testFeedback(testOut, true, "Looks good."); return; }
        if (res.no_health_check) { testFeedback(testOut, false, "No health check for this connector — run an operation to verify."); return; }
        testFeedback(testOut, false, res.error || "Test failed.");
      });
    });
    actions.appendChild(test);

    // updateDirtyUI shows Reset (and unlocks Save) only when there are
    // unsaved edits. Called on every field change and after a reset.
    function updateDirtyUI() {
      reset.classList.toggle("hidden", !anyDirty());
    }
    updateDirtyUI();

    var dup = ghostBtn("Duplicate");
    dup.addEventListener("click", function () {
      dup.disabled = true;
      fetch(wsURL("/" + encodeURIComponent(inst.id) + "/duplicate"), { method: "POST" })
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (n) { if (n && n.id) expanded[n.id] = true; load(); })
        .catch(function () { dup.disabled = false; });
    });
    actions.appendChild(dup);

    var del = document.createElement("button");
    del.type = "button";
    del.className = "ml-auto text-[11px] font-medium text-neg-400 hover:underline";
    del.textContent = "Remove";
    del.addEventListener("click", function () {
      fetch(wsURL("/" + encodeURIComponent(inst.id)), { method: "DELETE" })
        .then(function () { delete expanded[inst.id]; load(); })
        .catch(function () {});
    });
    actions.appendChild(del);

    body.appendChild(actions);
    card.appendChild(body);
    return card;
  }

  function fieldRow(f, inputs, onDirty) {
    var wrap = document.createElement("div");
    var label = document.createElement("label");
    label.className = "block text-xs font-medium text-black-800 dark:text-white-200 mb-1";
    label.textContent = f.label || f.key;
    if (f.required) {
      var req = document.createElement("span");
      req.className = "ml-1 text-neg-400";
      req.textContent = "*";
      label.appendChild(req);
    }
    if (f.set) {
      var s = document.createElement("span");
      s.className = "ml-1 text-[10px] text-green-600 dark:text-green-400";
      s.textContent = "• set";
      label.appendChild(s);
    }
    wrap.appendChild(label);

    var input;
    if (f.type === "dropdown") {
      input = document.createElement("select");
      input.className = INPUT_CLASS;
      (f.options || []).forEach(function (opt) {
        var o = document.createElement("option");
        o.value = opt; o.textContent = opt;
        if (opt === f.value) o.selected = true;
        input.appendChild(o);
      });
    } else {
      input = document.createElement("input");
      input.type = f.secret ? "password" : "text";
      input.className = INPUT_CLASS;
      // Secrets always load blank (the value is never echoed back); the
      // placeholder signals one is already set. Non-secrets prefill.
      input.value = f.secret ? "" : (f.value || "");
      input.placeholder = f.secret ? (f.set ? "•••••• (set — leave empty to keep)" : "Enter to set") : "";
      if (f.secret) input.autocomplete = "new-password";
    }
    // Dirty-tracking: remember the loaded value; a field counts as edited
    // only once its value diverges from it. Browser autofill that the user
    // never confirms leaves orig === value, so it stays clean and is never
    // sent. The check runs on each input/change, not just the first, so
    // typing then deleting back to the original clears the dirty flag.
    input.dataset.orig = input.value;
    input.dataset.dirty = "0";
    var markDirty = function () {
      input.dataset.dirty = input.value !== input.dataset.orig ? "1" : "0";
      if (onDirty) onDirty();
    };
    input.addEventListener("input", markDirty);
    input.addEventListener("change", markDirty);
    inputs[f.key] = input;
    wrap.appendChild(input);

    if (f.help) {
      var h = document.createElement("p");
      h.className = "mt-1 text-[11px] text-black-700 dark:text-black-600";
      h.textContent = f.help;
      wrap.appendChild(h);
    }
    return wrap;
  }

  function primaryBtn(text) {
    var b = document.createElement("button");
    b.type = "button";
    b.className = "rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors";
    b.textContent = text;
    return b;
  }
  function ghostBtn(text) {
    var b = document.createElement("button");
    b.type = "button";
    b.className = "rounded-lg border border-white-400 dark:border-navy-600 px-3 py-2 text-sm font-medium text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors";
    b.textContent = text;
    return b;
  }
  function showErr(el, msg) { el.textContent = msg; el.classList.remove("hidden"); }
  function testFeedback(el, ok, msg) {
    el.className = "text-xs font-medium " + (ok ? "text-green-600 dark:text-green-400" : "text-neg-400");
    el.textContent = msg;
  }

  function postJSON(url, body, done) {
    fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    })
      .then(function (r) {
        if (r.ok) { done(true); return; }
        return r.text().then(function (t) { done(false, t); });
      })
      .catch(function () { done(false, "Network error."); });
  }

  // postJSONRaw is postJSON for endpoints that return a JSON verdict the
  // caller needs to inspect (the test probe). done(parsedJSON | null).
  function postJSONRaw(url, body, done) {
    fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (res) { done(res); })
      .catch(function () { done(null); });
  }

  window.WickSessionWorkspace = { load: load };
  // Back-compat alias — older callers referenced WickSessionConfig.
  window.WickSessionConfig = window.WickSessionWorkspace;

  // Prime the rail-tab count once at startup so the badge is correct
  // before the user ever opens the Workspace tab. load() renders into the
  // (hidden) panel and sets the badge; reopening the tab just refreshes.
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", load);
  } else {
    load();
  }
})();
