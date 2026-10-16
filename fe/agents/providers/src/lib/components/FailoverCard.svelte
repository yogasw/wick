<script lang="ts">
  import { onMount } from "svelte";
  import { Button, ProviderPicker, buildProviderOptions, withModelListMeta } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import type { ProviderListItem } from "@wick-fe/common-api";
  import {
    apiGetFailover,
    apiSaveFailover,
    apiResetProviderHealth,
    apiFailoverOptions,
    apiFailoverOptionModels,
    type FailoverConfig,
  } from "$lib/api.js";
  import type { ProviderHealthDTO } from "$lib/types.js";
  import {
    FAILOVER_TRIGGERS,
    MAX_FAILOVER_TARGETS,
    MIN_RECHECK_MINUTES,
    MAX_RECHECK_MINUTES,
    chainProblems,
    crossTypeNote,
    clockLabel,
    healthLabel,
    healthBadgeClass,
    pickValue,
    fromPickValue,
  } from "$lib/failover.js";

  /* Failover has its own card, like IdleCompactCard: its endpoint has its
     own permission (the failover owner permission), so whoever may read
     the instance sees it and only can_edit unlocks the controls. The whole
     config saves at once — the server validates the chain as a unit. */
  type Props = {
    base: string;
    type: string;
    name: string;
    /* health is the account's recorded failure, null while healthy. */
    health?: ProviderHealthDTO | null;
    /* onChanged reloads the page after a save or a reset. */
    onChanged?: () => void;
  };
  let { base, type, name, health = null, onChanged }: Props = $props();

  let cfg = $state<FailoverConfig>({ enabled: false, triggers: [], chain: [], recheck_minutes: 10, return_home: true });
  let errors = $state<string[]>([]);
  let warnings = $state<{ instance: string; message: string }[]>([]);
  let canEdit = $state(false);
  let loaded = $state(false);
  let loadError = $state("");
  let saving = $state(false);
  let resetting = $state(false);
  let providers = $state<ProviderListItem[]>([]);
  // The recheck field is text while typed so an empty or out-of-range
  // value can be shown and refused instead of silently clamped.
  let recheckText = $state("10");
  // One stable id per chain row, kept beside cfg.chain (which is what is
  // saved), so a reorder moves each row's picker state with the row.
  let rowIds = $state<number[]>([]);
  let nextRowId = 0;

  function apply(c: FailoverConfig) {
    cfg = { ...c, triggers: [...c.triggers], chain: c.chain.map((t) => ({ instance: t.instance, model_id: t.model_id ?? "" })) };
    rowIds = c.chain.map(() => nextRowId++);
    recheckText = String(c.recheck_minutes);
  }

  onMount(async () => {
    try {
      const [r, opts] = await Promise.all([apiGetFailover(base, type, name), Promise.resolve().then(() => apiFailoverOptions(base)).catch(() => [])]);
      if (r) {
        apply(r.config);
        errors = r.errors;
        warnings = r.warnings;
        canEdit = r.can_edit;
      }
      providers = opts ?? [];
    } catch (e) {
      loadError = e instanceof Error ? e.message : "Could not load failover";
    } finally {
      loaded = true;
    }
  });

  const selfKey = $derived(`${type}/${name}`);
  // The instance itself is never a choice; validation still catches a
  // stored chain that names it.
  const pickable = $derived(providers.filter((p) => `${p.type}/${p.name}` !== selfKey));
  const problems = $derived(chainProblems(type, name, cfg.chain));
  const recheck = $derived(Number(recheckText));
  const recheckValid = $derived(
    /^\d+$/.test(recheckText.trim()) && recheck >= MIN_RECHECK_MINUTES && recheck <= MAX_RECHECK_MINUTES,
  );
  const invalid = $derived(!recheckValid || problems.some(Boolean) || cfg.chain.some((t) => !t.instance));
  const status = $derived(healthLabel(health));

  /* Server warnings for one target. The history one is replaced by the
     fuller cross-type alert, which also tracks unsaved edits. */
  function warningsFor(tag: string): string[] {
    return warnings.filter((w) => w.instance === tag && !w.message.startsWith("history does not carry over")).map((w) => w.message);
  }

  function toggleTrigger(key: string, on: boolean) {
    cfg.triggers = on ? [...cfg.triggers.filter((t) => t !== key), key] : cfg.triggers.filter((t) => t !== key);
  }
  function addTarget() {
    if (cfg.chain.length < MAX_FAILOVER_TARGETS) {
      cfg.chain.push({ instance: "", model_id: "" });
      rowIds.push(nextRowId++);
    }
  }
  function removeTarget(i: number) {
    cfg.chain.splice(i, 1);
    rowIds.splice(i, 1);
  }
  function moveTarget(i: number, by: number) {
    const j = i + by;
    if (j < 0 || j >= cfg.chain.length) return;
    const next = [...cfg.chain];
    [next[i], next[j]] = [next[j], next[i]];
    cfg.chain = next;
    const ids = [...rowIds];
    [ids[i], ids[j]] = [ids[j], ids[i]];
    rowIds = ids;
  }
  function setTarget(i: number, v: string) {
    const before = cfg.chain[i]?.instance;
    cfg.chain[i] = fromPickValue(v);
    // The server's warnings were about the saved pick: once the row
    // changes they no longer apply until the next save re-checks it.
    const after = cfg.chain[i].instance;
    warnings = warnings.filter((w) => w.instance !== before && w.instance !== after);
  }

  function loadModels(optionValue: string, opts?: { entry?: string; refresh?: boolean }) {
    return apiFailoverOptionModels(base, optionValue, opts).then((r) => withModelListMeta(r?.models ?? [], r ?? {}));
  }

  async function save() {
    if (invalid || !canEdit) return;
    saving = true;
    try {
      const r = await apiSaveFailover(base, type, name, { ...cfg, recheck_minutes: recheck });
      errors = r.errors;
      warnings = r.warnings;
      if (r.errors.length > 0) {
        toastError("Failover not saved");
        return;
      }
      apply(r.config);
      toastOk("Failover saved");
      onChanged?.();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Save failed");
    } finally {
      saving = false;
    }
  }

  async function resetHealth() {
    resetting = true;
    try {
      await apiResetProviderHealth(base, type, name);
      toastOk("Status reset");
      onChanged?.();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Reset failed");
    } finally {
      resetting = false;
    }
  }

  const inputCls =
    "rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-1.5 text-xs text-black-900 dark:text-white-100 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 disabled:opacity-60";
  const iconBtn =
    "rounded border border-white-400 dark:border-navy-600 px-1.5 py-0.5 text-xs text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-40 disabled:cursor-not-allowed";
