<script lang="ts">
  import { Button } from "@wick-fe/common-ui";
  import { listConnectors, listPlugins, installPlugin } from "$lib/api.js";
  import { push } from "$lib/router.js";
  import type { ConnectorDef, PluginEntry } from "$lib/types.js";

  // A grid card is either a built-in/installed connector (ConnectorDef) or a
  // not-yet-downloaded plugin from the catalog (PluginEntry). The `available`
  // flag tells the template which one it is — available cards render a Download
  // button instead of linking to the connector detail page. There is NO separate
  // "Available to install" section: available plugins flow into the same
  // category grid as everything else (their category comes from DefaultTags).
  type Card =
    | (ConnectorDef & { available?: false })
    | (PluginEntry & { available: true; category: string; category_desc: string });

  type Group = {
    name: string;
    description: string;
    cards: Card[];
  };

  // Pseudo-filter (not a category): "Installed" shows everything ready to use —
  // built-in connectors plus already-downloaded plugins — excluding only
  // not-yet-downloaded (available) plugins.
  const INSTALLED_FILTER = "Installed";

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

  // Available plugins become cards grouped under their manifest-derived category
  // (from DefaultTags, exactly like built-ins); no real category → "Other".
  let availableCards = $derived.by<Card[]>(() =>
    available.map((p) => ({
      ...p,
      available: true as const,
      category: p.category || "Other",
      category_desc: "",
    })),
  );

  // All cards (built-in/installed connectors + available plugins) before filter.
  let allCards = $derived.by<Card[]>(() => [
    ...connectors.map((c) => ({ ...c, available: false as const })),
    ...availableCards,
  ]);

  // A card is "installed" (ready to use) unless it's an available plugin.
  function isInstalled(c: Card): boolean {
    return !c.available;
  }

  // Category chips, alphabetical. "All" and "Installed" are rendered separately
  // as the first two chips (they're filters, not categories).
  let categories = $derived(
    Array.from(new Set(allCards.map((c) => c.category).filter(Boolean))).sort((a, b) =>
      a.localeCompare(b),
    ),
  );

  let filtered = $derived.by(() => {
    const q = query.toLowerCase().trim();
    return allCards.filter((c) => {
      const matchText =
        !q ||
        c.name.toLowerCase().includes(q) ||
        c.key.toLowerCase().includes(q) ||
        c.category.toLowerCase().includes(q) ||
        c.description.toLowerCase().includes(q);
      const matchCat =
        activeCategory === "all" ||
        (activeCategory === INSTALLED_FILTER ? isInstalled(c) : c.category === activeCategory);
      return matchText && matchCat;
    });
  });

  /* Always grouped into section cards (white background), whatever the active
     filter — All, Installed, a category chip, or a search. The chip/search only
     changes WHICH cards are in `filtered`; the layout stays the same so the page
     looks consistent. "Other" (no category) is handled separately below. */
  let groups = $derived.by<Group[]>(() => {
    const byName = new Map<string, Group>();
    const order: string[] = [];
    for (const c of filtered) {
      const name = c.category || "Other";
      if (name === "Other") continue; // handled by otherCards below
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

  // Uncategorized cards — rendered in their own "Other" section card.
  let otherCards = $derived(filtered.filter((c) => (c.category || "Other") === "Other"));

  // True when the filter leaves exactly ONE section on screen (one category and
  // no Other, or only Other). That section spans full width, so lay its cards
  // out multi-column instead of a single tall column.
  let soleSection = $derived(
    (groups.length === 1 && otherCards.length === 0) ||
      (groups.length === 0 && otherCards.length > 0),
  );

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
    // Available plugins are best-effort: a marketplace fetch failure must not
    // break the connector list. Loaded separately so a slow/blocked catalog
    // doesn't gate the page.
    loadAvailable();
  }

  let available = $state<PluginEntry[]>([]);
  let installing = $state<string>("");
  let availableError = $state("");

  async function loadAvailable() {
    availableError = "";
    try {
      const r = await listPlugins();
      // Show ALL available plugins, including ones with no build for this
      // server's OS/arch — those render with a disabled Download + a reason,
      // so the user knows the plugin exists rather than seeing an empty list.
      available = r.available ?? [];
      if (r.registry_error) availableError = r.registry_error;
    } catch (e) {
      availableError = e instanceof Error ? e.message : String(e);
    }
  }

  async function onInstall(p: PluginEntry) {
    installing = p.key;
    try {
      await installPlugin(p.key);
      // The reloader registers it within ~5s; refresh both lists so it moves
      // from Available into the connector grid.
      await load();
    } catch (e) {
      availableError = e instanceof Error ? e.message : String(e);
    } finally {
      installing = "";
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

<div class="space-y-6 pb-8">
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
        <input
          type="search"
          value={query}
          oninput={(e) => (query = (e.target as HTMLInputElement).value)}
          placeholder="Search connectors…"
          aria-label="Search connectors"
          class="min-w-0 flex-1 border-0 bg-transparent py-2 text-sm text-black-900 dark:text-white-100 placeholder-black-700 dark:placeholder-black-600 outline-none focus:ring-0"
        />
        <kbd class="hidden items-center rounded border border-white-400 dark:border-navy-600 px-2 py-0.5 font-mono text-xs text-black-700 dark:text-black-600 sm:inline-flex">/</kbd>
      </div>
      {#if categories.length > 0}
        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="rounded-full border px-3 py-1 text-xs font-medium transition-colors {chipClass(activeCategory === 'all')}"
            onclick={() => (activeCategory = "all")}
          >All</button>
          <button
            type="button"
            class="rounded-full border px-3 py-1 text-xs font-medium transition-colors {chipClass(activeCategory === INSTALLED_FILTER)}"
            onclick={() => (activeCategory = INSTALLED_FILTER)}
          >Installed</button>
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

    <!-- One card, used by both layouts: a Download tile for an available plugin,
         else a link to the connector detail page. -->
    {#snippet connectorCard(card: Card)}
      {#if card.available}
        <div
          data-plugin-card
          class="flex h-full flex-col gap-3 rounded-xl border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4 shadow-sm"
        >
          <div class="flex items-start gap-3">
            <span class="relative flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-lg bg-green-200 dark:bg-green-800 text-lg font-semibold text-green-700 dark:text-green-300">
              🔌
            </span>
            <span class="min-w-0 flex-1">
              <span class="flex items-center gap-2">
                <span class="truncate text-sm font-semibold text-black-900 dark:text-white-100">{card.name}</span>
                <span class="flex-shrink-0 rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-800 dark:text-black-600">
                  Plugin · v{card.version}
                </span>
              </span>
              {#if card.description}
                <span class="mt-1 block text-xs leading-relaxed text-black-700 dark:text-black-600">{card.description}</span>
              {/if}
              {#if !card.arch_ok}
                <span class="mt-2 block text-xs font-medium text-cau-400">
                  No build for {card.host ?? "this platform"}{#if card.os_arch?.length} · available: {card.os_arch.join(", ")}{/if}
                </span>
              {/if}
            </span>
          </div>
          <Button
            variant="primary"
            disabled={installing === card.key || !card.arch_ok}
            onclick={() => onInstall(card)}
          >
            {installing === card.key ? "Downloading…" : !card.arch_ok ? "Unavailable for your OS" : "Download"}
          </Button>
        </div>
      {:else}
        <a
          href={cardHref(card.key)}
          data-conn-card
          onclick={(e) => openConnector(e, card.key)}
          class="flex h-full items-start gap-3 rounded-xl border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4 shadow-sm transition-all duration-150 hover:-translate-y-px hover:border-green-400 hover:shadow-md"
        >
          <span class="relative flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-lg bg-green-200 dark:bg-green-800 text-lg font-semibold text-green-700 dark:text-green-300">
            {#if card.icon && (card.icon.startsWith("data:image/") || card.icon.startsWith("<svg"))}
              <img src={card.icon.startsWith("<svg") ? `data:image/svg+xml;base64,${btoa(unescape(encodeURIComponent(card.icon)))}` : card.icon} class="h-7 w-7 object-contain" alt="" />
            {:else}
              {card.icon || "🔌"}
            {/if}
            <span class="absolute -right-1 -bottom-1 flex h-4 w-4 items-center justify-center rounded-full bg-green-200 dark:bg-green-800 text-[8px] shadow-sm" aria-label="LLM connector">🔌</span>
          </span>
          <span class="min-w-0 flex-1">
            <span class="flex items-center gap-2">
              <span class="truncate text-sm font-semibold text-black-900 dark:text-white-100">{card.name}</span>
              {#if card.custom}
                <span class="flex-shrink-0 rounded-full bg-green-200 dark:bg-green-800 px-2 py-0.5 text-[10px] font-medium text-green-700 dark:text-green-300">{card.custom_source ? `Custom · ${card.custom_source}` : "Custom"}</span>
              {/if}
              {#if card.system}
                <span class="flex-shrink-0 rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-800 dark:text-black-600">System</span>
              {/if}
            </span>
            {#if card.description}
              <span class="mt-1 block text-xs leading-relaxed text-black-700 dark:text-black-600">{card.description}</span>
            {/if}
            <span class="mt-2 block text-xs text-black-700 dark:text-black-600">
              {statsLine(card)}
              {#if card.needs_setup_count > 0}
                <span class="font-medium text-cau-400"> · {card.needs_setup_count} needs setup</span>
              {/if}
              {#if card.disabled_count > 0}
                <span class="font-medium text-neg-400"> · {card.disabled_count} disabled</span>
              {/if}
              {#if card.needs_reload}
                <span class="font-medium text-cau-400"> · needs reload</span>
              {/if}
            </span>
          </span>
        </a>
      {/if}
    {/snippet}

    {#if filtered.length === 0}
      <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">No connectors match your filters.</div>
    {:else}
      <!-- One section card per real category (white background). With several
           sections they sit in a 3-column grid, each narrow, stacking their cards
           1-per-row inside. When the filter leaves a SINGLE section the card
           shrinks to fit its contents (w-fit) and lays its cards out in
           fixed-width columns — so 2 cards span 2 columns, not a stretched row. -->
      {#if groups.length > 0}
        <div class="grid grid-cols-1 gap-4 {soleSection ? '' : 'md:grid-cols-2 xl:grid-cols-3'}">
          {#each groups as group (group.name)}
            <section
              data-group
              data-group-name={group.name}
              class="flex flex-col rounded-2xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 shadow-sm {soleSection ? 'w-fit max-w-full' : ''}"
            >
              <header class="mb-4">
                <h2 class="text-base font-semibold text-black-900 dark:text-white-100">{group.name}</h2>
                {#if group.description}
                  <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">{group.description}</p>
                {/if}
              </header>
              {#if soleSection}
                <!-- Fixed-width cards that wrap. Section is w-fit, so it hugs the
                     cards: 2 cards → 2 columns wide, no empty space on the right. -->
                <div class="flex flex-wrap gap-3">
                  {#each group.cards as card (card.key)}
                    <div class="w-72 max-w-full">{@render connectorCard(card)}</div>
                  {/each}
                </div>
              {:else}
                <div class="grid grid-cols-1 gap-3">
                  {#each group.cards as card (card.key)}
                    {@render connectorCard(card)}
                  {/each}
                </div>
              {/if}
            </section>
          {/each}
        </div>
      {/if}
      <!-- "Other" (uncategorized) renders LAST, still inside a section card like
           the categories — but its cards lay out in a multi-column grid instead
           of stacking one-per-row (it usually holds the most cards). -->
      {#if otherCards.length > 0}
        <section
          data-group
          data-group-name="Other"
          class="flex flex-col rounded-2xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 shadow-sm {groups.length > 0 ? 'mt-4' : ''} {soleSection ? 'w-fit max-w-full' : ''}"
        >
          <header class="mb-4">
            <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Other</h2>
          </header>
          {#if soleSection}
            <div class="flex flex-wrap gap-3">
              {#each otherCards as card (card.key)}
                <div class="w-72 max-w-full">{@render connectorCard(card)}</div>
              {/each}
            </div>
          {:else}
            <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {#each otherCards as card (card.key)}
                {@render connectorCard(card)}
              {/each}
            </div>
          {/if}
        </section>
      {/if}
    {/if}

    <!-- Available plugins render inside the category grid above, mixed with
         built-ins. Only the marketplace-fetch error needs a standalone notice. -->
    {#if availableError}
      <div class="mt-6 rounded-2xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-5 py-4 shadow-sm">
        <p class="text-xs leading-relaxed text-cau-400">
          Marketplace unavailable — {availableError}
        </p>
      </div>
    {/if}
  {/if}
</div>
