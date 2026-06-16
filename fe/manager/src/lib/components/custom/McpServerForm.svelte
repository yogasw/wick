<script lang="ts">
  /* MCP server registration / edit form. Ports custom_mcp_form.templ +
     custom_mcp_form.js to the SPA: label/icon/url, the five auth-scheme
     panels, scheme-independent extra headers, the Test-connection card,
     the opt-out tool list, and the save gate (Save stays disabled until
     one successful Test in this session — re-enforced server-side in
     SaveServer). OAuth runs the popup login before the probe. Edit mode
     prefills from the JSON endpoint and surfaces enable/disable/delete.

     `serverId` is the stored row id (path param on the edit route);
     empty registers a new server. The page coexists with the legacy
     templ form — both hit the same backend services. */
  import { Button, TextInput, TextArea, ConfirmDialog } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { push } from "$lib/router.js";
  import {
    getMcpServerForm,
    testMcpServer,
    saveMcpServer,
    setCustomDefDisabled,
    deleteCustomDef,
  } from "$lib/api.js";
  import { startOAuthLogin, type OAuthLogin } from "./mcpOAuth.js";
  import McpAuthPanel from "./McpAuthPanel.svelte";
  import McpHeadersEditor from "./McpHeadersEditor.svelte";
  import McpToolExcludeList from "./McpToolExcludeList.svelte";
  import IconPicker from "../icon/IconPicker.svelte";
  import type { McpServerForm, McpTool } from "$lib/types.js";

  type Props = { serverId?: string };
  let { serverId = "" }: Props = $props();

  let editMode = $derived(!!serverId);

  function blankForm(): McpServerForm {
    return {
      label: "",
      icon: "",
      description: "",
      url: "",
      auth_scheme: "none",
      auth_secret: "",
      auth_headers: [],
      headers: [],
      sso: { audience: "", ttl_seconds: 300 },
      oauth: { client_id: "", client_secret: "", scopes: "" },
      excluded: [],
      oauth_login_id: "",
    };
  }

  let form = $state<McpServerForm>(blankForm());
  let tools = $state<McpTool[]>([]);
  let defID = $state("");
  let disabled = $state(false);

  let loading = $state(true);
  let error = $state("");
  let testedOK = $state(false);
  let oauthConnected = $state(false);
  let testing = $state(false);
  let testResult = $state<{ ok: boolean; count?: number; latency?: number; names?: string; error?: string } | null>(null);
  let saving = $state(false);
  let confirmDelete = $state(false);
  /* Bump on every nested-form edit so $derived recomputes off the
     mutated-in-place form object (Svelte does not deep-track it). */
  let rev = $state(0);

  let activeLogin: OAuthLogin | null = null;

  function invalidateTest() {
    rev += 1;
    testedOK = false;
  }

  /* Editing the URL drops a stale OAuth session — a different server
     needs a fresh login. */
  function onUrlChange(v: string) {
    form.url = v;
    oauthConnected = false;
    form.oauth_login_id = "";
    invalidateTest();
  }

  async function load() {
    loading = true;
    error = "";
    try {
      if (editMode) {
        const res = await getMcpServerForm(serverId);
        form = { ...blankForm(), ...res.form };
        if (!form.sso) form.sso = { audience: "", ttl_seconds: 300 };
        if (!form.oauth) form.oauth = { client_id: "", client_secret: "", scopes: "" };
        form.excluded = res.form.excluded ?? [];
        tools = res.tools ?? [];
        if (res.info) {
          defID = res.info.def_id;
          disabled = res.info.disabled;
        }
      }
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  async function runTest() {
    if (testing) return;
    testing = true;
    error = "";
    testResult = null;
    try {
      /* The oauth scheme needs a signed-in account before the probe can
         authenticate — run the popup login first. */
      if (form.auth_scheme === "oauth" && !form.oauth_login_id) {
        activeLogin = startOAuthLogin($state.snapshot(form));
        form.oauth_login_id = await activeLogin.promise;
        activeLogin = null;
        oauthConnected = true;
      }
      let res = await testMcpServer($state.snapshot(form));
      if (res.needs_login) {
        /* The server says the token is gone — clear and re-login once. */
        form.oauth_login_id = "";
        oauthConnected = false;
        activeLogin = startOAuthLogin($state.snapshot(form));
        form.oauth_login_id = await activeLogin.promise;
        activeLogin = null;
        oauthConnected = true;
        res = await testMcpServer($state.snapshot(form));
      }
      testedOK = !!res.ok;
      if (res.ok) {
        tools = (res.tools ?? []).map((t) => ({ name: t.name, description: t.description }));
        const names = (res.tools ?? []).slice(0, 8).map((t) => t.name).join(" · ");
        testResult = {
          ok: true,
          count: (res.tools ?? []).length,
          latency: res.latency_ms,
          names: names + ((res.tools ?? []).length > 8 ? " · …" : ""),
        };
      } else {
        testResult = { ok: false, error: res.error || "unknown error" };
      }
    } catch (e) {
      testResult = { ok: false, error: e instanceof Error ? e.message : String(e) };
    } finally {
      testing = false;
    }
  }

  async function save() {
    if (!testedOK || saving) return;
    saving = true;
    error = "";
    try {
      const res = await saveMcpServer($state.snapshot(form), testedOK, serverId);
      toastOk(editMode ? "Server updated" : "Server saved");
      if (res.redirect) {
        window.location.href = res.redirect;
      }
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      toastError("Save failed", error);
    } finally {
      saving = false;
    }
  }

  async function toggleDisabled() {
    if (!defID) return;
    try {
      disabled = await setCustomDefDisabled(defID, !disabled);
      toastOk(disabled ? "Connector disabled" : "Connector enabled");
    } catch (e) {
      toastError("Action failed", e instanceof Error ? e.message : String(e));
    }
  }

  async function doDelete() {
    confirmDelete = false;
    if (!defID) return;
    try {
      await deleteCustomDef(defID);
      toastOk("Connector deleted");
      push("/");
    } catch (e) {
      toastError("Delete failed", e instanceof Error ? e.message : String(e));
    }
  }

  $effect(() => {
    load();
    return () => {
      activeLogin?.cancel();
      activeLogin = null;
    };
  });
</script>

{#if loading}
  <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
{:else}
  {#key rev}
    <div class="space-y-4">
      <div class="sticky top-0 z-30 -mx-6 -mt-6 border-b border-white-300 bg-white-200 px-6 py-3 dark:border-navy-600 dark:bg-navy-800">
        <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
          <h1 class="min-w-0 truncate text-lg font-semibold text-black-900 dark:text-white-100">
            {editMode ? "Edit MCP server" : "Register MCP server"}
          </h1>
          <div class="flex flex-wrap items-center gap-2 sm:flex-shrink-0">
            {#if editMode && defID}
              <Button variant="secondary" onclick={toggleDisabled}>{disabled ? "Enable" : "Disable"}</Button>
              <Button variant="danger" onclick={() => (confirmDelete = true)}>Delete</Button>
            {/if}
            <Button variant="primary" size="lg" disabled={!testedOK || saving} onclick={save}>
              {#if saving}Saving…{:else if editMode}Save changes{:else}Save &amp; create →{/if}
            </Button>
          </div>
        </div>
      </div>

      {#if error}
        <div class="rounded-lg border border-neg-400 bg-neg-100 px-4 py-3 text-sm font-medium text-neg-400">✗ {error}</div>
      {/if}

      <p class="text-sm text-black-800 dark:text-black-600">
        Wick forwards JSON-RPC (<code class="font-mono text-xs">initialize</code>, <code class="font-mono text-xs">tools/list</code>, <code class="font-mono text-xs">tools/call</code>) to this URL per request. Stdio servers are out of scope — expose them over HTTP with a sidecar.
      </p>

      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-6">
        <div class="grid grid-cols-12 gap-4">
          <div class="col-span-3 sm:col-span-2">
            <span class="block text-xs font-medium text-black-800 dark:text-black-600">Icon</span>
            <div class="mt-1">
              <IconPicker value={form.icon} onChange={(v) => { form.icon = v; invalidateTest(); }} ariaLabel="Icon" />
            </div>
          </div>
          <div class="col-span-9 sm:col-span-4">
            <span class="block text-xs font-medium text-black-800 dark:text-black-600">Label</span>
            <div class="mt-1">
              <TextInput
                value={form.label}
                onChange={(v) => { form.label = v; invalidateTest(); }}
                placeholder="Internal Tools MCP"
                ariaLabel="Label"
              />
            </div>
            <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Display name — becomes the connector's name (and its key, slugified).</p>
          </div>
          <div class="col-span-12 sm:col-span-6">
            <span class="block text-xs font-medium text-black-800 dark:text-black-600">MCP URL <span class="text-neg-400">*</span></span>
            <div class="mt-1">
              <TextInput
                type="url"
                value={form.url}
                onChange={onUrlChange}
                placeholder="https://mcp.internal.example.com/v1"
                ariaLabel="MCP URL"
              />
            </div>
            <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Streamable-HTTP endpoint.</p>
          </div>
          <div class="col-span-12">
            <span class="block text-xs font-medium text-black-800 dark:text-black-600">Description (optional)</span>
            <div class="mt-1">
              <TextArea
                value={form.description}
                onChange={(v) => { form.description = v; invalidateTest(); }}
                rows={4}
                placeholder="Leave empty to use the server's own description"
                ariaLabel="Description"
              />
            </div>
            <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Empty = adopt the server's self-description (from its <code class="font-mono">initialize</code> instructions) and keep it fresh on every re-sync. Write your own to lock it — clearing the field hands it back to the server.</p>
          </div>
        </div>

        <McpAuthPanel {form} {oauthConnected} onChange={invalidateTest} />

        <div class="mt-6">
          <div class="flex items-center justify-between">
            <span class="text-xs font-medium text-black-800 dark:text-black-600">Extra headers (any scheme — appended on top)</span>
          </div>
          <div class="mt-2">
            <McpHeadersEditor rows={form.headers} onChange={(rows) => { form.headers = rows; invalidateTest(); }} addLabel="+ Add header" />
          </div>
          <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Routing / tenancy headers that aren't auth. Each row independently markable as secret.</p>
        </div>

        <div class="mt-6 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4">
          <div class="flex items-center justify-between gap-4">
            <div>
              <p class="text-sm font-semibold text-black-900 dark:text-white-100">Test connection</p>
              <p class="text-xs text-black-800 dark:text-black-600">Fires initialize + tools/list with the current values. Save unlocks after one success.</p>
            </div>
            <Button variant="primary" disabled={testing} onclick={runTest}>
              {testing ? "Testing…" : "▶ Test now"}
            </Button>
          </div>
          {#if testResult}
            <div class="mt-3">
              {#if testResult.ok}
                <div class="rounded-lg border border-pos-400 bg-pos-100 px-3 py-2">
                  <p class="text-sm font-semibold text-pos-400">✓ Connected · {testResult.count} tools discovered · {testResult.latency}ms</p>
                  <p class="mt-1 font-mono text-[11px] text-black-800">{testResult.names}</p>
                </div>
              {:else}
                <div class="rounded-lg border border-neg-400 bg-neg-100 px-3 py-2">
                  <p class="text-sm font-semibold text-neg-400">✗ Connection failed</p>
                  <p class="mt-1 font-mono text-[11px] text-black-800">{testResult.error}</p>
                </div>
              {/if}
            </div>
          {/if}
        </div>

        <div class="mt-6 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4">
          <p class="text-sm font-semibold text-black-900 dark:text-white-100">Tools</p>
          <p class="mt-0.5 text-xs text-black-800 dark:text-black-600">
            Every tool this server lists is exposed automatically. Use Exclude to hide a tool.
          </p>
          <div class="mt-3">
            <McpToolExcludeList {tools} excluded={form.excluded} onChange={(ex) => { form.excluded = ex; rev += 1; }} />
          </div>
        </div>
      </div>
    </div>
  {/key}

  <ConfirmDialog
    open={confirmDelete}
    title="Delete this MCP connector?"
    body="Deletes the definition and its instances. This cannot be undone."
    confirmLabel="Delete"
    destructive
    onConfirm={doDelete}
    onCancel={() => (confirmDelete = false)}
  />
{/if}