</script>

{#snippet switchBtn(on: boolean, label: string, testid: string, onclick: () => void)}
  <button
    type="button"
    role="switch"
    aria-label={label}
    aria-checked={on}
    data-testid={testid}
    disabled={!canEdit}
    {onclick}
    class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors disabled:opacity-60 {on ? 'bg-green-500' : 'bg-white-400 dark:bg-navy-600'}"
  >
    <span class="inline-block h-5 w-5 transform rounded-full bg-white-100 shadow transition-transform {on ? 'translate-x-5' : 'translate-x-0.5'}"></span>
  </button>
{/snippet}

<div data-testid="failover-card" class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 space-y-4">
  <div class="flex items-start justify-between gap-4">
    <div>
      <p class="text-sm font-semibold text-black-900 dark:text-white-100">Auto failover</p>
      <p class="mt-1 text-[11px] text-black-700 dark:text-black-600 leading-relaxed">
        When this provider hits a usage limit, loses its login or is unavailable, move the session to the next target below and continue the turn there.
      </p>
    </div>
    {@render switchBtn(cfg.enabled, "Auto failover", "failover-toggle", () => (cfg.enabled = !cfg.enabled))}
  </div>

  <!-- Health of this instance's account, with the manual reset. -->
  <div data-testid="failover-health" class="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-white-300 dark:border-navy-600 px-3 py-2">
    {#if status}
      <div class="text-xs text-black-900 dark:text-white-100">
        <span class="font-medium">Status:</span>
        <span data-testid="failover-health-label" class="ml-1 rounded px-1.5 py-0.5 font-medium {healthBadgeClass(health)}">{status}</span>
        {#if health?.Since}<span class="ml-1 text-black-700 dark:text-black-600">since {clockLabel(health.Since)}</span>{/if}
        {#if health?.Reason}<p class="mt-1 text-[11px] text-black-700 dark:text-black-600 break-words">{health.Reason}</p>{/if}
      </div>
      {#if canEdit}
        <Button variant="secondary" size="sm" disabled={resetting} onclick={resetHealth} testid="failover-reset">Reset status</Button>
      {/if}
    {:else}
      <span class="text-xs text-black-900 dark:text-white-100"><span class="font-medium">Status:</span> <span class="text-green-600 dark:text-green-400">Healthy</span></span>
    {/if}
  </div>

  {#if !loaded}
    <p class="text-xs text-black-700 dark:text-black-600">Loading…</p>
  {:else if loadError}
    <p data-testid="failover-load-error" class="text-xs text-red-600 dark:text-red-400">{loadError}</p>
  {:else}
    {#if !canEdit}
      <p class="text-[11px] text-black-700 dark:text-black-600">You can see this failover setup but not change it.</p>
    {/if}

    <div>
      <p class="text-xs font-medium text-black-900 dark:text-white-100 mb-1.5">Fail over on</p>
      <div class="flex flex-wrap gap-4">
        {#each FAILOVER_TRIGGERS as tr (tr.key)}
          <label class="inline-flex items-center gap-1.5 text-xs text-black-900 dark:text-white-100">
            <input
              type="checkbox"
              data-testid={`failover-trigger-${tr.key}`}
              checked={cfg.triggers.includes(tr.key)}
              disabled={!canEdit}
              onchange={(e) => toggleTrigger(tr.key, (e.currentTarget as HTMLInputElement).checked)}
            />
            {tr.label}
          </label>
        {/each}
      </div>
    </div>

    <div class="space-y-2">
      <div class="flex items-center justify-between">
        <p class="text-xs font-medium text-black-900 dark:text-white-100">Targets, tried in order</p>
        <span class="text-[11px] text-black-700 dark:text-black-600">{cfg.chain.length} / {MAX_FAILOVER_TARGETS}</span>
      </div>
      {#if cfg.chain.length === 0}
        <p class="text-[11px] text-black-700 dark:text-black-600">No targets yet. Add up to {MAX_FAILOVER_TARGETS} providers to move to.</p>
      {/if}
      {#each cfg.chain as t, i (rowIds[i] ?? `row-${i}`)}
        {@const note = crossTypeNote(type, t.instance)}
        <div data-testid="failover-target" class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-200/40 dark:bg-navy-800 p-2.5 space-y-1.5">
          <div class="flex items-center gap-2">
            <span class="w-5 text-[11px] font-semibold text-black-700 dark:text-black-600">{i + 1}.</span>
            <div class="min-w-0 flex-1" data-testid={`failover-target-pick-${i}`} data-value={pickValue(t)}>
              <!-- ProviderPicker has no disabled state: a viewer gets the
                   target as text rather than a picker they could change. -->
              {#if canEdit}
                <ProviderPicker
                  options={buildProviderOptions(pickable, pickValue(t))}
                  value={pickValue(t)}
                  onChange={(v) => setTarget(i, v)}
                  placeholder="Pick a provider"
                  {loadModels}
                />
              {:else}
                <span data-testid={`failover-target-text-${i}`} class="block truncate font-mono text-xs text-black-900 dark:text-white-100" title={pickValue(t)}>
                  {t.instance}{#if t.model_id}<span class="text-black-700 dark:text-black-600"> · {t.model_id}</span>{/if}
                </span>
              {/if}
            </div>
            <button type="button" class={iconBtn} aria-label="Move up" data-testid={`failover-up-${i}`} disabled={!canEdit || i === 0} onclick={() => moveTarget(i, -1)}>↑</button>
            <button type="button" class={iconBtn} aria-label="Move down" data-testid={`failover-down-${i}`} disabled={!canEdit || i === cfg.chain.length - 1} onclick={() => moveTarget(i, 1)}>↓</button>
            <button type="button" class={iconBtn} aria-label="Remove" data-testid={`failover-remove-${i}`} disabled={!canEdit} onclick={() => removeTarget(i)}>✕</button>
          </div>
          {#if !t.instance}
            <p class="pl-7 text-[11px] text-black-700 dark:text-black-600">Pick a provider and model.</p>
          {/if}
          {#if problems[i]}
            <p data-testid={`failover-problem-${i}`} class="pl-7 text-[11px] text-red-600 dark:text-red-400">{problems[i]}</p>
          {/if}
          {#if note}
            <p data-testid={`failover-crosstype-${i}`} class="ml-7 rounded-md border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-900/30 px-2 py-1 text-[11px] text-amber-700 dark:text-amber-300">{note}</p>
          {/if}
          {#each warningsFor(t.instance) as w}
            <p data-testid={`failover-warning-${i}`} class="pl-7 text-[11px] text-amber-600 dark:text-amber-400">{w}</p>
          {/each}
        </div>
      {/each}
      {#if canEdit}
        <button
          type="button"
          data-testid="failover-add"
          disabled={cfg.chain.length >= MAX_FAILOVER_TARGETS}
          onclick={addTarget}
          class="text-[11px] text-green-600 hover:underline disabled:opacity-40 disabled:no-underline disabled:cursor-not-allowed"
        >+ Add target</button>
      {/if}
    </div>

    <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-4">
      <label class="block">
        <span class="block text-xs font-medium text-black-900 dark:text-white-100 mb-1.5">Recheck every N min</span>
        <input
          type="number"
          min={MIN_RECHECK_MINUTES}
          max={MAX_RECHECK_MINUTES}
          aria-label="Recheck every N min"
          data-testid="failover-recheck"
          disabled={!canEdit}
          value={recheckText}
          oninput={(e) => (recheckText = (e.currentTarget as HTMLInputElement).value)}
          class="{inputCls} w-24"
        />
        {#if !recheckValid}
          <span data-testid="failover-recheck-error" class="block mt-1 text-[11px] text-red-600 dark:text-red-400">Enter {MIN_RECHECK_MINUTES} to {MAX_RECHECK_MINUTES} minutes.</span>
        {:else}
          <span class="block mt-1 text-[11px] text-black-700 dark:text-black-600">How often a limited provider is checked again.</span>
        {/if}
      </label>
      <div class="flex items-start justify-between gap-3">
        <div>
          <span class="block text-xs font-medium text-black-900 dark:text-white-100">Return to this provider when it recovers</span>
          <span class="block mt-1 text-[11px] text-black-700 dark:text-black-600">The session moves back at the next message.</span>
        </div>
        {@render switchBtn(cfg.return_home, "Return to this provider when it recovers", "failover-return-home", () => (cfg.return_home = !cfg.return_home))}
      </div>
    </div>

    {#if errors.length > 0}
      <ul data-testid="failover-errors" class="rounded-md border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-900/30 px-3 py-2 text-[11px] text-red-700 dark:text-red-300 list-disc pl-6 space-y-0.5">
        {#each errors as e}<li>{e}</li>{/each}
      </ul>
    {/if}

    {#if canEdit}
      <div class="flex justify-end">
        <Button size="sm" disabled={saving || invalid} onclick={save} testid="failover-save">{saving ? "Saving…" : "Save"}</Button>
      </div>
    {/if}
  {/if}
</div>
