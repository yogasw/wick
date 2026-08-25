<script lang="ts">
  /* Admin-only Access Policy + Per-session config cards. Each toggle POSTs
     immediately and reflects the saved state, mirroring the legacy
     connectorAccessSection / connectorSessionConfigSection forms (which
     submit on every checkbox change). Access policy toggles submit together
     as one payload; session config has its own endpoint. */
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { setConnectorAccessPolicy, setConnectorSessionConfig, type AccessPolicy } from "$lib/api.js";
  import type { ConnectorDetail } from "$lib/types.js";

  type Props = {
    connectorKey: string;
    connectorId: string;
    data: ConnectorDetail;
    onchanged?: () => void;
  };
  let { connectorKey, connectorId, data, onchanged }: Props = $props();

  let enableSso = $state(false);
  let multiAccount = $state(false);
  let allowOthersConnectSso = $state(false);
  let allowOthersConfigure = $state(false);
  let allowSessionConfig = $state(false);
  let busy = $state(false);
  let sessionBusy = $state(false);

  $effect(() => {
    enableSso = data.enable_sso;
    multiAccount = data.multi_account;
    allowOthersConnectSso = data.allow_others_connect_sso;
    allowOthersConfigure = data.allow_others_configure;
    allowSessionConfig = data.session_config_allowed;
  });

  const hasOauth = $derived(data.oauth !== null);

  async function savePolicy(): Promise<void> {
    if (busy) return;
    busy = true;
    const policy: AccessPolicy = {
      allow_others_configure: allowOthersConfigure,
      allow_others_connect_sso: hasOauth ? allowOthersConnectSso : false,
      enable_sso: hasOauth ? enableSso : false,
      multi_account: hasOauth ? multiAccount : false,
    };
    try {
      await setConnectorAccessPolicy(connectorKey, connectorId, policy);
      toastOk("Access policy saved");
      onchanged?.();
    } catch (e) {
      toastError("Save failed", e instanceof Error ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  async function saveSessionConfig(): Promise<void> {
    if (sessionBusy) return;
    sessionBusy = true;
    try {
      allowSessionConfig = await setConnectorSessionConfig(connectorKey, connectorId, allowSessionConfig);
      toastOk("Per-session config saved");
    } catch (e) {
      allowSessionConfig = !allowSessionConfig;
      toastError("Save failed", e instanceof Error ? e.message : String(e));
    } finally {
      sessionBusy = false;
    }
  }
</script>

<section class="mt-8">
  <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Access policy</h2>
  <p class="mt-1 text-sm text-black-800 dark:text-black-600">
    Controls SSO configuration and what non-admin users with tag access to this instance are allowed to do.
  </p>
  <div class="mt-4 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 divide-y divide-white-300 dark:divide-navy-600">
    {#if hasOauth}
      <label class="flex cursor-pointer items-center justify-between gap-4 px-4 py-3 hover:bg-white-200 dark:hover:bg-navy-800">
        <div class="min-w-0 flex-1">
          <p class="text-sm font-medium text-black-900 dark:text-white-100">Enable SSO</p>
          <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">Activate the {data.oauth?.display_name} OAuth flow for this instance.</p>
        </div>
        <input type="checkbox" disabled={busy} bind:checked={enableSso} onchange={savePolicy} aria-label="Enable SSO" class="h-5 w-9 accent-green-500" />
      </label>
      {#if enableSso}
        <label class="flex cursor-pointer items-center justify-between gap-4 px-4 py-3 hover:bg-white-200 dark:hover:bg-navy-800">
          <div class="min-w-0 flex-1">
            <p class="text-sm font-medium text-black-900 dark:text-white-100">Multi-account</p>
            <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">Each OAuth connect adds a new account; off replaces the existing one.</p>
          </div>
          <input type="checkbox" disabled={busy} bind:checked={multiAccount} onchange={savePolicy} aria-label="Multi-account" class="h-5 w-9 accent-green-500" />
        </label>
        <label class="flex cursor-pointer items-center justify-between gap-4 px-4 py-3 hover:bg-white-200 dark:hover:bg-navy-800">
          <div class="min-w-0 flex-1">
            <p class="text-sm font-medium text-black-900 dark:text-white-100">Allow others to connect via SSO</p>
            <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">Users with tag access can connect their own {data.oauth?.display_name} account.</p>
          </div>
          <input type="checkbox" disabled={busy} bind:checked={allowOthersConnectSso} onchange={savePolicy} aria-label="Allow others to connect via SSO" class="h-5 w-9 accent-green-500" />
        </label>
      {/if}
    {/if}
    <label class="flex cursor-pointer items-center justify-between gap-4 px-4 py-3 hover:bg-white-200 dark:hover:bg-navy-800">
      <div class="min-w-0 flex-1">
        <p class="text-sm font-medium text-black-900 dark:text-white-100">Allow others to configure</p>
        <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">Users with tag access can edit credentials and settings on this instance.</p>
      </div>
      <input type="checkbox" disabled={busy} bind:checked={allowOthersConfigure} onchange={savePolicy} aria-label="Allow others to configure" class="h-5 w-9 accent-green-500" />
    </label>
  </div>
</section>

{#if data.session_config_capable}
  <section class="mt-8">
    <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Per-session config</h2>
    <p class="mt-1 text-sm text-black-800 dark:text-black-600">
      Let users temporarily override this instance's config for a single agent session without changing the saved instance.
    </p>
    <div class="mt-4 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
      <label class="flex cursor-pointer items-center justify-between gap-4 px-4 py-3 hover:bg-white-200 dark:hover:bg-navy-800">
        <div class="min-w-0 flex-1">
          <p class="text-sm font-medium text-black-900 dark:text-white-100">Allow per-session config override</p>
          <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">When on, users can clone this connector into a per-session instance from the session Config tab. Default off.</p>
        </div>
        <input type="checkbox" disabled={sessionBusy} bind:checked={allowSessionConfig} onchange={saveSessionConfig} aria-label="Allow per-session config override" class="h-5 w-9 accent-green-500" />
      </label>
    </div>
  </section>
{/if}
