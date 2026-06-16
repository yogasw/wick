<script lang="ts">
  import { Button, TextInput } from "@wick-fe/common-ui";
  import { listConnectors } from "$lib/api.js";
  import { push } from "$lib/router.js";
  import type { ConnectorDef } from "$lib/types.js";

  type Group = {
    name: string;
    description: string;
    cards: ConnectorDef[];
  };

  let connectors = $state<ConnectorDef[]>([]);
  let loading = $state(true);
  let error = $state("");

  let query = $state("");
  let activeCategory = $state("all");
  let newMenuOpen = $state(false);

  function go(path: string) {
    newMenuOpen = false;
    push(path);
  }

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
        c.category.toLowerCase().includes(q) ||
        c.description.toLowerCase().includes(q);
      const matchCat = activeCategory === "all" || c.category === activeCategory;
      return matchText && matchCat;
    });
  });

  /* Group the filtered set by category, preserving the server's order (the
     endpoint already sorts by category sort-order then name). The category
     subtitle is carried on each card as category_desc. */
  let groups = $derived.by<Group[]>(() => {
    const byName = new Map<string, Group>();
    const order: string[] = [];
    for (const c of filtered) {
      const name = c.category || "Other";
      let g = byName.get(name);
      if (!g) {
        g = { name, description: c.category_desc, cards: [] };
        byName.set(name, g);
        order.push(name);
      }
      g.cards.push(c);
    }
    return order.map((n) => byName.get(n)!);
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

  const appBase = document.getElementById("app")?.dataset.base ?? "";

  function cardHref(key: string): string {
    return `${appBase}/connectors/${encodeURIComponent(key)}`;
  }

  function openConnector(e: MouseEvent, key: string) {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
    e.preventDefault();
    push(`/connectors/${encodeURIComponent(key)}`);
  }

  function chipClass(active: boolean): string {
    return active
      ? "border-green-500 bg-green-500 text-white-100"
      : "border-white-400 dark:border-navy-600 text-black-800 dark:text-black-600 hover:border-green-400";
  }

  /* Mirrors the templ card stats line: MCP catalogs sync live so a zero op
     count means "not synced yet", not "no tools" — don't print a misleading
     count for them. */
  function statsLine(c: ConnectorDef): string {
    const base =
      c.custom && c.custom_source === "MCP" && c.op_count === 0
        ? `tools sync live · ${c.active_count} active`
        : `${c.op_count} operation(s) · ${c.active_count} active`;
    return base;
  }

  $effect(() => { load(); });

  function focusSearchOnSlash(e: KeyboardEvent): void {
    if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey) return;
    const el = document.activeElement;
    if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) return;
    const search = document.querySelector<HTMLInputElement>("input[aria-label='Search connectors']");
    if (search) { e.preventDefault(); search.focus(); }
  }
  $effect(() => {
    window.addEventListener("keydown", focusSearchOnSlash);
    return () => window.removeEventListener("keydown", focusSearchOnSlash);
  });
</script>

