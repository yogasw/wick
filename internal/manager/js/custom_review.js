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
  // iconDeleteBtn is the prominent top-right delete: a trash icon with a
  // 32px hit area and a red hover, used on field + operation cards.
  function iconDeleteBtn(title, onClick) {
    const b = el("button", "flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-neg-100 hover:text-neg-400 dark:text-black-600", { title: title || "Remove" });
    b.type = "button";
    b.innerHTML = '<svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m3 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" stroke-linecap="round" stroke-linejoin="round"/><path d="M10 11v6M14 11v6" stroke-linecap="round"/></svg>';
    b.addEventListener("click", () => { onClick(); renderAll(); });
    return b;
  }

  // labeledField stacks a small label above an input so the form reads
  // clearly on every width (no placeholder-only guessing).
  function labeledField(labelText, inputEl) {
    const wrap = el("div", "min-w-0");
    const lbl = el("label", "mb-1 block text-[11px] font-medium text-black-800 dark:text-black-600");
    lbl.textContent = labelText;
    wrap.append(lbl, inputEl);
    return wrap;
  }

  // toggleSwitch is the compact on/off switch (same visual as the Access
  // Policy toggles), with its label to the right. Replaces the tiny
  // checkboxes that collided with the inputs on narrow rows.
  function toggleSwitch(checked, label, onChange) {
    const wrap = el("label", "flex cursor-pointer select-none items-center gap-2");
    const sw = el("span", "relative inline-block h-5 w-9 shrink-0");
    const input = el("input", "peer sr-only");
    input.type = "checkbox";
    input.checked = !!checked;
    const track = el("span", "absolute inset-0 rounded-full bg-white-400 transition-colors peer-checked:bg-green-500 dark:bg-navy-600");
    const thumb = el("span", "absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white-100 shadow transition-transform peer-checked:translate-x-4");
    input.addEventListener("change", () => { onChange(input.checked); refreshPreview(); });
    sw.append(input, track, thumb);
    const txt = el("span", "text-xs font-medium text-black-800 dark:text-black-600");
    txt.textContent = label;
    wrap.append(sw, txt);
    return wrap;
  }

  // ── field rows (shared by configs + op inputs) ─────────────────────
  // Each field is a self-contained card: labeled inputs in a grid that is
  // 1-column on mobile and 2-column from sm up, a delete icon top-right,
  // and the Required / Secret switches on their own row. Nothing overlaps
  // at any width.
  function fieldRow(f, list, idx) {
    const secretBg = f.secret || f.widget === "secret";
    const card = el("div", "rounded-xl border border-white-300 p-3 dark:border-navy-600 " + (secretBg ? "bg-cau-100/60 dark:bg-cau-100/10" : "bg-white-100 dark:bg-navy-700"));

    const top = el("div", "flex items-start gap-2");
    const grid = el("div", "grid min-w-0 flex-1 grid-cols-1 gap-3 sm:grid-cols-2");

    const key = textInput(f.key, "field_key", true, (v) => { f.key = v; });
    const widget = select(WIDGETS, f.widget, (v) => {
      f.widget = v;
      if (v === "secret") f.secret = true;
      renderAll();
    });
    const def = textInput(f.default, "default value", false, (v) => { f.default = v; });
    if (f.widget === "secret") def.type = "password";
    const desc = textInput(f.desc, "what this field is for", false, (v) => { f.desc = v; });

    grid.append(
      labeledField("Key", key),
      labeledField("Type", widget),
      labeledField("Default", def),
      labeledField("Description", desc),
    );

    const del = iconDeleteBtn("Remove field", () => list.splice(idx, 1));
    top.append(grid, del);
    card.appendChild(top);

    if (f.widget === "dropdown") {
      const opts = textInput(f.options, "options: a|b|c", true, (v) => { f.options = v; });
      const optWrap = labeledField("Options", opts);
      optWrap.classList.add("mt-3");
      card.appendChild(optWrap);
    }

    const flags = el("div", "mt-3 flex flex-wrap items-center gap-x-6 gap-y-2 border-t border-white-300 pt-3 dark:border-navy-600");
    flags.append(
      toggleSwitch(f.required, "Required", (v) => { f.required = v; }),
      toggleSwitch(f.secret, "Secret", (v) => { f.secret = v; renderAll(); }),
    );
    card.appendChild(flags);

    return card;
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

    const head = el("div", "flex items-start gap-2");
    const fields = el("div", "flex min-w-0 flex-1 flex-wrap items-center gap-2");
    const key = textInput(op.key, "op_key", true, (v) => { op.key = v; });
    key.classList.add("max-w-[10rem]");
    const name = textInput(op.name, "Display name", false, (v) => { op.name = v; });
    name.classList.add("max-w-[14rem]");
    const destr = checkbox(op.destructive, "destructive", (v) => { op.destructive = v; });
    fields.append(key, name, destr);
    if (op.mcp_source) {
      const chip = el("span", "inline-flex items-center rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[11px] font-medium text-black-800 dark:text-black-600");
      chip.textContent = "MCP · " + op.mcp_source.tool_name;
      fields.appendChild(chip);
    }
    head.append(fields, iconDeleteBtn("Delete operation", () => draft.ops.splice(idx, 1)));
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
      allow_session_config: !!(document.getElementById("cc-allow-session-config") || {}).checked,
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

  // ── navigator (Jump list + JSON), collapsible / slide-over ──────────
  function isDesktop() { return window.matchMedia("(min-width: 1024px)").matches; }

  // renderNav rebuilds the Jump list from the current draft. Each entry
  // scrolls (+ highlights) the matching editor card. Indices track the
  // ids assigned in renderAll (cc-cfg-N / cc-op-N).
  function renderNav() {
    const box = root.querySelector("[data-cc-nav-jump]");
    if (!box) return;
    box.innerHTML = "";
    box.appendChild(navSection("Configs", draft.configs, (f) => f.key || "(unnamed)", "cc-cfg-"));
    box.appendChild(navSection("Operations", draft.ops, (o) => o.name || o.key || "(unnamed)", "cc-op-"));
  }
  function navSection(title, items, labelFn, prefix) {
    const wrap = el("div", "mb-3");
    const h = el("div", "px-2 pb-1 text-[10px] font-semibold uppercase tracking-wider text-black-700 dark:text-black-600");
    h.textContent = title + " (" + items.length + ")";
    wrap.appendChild(h);
    if (!items.length) {
      const e = el("p", "px-2 text-[11px] text-black-700 dark:text-black-600");
      e.textContent = "None yet.";
      wrap.appendChild(e);
      return wrap;
    }
    items.forEach((it, i) => {
      const b = el("button", "flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-xs text-black-800 transition-colors hover:bg-white-200 hover:text-green-600 dark:text-black-600 dark:hover:bg-navy-800");
      b.type = "button";
      const dot = el("span", "h-1.5 w-1.5 flex-shrink-0 rounded-full bg-green-500");
      const t = el("span", "min-w-0 truncate");
      t.textContent = labelFn(it);
      b.append(dot, t);
      b.addEventListener("click", () => jumpTo(prefix + i));
      wrap.appendChild(b);
    });
    return wrap;
  }
  function jumpTo(id) {
    const target = document.getElementById(id);
    if (!target) return;
    if (!isDesktop()) closeSlideOver();
    target.scrollIntoView({ behavior: "smooth", block: "start" });
    target.classList.add("ring-2", "ring-green-400");
    setTimeout(() => target.classList.remove("ring-2", "ring-green-400"), 1500);
  }

  function setNavTab(which) {
    const jumpPane = root.querySelector("[data-cc-nav-jump]");
    const jsonPane = root.querySelector("[data-cc-nav-json]");
    if (jumpPane) jumpPane.classList.toggle("hidden", which !== "jump");
    if (jsonPane) jsonPane.classList.toggle("hidden", which !== "json");
    root.querySelectorAll("[data-cc-nav-tab]").forEach((btn) => {
      const active = btn.dataset.ccNavTab === which;
      btn.className = "rounded-lg px-3 py-1.5 text-xs font-medium " +
        (active
          ? "bg-green-500/10 text-green-600 dark:text-green-400"
          : "text-black-700 hover:text-green-600 dark:text-black-600");
    });
  }

  function openSlideOver() {
    root.querySelector("[data-cc-nav]")?.classList.remove("translate-x-full");
    root.querySelector("[data-cc-nav-backdrop]")?.classList.remove("hidden");
  }
  function closeSlideOver() {
    root.querySelector("[data-cc-nav]")?.classList.add("translate-x-full");
    root.querySelector("[data-cc-nav-backdrop]")?.classList.add("hidden");
  }
  function collapseDesktop() {
    root.querySelector("[data-cc-editor]")?.classList.remove("lg:col-span-7");
    root.querySelector("[data-cc-editor]")?.classList.add("lg:col-span-12");
    root.querySelector("[data-cc-nav-col]")?.classList.add("lg:hidden");
    root.querySelector("[data-cc-nav-open]")?.classList.remove("lg:hidden");
  }
  function expandDesktop() {
    root.querySelector("[data-cc-editor]")?.classList.add("lg:col-span-7");
    root.querySelector("[data-cc-editor]")?.classList.remove("lg:col-span-12");
    root.querySelector("[data-cc-nav-col]")?.classList.remove("lg:hidden");
    root.querySelector("[data-cc-nav-open]")?.classList.add("lg:hidden");
  }

  function setupNav() {
    root.querySelectorAll("[data-cc-nav-tab]").forEach((btn) => {
      btn.addEventListener("click", () => setNavTab(btn.dataset.ccNavTab));
    });
    // Open button: expand on desktop, slide-over on mobile.
    root.querySelector("[data-cc-nav-open]")?.addEventListener("click", () => {
      if (isDesktop()) expandDesktop(); else openSlideOver();
    });
    // Close button: collapse on desktop, close slide-over on mobile.
    root.querySelector("[data-cc-nav-close]")?.addEventListener("click", () => {
      if (isDesktop()) collapseDesktop(); else closeSlideOver();
    });
    root.querySelector("[data-cc-nav-backdrop]")?.addEventListener("click", closeSlideOver);
    setNavTab("jump");
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
    // Save button lives in the sticky page toolbar, outside [data-cc-review].
    const btn = document.querySelector("[data-cc-save]");
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
      // Edit mode: the server already reloaded the live module, so the
      // change is applied. Quick inline feedback on the Save button itself
      // (it lives in the sticky toolbar — no banner, no scrolling).
      if (data.reload_error) {
        showError("Saved, but live reload failed: " + data.reload_error + " — open the connector to retry.");
      } else if (btn) {
        const orig = btn.textContent;
        btn.textContent = "Saved ✓";
        clearTimeout(save._flash);
        save._flash = setTimeout(function () { btn.textContent = orig; }, 1600);
      }
    } catch (err) {
      showError(String(err));
    } finally {
      if (btn) btn.disabled = false;
    }
  }

  // ── render ─────────────────────────────────────────────────────────
  function renderAll() {
    const cfgBox = document.getElementById("cc-configs");
    renderFieldList(cfgBox, draft.configs);
    const opsBox = document.getElementById("cc-ops");
    opsBox.innerHTML = "";
    if (!draft.ops.length) {
      const empty = el("p", "text-xs text-black-700 dark:text-black-600");
      empty.textContent = "No operations yet. Add at least one.";
      opsBox.appendChild(empty);
    }
    draft.ops.forEach((op, i) => opsBox.appendChild(opCard(op, i)));
    // Anchor each card so the Jump navigator can scroll to it (scroll-mt
    // clears the sticky toolbar). Indices mirror the nav list.
    Array.from(cfgBox.children).forEach((c, i) => { c.id = "cc-cfg-" + i; c.classList.add("scroll-mt-24", "transition-shadow"); });
    Array.from(opsBox.children).forEach((c, i) => { c.id = "cc-op-" + i; c.classList.add("scroll-mt-24", "transition-shadow"); });
    renderNav();
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
  const allowSessionCfg = document.getElementById("cc-allow-session-config");
  if (allowSessionCfg) {
    allowSessionCfg.checked = !!draft.allow_session_config;
    allowSessionCfg.addEventListener("change", refreshPreview);
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
  document.querySelector("[data-cc-save]")?.addEventListener("click", save);

  setupNav();
  renderAll();
})();
