<script lang="ts">
  /* Per-row admin surface: label rename, credentials/configs form, a
     health-check probe, the operations table, rate limit, access policy,
     and accounts. Mirrors the legacy connector_detail.templ page (disable/
     duplicate/delete live on the connector list's per-row menu, not here).
     Config auto-save is owned by ConfigsForm; the rest POSTs through the
     JSON api client. */
  import { Button, TextInput, TextArea, NumberInput } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import {
    getConnectorRow,
    setConnectorLabel,
    setConnectorDescription,
    runHealthCheck,
    setConnectorRateLimit,
  } from "$lib/api.js";
  import type { ConnectorDetail } from "$lib/types.js";
  import ConfigsForm from "./fields/ConfigsForm.svelte";
  import OperationsTable from "./OperationsTable.svelte";
  import AccountsSection from "./AccountsSection.svelte";
  import AccessPolicySection from "./AccessPolicySection.svelte";
  import ActiveSessionsSection from "./ActiveSessionsSection.svelte";
  import ExtensionsSection from "./ExtensionsSection.svelte";
  import { setBreadcrumbNames, clearBreadcrumbNames } from "$lib/stores/breadcrumb.js";

  type Props = { connectorKey: string; connectorId: string };
  let { connectorKey, connectorId }: Props = $props();

  let data = $state<ConnectorDetail | null>(null);
  let loading = $state(true);
  let error = $state("");
  let labelDraft = $state("");
  let descDraft = $state("");
  let descEnabled = $state(false);
  let rateDraft = $state(0);
  let rateBusy = $state(false);
  let healthBusy = $state(false);
  let healthBanner = $state<{ ok: boolean; msg: string } | null>(null);

  async function load(silent = false) {
    if (!silent) loading = true;
    try {
      data = await getConnectorRow(connectorKey, connectorId);
      labelDraft = data.label;
      descDraft = data.description ?? "";
      // When the connector requires the AI description, the section is always
      // shown (it can't be toggled off) so the operator can't miss it.
      descEnabled = data.require_ai_description === true || descDraft.trim() !== "";
      rateDraft = data.rate_limit_rpm;
      error = "";
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (silent) {
        toastError("Refresh failed", msg);
      } else {
        error = msg;
      }
    } finally {
      if (!silent) loading = false;
    }
  }

  function refresh() {
    load(true);
  }

  async function saveLabel() {
    if (!data || !labelDraft.trim()) return;
    try {
      await setConnectorLabel(connectorKey, connectorId, labelDraft.trim());
      data = { ...data, label: labelDraft.trim() };
      toastOk("Label saved");
    } catch (e) {
      toastError("Save failed", e instanceof Error ? e.message : String(e));
    }
  }

  // AI description auto-saves (debounced), matching the config fields — no Save
  // button. Persists descDraft as-is; empty clears it server-side.
  let descStatus = $state<"" | "saving" | "saved" | "error">("");
  let descTimer: ReturnType<typeof setTimeout> | undefined;

  async function persistDescription() {
    if (!data) return;
    descStatus = "saving";
    try {
      await setConnectorDescription(connectorKey, connectorId, descDraft.trim());
      data = { ...data, description: descDraft.trim() };
      descStatus = "saved";
      setTimeout(() => {
        if (descStatus === "saved") descStatus = "";
      }, 2000);
    } catch (e) {
      descStatus = "error";
      toastError("Save failed", e instanceof Error ? e.message : String(e));
    }
  }

  function onDescInput(v: string) {
    descDraft = v;
    if (descTimer) clearTimeout(descTimer);
    descTimer = setTimeout(persistDescription, 800);
  }

  async function checkHealth() {
    if (healthBusy) return;
    healthBusy = true;
    healthBanner = null;
    try {
      const res = await runHealthCheck(connectorKey, connectorId);
      if (!res.ok) {
        healthBanner = { ok: false, msg: res.error ?? "Permission check failed" };
      } else {
        const locked = res.newly_locked ?? [];
        const cleared = res.newly_cleared ?? [];
        const parts: string[] = [];
        if (locked.length) parts.push(`System-disabled: ${locked.join(", ")}.`);
        if (cleared.length) parts.push(`Cleared: ${cleared.join(", ")}.`);
        if (!parts.length) parts.push("No changes — every operation has the permissions it needs.");
        healthBanner = { ok: true, msg: parts.join(" ") };
        await load(true);
      }
    } catch (e) {
      healthBanner = { ok: false, msg: e instanceof Error ? e.message : String(e) };
    } finally {
      healthBusy = false;
    }
  }

  async function saveRateLimit() {
    if (!data || rateBusy) return;
    rateBusy = true;
    try {
      const saved = await setConnectorRateLimit(connectorKey, connectorId, rateDraft);
      rateDraft = saved;
      data = { ...data, rate_limit_rpm: saved };
      toastOk("Rate limit saved");
    } catch (e) {
      toastError("Save failed", e instanceof Error ? e.message : String(e));
    } finally {
      rateBusy = false;
    }
  }

  $effect(() => {
    if (data) setBreadcrumbNames({ connector: data.name, row: data.label });
  });

  $effect(() => {
    load();
    return clearBreadcrumbNames;
  });
