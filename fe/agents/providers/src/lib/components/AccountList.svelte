<script lang="ts">
  import type { Snippet } from "svelte";
  import { Button } from "@wick-fe/common-ui";
  import { prettyPlan, usageLabel, fmtResetsIn, validTime, type PoolAccount } from "$lib/logintty.js";

  /* The accounts of one instance (Instance → Account → Model): one row per
     login to one provider, grouped by provider. Type-agnostic — the API
     fills {id, label, provider, status, usage?} per type (omp: its
     credential pool; opencode: auth.json providers), so a new type only
     has to supply data. Removal is per provider because that is the
     granularity the CLIs expose. */
  type Props = {
    accounts: PoolAccount[];
    removeLabel: string;
    /* Provider whose removal is in flight ("" = none). */
    removing: string;
    canAdd: boolean;
    adding: boolean;
    onAdd: () => void;
    /* Called with the provider, plus the account id when removal is per
       account (perAccount). */
    onRemove: (provider: string, accountId?: string) => void;
    /* opencode: every row is its own login (one per data folder), so each
       row gets its own remove; omp only removes a whole provider. */
    perAccount?: boolean;
    note?: Snippet;
  };
  let { accounts, removeLabel, removing, canAdd, adding, onAdd, onRemove, perAccount = false, note }: Props = $props();

  let providers = $derived([...new Set(accounts.map((a) => a.provider))]);

  function kindLabel(k: string): string {
    if (k === "api" || k === "api_key") return "API key";
    return k;
  }
  function fmt(iso: string): string {
    return validTime(iso) ? new Date(iso).toLocaleString() : "";
  }
</script>

<div class="space-y-2" data-testid="panel-accounts">
  <div class="flex items-center justify-between gap-2">
    <p class="text-[11px] font-semibold tracking-wide text-black-700 dark:text-black-600">ACCOUNTS ({accounts.length})</p>
    {#if canAdd}
      <Button variant="secondary" testid="panel-add-account" disabled={adding} onclick={onAdd}>Add another account</Button>
    {/if}
  </div>
  {#each providers as prov (prov)}
    <div class="rounded-lg border border-white-300 dark:border-navy-600 divide-y divide-white-300 dark:divide-navy-600">
      <div class="flex items-center justify-between gap-2 px-3 py-1.5 text-xs">
        <span class="font-mono font-medium text-black-900 dark:text-white-100">{prov}</span>
        {#if !perAccount}
          <button type="button" data-testid="panel-logout-{prov}" class="rounded px-1.5 py-0.5 text-[11px] text-neg-400 hover:bg-white-300 dark:hover:bg-navy-600 disabled:opacity-50" disabled={removing !== ""} onclick={() => onRemove(prov)}>
            {removing === prov ? "Removing…" : removeLabel}
          </button>
        {/if}
      </div>
      {#each accounts.filter((a) => a.provider === prov) as a (a.id)}
        <div data-testid="panel-account-row" class="px-3 py-1.5 text-xs space-y-0.5">
          <div class="flex items-baseline justify-between gap-3">
            <span class="min-w-0 truncate text-black-900 dark:text-white-100">{a.label}{#if a.kind}<span class="ml-1 text-[11px] text-black-600 dark:text-black-700">({kindLabel(a.kind)})</span>{/if}</span>
            {#if perAccount}
              <button type="button" data-testid="panel-logout-{a.id}" class="ml-auto shrink-0 rounded px-1.5 py-0.5 text-[11px] text-neg-400 hover:bg-white-300 dark:hover:bg-navy-600 disabled:opacity-50" disabled={removing !== ""} onclick={() => onRemove(a.provider, a.id)}>
                {removing === a.id ? "Removing…" : removeLabel}
              </button>
            {/if}
            {#if a.status === "disabled"}
              <span class="shrink-0 rounded bg-neg-100 dark:bg-neg-400/20 px-1.5 py-0.5 text-[11px] font-semibold text-neg-400">disabled</span>
            {:else}
              <span class="shrink-0 rounded bg-pos-100 dark:bg-pos-400/20 px-1.5 py-0.5 text-[11px] font-semibold text-pos-400">active</span>
            {/if}
          </div>
          {#if a.email && a.email !== a.label}
            <p class="text-[11px] font-mono text-black-700 dark:text-black-600 truncate">{a.email}</p>
          {/if}
          {#if a.plan || a.org}
            <p class="text-[11px] text-black-700 dark:text-black-600">{[a.plan ? `Plan: ${prettyPlan(a.plan, a.provider)}` : "", a.org].filter(Boolean).join(" · ")}</p>
          {/if}
          {#if a.status === "disabled"}
            <p class="text-[11px] text-neg-400">{a.disabledCause}{fmt(a.disabledAt) ? ` (since ${fmt(a.disabledAt)})` : ""} — log in again to restore</p>
          {/if}
          {#each a.usage ?? [] as w (w.key)}
            <p class="text-[11px] text-black-700 dark:text-black-600">{usageLabel(w.key)}: {Math.round(w.utilization)}%{fmtResetsIn(w.resetsAt, Date.now()) ? ` · resets in ${fmtResetsIn(w.resetsAt, Date.now())}` : ""}</p>
          {/each}
        </div>
      {/each}
    </div>
  {/each}
  {#if note}
    <p class="text-[11px] text-black-700 dark:text-black-600">{@render note()}</p>
  {/if}
</div>
