<script lang="ts">
  import { clock24 } from "@wick-fe/common-ui";
  // Live model list for an omp/opencode instance: the models its CLI lists
  // (`omp models` / `opencode models`), narrowed by a filter in the shared
  // grammar, with a pinned default. With the SAVED filter the list and the
  // default come from the composer picker's own endpoint
  // (apiGetEffectiveLiveModels → ModelSets → provider.EffectiveLiveModels:
  // refusals marked, a refused model never the default) — the way the wick
  // provider page reads its live sets. Only an UNSAVED filter is previewed
  // client-side over the raw CLI list, with the same grammar. Opening the
  // panel only READS what the server knows (omp/opencode files or a running
  // server — never a CLI run); "Refresh" is the one action that runs the
  // CLI. A refused model is marked "not available for this account" and
  // can be re-checked explicitly; Refresh does not clear it.
  import { onMount } from "svelte";
  import { Select, Button, matchModelFilter, MODEL_FILTER_HELP } from "@wick-fe/common-ui";
  import { apiGetCLIModels, apiGetEffectiveLiveModels, apiRecheckCLIModel, isOpencodeHostedModel, type CLIModel } from "$lib/api.js";

  interface Props {
    /* Reports how many models the CLI listed (the section summary). */
    onCount?: (n: number) => void;
    base: string;
    type: string;
    name: string;
    filter: string;
    pin: string;
    onSaveFilter: (v: string) => void;
    onSavePin: (v: string) => void;
  }
  let { base, type, name, filter, pin, onSaveFilter, onSavePin, onCount }: Props = $props();

  // How many preview rows render before "show more" — the list can be 100+.
  const PREVIEW_LIMIT = 50;

  let models = $state<CLIModel[]>([]);
  let offered = $state<CLIModel[]>([]);
  let serverDefault = $state("");
  // The saved filter `offered` was computed with (null: not fetched yet).
  let offeredFor = $state<string | null>(null);
  let hostedAllowed = $state(true);
  // Fresh CLI run in flight over a list already on screen.
  let updating = $state(false);
  let fetchedAt = $state("");
  let source = $state("");
  let loading = $state(false);
  let err = $state("");
  let draft = $state("");
  let search = $state("");
  let showAll = $state(false);

  $effect(() => { draft = filter; });

  async function fetchModels(refresh: boolean) {
    loading = true;
    updating = refresh && models.length > 0;
    err = "";
    try {
      const r = await apiGetCLIModels(base, type, name, refresh);
      models = r.models;
      // After a refresh the server cache is fresh, so this reads it too.
      await loadOffered();
      onCount?.(models.length);
      hostedAllowed = r.hostedAllowed;
      fetchedAt = r.fetchedAt;
      source = r.source ?? "";
      err = r.error ?? "";
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
      updating = false;
    }
  }
  // The server's effective list for the SAVED filter (refusals, default).
  async function loadOffered() {
    const f = filter;
    try {
      offered = (await apiGetEffectiveLiveModels(base, type, name)) ?? [];
    } catch {
      offered = [];
    }
    serverDefault = offered.find((m) => m.default && !m.unavailable)?.id ?? "";
    offeredFor = f;
  }
  // Opening reads what the server already knows (its saved list, the
  // CLI's files, a running server) — no CLI run; Refresh is that.
  onMount(() => { void fetchModels(false); });
  // A saved filter changes the server's list: read it again, or the panel
  // shows the previous filter's rows as if they were this one's.
  $effect(() => {
    if (offeredFor !== null && filter !== offeredFor) void loadOffered();
  });

  async function recheck(id: string) {
    try {
      await apiRecheckCLIModel(base, type, name, id);
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
      return;
    }
    await fetchModels(false);
  }

  const hay = (m: CLIModel) => `${m.id} ${m.desc ?? ""}`;
  // Hosted opencode models only count when the server says they may run.
  const eligible = $derived(models.filter((m) => type !== "opencode" || hostedAllowed || !isOpencodeHostedModel(m.id)));
  const hiddenHosted = $derived(models.length - eligible.length);
  const dirty = $derived(draft.trim() !== filter.trim());
  // Saved filter: the server's effective list. Unsaved: a client preview.
  const useServer = $derived(!dirty && offered.length > 0 && offeredFor === filter);
  const matched = $derived(useServer ? offered : eligible.filter((m) => matchModelFilter(hay(m), draft)));
  const usable = $derived(matched.filter((m) => !m.unavailable));
  const pinUnavailable = $derived(!!pin && matched.some((m) => m.id === pin && m.unavailable));
  const effectiveDefault = $derived(
    useServer && serverDefault ? serverDefault : (usable.some((m) => m.id === pin) ? pin : (usable[0]?.id ?? "")),
  );
  const searched = $derived(search.trim() ? matched.filter((m) => matchModelFilter(hay(m), search)) : matched);
  const shown = $derived(showAll ? searched : searched.slice(0, PREVIEW_LIMIT));
  // A refused model cannot be chosen as Default.
  const pinOptions = $derived([
    // What an empty pin runs: the server's pick (last worked, else first
    // usable) when it is known, else the first usable row.
    { label: "First match", value: "", description: (useServer && !pin && serverDefault ? serverDefault : usable[0]?.id) ?? "no model matches" },
    ...usable.map((m) => ({ label: m.id, value: m.id, description: m.desc })),
  ]);

  function saveFilter() { onSaveFilter(draft.trim()); }
