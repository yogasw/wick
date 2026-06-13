// Session Workspace tab — a USER-initiated way to spin up ephemeral
// connector instances scoped to this session (clone a base connector,
// point it at staging, use a different key). Renders into the Workspace
// panel of the Context slide-over (see context_panel.templ). Instances
// POST to /sessions/{id}/workspace[/{cid}]; secrets are encrypted
// server-side, never echoed back, never seen by the agent. Cards are
// collapsible so a session with many instances stays tidy.
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

  function render(data) {
    var box = listEl();
    if (!box) return;
    box.innerHTML = "";
    var instances = data.instances || [];
    var bases = data.bases || [];

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
    var title = document.createElement("span");
    title.className = "flex-1 min-w-0 truncate text-sm font-medium text-black-900 dark:text-white-100";
    title.textContent = inst.label || inst.id;
    head.appendChild(title);
    head.appendChild(statusBadge(inst.status));
    card.appendChild(head);

    var body = document.createElement("div");
    body.className = "p-3 space-y-3" + (isOpen ? "" : " hidden");
    head.addEventListener("click", function () {
      var nowOpen = body.classList.toggle("hidden") === false;
      expanded[inst.id] = nowOpen;
      chevron.style.transform = nowOpen ? "rotate(90deg)" : "";
    });

    var inputs = {};
    (inst.fields || []).forEach(function (f) {
      body.appendChild(fieldRow(f, inputs));
    });

    var err = document.createElement("p");
    err.className = "hidden text-xs font-medium text-neg-400";
    body.appendChild(err);

    var testOut = document.createElement("p");
    testOut.className = "hidden text-xs font-medium";
    body.appendChild(testOut);

    // Action row: Save / Test / Duplicate / Delete.
    var actions = document.createElement("div");
    actions.className = "flex flex-wrap items-center gap-2 pt-1";

    var save = primaryBtn("Save");
    save.addEventListener("click", function () {
      var values = {};
      Object.keys(inputs).forEach(function (k) {
        var v = (inputs[k].value || "").trim();
        if (v !== "") values[k] = v;
      });
      if (!Object.keys(values).length) {
        showErr(err, "Nothing to save — fill at least one field.");
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

    var test = ghostBtn("Test");
    test.addEventListener("click", function () {
      test.disabled = true;
      testOut.className = "text-xs font-medium text-black-700 dark:text-black-600";
      testOut.textContent = "Testing…";
      fetch(wsURL("/" + encodeURIComponent(inst.id) + "/test"), { method: "POST" })
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (res) {
          test.disabled = false;
          if (!res) { testFeedback(testOut, false, "Test failed."); return; }
          if (res.ok) { testFeedback(testOut, true, "Looks good."); return; }
          if (res.no_health_check) { testFeedback(testOut, false, "No health check for this connector — run an operation to verify."); return; }
          testFeedback(testOut, false, res.error || "Test failed.");
        })
        .catch(function () { test.disabled = false; testFeedback(testOut, false, "Network error."); });
    });
    actions.appendChild(test);

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

  function fieldRow(f, inputs) {
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
      input.value = f.secret ? "" : (f.value || "");
      input.placeholder = f.secret ? (f.set ? "•••••• (set — leave empty to keep)" : "Enter to set") : "";
      if (f.secret) input.autocomplete = "new-password";
    }
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

  window.WickSessionConfig = { load: load };
})();
