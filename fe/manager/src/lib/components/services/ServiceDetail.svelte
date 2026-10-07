<script lang="ts">
  /* Admin view of one service plugin: supervisor status with Stop / Start /
     Restart, auto-off (the plugin's default, the admin's Default / On / Off
     override and the idle limit; a sleeping service wakes on the next
     request), the manifest config (secrets write-only; saved values are pushed
     to the running plugin), the routes and who may reach them, access tokens (Generate /
     Rotate / Revoke — the secret is shown once), the callback token switch,
     and the last log lines (refreshed every 3 s). */
  import PluginUpdateMenu from "$lib/components/plugins/PluginUpdateMenu.svelte";
  import { Button, TextInput, ConfirmDialog } from "@wick-fe/common-ui";
  import { toastError } from "@wick-fe/common-stores";
  import {
    getServicePlugin, serviceAction, setServiceConfig, setServiceAutoOff, generateServiceToken, rotateServiceToken, revokeServiceToken,
    type ServicePlugin, type ServiceConfigField, type ServiceAutoOffMode,
  } from "$lib/api.js";
  import type { ConfigField } from "$lib/types.js";
  import FieldWidget from "$lib/components/fields/FieldWidget.svelte";
  import { setBreadcrumbNames, clearBreadcrumbNames } from "$lib/stores/breadcrumb.js";

  type Props = { serviceKey: string };
  let { serviceKey }: Props = $props();

  const REFRESH_MS = 3000;

  let data = $state<ServicePlugin | null>(null);
  let loading = $state(true);
  let error = $state("");
  let busy = $state("");
  let tokenName = $state("");
  let secret = $state<{ name: string; value: string } | null>(null);
  /* Edits in progress, by key. The 3 s refresh never touches them, so typing
     is not clobbered; Save sends only these. */
  let draft = $state<Record<string, string>>({});
  let formKey = $state(0);

  const asField = (c: ServiceConfigField): ConfigField => ({
    key: c.key, type: c.type || "text", value: c.value, options: c.options || "", required: c.required,
    is_secret: c.is_secret, has_value: c.has_value, description: c.description || "", visible_when: "", env_override: "",
  });
  let dirty = $derived(Object.keys(draft).length > 0);

  async function saveConfig(): Promise<void> {
    busy = "config";
    try {
      data = await setServiceConfig(serviceKey, draft);
      draft = {};
      formKey++; // remount the inputs so secrets clear back to "stored"
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      busy = "";
    }
  }

  /* Auto-off edits in progress (null = not editing; the refresh shows the
     saved values meanwhile). */
  let aoMode = $state<ServiceAutoOffMode | null>(null);
  let aoIdle = $state<string | null>(null);
  let aoConfirm = $state(false);
  const autoOffModes: { value: ServiceAutoOffMode; label: string }[] = [
    { value: "default", label: "Default" },
    { value: "on", label: "On" },
    { value: "off", label: "Off" },
  ];
  let ao = $derived(data?.auto_off);
  let aoModeShown = $derived(aoMode ?? ao?.mode ?? "default");
  let aoIdleShown = $derived(aoIdle ?? String(Math.round((ao?.idle_seconds ?? 900) / 60)));
  let aoDirty = $derived(aoMode !== null || aoIdle !== null);
  /* Forcing auto-off on against the plugin's word. */
  let aoRisky = $derived(aoModeShown === "on" && !ao?.supported);

  const fmtIdle = (sec: number): string =>
    sec % 3600 === 0 ? `${sec / 3600}h` : sec % 60 === 0 ? `${sec / 60}m` : `${sec}s`;
  const autoOffBadge = (a: NonNullable<typeof ao>): string => {
    if (a.mode === "on") return `Auto-off: on (forced, idle ${fmtIdle(a.idle_seconds)})`;
    if (a.mode === "off") return "Auto-off: off (forced)";
    return a.enabled ? `Auto-off: on (plugin default, idle ${fmtIdle(a.idle_seconds)})` : "Auto-off: off (plugin default)";
  };
  const hasTime = (s?: string) => !!s && !s.startsWith("0001-");

  async function saveAutoOff(confirm = false): Promise<void> {
    if (!ao) return;
    const minutes = Number(aoIdleShown);
    if (!Number.isFinite(minutes) || minutes < 1 || minutes > 10080) {
      toastError("Idle limit must be between 1 and 10080 minutes.");
      return;
    }
    if (aoRisky && !confirm) {
      aoConfirm = true;
      return;
    }
    aoConfirm = false;
    busy = "auto-off";
    try {
      // The plugin's own limit is sent as 0 so it keeps following the plugin.
      const secs = Math.round(minutes * 60);
      data = await setServiceAutoOff(serviceKey, aoModeShown, secs === ao.default_idle_seconds ? 0 : secs, confirm);
      aoMode = null;
      aoIdle = null;
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      busy = "";
    }
  }

  async function load(silent = false): Promise<void> {
    if (!silent) loading = true;
    try {
      data = await getServicePlugin(serviceKey);
      error = "";
    } catch (e) {
      if (!silent) error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  async function act(action: "start" | "stop" | "restart" | "callback-revoke" | "callback-allow"): Promise<void> {
    busy = action;
    try {
      data = await serviceAction(serviceKey, action);
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      busy = "";
    }
  }

  async function generate(): Promise<void> {
    busy = "token";
    try {
      const r = await generateServiceToken(serviceKey, tokenName.trim() || "token");
      secret = { name: r.token.name, value: r.secret };
      tokenName = "";
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      busy = "";
    }
  }

  async function rotate(id: string): Promise<void> {
    busy = "rotate:" + id;
    try {
      const r = await rotateServiceToken(serviceKey, id);
      secret = { name: r.token.name, value: r.secret };
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      busy = "";
    }
  }

  async function revoke(id: string): Promise<void> {
    busy = "revoke:" + id;
    try {
      await revokeServiceToken(serviceKey, id);
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      busy = "";
    }
  }

  const stateClasses: Record<string, string> = {
    running: "bg-pos-100 text-pos-400",
    starting: "bg-prog-100 text-prog-400",
    backoff: "bg-neg-100 text-neg-400",
    sleeping: "bg-prog-100 text-prog-400",
    stopped: "bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600",
  };
  const authLabel: Record<string, string> = { public: "Public", token: "Token", "wick-session": "Wick login" };
  const fmt = (s?: string) => (s ? new Date(s).toLocaleString() : "—");

  let running = $derived(data?.status.state === "running" || data?.status.state === "starting" || data?.status.state === "sleeping");

  $effect(() => {
    if (data) setBreadcrumbNames({ service: data.name || data.key });
  });

  $effect(() => {
    load();
    const t = setInterval(() => load(true), REFRESH_MS);
    return () => {
      clearInterval(t);
      clearBreadcrumbNames();
    };
  });
</script>

{#if loading}
  <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
{:else if error}
  <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
{:else if data}
  <div class="space-y-6" data-testid="service-detail">
    <div class="flex items-start justify-between gap-4">
      <div class="flex items-center gap-3">
        <div class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-lg bg-green-200 dark:bg-green-800 text-lg font-semibold text-green-700 dark:text-green-300">⚙</div>
        <div>
          <div class="flex items-center gap-2">
            <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">{data.name || data.key}</h1>
            <PluginUpdateMenu pluginKey={serviceKey} />
            <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">plugin v{data.version}</span>
            <span class="rounded-full px-2 py-0.5 text-[10px] font-medium {stateClasses[data.status.state]}" data-testid="service-state">{data.status.state}</span>
            {#if data.auto_off}
              <span class="rounded-full px-2 py-0.5 text-[10px] font-medium {data.auto_off.enabled ? 'bg-pos-100 text-pos-400' : 'bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600'}" data-testid="auto-off-badge">{autoOffBadge(data.auto_off)}</span>
            {/if}
          </div>
          {#if data.description}
            <p class="mt-0.5 text-sm text-black-800 dark:text-black-600">{data.description}</p>
          {/if}
          <p class="mt-0.5 font-mono text-xs text-black-700 dark:text-black-600">{data.path}</p>
        </div>
      </div>
      <div class="flex items-center gap-2">
        {#if running}
          <Button size="md" variant="danger" disabled={!!busy} onclick={() => act("stop")}>{busy === "stop" ? "Stopping…" : "Stop"}</Button>
        {:else}
          <Button size="md" disabled={!!busy} onclick={() => act("start")}>{busy === "start" ? "Starting…" : "Start"}</Button>
        {/if}
        <Button size="md" disabled={!!busy} onclick={() => act("restart")}>{busy === "restart" ? "Restarting…" : "Restart"}</Button>
      </div>
    </div>

    <section class="grid grid-cols-1 gap-3 sm:grid-cols-3">
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
        <p class="text-xs text-black-700 dark:text-black-600">Started</p>
        <p class="mt-1 text-sm text-black-900 dark:text-white-100">{fmt(data.status.started_at)}</p>
      </div>
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
        <p class="text-xs text-black-700 dark:text-black-600">Restarts</p>
        <p class="mt-1 text-sm text-black-900 dark:text-white-100">{data.status.restarts}</p>
      </div>
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
        <p class="text-xs text-black-700 dark:text-black-600">Last error</p>
        <p class="mt-1 truncate text-sm text-black-900 dark:text-white-100" title={data.status.last_error}>{data.status.last_error || "—"}</p>
      </div>
    </section>

    {#if data.status.state === "sleeping"}
      <div class="rounded-xl border border-prog-300 bg-prog-100 px-4 py-3 text-sm" role="status" data-testid="sleeping-banner">
        <p class="font-medium text-prog-400">Sleeping — wakes on the next request</p>
        <p class="mt-0.5 text-xs text-black-800 dark:text-black-700">
          Asleep since {fmt(data.status.slept_at)}{#if hasTime(data.status.last_active)} · last active {fmt(data.status.last_active)}{/if}{#if data.status.last_wake_ms} · last wake took ~{(data.status.last_wake_ms / 1000).toFixed(1)}s{/if}
        </p>
      </div>
    {/if}

    {#if ao}
      <section data-testid="service-auto-off">
        <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Auto-off</h2>
        <p class="mt-1 text-sm text-black-800 dark:text-black-600">Stops the service after it has been idle (no request in flight, no remote turn open) and starts it again on the next request, which waits for the boot.</p>
        <div class="mt-3 space-y-3 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4 text-sm">
          {#if ao.supported}
            <p class="text-black-900 dark:text-white-100" data-testid="auto-off-plugin">The plugin supports auto-off (default idle {fmtIdle(ao.default_idle_seconds)}).</p>
          {:else}
            <div data-testid="auto-off-plugin">
              <p class="text-black-900 dark:text-white-100">This service cannot auto-off</p>
              <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">{ao.reason || "The plugin does not declare auto-off support."}</p>
            </div>
          {/if}
          <div class="flex flex-wrap items-center gap-3">
            <div class="inline-flex overflow-hidden rounded-lg border border-white-300 dark:border-navy-600" role="radiogroup" aria-label="Auto-off mode">
              {#each autoOffModes as m (m.value)}
                <button
                  type="button"
                  role="radio"
                  aria-checked={aoModeShown === m.value}
                  disabled={!!busy}
                  class="px-3 py-1.5 text-xs font-medium {aoModeShown === m.value ? 'bg-green-200 dark:bg-green-800 text-green-700 dark:text-green-300' : 'text-black-800 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-600'}"
                  onclick={() => (aoMode = m.value)}
                >{m.label}</button>
              {/each}
            </div>
            <label class="flex items-center gap-2 text-xs text-black-800 dark:text-black-600">
              Idle limit
              <span class="w-20"><TextInput value={aoIdleShown} onChange={(v) => (aoIdle = v)} ariaLabel="Idle limit in minutes" /></span>
              minutes
            </label>
            <Button size="sm" disabled={!aoDirty || !!busy} onclick={() => saveAutoOff()}>{busy === "auto-off" ? "Saving…" : "Save auto-off"}</Button>
            {#if aoDirty}<Button size="sm" variant="secondary" disabled={!!busy} onclick={() => { aoMode = null; aoIdle = null; }}>Discard</Button>{/if}
          </div>
          <p class="text-xs text-black-700 dark:text-black-600">Default follows the plugin; On and Off override it.</p>
          {#if aoRisky}
            <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-3 py-2 text-xs text-red-700 dark:text-red-400" role="alert" data-testid="auto-off-warning">
              The plugin says it cannot auto-off: {ao.reason || "no reason given"}. Forcing it may stop background work that no request will wake up.
            </div>
          {/if}
        </div>
      </section>
    {/if}

    {#if data.configs?.length}
      <section data-testid="service-config">
        <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Configuration</h2>
        <p class="mt-1 text-sm text-black-800 dark:text-black-600">Sent to the plugin when it starts and pushed again on save. Secrets are stored encrypted and never shown.</p>
        <div class="mt-3 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
          {#key formKey}
            {#each data.configs as c (c.key)}
              <div class="grid grid-cols-1 gap-2 border-b border-white-300 dark:border-navy-600 px-4 py-3 sm:grid-cols-3 sm:items-center">
                <div>
                  <p class="font-mono text-sm text-black-900 dark:text-white-100">{c.key}{#if c.required}<span class="text-neg-400"> *</span>{:else}<span class="ml-1.5 font-sans text-[10px] text-black-700 dark:text-black-600" data-testid="config-optional">optional</span>{/if}</p>
                  {#if c.description}<p class="mt-0.5 text-xs text-black-700 dark:text-black-600">{c.description}</p>{/if}
                </div>
                <div class="sm:col-span-2">
                  <FieldWidget field={asField(c)} value={draft[c.key] ?? c.value} disabled={!!busy} onChange={(v) => (draft = { ...draft, [c.key]: v })} />
                  {#if c.is_secret}
                    <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">{c.has_value ? "Stored — leave blank to keep." : "Not set."}</p>
                  {/if}
                </div>
              </div>
            {/each}
          {/key}
          <div class="flex items-center justify-end gap-2 px-4 py-3">
            {#if dirty}<Button size="sm" variant="secondary" disabled={!!busy} onclick={() => { draft = {}; formKey++; }}>Discard</Button>{/if}
            <Button size="sm" disabled={!dirty || !!busy} onclick={saveConfig}>{busy === "config" ? "Saving…" : "Save configuration"}</Button>
          </div>
        </div>
      </section>
    {/if}

    <section>
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Routes</h2>
      <div class="mt-3 overflow-hidden rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
        {#each data.routes as r (r.prefix)}
          <div class="flex items-center justify-between border-b border-white-300 dark:border-navy-600 px-4 py-2 text-sm last:border-b-0">
            <span class="font-mono text-black-900 dark:text-white-100">{data.path.replace(/\/$/, "")}{r.prefix}</span>
            <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[11px] text-black-800 dark:text-black-600">{authLabel[r.auth] ?? r.auth}</span>
          </div>
        {/each}
      </div>
      {#if data.capabilities?.includes("remote_source")}
        <p class="mt-2 text-xs text-black-700 dark:text-black-600">Offers a remote agent source — pick "Plugin" in Team › New agent › Remote agent.</p>
      {/if}
    </section>

    <section>
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Access tokens</h2>
      <p class="mt-1 text-sm text-black-800 dark:text-black-600">Bearer tokens for the routes marked Token. Only a hash is kept; the secret shows once.</p>
      {#if secret}
        <div class="mt-3 rounded-lg border border-cau-300 bg-cau-100 px-4 py-3 text-sm" role="alert" data-testid="token-secret">
          <p class="text-black-900 dark:text-black-800">Copy the token <span class="font-medium">{secret.name}</span> now — it will not be shown again.</p>
          <div class="mt-2 flex items-center gap-2">
            <code class="flex-1 truncate rounded bg-white-100 px-2 py-1 font-mono text-xs text-black-900">{secret.value}</code>
            <Button size="sm" onclick={() => navigator.clipboard?.writeText(secret?.value ?? "")}>Copy</Button>
            <Button size="sm" variant="secondary" onclick={() => (secret = null)}>Done</Button>
          </div>
        </div>
      {/if}
      <div class="mt-3 overflow-hidden rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
        {#each data.tokens ?? [] as t (t.id)}
          <div class="flex items-center gap-3 border-b border-white-300 dark:border-navy-600 px-4 py-2 text-sm last:border-b-0">
            <span class="flex-1 text-black-900 dark:text-white-100">{t.name} <span class="font-mono text-xs text-black-700 dark:text-black-600">…{t.hint}</span></span>
            <span class="text-xs text-black-700 dark:text-black-600">created {fmt(t.created_at)} · last used {fmt(t.last_used)}</span>
            <Button size="sm" variant="secondary" disabled={!!busy} onclick={() => rotate(t.id)}>Rotate</Button>
            <Button size="sm" variant="danger" disabled={!!busy} onclick={() => revoke(t.id)}>Revoke</Button>
          </div>
        {:else}
          <p class="px-4 py-3 text-sm text-black-700 dark:text-black-600">No tokens yet.</p>
        {/each}
        <div class="flex items-center gap-2 border-t border-white-300 dark:border-navy-600 px-4 py-3">
          <div class="w-64"><TextInput value={tokenName} onChange={(v) => (tokenName = v)} placeholder="Token name (e.g. partner-bot)" ariaLabel="Token name" /></div>
          <Button size="sm" disabled={!!busy} onclick={generate}>{busy === "token" ? "Generating…" : "Generate token"}</Button>
        </div>
      </div>
    </section>

    <section>
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Callback to wick</h2>
      <div class="mt-3 flex items-center justify-between gap-4 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4 text-sm">
        <div>
          <p class="text-black-900 dark:text-white-100">WICK_PLUGIN_TOKEN — {data.callback_revoked ? "revoked" : "issued on every start"}</p>
          <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">Scopes: {data.callback_scopes?.length ? data.callback_scopes.join(", ") : "whoami only"}</p>
        </div>
        {#if data.callback_revoked}
          <Button size="sm" disabled={!!busy} onclick={() => act("callback-allow")}>Allow</Button>
        {:else}
          <Button size="sm" variant="danger" disabled={!!busy} onclick={() => act("callback-revoke")}>Revoke</Button>
        {/if}
      </div>
    </section>

    <section>
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Log <span class="text-xs font-normal text-black-700 dark:text-black-600">last {data.logs?.length ?? 0} lines</span></h2>
      <pre class="mt-3 max-h-80 overflow-auto rounded-xl bg-navy-800 p-4 font-mono text-xs leading-5 text-white-100" data-testid="service-log">{(data.logs ?? []).join("\n") || "No output yet."}</pre>
    </section>
  </div>
{/if}

<ConfirmDialog
  open={aoConfirm}
  title="Force auto-off on?"
  body={`The plugin says it cannot auto-off: ${ao?.reason || "no reason given"}. Forcing it may stop background work that no request will wake up.`}
  confirmLabel="Force on"
  destructive
  onConfirm={() => saveAutoOff(true)}
  onCancel={() => (aoConfirm = false)}
/>