</script>

{#if loading}
  <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
{:else if error}
  <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
{:else if data}
  <div class="space-y-8 pb-8">
    <div class="flex items-start justify-between gap-3">
      <div class="flex items-center gap-3 min-w-0">
        <span class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-lg bg-green-200 dark:bg-green-800 text-lg" aria-hidden="true">{data.icon || "🔌"}</span>
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-2">
            <h1 class="text-[1.375rem] font-semibold text-black-900 dark:text-white-100">{data.label}</h1>
            {#if data.disabled}
              <span class="inline-flex items-center rounded-full bg-white-300 dark:bg-navy-600 px-2.5 py-0.5 text-xs font-medium text-black-700 dark:text-black-600">Disabled</span>
            {:else}
              <span class="inline-flex items-center rounded-full bg-pos-100 px-2.5 py-0.5 text-xs font-medium text-pos-400">Enabled</span>
            {/if}
          </div>
          <p class="mt-0.5 font-mono text-[11px] text-black-700 dark:text-black-600">{data.id}</p>
          {#if data.description}
            <p class="mt-1 max-w-xl text-sm text-black-800 dark:text-black-600">{data.description}</p>
          {/if}
        </div>
      </div>
    </div>

    <section>
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Label</h2>
      <div class="mt-3 flex items-center gap-2">
        <div class="w-full max-w-md">
          <TextInput value={labelDraft} disabled={!data.can_configure} onChange={(v) => (labelDraft = v)} ariaLabel="Connector label" />
        </div>
        <Button size="lg" disabled={!data.can_configure} onclick={saveLabel}>Save</Button>
      </div>
    </section>

    <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm">
      <div class="flex items-start justify-between gap-4 px-5 py-4">
        <div class="min-w-0">
          <div class="flex items-center gap-2">
            <h2 class="text-base font-semibold text-black-900 dark:text-white-100">AI description</h2>
            {#if data.require_ai_description}
              <span class="rounded-full bg-neg-400/15 px-2 py-0.5 text-[11px] font-medium text-neg-400">Required</span>
            {/if}
            {#if descEnabled && descStatus}
              <span
                class="text-[11px] {descStatus === 'error' ? 'text-neg-400' : descStatus === 'saved' ? 'text-pos-400' : 'text-black-700 dark:text-black-600'}"
              >
                {descStatus === "saving" ? "saving…" : descStatus === "saved" ? "✓ saved" : descStatus === "error" ? "✗ failed" : ""}
              </span>
            {/if}
          </div>
          <p class="mt-1 text-sm text-black-800 dark:text-black-600">
            Extra guidance the AI sees for this connector — when to use it, team notes, constraints. Appended to the built-in description.
            {#if data.require_ai_description}
              <span class="text-black-900 dark:text-white-200">This connector requires it — it stays “needs setup” until you fill it in (e.g. state who may use this instance).</span>
            {/if}
          </p>
        </div>
        <!-- Pill switch: shows/hides the field to keep the page tidy. When the
             connector requires the AI description it's forced on (can't hide). -->
        <!-- Knob offset uses inline style, not translate-x utilities: those
             (translate-x-5 / -0.5) get purged from the manager CSS and the knob
             would never move. Inline transform is purge-proof. -->
        <button
          type="button"
          role="switch"
          aria-checked={descEnabled}
          aria-label="Toggle AI description"
          disabled={!data.can_configure || data.require_ai_description}
          onclick={() => {
            if (data?.require_ai_description) return;
            descEnabled = !descEnabled;
          }}
          class="relative mt-1 inline-flex h-6 w-11 flex-shrink-0 items-center rounded-full transition-colors {data.can_configure && !data.require_ai_description
            ? 'cursor-pointer'
            : 'opacity-50'} {descEnabled ? 'bg-green-500' : 'bg-white-400 dark:bg-navy-600'}"
        >
          <span
            class="inline-block h-5 w-5 rounded-full bg-white-100 shadow transition-transform"
            style="transform: translateX({descEnabled ? '22px' : '2px'});"
          ></span>
        </button>
      </div>
      {#if descEnabled}
        <div class="border-t border-white-300 dark:border-navy-600 px-5 py-4">
          <TextArea
            value={descDraft}
            disabled={!data.can_configure}
            rows={4}
            onChange={onDescInput}
            ariaLabel="Connector AI description"
          />
          {#if data.require_ai_description && descDraft.trim() === ""}
            <p class="mt-2 text-[11px] text-neg-400">Required — this instance stays “needs setup” until you fill this in.</p>
          {:else}
            <p class="mt-2 text-[11px] text-black-700 dark:text-black-600">Saves automatically as you type.</p>
          {/if}
        </div>
      {/if}
    </section>

    {#if healthBanner}
      <div class="rounded-xl border px-4 py-3 text-sm {healthBanner.ok ? 'border-pos-300 bg-pos-100 text-pos-400' : 'border-neg-300 bg-neg-100 text-neg-400'}">{healthBanner.msg}</div>
    {/if}

    {#if connectorKey === "playwright_browser"}
      <ActiveSessionsSection connectorId={connectorId} />
      <ExtensionsSection connectorId={connectorId} />
    {/if}

    <section>
      <div class="flex items-center justify-between gap-3">
        <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Credentials</h2>
        {#if data.has_health_check}
          <Button variant="secondary" size="md" disabled={healthBusy} onclick={checkHealth}>{healthBusy ? "Checking…" : "Check Permissions"}</Button>
        {/if}
      </div>
      <p class="mt-1 text-sm text-black-800 dark:text-black-600">Per-row values shared by every operation on this connector.</p>
      <ConfigsForm
        connectorKey={connectorKey}
        connectorId={connectorId}
        fields={data.fields ?? []}
        canConfigure={data.can_configure}
      />
    </section>

    {#if data.can_manage_policy}
      <AccessPolicySection connectorKey={connectorKey} connectorId={connectorId} data={data} onchanged={refresh} />
    {/if}

    {#if data.oauth}
      <AccountsSection
        connectorKey={connectorKey}
        connectorId={connectorId}
        accounts={data.accounts ?? []}
        operations={data.operations ?? []}
        oauth={data.oauth}
        enableSso={data.enable_sso}
        multiAccount={data.multi_account}
        onchanged={refresh}
      />
    {/if}

    <section>
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Rate limit</h2>
      <p class="mt-1 text-sm text-black-800 dark:text-black-600">
        Maximum MCP and test-panel calls per minute for this connector instance. Set to 0 to disable limiting.
      </p>
      <div class="mt-3 flex items-center gap-2">
        <div class="w-32">
          <NumberInput value={rateDraft} min={0} disabled={!data.can_configure} onChange={(v) => (rateDraft = v)} ariaLabel="Rate limit per minute" />
        </div>
        <span class="text-sm text-black-800 dark:text-black-600">requests / min</span>
        <Button disabled={!data.can_configure || rateBusy} onclick={saveRateLimit}>Save</Button>
        <span class="text-xs text-black-700 dark:text-black-600">{data.rate_limit_rpm > 0 ? `Currently limited to ${data.rate_limit_rpm} rpm` : "Currently unlimited"}</span>
      </div>
    </section>

    <OperationsTable
      operations={data.operations ?? []}
      categories={data.categories ?? []}
      connectorKey={connectorKey}
      connectorId={connectorId}
      canConfigure={data.can_configure}
    />
  </div>
{/if}
