// ask_user UI — the inline question card + the multi-question wizard
// modal. Split out of agents.js (which got too long). Self-contained:
// reads the session id / base from the page, POSTs answers to
// /sessions/{id}/answer, and is driven by the SSE stream via the
// window.WickAskUser hooks that agents.js calls on ask_user events.
(function () {
  "use strict";

  function base() {
    var el = document.querySelector("[data-session-id]");
    return el ? (el.dataset.base || "") : "";
  }
  function sessionID() {
    var el = document.querySelector("[data-session-id]");
    return el ? (el.dataset.sessionId || "") : "";
  }

  function submitAskAnswer(body) {
    var b = base();
    var sid = sessionID();
    if (!b || !sid) return;
    fetch(b + "/sessions/" + encodeURIComponent(sid) + "/answer", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }).then(function () {
      // ask_user_resolved SSE dismisses the card/modal across all tabs.
    }).catch(function () {});
  }

  // ── inline card (single question) ─────────────────────────────────
  var askUserCurrent = null;

  function cardError(msg) {
    var el = document.querySelector("#ask-user-card [data-ask-error]");
    if (!el) return;
    if (msg) { el.textContent = msg; el.classList.remove("hidden"); }
    else { el.textContent = ""; el.classList.add("hidden"); }
  }

  function showAskUserCard(req) {
    if (req && Array.isArray(req.fields) && req.fields.length > 0) {
      showAskFormModal(req);
      return;
    }
    var card = document.getElementById("ask-user-card");
    if (!card || !req || !req.id) return;
    askUserCurrent = req;
    cardError("");
    card.querySelector("[data-ask-question]").textContent = req.question || "";
    var optsBox = card.querySelector("[data-ask-options]");
    optsBox.innerHTML = "";
    (req.options || []).forEach(function (opt) {
      var btn = document.createElement("button");
      btn.type = "button";
      btn.className = "rounded-lg border border-amber-400 dark:border-amber-700 px-3 py-1.5 text-xs font-medium text-amber-700 dark:text-amber-300 hover:bg-amber-100 dark:hover:bg-amber-900/30 transition-colors";
      btn.textContent = opt.label;
      btn.addEventListener("click", function () {
        submitAskAnswer({ id: req.id, value: opt.value });
      });
      optsBox.appendChild(btn);
    });
    var freeForm = card.querySelector("[data-ask-freeform]");
    var textInput = card.querySelector("[data-ask-text]");
    if (req.allow_freeform) {
      freeForm.classList.remove("hidden");
      if (textInput) textInput.value = "";
    } else {
      freeForm.classList.add("hidden");
    }
    card.classList.remove("hidden");
  }

  function hideAskUserCard(payload) {
    hideAskFormModal(payload);
    var card = document.getElementById("ask-user-card");
    if (!card) return;
    if (payload && askUserCurrent && payload.id !== askUserCurrent.id) return;
    card.classList.add("hidden");
    askUserCurrent = null;
  }

  document.addEventListener("submit", function (e) {
    var form = e.target.closest("[data-ask-freeform]");
    if (!form || !askUserCurrent) return;
    e.preventDefault();
    var text = form.querySelector("[data-ask-text]").value.trim();
    if (!text) {
      cardError("Type an answer, or click one of the options above.");
      return;
    }
    cardError("");
    submitAskAnswer({ id: askUserCurrent.id, text: text });
  });

  // ── wizard modal (multi-question / structured fields) ─────────────
  var askFormCurrent = null;
  var askFormStep = 0;
  var askFormAnswers = {};
  var askFormGet = null; // current step's value getter

  var FIELD_INPUT_CLASS =
    "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none";
  var ROW_BASE =
    "w-full flex items-center gap-3 px-3 py-2.5 rounded-lg border text-left transition-colors cursor-pointer";
  var ROW_OFF = " border-white-300 dark:border-navy-600 hover:bg-white-200 dark:hover:bg-navy-700";
  var ROW_ON = " border-green-500 bg-white-200 dark:bg-navy-700";
  var BADGE_BASE = "flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-xs font-semibold";
  var BADGE_OFF = " bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600";
  var BADGE_ON = " bg-green-500 text-white-100";

  function parseArr(s) {
    if (!s) return null;
    try { var a = JSON.parse(s); return Array.isArray(a) ? a : null; } catch (e) { return null; }
  }

  function optionBody(o) {
    var body = document.createElement("div");
    body.className = "flex-1 min-w-0";
    var lab = document.createElement("div");
    lab.className = "text-sm text-black-900 dark:text-white-100 truncate";
    lab.textContent = o.label || o.value;
    body.appendChild(lab);
    if (o.description) {
      var desc = document.createElement("div");
      desc.className = "text-xs text-black-700 dark:text-black-600 truncate";
      desc.textContent = o.description;
      body.appendChild(desc);
    }
    return body;
  }

  // renderField builds the body for one wizard step and returns a
  // getter for its value ("" = unanswered). initial pre-fills from a
  // prior answer so Back restores state.
  function renderField(f, initial) {
    var opts = Array.isArray(f.options) ? f.options : [];
    var box = document.createElement("div");
    box.className = "space-y-2";

    if (f.type === "rank" && opts.length > 0) {
      var order = parseArr(initial) || opts.map(function (o) { return o.value; });
      var byVal = {};
      opts.forEach(function (o) { byVal[o.value] = o; });
      var list = document.createElement("div");
      list.className = "space-y-2";
      function repaint() {
        list.innerHTML = "";
        order.forEach(function (val, i) {
          var o = byVal[val] || { label: val, value: val };
          var row = document.createElement("div");
          row.className = ROW_BASE + ROW_OFF + " cursor-grab";
          row.draggable = true;
          var badge = document.createElement("span");
          badge.className = BADGE_BASE + BADGE_OFF;
          badge.textContent = String(i + 1);
          row.appendChild(badge);
          row.appendChild(optionBody(o));
          var handle = document.createElement("span");
          handle.className = "shrink-0 text-black-700 dark:text-black-600 text-lg leading-none select-none";
          handle.textContent = "≡";
          row.appendChild(handle);
          row.addEventListener("dragstart", function (e) { e.dataTransfer.setData("text/plain", val); });
          row.addEventListener("dragover", function (e) { e.preventDefault(); });
          row.addEventListener("drop", function (e) {
            e.preventDefault();
            var from = e.dataTransfer.getData("text/plain");
            if (from === val) return;
            var fi = order.indexOf(from), ti = order.indexOf(val);
            order.splice(fi, 1);
            order.splice(ti, 0, from);
            repaint();
          });
          list.appendChild(row);
        });
      }
      repaint();
      box.appendChild(list);
      return { el: box, get: function () { return JSON.stringify(order); } };
    }

    if ((f.type === "choice" || f.type === "multi") && opts.length > 0) {
      var multi = f.type === "multi";
      var selected = multi ? (parseArr(initial) || []) : (initial || null);
      var rows = [];
      var counter = null;
      function paint() {
        rows.forEach(function (r) {
          var on = multi ? selected.indexOf(r.value) >= 0 : selected === r.value;
          r.row.className = ROW_BASE + (on ? ROW_ON : ROW_OFF);
          r.badge.className = BADGE_BASE + (on ? BADGE_ON : BADGE_OFF);
          if (multi) r.badge.textContent = on ? "✓" : "";
        });
        if (counter) counter.textContent = selected.length + " selected";
      }
      opts.forEach(function (o, i) {
        var row = document.createElement("button");
        row.type = "button";
        row.className = ROW_BASE + ROW_OFF;
        var badge = document.createElement("span");
        badge.className = BADGE_BASE + BADGE_OFF;
        badge.textContent = multi ? "" : String(i + 1);
        row.appendChild(badge);
        row.appendChild(optionBody(o));
        row.addEventListener("click", function () {
          formError("");
          if (multi) {
            var idx = selected.indexOf(o.value);
            if (idx >= 0) selected.splice(idx, 1); else selected.push(o.value);
            paint();
          } else {
            selected = o.value;
            paint();
            // Single-select: picking IS the answer — auto-advance (or
            // submit on the last step), like a normal form. Short delay
            // so the highlight is visible first.
            setTimeout(function () { askStepForward(false); }, 140);
          }
        });
        rows.push({ row: row, badge: badge, value: o.value });
        box.appendChild(row);
      });
      var freeInput = null;
      if (f.allow_freeform) {
        freeInput = document.createElement("input");
        freeInput.type = "text";
        freeInput.placeholder = f.placeholder || "Other…";
        freeInput.className = FIELD_INPUT_CLASS;
        freeInput.addEventListener("input", function () { formError(""); });
        box.appendChild(freeInput);
      }
      if (multi) {
        counter = document.createElement("div");
        counter.className = "text-xs text-black-700 dark:text-black-600";
        box.appendChild(counter);
      }
      paint();
      return {
        el: box,
        get: function () {
          if (freeInput && freeInput.value.trim() !== "") return freeInput.value.trim();
          if (multi) return selected.length ? JSON.stringify(selected) : "";
          return selected || "";
        },
      };
    }

    if (f.type === "dropdown" && opts.length > 0) {
      var sel = document.createElement("select");
      sel.className = FIELD_INPUT_CLASS;
      opts.forEach(function (o) {
        var opt = document.createElement("option");
        opt.value = o.value;
        opt.textContent = o.label || o.value;
        if (o.value === (initial || f.value)) opt.selected = true;
        sel.appendChild(opt);
      });
      sel.addEventListener("change", function () { formError(""); });
      box.appendChild(sel);
      return { el: box, get: function () { return (sel.value || "").trim(); } };
    }

    var input = document.createElement("input");
    input.type = f.type === "secret" ? "password" : f.type === "number" ? "number" : "text";
    input.value = initial != null ? initial : (f.value || "");
    input.placeholder = f.placeholder || "";
    if (f.type === "secret") input.autocomplete = "new-password";
    input.className = FIELD_INPUT_CLASS;
    input.addEventListener("input", function () { formError(""); });
    // Enter advances (or submits on the last step), like a normal form.
    input.addEventListener("keydown", function (e) {
      if (e.key === "Enter") { e.preventDefault(); askStepForward(false); }
    });
    box.appendChild(input);
    // Focus the field so the user can type/Enter immediately.
    setTimeout(function () { try { input.focus(); } catch (e) {} }, 0);
    return { el: box, get: function () { return (input.value || "").trim(); } };
  }

  function askFields() {
    return (askFormCurrent && askFormCurrent.fields) || [];
  }

  function formError(msg) {
    var modal = document.getElementById("ask-form-modal");
    if (!modal) return;
    var el = modal.querySelector("[data-ask-form-error]");
    if (!el) return;
    if (msg) { el.textContent = msg; el.classList.remove("hidden"); }
    else { el.textContent = ""; el.classList.add("hidden"); }
  }

  function renderAskStep() {
    var modal = document.getElementById("ask-form-modal");
    if (!modal) return;
    var fields = askFields();
    var f = fields[askFormStep];
    if (!f) return;
    var total = fields.length;
    formError("");

    var q = modal.querySelector("[data-ask-form-question]");
    q.textContent = (f.label || f.key || "") + (f.required ? " *" : "");
    modal.querySelector("[data-ask-form-help]").textContent = f.help || "";
    modal.querySelector("[data-ask-form-progress]").textContent =
      total > 1 ? (askFormStep + 1) + " / " + total : "";

    var body = modal.querySelector("[data-ask-form-fields]");
    body.innerHTML = "";
    var rendered = renderField(f, askFormAnswers[f.key]);
    askFormGet = rendered.get;
    body.appendChild(rendered.el);

    modal.querySelector("[data-ask-form-prev]").classList.toggle("hidden", askFormStep === 0);
    modal.querySelector("[data-ask-form-next]").textContent = askFormStep >= total - 1 ? "Submit" : "Next";
    // Required questions can't be skipped.
    modal.querySelector("[data-ask-form-skip]").classList.toggle("hidden", !!f.required);
  }

  function showAskFormModal(req) {
    var modal = document.getElementById("ask-form-modal");
    if (!modal || !req || !req.id) return;
    askFormCurrent = req;
    askFormStep = 0;
    askFormAnswers = {};
    askFormGet = null;
    renderAskStep();
    modal.classList.remove("hidden");
  }

  function hideAskFormModal(payload) {
    var modal = document.getElementById("ask-form-modal");
    if (!modal) return;
    if (payload && askFormCurrent && payload.id !== askFormCurrent.id) return;
    modal.classList.add("hidden");
    askFormCurrent = null;
    askFormGet = null;
  }

  // recordStep saves the current step's answer. Returns false (and
  // surfaces a clear error) when a required field is empty so the
  // caller blocks navigation — the user always knows WHY submit
  // didn't advance.
  function recordStep(skip) {
    var f = askFields()[askFormStep];
    if (!f) return true;
    var val = askFormGet ? askFormGet() : "";
    if (skip) val = "";
    if (!skip && f.required && !val) {
      formError("“" + (f.label || f.key) + "” is required — answer it to continue.");
      var body = document.getElementById("ask-form-modal").querySelector("[data-ask-form-fields]");
      var first = body && body.querySelector("input,select,button");
      if (first) first.classList.add("border-neg-400");
      return false;
    }
    if (val !== "") askFormAnswers[f.key] = val;
    else delete askFormAnswers[f.key];
    return true;
  }

  function askStepForward(skip) {
    if (!recordStep(skip)) return;
    var total = askFields().length;
    if (askFormStep >= total - 1) {
      submitAskAnswer({ id: askFormCurrent.id, values: askFormAnswers });
      return;
    }
    askFormStep++;
    renderAskStep();
  }

  document.addEventListener("click", function (e) {
    if (!askFormCurrent) return;
    if (e.target.closest("[data-ask-form-next]")) { askStepForward(false); return; }
    if (e.target.closest("[data-ask-form-skip]")) { askStepForward(true); return; }
    if (e.target.closest("[data-ask-form-prev]")) {
      if (askFormStep > 0) { askFormStep--; renderAskStep(); }
      return;
    }
  });

  // Rehydrate any in-flight ask on page load (e.g. tab opened after the
  // agent asked) so the card/modal reappears.
  if (sessionID()) {
    var b0 = base();
    if (b0) {
      fetch(b0 + "/sessions/" + encodeURIComponent(sessionID()) + "/asks")
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (data) {
          if (data && Array.isArray(data.pending) && data.pending.length > 0) {
            showAskUserCard(data.pending[0]);
          }
        })
        .catch(function () {});
    }
  }

  // SSE hooks — agents.js owns the one EventSource and calls these on
  // ask_user / ask_user_resolved events.
  window.WickAskUser = {
    show: showAskUserCard,
    hide: hideAskUserCard,
  };
})();
