<script lang="ts">
  /* Admin view of one service plugin: supervisor status with Stop / Start /
     Restart, the routes and who may reach them, access tokens (Generate /
     Rotate / Revoke — the secret is shown once), the callback token switch,
     and the last log lines (refreshed every 3 s). */
  import { Button, TextInput } from "@wick-fe/common-ui";
  import { toastError } from "@wick-fe/common-stores";
  import {
    getServicePlugin, serviceAction, generateServiceToken, rotateServiceToken, revokeServiceToken,
    type ServicePlugin,
  } from "$lib/api.js";
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
    stopped: "bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600",
  };
  const authLabel: Record<string, string> = { public: "Public", token: "Token", "wick-session": "Wick login" };
  const fmt = (s?: string) => (s ? new Date(s).toLocaleString() : "—");

  let running = $derived(data?.status.state === "running" || data?.status.state === "starting");

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
            <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">plugin v{data.version}</span>
            <span class="rounded-full px-2 py-0.5 text-[10px] font-medium {stateClasses[data.status.state]}" data-testid="service-state">{data.status.state}</span>
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