</script>

<div class="space-y-3" data-testid="live-models-panel">
  <div>
    <label for="live-model-filter" class="text-xs font-medium text-black-900 dark:text-white-100">Filter</label>
    <div class="mt-1 flex items-center gap-2">
      <input
        id="live-model-filter"
        type="text"
        data-testid="live-models-filter"
        bind:value={draft}
        onkeydown={(e) => { if (e.key === "Enter") saveFilter(); }}
        placeholder="e.g. claude|gpt !mini — empty = all"
        class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-1.5 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800"
      />
      <Button size="sm" disabled={!dirty} onclick={saveFilter} testid="live-models-save-filter">Save</Button>
    </div>
    <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">{MODEL_FILTER_HELP}</p>
  </div>

  <div class="flex items-center justify-between gap-2 flex-wrap">
    <span class="text-xs text-black-800 dark:text-black-600" data-testid="live-models-count">
      {#if loading && models.length === 0}
        Loading models from the CLI…
      {:else}
        <strong class="text-black-900 dark:text-white-100">{matched.length}</strong> of {eligible.length} models match
        {#if hiddenHosted > 0}<span> · {hiddenHosted} hosted opencode models hidden (enable opencode_allow_hosted or log in to opencode Zen)</span>{/if}
      {/if}
    </span>
    <span class="flex items-center gap-2">
      {#if updating}<span class="text-[11px] text-black-700 dark:text-black-600" data-testid="live-models-updating">updating from the CLI…</span>
      {:else if fetchedAt}<span class="text-[11px] text-black-700 dark:text-black-600" data-testid="live-models-updated">updated {clock24(fetchedAt, true)}{source ? ` · ${source}` : ""}</span>
      {:else if !loading}<span class="text-[11px] text-amber-600 dark:text-amber-400" data-testid="live-models-empty">no model list yet — click Refresh</span>{/if}
      <Button size="sm" variant="secondary" disabled={loading} onclick={() => fetchModels(true)} testid="live-models-refresh">{loading ? "Refreshing…" : "Refresh"}</Button>
    </span>
  </div>
  {#if err}
    <p class="text-[11px] text-amber-600 dark:text-amber-400" data-testid="live-models-error">{err}</p>
  {/if}

  <div>
    <span class="text-xs font-medium text-black-900 dark:text-white-100">Default model</span>
    <div class="mt-1">
      <Select value={usable.some((m) => m.id === pin) ? pin : ""} options={pinOptions} onChange={(v) => onSavePin(v)} size="sm" searchable ariaLabel="Default model" />
    </div>
    {#if pinUnavailable}
      <p class="mt-1 text-[11px] text-amber-600 dark:text-amber-400" data-testid="live-models-pin-unavailable">Pinned {pin} is not available for this account — using {effectiveDefault || "nothing"}.</p>
    {:else if pin && !matched.some((m) => m.id === pin) && models.length > 0}
      <p class="mt-1 text-[11px] text-amber-600 dark:text-amber-400">Pinned {pin} is no longer in the filtered list — using {effectiveDefault || "nothing"}.</p>
    {/if}
  </div>

  {#if matched.length > 0}
    <div class="rounded-lg border border-white-300 dark:border-navy-600">
      <input
        type="text"
        bind:value={search}
        placeholder="Search the matched models…"
        aria-label="Search matched models"
        class="w-full border-b border-white-300 dark:border-navy-600 bg-transparent px-3 py-1.5 text-xs text-black-900 dark:text-white-100 outline-none"
      />
      <ul class="max-h-56 overflow-y-auto divide-y divide-white-300 dark:divide-navy-600" data-testid="live-models-list">
        {#each shown as m (m.id)}
          <li class="flex items-center justify-between gap-2 px-3 py-1 text-xs {m.unavailable ? 'opacity-60' : ''}" data-unavailable={m.unavailable ? "true" : undefined}>
            <span class="font-mono text-black-900 dark:text-white-100 truncate {m.unavailable ? 'line-through' : ''}">{m.id}</span>
            {#if m.unavailable}
              <span class="flex shrink-0 items-center gap-2">
                <span class="rounded bg-black-500/10 px-1.5 text-[10px] font-medium text-black-700 dark:text-black-600" title={m.reason || undefined}>not available for this account</span>
                <button type="button" class="text-[10px] text-green-600 dark:text-green-400 hover:underline" onclick={() => recheck(m.id)} data-testid="live-models-recheck">Re-check</button>
              </span>
            {:else if m.id === effectiveDefault}<span class="shrink-0 rounded bg-green-100 dark:bg-green-900 px-1.5 text-[10px] font-medium text-green-700 dark:text-green-300">default</span>{/if}
          </li>
        {/each}
      </ul>
      {#if searched.length > shown.length}
        <button type="button" class="w-full px-3 py-1 text-[11px] text-green-600 dark:text-green-400 hover:underline" onclick={() => (showAll = true)}>Show all {searched.length}</button>
      {/if}
    </div>
  {/if}
</div>
