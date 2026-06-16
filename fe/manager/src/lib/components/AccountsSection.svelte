<script lang="ts">
  /* Connected-accounts management for an OAuth/SSO connector row. Lists each
     account, connects a new one via the OAuth popup (start_url), lets the
     row's configurer (or the account's own user) disconnect, and edit which
     operations are enabled for that account. Mirrors the legacy
     connector_account_ops.templ + the account ops / disconnect routes and the
     per-row OAuth Connect button on connector_detail.templ. */
  import { Button, ConfirmDialog } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { disconnectConnectorAccount, setAccountDisabledOps } from "$lib/api.js";
  import { startConnectorOAuth, type OAuthConnect } from "./connectorOAuth.js";
  import type { ConnectorAccount, ConnectorOAuthMeta, ConnectorOp } from "$lib/types.js";

  type Props = {
    connectorKey: string;
    connectorId: string;
    accounts: ConnectorAccount[];
    operations: ConnectorOp[];
    oauth: ConnectorOAuthMeta | null;
    enableSso: boolean;
    multiAccount: boolean;
    onchanged?: () => void;
  };
  let { connectorKey, connectorId, accounts, operations, oauth, enableSso, multiAccount, onchanged }: Props = $props();

  let connecting = $state(false);
  let confirmId = $state("");
  let handle: OAuthConnect | null = null;

  /* Per-account operation editor: the account whose editor is open, its
     working disabled-ops set, and the in-flight save flag. */
  let editId = $state("");
  let editDisabled = $state<Set<string>>(new Set());
  let savingOps = $state(false);

  const canConnect = $derived(enableSso && oauth !== null && oauth.start_url !== "");
  const connectLabel = $derived(
    accounts.length > 0 ? (multiAccount ? "+ Connect another account" : "Reconnect") : "Connect account",
  );

  function connect(): void {
    if (!oauth || !oauth.start_url || connecting) return;
    connecting = true;
    handle = startConnectorOAuth(oauth.start_url);
    handle.promise
      .then(() => {
        toastOk("Account connected");
        onchanged?.();
      })
      .catch((e) => {
        toastError("Connect failed", e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        connecting = false;
        handle = null;
      });
  }

  async function doDisconnect(): Promise<void> {
    const id = confirmId;
    confirmId = "";
    try {
      await disconnectConnectorAccount(connectorKey, connectorId, id);
      toastOk("Account disconnected");
      onchanged?.();
    } catch (e) {
      toastError("Disconnect failed", e instanceof Error ? e.message : String(e));
    }
  }

  function openOps(acc: ConnectorAccount): void {
    editId = acc.id;
    editDisabled = new Set(acc.disabled_ops ?? []);
  }

  function toggleOp(key: string): void {
    const next = new Set(editDisabled);
    if (next.has(key)) {
      next.delete(key);
    } else {
      next.add(key);
    }
    editDisabled = next;
  }

  async function saveOps(): Promise<void> {
    if (savingOps) return;
    savingOps = true;
    try {
      await setAccountDisabledOps(connectorKey, connectorId, editId, [...editDisabled]);
      toastOk("Account operations saved");
      editId = "";
      onchanged?.();
    } catch (e) {
      toastError("Save failed", e instanceof Error ? e.message : String(e));
    } finally {
      savingOps = false;
    }
  }

  $effect(() => () => handle?.cancel());
</script>

{#if oauth}
  <section class="mt-8">
    <div class="flex items-center justify-between gap-3">
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Connected accounts</h2>
      {#if canConnect}
        <Button variant="secondary" size="md" disabled={connecting} onclick={connect}>
          {connecting ? "Connecting…" : connectLabel}
        </Button>
      {/if}
    </div>
    <p class="mt-1 text-sm text-black-800 dark:text-black-600">
      {oauth.display_name} accounts connected to this instance via OAuth.
    </p>

    {#if accounts.length === 0}
      <p class="mt-3 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-4 py-3 text-sm text-black-700 dark:text-black-600">
        {#if canConnect}
          No accounts connected yet. Use Connect account to link one.
        {:else if !enableSso}
          SSO is disabled for this instance — enable it in Access Policy first.
        {:else}
          OAuth is not configured — set the Client ID in Credentials first.
        {/if}
      </p>
    {:else}
      <div class="mt-3 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 divide-y divide-white-300 dark:divide-navy-600">
        {#each accounts as acc (acc.id)}
          <div class="px-4 py-3">
            <div class="flex items-center justify-between gap-4">
              <div class="min-w-0">
                <p class="text-sm font-medium text-black-900 dark:text-white-100">@{acc.display_name}</p>
                {#if acc.disabled_ops && acc.disabled_ops.length}
                  <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600">{acc.disabled_ops.length} operation(s) disabled for this account</p>
                {/if}
              </div>
              {#if acc.can_manage}
                <div class="flex flex-shrink-0 items-center gap-2">
                  {#if operations.length}
                    <Button variant="secondary" size="sm" onclick={() => (editId === acc.id ? (editId = "") : openOps(acc))}>
                      {editId === acc.id ? "Close" : "Manage operations"}
                    </Button>
                  {/if}
                  <Button variant="danger" size="sm" onclick={() => (confirmId = acc.id)}>Disconnect</Button>
                </div>
              {/if}
            </div>

            {#if editId === acc.id}
              <div class="mt-3 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-3">
                <p class="mb-2 text-xs text-black-700 dark:text-black-600">Uncheck an operation to disable it for this account only.</p>
                <div class="max-h-64 space-y-1 overflow-y-auto">
                  {#each operations as op (op.key)}
                    <label class="flex items-center gap-2 text-sm text-black-800 dark:text-white-100">
                      <input type="checkbox" class="accent-green-500" checked={!editDisabled.has(op.key)} disabled={savingOps} onchange={() => toggleOp(op.key)} />
                      <span class="truncate">{op.name}</span>
                      {#if op.destructive}
                        <span class="flex-shrink-0 rounded bg-cau-100 px-1.5 py-0.5 text-[10px] font-medium text-cau-400">destructive</span>
                      {/if}
                    </label>
                  {/each}
                </div>
                <div class="mt-3 flex items-center gap-2">
                  <Button size="sm" disabled={savingOps} onclick={saveOps}>{savingOps ? "Saving…" : "Save"}</Button>
                  <Button variant="secondary" size="sm" disabled={savingOps} onclick={() => (editId = "")}>Cancel</Button>
                </div>
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </section>

  <ConfirmDialog
    open={confirmId !== ""}
    title="Disconnect this account?"
    body="The stored OAuth token is removed. The user can reconnect at any time."
    confirmLabel="Disconnect"
    destructive
    onConfirm={doDisconnect}
    onCancel={() => (confirmId = "")}
  />
{/if}