<div class="space-y-6">
  <div class="flex items-start justify-between gap-4">
    <div class="flex items-center gap-3">
      <div class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-lg bg-green-200 dark:bg-green-800 text-lg font-semibold text-green-700 dark:text-green-300" aria-hidden="true">🔌</div>
      <div>
        <h1 class="text-[1.375rem] font-semibold text-black-900 dark:text-white-100">Connectors</h1>
        <p class="mt-0.5 text-sm text-black-800 dark:text-black-600">
          LLM-callable connectors that wrap external APIs. Pick one to manage its instances and operations.
        </p>
      </div>
    </div>
    <div class="relative flex-shrink-0">
      <Button size="lg" onclick={() => (newMenuOpen = !newMenuOpen)}>＋ New connector</Button>
      {#if newMenuOpen}
        <div class="absolute right-0 z-20 mt-2 w-80 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-2 shadow-lg">
          <button type="button" class="flex w-full items-start gap-3 rounded-lg px-3 py-2 text-left hover:bg-white-200 dark:hover:bg-navy-800" onclick={() => go("/custom/paste")}>
            <span class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-green-200 dark:bg-green-800 text-lg text-green-700 dark:text-green-300" aria-hidden="true">📋</span>
            <span class="min-w-0">
              <span class="block text-sm font-medium text-black-900 dark:text-white-100">From paste</span>
              <span class="block text-xs text-black-700 dark:text-black-600">Paste a cURL (or anything, via AI) — wick extracts the fields</span>
            </span>
          </button>
          <button type="button" class="flex w-full items-start gap-3 rounded-lg px-3 py-2 text-left hover:bg-white-200 dark:hover:bg-navy-800" onclick={() => go("/custom/mcp")}>
            <span class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-green-200 dark:bg-green-800 text-lg text-green-700 dark:text-green-300" aria-hidden="true">🔌</span>
            <span class="min-w-0">
              <span class="block text-sm font-medium text-black-900 dark:text-white-100">From MCP server</span>
              <span class="block text-xs text-black-700 dark:text-black-600">HTTP MCP — pick tools to import</span>
            </span>
          </button>
          <button type="button" class="flex w-full items-start gap-3 rounded-lg px-3 py-2 text-left hover:bg-white-200 dark:hover:bg-navy-800" onclick={() => go("/custom/manual")}>
            <span class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-green-200 dark:bg-green-800 text-lg text-green-700 dark:text-green-300" aria-hidden="true">✎</span>
            <span class="min-w-0">
              <span class="block text-sm font-medium text-black-900 dark:text-white-100">Blank / manual</span>
              <span class="block text-xs text-black-700 dark:text-black-600">Build Meta + Configs + Operations by hand</span>
            </span>
          </button>
        </div>
      {/if}
    </div>
  </div>

  {#if loading}
    <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div
      class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400"
    >{error}</div>
  {:else}
    <div class="flex flex-col gap-3">
      <div class="flex max-w-lg items-center gap-3 rounded-xl border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 focus-within:border-green-500">
        <svg class="h-5 w-5 flex-shrink-0 text-black-700" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24" aria-hidden="true">
          <circle cx="11" cy="11" r="8"></circle><path d="m21 21-4.35-4.35"></path>
        </svg>
        <div class="min-w-0 flex-1">
          <TextInput type="search" value={query} onChange={(v) => (query = v)} placeholder="Search connectors…" ariaLabel="Search connectors" />
        </div>
        <kbd class="hidden items-center rounded border border-white-400 dark:border-navy-600 px-2 py-0.5 font-mono text-xs text-black-700 dark:text-black-600 sm:inline-flex">/</kbd>
      </div>
      {#if categories.length > 0}
        <div class="flex flex-wrap items-center gap-2">
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

    {#if groups.length === 0}
      <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">No connectors match your filters.</div>
    {:else}
      <div class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        {#each groups as group (group.name)}
          <section
            data-group
            data-group-name={group.name}
            class="flex flex-col rounded-2xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 shadow-sm"
          >
            <header class="mb-4">
              <h2 class="text-base font-semibold text-black-900 dark:text-white-100">{group.name}</h2>
              {#if group.description}
                <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">{group.description}</p>
              {/if}
            </header>
            <div class="grid grid-cols-1 gap-3">
              {#each group.cards as conn (conn.key)}
                <a
                  href={cardHref(conn.key)}
                  data-conn-card
                  onclick={(e) => openConnector(e, conn.key)}
                  class="flex h-full items-start gap-3 rounded-xl border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4 shadow-sm transition-all duration-150 hover:-translate-y-px hover:border-green-400 hover:shadow-md"
                >
                  <span class="relative flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-lg bg-green-200 dark:bg-green-800 text-lg font-semibold text-green-700 dark:text-green-300">
                    {#if conn.icon && (conn.icon.startsWith("data:image/") || conn.icon.startsWith("<svg"))}
                      <img src={conn.icon.startsWith("<svg") ? `data:image/svg+xml;base64,${btoa(unescape(encodeURIComponent(conn.icon)))}` : conn.icon} class="h-7 w-7 object-contain" alt="" />
                    {:else}
                      {conn.icon || "🔌"}
                    {/if}
                    <span class="absolute -right-1 -bottom-1 flex h-4 w-4 items-center justify-center rounded-full bg-green-200 dark:bg-green-800 text-[8px] shadow-sm" aria-label="LLM connector">🔌</span>
                  </span>
                  <span class="min-w-0 flex-1">
                    <span class="flex items-center gap-2">
                      <span class="truncate text-sm font-semibold text-black-900 dark:text-white-100">{conn.name}</span>
                      {#if conn.custom}
                        <span class="flex-shrink-0 rounded-full bg-green-200 dark:bg-green-800 px-2 py-0.5 text-[10px] font-medium text-green-700 dark:text-green-300">{conn.custom_source ? `Custom · ${conn.custom_source}` : "Custom"}</span>
                      {/if}
                      {#if conn.system}
                        <span class="flex-shrink-0 rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-800 dark:text-black-600">System</span>
                      {/if}
                    </span>
                    {#if conn.description}
                      <span class="mt-1 block text-xs leading-relaxed text-black-700 dark:text-black-600">{conn.description}</span>
                    {/if}
                    <span class="mt-2 block text-xs text-black-700 dark:text-black-600">
                      {statsLine(conn)}
                      {#if conn.needs_setup_count > 0}
                        <span class="font-medium text-cau-400"> · {conn.needs_setup_count} needs setup</span>
                      {/if}
                      {#if conn.disabled_count > 0}
                        <span class="font-medium text-neg-400"> · {conn.disabled_count} disabled</span>
                      {/if}
                      {#if conn.needs_reload}
                        <span class="font-medium text-cau-400"> · needs reload</span>
                      {/if}
                    </span>
                  </span>
                </a>
              {/each}
            </div>
          </section>
        {/each}
      </div>
    {/if}
  {/if}
</div>
