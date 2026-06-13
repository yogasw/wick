// custom_review.js — the shared draft editor behind the review page,
// the edit page, and (with custom_manual.js on top) the manual
// builder. Renders the draft into the static shell customDraftForm
// lays out, keeps a live JSON preview of the custom_connectors row,
// and posts the Draft to the save endpoint.
(function () {
  const root = document.querySelector("[data-cc-review]");
  if (!root) return;

  const mode = root.dataset.mode || "new";
  const saveURL = root.dataset.saveUrl;

  const WIDGETS = ["text", "textarea", "dropdown", "number", "checkbox", "bool", "secret", "email", "url", "date", "datetime"];
  const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];

  // ── draft state ────────────────────────────────────────────────────
  let draft = loadDraft();

  function loadDraft() {
    const embedded = document.getElementById("cc-draft");
    if (embedded) {
      try { return normalize(JSON.parse(embedded.textContent)); } catch (e) { /* fall through */ }
    }
    const stored = sessionStorage.getItem("wick_custom_draft");
    if (stored) {
      try { return normalize(JSON.parse(stored)); } catch (e) { /* fall through */ }
    }
    return normalize({});
  }

  function normalize(d) {
    d = d || {};
    d.key = d.key || "";
    d.name = d.name || "";
    d.description = d.description || "";
    d.icon = d.icon || "🔌";
    d.source = d.source || "manual";
    d.category = d.category || "";
    d.configs = Array.isArray(d.configs) ? d.configs : [];
    d.ops = Array.isArray(d.ops) ? d.ops : [];
    d.configs.forEach((f) => normalizeField(f));
    d.ops.forEach((op) => {
      op.key = op.key || "";
      op.name = op.name || "";
      op.description = op.description || "";
      op.destructive = !!op.destructive;
      op.inputs = Array.isArray(op.inputs) ? op.inputs : [];
      op.inputs.forEach((f) => normalizeField(f));
      if (!op.mcp_source && !op.request) {
        op.request = { method: "GET", url_template: "", headers: {}, body_template: "", content_type: "" };
      }
      if (op.request) {
        op.request.headers = op.request.headers || {};
      }
    });
    return d;
  }

  function normalizeField(f) {
    f.key = f.key || "";
    f.widget = f.widget || "text";
    f.secret = !!f.secret;
    f.required = !!f.required;
    f.default = f.default || "";
    f.desc = f.desc || "";
    f.options = f.options || "";
  }

  // ── tiny DOM helpers ───────────────────────────────────────────────
  const INPUT_CLS = "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-xs text-black-900 dark:text-white-100 outline-none focus:border-green-500";
  function el(tag, cls, attrs) {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    Object.entries(attrs || {}).forEach(([k, v]) => n.setAttribute(k, v));
    return n;
  }
  function textInput(value, placeholder, mono, onInput) {
    const i = el("input", INPUT_CLS + (mono ? " font-mono" : ""));
    i.type = "text";
    i.value = value || "";
    if (placeholder) i.placeholder = placeholder;
    i.addEventListener("input", () => { onInput(i.value); refreshPreview(); });
    return i;
  }
  function checkbox(checked, label, onChange) {
    const wrap = el("label", "flex items-center gap-1 text-[11px] text-black-800 dark:text-black-600 whitespace-nowrap cursor-pointer");
    const c = el("input", "accent-green-500");
    c.type = "checkbox";
    c.checked = !!checked;
    c.addEventListener("change", () => { onChange(c.checked); refreshPreview(); });
    wrap.appendChild(c);
    wrap.appendChild(document.createTextNode(" " + label));
    return wrap;
  }
  function select(options, value, onChange) {
    const s = el("select", INPUT_CLS);
    options.forEach((o) => {
      const opt = document.createElement("option");
      opt.value = o; opt.textContent = o;
      if (o === value) opt.selected = true;
      s.appendChild(opt);
    });
    s.addEventListener("change", () => { onChange(s.value); refreshPreview(); });
    return s;
  }
  function removeBtn(onClick) {
    const b = el("button", "rounded px-2 py-1 text-xs text-black-700 hover:text-neg-400");
    b.type = "button";
    b.textContent = "✕";
    b.title = "Remove";
    b.addEventListener("click", () => { onClick(); renderAll(); });
    return b;
  }

  // ── field rows (shared by configs + op inputs) ─────────────────────
  function fieldRow(f, list, idx) {
    const secretBg = f.secret || f.widget === "secret";
    const row = el("div", "grid grid-cols-12 items-center gap-2 rounded-lg p-1.5" + (secretBg ? " bg-cau-100 dark:bg-cau-100/10" : ""));
    const key = textInput(f.key, "key", true, (v) => { f.key = v; });
    key.classList.add("col-span-3");
    const widget = select(WIDGETS, f.widget, (v) => {
      f.widget = v;
      if (v === "secret") f.secret = true;
      renderAll();
    });
    widget.classList.add("col-span-2");
    const def = textInput(f.default, "default", false, (v) => { f.default = v; });
    def.classList.add("col-span-3");
    if (f.widget === "secret") def.type = "password";
    const desc = textInput(f.desc, "description", false, (v) => { f.desc = v; });
    desc.classList.add("col-span-2");
    const flags = el("div", "col-span-2 flex items-center justify-end gap-2");
    flags.appendChild(checkbox(f.required, "req", (v) => { f.required = v; }));
    flags.appendChild(checkbox(f.secret, "secret", (v) => { f.secret = v; renderAll(); }));
    flags.appendChild(removeBtn(() => list.splice(idx, 1)));
    row.append(key, widget, def, desc, flags);
    if (f.widget === "dropdown") {
      const opts = textInput(f.options, "options: a|b|c", true, (v) => { f.options = v; });
      opts.classList.add("col-span-12");
      row.appendChild(opts);
    }
    return row;
  }

  function renderFieldList(container, list) {
    container.innerHTML = "";
    if (!list.length) {
      const empty = el("p", "text-xs text-black-700 dark:text-black-600");
      empty.textContent = "No fields yet.";
      container.appendChild(empty);
    }
    list.forEach((f, i) => container.appendChild(fieldRow(f, list, i)));
  }

  // ── operations ─────────────────────────────────────────────────────
  function opCard(op, idx) {
    const card = el("div", "rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4 space-y-3");

    const head = el("div", "flex flex-wrap items-center gap-2");
    const key = textInput(op.key, "op_key", true, (v) => { op.key = v; });
    key.classList.add("max-w-[10rem]");
    const name = textInput(op.name, "Display name", false, (v) => { op.name = v; });
    name.classList.add("max-w-[14rem]");
    const destr = checkbox(op.destructive, "destructive", (v) => { op.destructive = v; });
    head.append(key, name, destr);
    if (op.mcp_source) {
      const chip = el("span", "inline-flex items-center rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[11px] font-medium text-black-800 dark:text-black-600");
      chip.textContent = "MCP · " + op.mcp_source.tool_name;
      head.appendChild(chip);
    }
    head.appendChild(removeBtn(() => draft.ops.splice(idx, 1)));
    card.appendChild(head);

    const desc = textInput(op.description, "Description shown to the LLM — action verbs, be specific.", false, (v) => { op.description = v; });
    card.appendChild(desc);

    const inputsHead = el("div", "flex items-center justify-between");
    const inputsLabel = el("span", "text-[11px] font-semibold uppercase tracking-wider text-black-800 dark:text-black-600");
    inputsLabel.textContent = "Inputs (per-call, LLM provides)";
    const addInput = el("button", "rounded-lg border border-white-400 dark:border-navy-600 px-2 py-0.5 text-[11px] text-black-800 dark:text-black-600 hover:border-green-400 hover:text-green-600");
    addInput.type = "button";
    addInput.textContent = "+ Add input";
    addInput.addEventListener("click", () => { op.inputs.push(normFieldNew()); renderAll(); });
    inputsHead.append(inputsLabel, addInput);
    card.appendChild(inputsHead);
    const inputsBox = el("div", "space-y-2");
    renderFieldList(inputsBox, op.inputs);
    card.appendChild(inputsBox);

    if (op.request) {
      const req = op.request;
      const reqLabel = el("div", "text-[11px] font-semibold uppercase tracking-wider text-black-800 dark:text-black-600");
      reqLabel.textContent = "Request";
      card.appendChild(reqLabel);

      const line = el("div", "grid grid-cols-12 gap-2");
      const method = select(METHODS, (req.method || "GET").toUpperCase(), (v) => { req.method = v; });
      method.classList.add("col-span-3");
      const url = textInput(req.url_template, "{{.cfg.base_url}}/path", true, (v) => { req.url_template = v; });
      url.classList.add("col-span-9");
      line.append(method, url);
      card.appendChild(line);

      const hdrLabel = el("div", "flex items-center justify-between");
      const hl = el("span", "text-[11px] text-black-800 dark:text-black-600");
      hl.textContent = "Headers (values are templates)";
      const addHdr = el("button", "rounded-lg border border-white-400 dark:border-navy-600 px-2 py-0.5 text-[11px] text-black-800 dark:text-black-600 hover:border-green-400 hover:text-green-600");
      addHdr.type = "button";
      addHdr.textContent = "+ Add header";
      addHdr.addEventListener("click", () => { req.headers[""] = ""; renderAll(); });
      hdrLabel.append(hl, addHdr);
      card.appendChild(hdrLabel);

      const hdrBox = el("div", "space-y-2");
      Object.entries(req.headers).forEach(([hk, hv]) => {
        const r = el("div", "grid grid-cols-12 items-center gap-2");
        const ki = textInput(hk, "Header-Name", true, () => {});
        ki.classList.add("col-span-4");
        // header keys rename on change-commit, not per keystroke, to keep focus stable
        ki.addEventListener("change", () => {
          const nv = req.headers[hk];
          delete req.headers[hk];
          if (ki.value) req.headers[ki.value] = nv;
          renderAll();
        });
        const vi = textInput(hv, "Bearer {{.cfg.auth_value}}", true, (v) => { req.headers[hk] = v; });
        vi.classList.add("col-span-7");
        const del = removeBtn(() => { delete req.headers[hk]; });
        r.append(ki, vi, del);
        hdrBox.appendChild(r);
      });
      card.appendChild(hdrBox);

      const bodyLabel = el("label", "block text-[11px] text-black-800 dark:text-black-600");
      bodyLabel.textContent = "Body template (Go text/template — {{.cfg.*}} / {{.in.*}}; funcs: default, lower, upper, b64, urlquery, js, printf)";
      const body = el("textarea", INPUT_CLS + " font-mono");
      body.rows = 3;
      body.value = req.body_template || "";
      body.addEventListener("input", () => { req.body_template = body.value; refreshPreview(); });
      const ct = textInput(req.content_type, "content type, e.g. application/json", true, (v) => { req.content_type = v; });
      card.append(bodyLabel, body, ct);
    }
    return card;
  }

  function normFieldNew() {
    return { key: "", widget: "text", secret: false, required: false, default: "", desc: "", options: "" };
  }

  // ── preview + save ─────────────────────────────────────────────────
  function currentDraft() {
    return {
      key: (document.getElementById("cc-key") || {}).value || draft.key,
      name: (document.getElementById("cc-name") || {}).value || "",
      description: (document.getElementById("cc-desc") || {}).value || "",
      icon: (document.getElementById("cc-icon") || {}).value || "🔌",
      source: draft.source,
      category: (document.getElementById("cc-category") || {}).value || "",
      single: !!(document.getElementById("cc-single") || {}).checked,
      configs: draft.configs,
      ops: draft.ops,
    };
  }

  function refreshPreview() {
    const pre = document.getElementById("cc-preview");
    if (pre) pre.textContent = JSON.stringify(currentDraft(), null, 2);
    const tag = document.getElementById("cc-tag-name");
    if (tag) {
      const k = (document.getElementById("cc-key") || {}).value || draft.key || "…";
      tag.textContent = "custom:" + k;
    }
  }

  function showError(msg) {
    const box = root.querySelector("[data-cc-error]");
    const txt = root.querySelector("[data-cc-error-text]");
    if (txt) txt.textContent = msg;
    if (box) box.classList.remove("hidden");
    box?.scrollIntoView({ behavior: "smooth", block: "center" });
  }
  function hideError() {
    root.querySelector("[data-cc-error]")?.classList.add("hidden");
  }

  async function save() {
    hideError();
    const btn = root.querySelector("[data-cc-save]");
    if (btn) btn.disabled = true;
    try {
      const resp = await fetch(saveURL, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(currentDraft()),
      });
      const data = await resp.json();
      if (!resp.ok) {
        showError(data.error || "Save failed.");
        return;
      }
      if (data.redirect) {
        sessionStorage.removeItem("wick_custom_draft");
        location.href = data.redirect;
        return;
      }
      // Edit mode: stay, surface the reload hint.
      const hint = root.querySelector("[data-cc-reload-hint]");
      if (hint) {
        hint.classList.remove("hidden");
        hint.classList.add("flex");
        hint.scrollIntoView({ behavior: "smooth", block: "center" });
      }
    } catch (err) {
      showError(String(err));
    } finally {
      if (btn) btn.disabled = false;
    }
  }

  // ── render ─────────────────────────────────────────────────────────
  function renderAll() {
    renderFieldList(document.getElementById("cc-configs"), draft.configs);
    const opsBox = document.getElementById("cc-ops");
    opsBox.innerHTML = "";
    if (!draft.ops.length) {
      const empty = el("p", "text-xs text-black-700 dark:text-black-600");
      empty.textContent = "No operations yet. Add at least one.";
      opsBox.appendChild(empty);
    }
    draft.ops.forEach((op, i) => opsBox.appendChild(opCard(op, i)));
    refreshPreview();
  }

  // Meta seed values (inputs are static templ markup).
  const metaIDs = { "cc-key": "key", "cc-name": "name", "cc-desc": "description", "cc-icon": "icon" };
  Object.entries(metaIDs).forEach(([id, prop]) => {
    const input = document.getElementById(id);
    if (!input) return;
    input.value = draft[prop] || (prop === "icon" ? "🔌" : "");
    // hidden picker inputs repaint their preview on change
    input.dispatchEvent(new Event("change"));
    input.addEventListener("input", refreshPreview);
    input.addEventListener("change", refreshPreview);
  });
  const cat = document.getElementById("cc-category");
  if (cat) {
    cat.value = draft.category || "";
    cat.addEventListener("change", refreshPreview);
  }
  const single = document.getElementById("cc-single");
  if (single) {
    single.checked = !!draft.single;
    single.addEventListener("change", refreshPreview);
  }

  root.querySelector("[data-cc-add-config]")?.addEventListener("click", () => {
    draft.configs.push(normFieldNew());
    renderAll();
  });
  root.querySelector("[data-cc-add-op]")?.addEventListener("click", () => {
    draft.ops.push({
      key: "", name: "", description: "", destructive: false, inputs: [],
      request: { method: "GET", url_template: "{{.cfg.base_url}}/", headers: {}, body_template: "", content_type: "" },
    });
    renderAll();
  });
  root.querySelector("[data-cc-save]")?.addEventListener("click", save);

  renderAll();
})();
