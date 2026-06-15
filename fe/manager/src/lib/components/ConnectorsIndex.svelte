<script lang="ts">
  import { TextInput } from "@wick-fe/common-ui";
  import { listConnectors } from "$lib/api.js";
  import type { ConnectorDef } from "$lib/types.js";

  let connectors = $state<ConnectorDef[]>([]);
  let loading = $state(true);
  let error = $state("");

  let query = $state("");
  let activeCategory = $state("all");

  let categories = $derived(
    Array.from(new Set(connectors.map((c) => c.category).filter(Boolean))).sort(),
  );

  let filtered = $derived.by(() => {
    const q = query.toLowerCase().trim();
    return connectors.filter((c) => {
      const matchText =
        !q ||
        c.name.toLowerCase().includes(q) ||
        c.key.toLowerCase().includes(q) ||
        c.category.toLowerCase().includes(q);
      const matchCat = activeCategory === "all" || c.category === activeCategory;
      return matchText && matchCat;
    });
  });

  async function load() {
    loading = true;
    error = "";
    try {
      connectors = await listConnectors();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  function chipClass(active: boolean): string {
    return active
      ? "border-green-500 bg-green-500 text-white-100"
      : "border-white-400 dark:border-navy-600 text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700";
  }

  $effect(() => { load(); });
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between gap-4">
    <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Connectors</h1>
  </div>

  {#if loading}
    <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div
      class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400"
    >{error}</div>
  {:else}
    <div class="flex flex-col gap-3">
      <div class="max-w-sm">
        <TextInput
          type="search"
          value={query}
          onChange={(v) => (query = v)}
          placeholder="Search connectors…"
          ariaLabel="Search connectors"
        />
      </div>
      {#if categories.length > 0}
        <div class="flex flex-wrap gap-2">
          <button
            type="button"
            class="rounded-full border px-3 py-1 text-xs font-medium transition-colors {chipClass(activeCategory === 'all')}"
            onclick={() => (activeCategory = "all")}
          >All</button>
          {#each categories as cat (cat)}
            <button
              type="button"
              class="rounded-full border px-3 py-1 text-xs font-medium transition-colors {chipClass(activeCategory === cat)}"
              onclick={() => (activeCategory = cat)}
            >{cat}</button>
          {/each}
        </div>
      {/if}
    </div>

    {#if filtered.length === 0}
      <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">No connectors match your filters.</div>
    {:else}
      <div
        class="grid gap-4"
        style="grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));"
      >
        {#each filtered as conn (conn.key)}
          <a
            href={`/manager/connectors/${encodeURIComponent(conn.key)}`}
            data-conn-card
            class="flex items-start gap-3 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4 shadow-sm transition-colors hover:border-green-500 hover:shadow-md"
          >
            <span class="text-2xl leading-none flex-shrink-0" aria-hidden="true">{conn.icon || "🔌"}</span>
            <span class="min-w-0">
              <span class="block truncate text-sm font-medium text-black-900 dark:text-white-100">{conn.name}</span>
              {#if conn.category}
                <span class="block truncate text-xs text-black-700 dark:text-black-600">{conn.category}</span>
              {/if}
              <span class="mt-1 flex flex-wrap gap-1">
                {#if conn.custom}
                  <span class="rounded bg-blue-100 dark:bg-blue-900/40 px-1.5 py-0.5 text-[10px] font-medium text-blue-700 dark:text-blue-300">Custom</span>
                {/if}
                {#if conn.disabled}
                  <span class="rounded bg-slate-100 dark:bg-navy-600 px-1.5 py-0.5 text-[10px] font-medium text-slate-600 dark:text-slate-300">Disabled</span>
                {/if}
              </span>
            </span>
          </a>
        {/each}
      </div>
    {/if}
  {/if}
</div>
