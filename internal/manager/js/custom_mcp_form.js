// custom_mcp_form.js — MCP server registration/edit form: auth panel
// toggling, header row editors, Test connection, the tool exclude-list
// (opt-out — everything the server lists is exposed unless ticked),
// and the save gate (submit stays disabled until one successful test
// in this session).
(function () {
  const root = document.querySelector("[data-cc-mcp-form]");
  if (!root) return;

  const serverID = root.dataset.serverId || "";
  const testURL = root.dataset.testUrl;
  const saveURL = root.dataset.saveUrl;
  const oauthStartURL = root.dataset.oauthStartUrl;

  const labelI = document.getElementById("cc-srv-label");
  const iconI = document.getElementById("cc-srv-icon");
  const descI = document.getElementById("cc-srv-desc");
  const urlI = document.getElementById("cc-srv-url");
  const bearerI = document.getElementById("cc-srv-bearer");
  const ssoAudI = document.getElementById("cc-srv-sso-aud");
  const ssoTTLI = document.getElementById("cc-srv-sso-ttl");
  const authHeadersBox = document.getElementById("cc-srv-auth-headers");
  const extraHeadersBox = document.getElementById("cc-srv-extra-headers");
  const toolsBox = document.getElementById("cc-srv-tools");
  const oauthIDI = document.getElementById("cc-srv-oauth-id");
  const oauthSecretI = document.getElementById("cc-srv-oauth-secret");
  const oauthScopesI = document.getElementById("cc-srv-oauth-scopes");
  const oauthStatus = root.querySelector("[data-cc-oauth-status]");
  const testBtn = root.querySelector("[data-cc-test]");
  const testResult = root.querySelector("[data-cc-test-result]");
  const saveBtn = root.querySelector("[data-cc-save-server]");
  const errBox = root.querySelector("[data-cc-form-error]");
  const errText = root.querySelector("[data-cc-form-error-text]");

  let authHeaders = [];
  let extraHeaders = [];
  let tools = []; // [{name, description}] — live catalog (edit prefill or last test)
  let excluded = new Set();
  let testedOK = false;
  let oauthLoginID = ""; // completed popup login this form session

  // Prefill (edit mode) from the embedded form JSON.
  const embedded = document.getElementById("cc-server-form");
  if (embedded) {
    try {
      const f = JSON.parse(embedded.textContent) || {};
      if (labelI) labelI.value = f.label || "";
      if (iconI) {
        iconI.value = f.icon || "";
        iconI.dispatchEvent(new Event("change"));
      }
      if (descI) descI.value = f.description || "";
      if (urlI) urlI.value = f.url || "";
      if (bearerI) bearerI.value = f.auth_secret || "";
      if (ssoAudI) ssoAudI.value = (f.sso && f.sso.audience) || "";
      if (ssoTTLI && f.sso && f.sso.ttl_seconds) ssoTTLI.value = String(f.sso.ttl_seconds);
      authHeaders = Array.isArray(f.auth_headers) ? f.auth_headers : [];
      extraHeaders = Array.isArray(f.headers) ? f.headers : [];
      excluded = new Set(Array.isArray(f.excluded) ? f.excluded : []);
      if (f.oauth) {
        if (oauthIDI) oauthIDI.value = f.oauth.client_id || "";
        if (oauthSecretI) oauthSecretI.value = f.oauth.client_secret || "";
        if (oauthScopesI) oauthScopesI.value = f.oauth.scopes || "";
      }
      selectScheme(f.auth_scheme || "none");
    } catch (e) {
      selectScheme("none");
    }
  } else {
    selectScheme("none");
  }

  // Edit pages embed the live tool catalog probed server-side.
  const embeddedTools = document.getElementById("cc-server-tools");
  if (embeddedTools) {
    try {
      tools = JSON.parse(embeddedTools.textContent) || [];
    } catch (e) { /* tools stay empty */ }
  }

  function scheme() {
    const checked = root.querySelector('input[name="cc-auth-scheme"]:checked');
    return checked ? checked.value : "none";
  }

  function selectScheme(name) {
    root.querySelectorAll('input[name="cc-auth-scheme"]').forEach((r) => {
      r.checked = r.value === name;
    });
    paintScheme();
  }

  function paintScheme() {
    const active = scheme();
    root.querySelectorAll("[data-cc-auth-panel]").forEach((p) => {
      p.classList.toggle("hidden", p.dataset.ccAuthPanel !== active);
    });
    root.querySelectorAll("[data-cc-auth-choice]").forEach((l) => {
      l.classList.toggle("border-green-500", l.dataset.ccAuthChoice === active);
    });
  }
  root.querySelectorAll('input[name="cc-auth-scheme"]').forEach((r) => {
    r.addEventListener("change", () => { invalidateTest(); paintScheme(); });
  });

  // ── header row editors ─────────────────────────────────────────────
  const INPUT_CLS = "rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1.5 font-mono text-xs text-black-900 dark:text-white-100 outline-none focus:border-green-500";

  function renderHeaders(box, list) {
    if (!box) return;
    box.innerHTML = "";
    if (!list.length) {
      const p = document.createElement("p");
      p.className = "text-[11px] text-black-700 dark:text-black-600";
      p.textContent = "No rows yet.";
      box.appendChild(p);
    }
    list.forEach((row, i) => {
      const r = document.createElement("div");
      r.className = "grid grid-cols-12 items-center gap-2" + (row.secret ? " rounded-lg bg-cau-100 dark:bg-cau-100/10 p-1" : "");
      const k = document.createElement("input");
      k.className = INPUT_CLS + " col-span-4";
      k.placeholder = "X-API-Key";
      k.value = row.key || "";
      k.addEventListener("input", () => { row.key = k.value; invalidateTest(); });
      const v = document.createElement("input");
      v.className = INPUT_CLS + " col-span-5";
      v.type = row.secret ? "password" : "text";
      v.placeholder = "value";
      v.value = row.value || "";
      v.addEventListener("input", () => { row.value = v.value; invalidateTest(); });
      const sec = document.createElement("label");
      sec.className = "col-span-2 flex items-center gap-1 text-[11px] text-black-800 dark:text-black-600 cursor-pointer";
      const c = document.createElement("input");
      c.type = "checkbox";
      c.checked = !!row.secret;
      c.className = "accent-green-500";
      c.addEventListener("change", () => { row.secret = c.checked; invalidateTest(); renderHeaders(box, list); });
      sec.appendChild(c);
      sec.appendChild(document.createTextNode(" Sec"));
      const del = document.createElement("button");
      del.type = "button";
      del.className = "col-span-1 text-xs text-black-700 hover:text-neg-400";
      del.textContent = "✕";
      del.addEventListener("click", () => { list.splice(i, 1); invalidateTest(); renderHeaders(box, list); });
      r.append(k, v, sec, del);
      box.appendChild(r);
    });
  }
  renderHeaders(authHeadersBox, authHeaders);
  renderHeaders(extraHeadersBox, extraHeaders);

  root.querySelector("[data-cc-add-auth-header]")?.addEventListener("click", () => {
    authHeaders.push({ key: "", value: "", secret: true });
    renderHeaders(authHeadersBox, authHeaders);
  });
  root.querySelector("[data-cc-add-extra-header]")?.addEventListener("click", () => {
    extraHeaders.push({ key: "", value: "", secret: false });
    renderHeaders(extraHeadersBox, extraHeaders);
  });

  // ── tool exclude-list ──────────────────────────────────────────────
  function renderTools() {
    if (!toolsBox) return;
    toolsBox.innerHTML = "";
    if (!tools.length) {
      const p = document.createElement("p");
      p.className = "text-[11px] text-black-700 dark:text-black-600";
      p.textContent = "Run Test now to discover this server's tools.";
      toolsBox.appendChild(p);
      return;
    }
    tools.forEach((t) => {
      const isExcluded = excluded.has(t.name);
      const row = document.createElement("div");
      row.className =
        "flex items-start gap-3 rounded-lg border px-3 py-2 " +
        (isExcluded
          ? "border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 opacity-60"
          : "border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700");
      const body = document.createElement("div");
      body.className = "min-w-0 flex-1";
      const name = document.createElement("p");
      name.className = "font-mono text-xs font-semibold text-black-900 dark:text-white-100" + (isExcluded ? " line-through" : "");
      name.textContent = t.name;
      body.appendChild(name);
      if (t.description) {
        const d = document.createElement("p");
        d.className = "mt-0.5 text-[11px] leading-relaxed text-black-700 dark:text-black-600";
        d.textContent = t.description;
        body.appendChild(d);
      }
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = isExcluded
        ? "flex-shrink-0 rounded-lg border border-green-400 px-2.5 py-1 text-[11px] font-medium text-green-600 hover:bg-green-100 dark:hover:bg-green-800"
        : "flex-shrink-0 rounded-lg border border-white-400 dark:border-navy-600 px-2.5 py-1 text-[11px] font-medium text-black-700 dark:text-black-600 hover:border-neg-400 hover:text-neg-400";
      btn.textContent = isExcluded ? "Include" : "Exclude";
      btn.addEventListener("click", () => {
        if (isExcluded) excluded.delete(t.name);
        else excluded.add(t.name);
        renderTools();
      });
      row.append(body, btn);
      toolsBox.appendChild(row);
    });
  }
  renderTools();

  // ── form state ─────────────────────────────────────────────────────
  function formPayload() {
    return {
      label: labelI ? labelI.value.trim() : "",
      icon: iconI ? iconI.value.trim() : "",
      description: descI ? descI.value.trim() : "",
      url: urlI ? urlI.value.trim() : "",
      auth_scheme: scheme(),
      auth_secret: bearerI ? bearerI.value : "",
      auth_headers: authHeaders.filter((h) => h.key),
      headers: extraHeaders.filter((h) => h.key),
      sso: {
        audience: ssoAudI ? ssoAudI.value.trim() : "",
        ttl_seconds: ssoTTLI ? parseInt(ssoTTLI.value, 10) || 300 : 300,
      },
      oauth: {
        client_id: oauthIDI ? oauthIDI.value.trim() : "",
        client_secret: oauthSecretI ? oauthSecretI.value : "",
        scopes: oauthScopesI ? oauthScopesI.value.trim() : "",
      },
      oauth_login_id: oauthLoginID,
      excluded: Array.from(excluded),
    };
  }

  function invalidateTest() {
    testedOK = false;
    if (saveBtn) saveBtn.disabled = true;
  }
  [labelI, urlI, bearerI, ssoAudI, ssoTTLI, oauthIDI, oauthSecretI, oauthScopesI].forEach((i) => {
    i?.addEventListener("input", invalidateTest);
    i?.addEventListener("change", invalidateTest);
  });
  urlI?.addEventListener("input", () => { oauthLoginID = ""; paintOAuthStatus(); });

  function paintOAuthStatus() {
    oauthStatus?.classList.toggle("hidden", !oauthLoginID);
  }

  // ── oauth popup login ──────────────────────────────────────────────
  function oauthLogin() {
    return fetch(oauthStartURL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ form: formPayload() }),
    })
      .then((resp) => resp.json().then((data) => ({ ok: resp.ok, data })))
      .then(({ ok, data }) => {
        if (!ok) throw new Error(data.error || "OAuth discovery failed.");
        const popup = window.open(data.auth_url, "wick-mcp-oauth", "width=560,height=720");
        if (!popup) throw new Error("Popup blocked — allow popups for this site and retry.");
        const loginID = data.login_id;
        return new Promise((resolve, reject) => {
          // Two completion signals, neither relying on the popup
          // handle: COOP on the authorization server severs it, which
          // makes window.opener null AND window.closed lie — so no
          // closed-detection at all. The BroadcastChannel gives the
          // instant signal; polling the server's login session is the
          // truth that always lands.
          let channel = null;
          try { channel = new BroadcastChannel("wick-mcp-oauth"); } catch (e) {}
          function cleanup() {
            clearTimeout(timer);
            clearInterval(statusPoll);
            window.removeEventListener("message", onMsg);
            if (channel) channel.close();
          }
          const timer = setTimeout(() => {
            cleanup();
            reject(new Error("Login timed out — click Test now to try again."));
          }, 180000);
          function finish(err) {
            cleanup();
            if (err) {
              reject(new Error(err));
              return;
            }
            oauthLoginID = loginID;
            paintOAuthStatus();
            resolve();
          }
          const statusPoll = setInterval(() => {
            fetch("/manager/connectors/custom/mcp-servers/oauth/status?login_id=" + encodeURIComponent(loginID))
              .then((r) => r.json())
              .then((st) => {
                if (st.status === "done") finish();
                else if (st.status === "expired") finish("Login session expired — click Test now to try again.");
              })
              .catch(() => {});
          }, 2500);
          function handle(data) {
            if (!data || data.type !== "wick-mcp-oauth") return;
            finish(data.error || null);
          }
          function onMsg(e) {
            if (e.origin !== window.location.origin) return;
            handle(e.data);
          }
          window.addEventListener("message", onMsg);
          if (channel) channel.onmessage = (e) => handle(e.data);
        });
      });
  }

  function showError(msg) {
    if (errText) errText.textContent = msg;
    errBox?.classList.remove("hidden");
  }
  function hideError() {
    errBox?.classList.add("hidden");
  }

  // ── test connection ────────────────────────────────────────────────
  async function runTest(allowLogin) {
    // The oauth scheme needs a signed-in account before the probe can
    // authenticate — run the popup login first (or again, when the
    // server reports the session token is gone).
    if (scheme() === "oauth" && !oauthLoginID) {
      if (!allowLogin) throw new Error("login required — sign in and test again");
      testBtn.textContent = "Waiting for login…";
      await oauthLogin();
      testBtn.textContent = "Testing…";
    }
    const resp = await fetch(testURL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(formPayload()),
    });
    const data = await resp.json();
    if (data.needs_login && allowLogin) {
      oauthLoginID = "";
      paintOAuthStatus();
      return runTest(false);
    }
    return data;
  }

  testBtn?.addEventListener("click", async () => {
    hideError();
    testBtn.disabled = true;
    const original = testBtn.textContent;
    testBtn.textContent = "Testing…";
    try {
      const data = await runTest(true);
      renderTestResult(data);
      testedOK = !!data.ok;
      if (data.ok) {
        tools = (data.tools || []).map((t) => ({ name: t.name, description: t.description }));
        renderTools();
      }
      if (saveBtn) saveBtn.disabled = !testedOK;
    } catch (err) {
      renderTestResult({ ok: false, error: String(err && err.message ? err.message : err) });
    } finally {
      testBtn.disabled = false;
      testBtn.textContent = original;
    }
  });

  function renderTestResult(res) {
    if (!testResult) return;
    testResult.classList.remove("hidden");
    if (res.ok) {
      const names = (res.tools || []).slice(0, 8).map((t) => t.name).join(" · ");
      testResult.innerHTML =
        '<div class="rounded-lg border border-pos-400 bg-pos-100 px-3 py-2">' +
        '<p class="text-sm font-semibold text-pos-400">✓ Connected · ' + (res.tools ? res.tools.length : 0) + " tools discovered · " + res.latency_ms + "ms</p>" +
        '<p class="mt-1 font-mono text-[11px] text-black-800" data-cc-names></p></div>';
      testResult.querySelector("[data-cc-names]").textContent = names + ((res.tools || []).length > 8 ? " · …" : "");
    } else {
      testResult.innerHTML =
        '<div class="rounded-lg border border-neg-400 bg-neg-100 px-3 py-2">' +
        '<p class="text-sm font-semibold text-neg-400">✗ Connection failed</p>' +
        '<p class="mt-1 font-mono text-[11px] text-black-800" data-cc-err></p></div>';
      testResult.querySelector("[data-cc-err]").textContent = res.error || "unknown error";
    }
  }

  // ── save ───────────────────────────────────────────────────────────
  saveBtn?.addEventListener("click", async () => {
    hideError();
    saveBtn.disabled = true;
    try {
      const resp = await fetch(saveURL, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ form: formPayload(), tested_ok: testedOK, id: serverID }),
      });
      const data = await resp.json();
      if (!resp.ok) {
        showError(data.error || "Save failed.");
        saveBtn.disabled = !testedOK;
        return;
      }
      if (data.redirect) location.href = data.redirect;
    } catch (err) {
      showError(String(err));
      saveBtn.disabled = !testedOK;
    }
  });
})();
