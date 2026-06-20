<script lang="ts">
  /* Operations panel for the connector detail page. Operations are grouped
     into category cards (one card per category: title, description, count,
     per-card Enable/Disable all, and the ops table). A sticky right-hand
     "Sections" sidebar lists every category with its op count and jumps to
     the card on click. A global header carries the total count, a search box
     that filters ops across every category (cards with no match hide), and
     selection-aware bulk controls (Enable/Disable selected, falling back to
     Enable/Disable all). Each op row keeps its enable toggle (optimistic,
     reverts on failure), select checkbox, and Test + History deep-links.

     A connector with no categories (or only an untitled group) renders a
     single untitled card with no sidebar — the flat layout. */
  import { Button } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { push } from "$lib/router.js";
  import { toggleConnectorOperation, bulkToggleOperations } from "$lib/api.js";
  import type { ConnectorOp, ConnectorCategory } from "$lib/types.js";

  type Props = {
    operations: ConnectorOp[];
    categories?: ConnectorCategory[];
    connectorKey: string;
    connectorId: string;
    canConfigure: boolean;
  };
  let { operations, categories = [], connectorKey, connectorId, canConfigure }: Props = $props();

  let ops = $state<ConnectorOp[]>([]);
  let busy = $state<Record<string, boolean>>({});
  let bulkBusy = $state(false);
  let query = $state("");
  let selected = $state<Record<string, boolean>>({});
  let navOpen = $state(false);
  /* Per-card refine state, keyed by category title: a local search box and
     a page cursor. Cards paginate at PAGE_SIZE; the pager hides for cards
     whose visible op count fits one page. */
  let cardQuery = $state<Record<string, string>>({});
  let cardPage = $state<Record<string, number>>({});

  const PAGE_SIZE = 5;

  $effect(() => {
    ops = operations.map((o) => ({ ...o }));
  });

  function matches(o: ConnectorOp, q: string): boolean {
    return (
      o.name.toLowerCase().includes(q) ||
      o.key.toLowerCase().includes(q) ||
      o.description.toLowerCase().includes(q)
    );
  }

  const filtered = $derived.by(() => {
    const q = query.trim().toLowerCase();
    if (!q) return ops;
    return ops.filter((o) => matches(o, q));
  });

  const selectedKeys = $derived(ops.filter((o) => selected[o.key]).map((o) => o.key));

  /* One card per category title, in declaration order, then a trailing
     untitled group for ops whose category is empty. Built from the visible
     (filtered) ops so search hides empty cards automatically. The category
     title doubles as the slug used for the jump anchor. */
  type Group = { title: string; description: string; ops: ConnectorOp[] };
  const groups = $derived.by<Group[]>(() => {
    const descByTitle = new Map<string, string>();
    const order: string[] = [];
    for (const c of categories) {
      descByTitle.set(c.title, c.description);
      order.push(c.title);
    }
    const byTitle = new Map<string, ConnectorOp[]>();
    for (const o of filtered) {
      const t = o.category ?? "";
      let arr = byTitle.get(t);
      if (!arr) {
        arr = [];
        byTitle.set(t, arr);
        if (!order.includes(t)) order.push(t);
      }
      arr.push(o);
    }
    const out: Group[] = [];
    for (const t of order) {
      const groupOps = byTitle.get(t);
      if (!groupOps || groupOps.length === 0) continue;
      out.push({ title: t, description: descByTitle.get(t) ?? "", ops: groupOps });
    }
    return out;
  });

  /* pagedGroups layers the per-card search + pagination on top of groups.
     For each card: apply its local query, then slice to the active page.
     `total` is the post-local-search count (drives the pager + footer);
     `pageOps` is the visible slice. The card title keys the per-card state. */
  type PagedGroup = Group & { total: number; pageOps: ConnectorOp[]; page: number; pages: number; from: number; to: number };
  const pagedGroups = $derived.by<PagedGroup[]>(() => {
    return groups.map((g) => {
      const cq = (cardQuery[g.title] ?? "").trim().toLowerCase();
      const matched = cq ? g.ops.filter((o) => matches(o, cq)) : g.ops;
      const pages = Math.max(1, Math.ceil(matched.length / PAGE_SIZE));
      const page = Math.min(cardPage[g.title] ?? 1, pages);
      const start = (page - 1) * PAGE_SIZE;
      const end = Math.min(start + PAGE_SIZE, matched.length);
      return {
        ...g,
        total: matched.length,
        pageOps: matched.slice(start, end),
        page,
        pages,
        from: matched.length ? start + 1 : 0,
        to: end,
      };
    });
  });

  function setCardQuery(title: string, v: string): void {
    cardQuery = { ...cardQuery, [title]: v };
    cardPage = { ...cardPage, [title]: 1 };
  }

  function cardPrev(title: string, page: number): void {
    if (page > 1) cardPage = { ...cardPage, [title]: page - 1 };
  }

  function cardNext(title: string, page: number, pages: number): void {
    if (page < pages) cardPage = { ...cardPage, [title]: page + 1 };
  }

  /* Whether the connector is genuinely categorized (any titled category).
     Drives the sidebar + per-card headers; an untitled-only connector
     renders flat. */
  const categorized = $derived(ops.some((o) => o.category));

  function cardId(title: string): string {
    return "ops-cat-" + title.toLowerCase().replace(/[^a-z0-9]+/g, "-");
  }

  function toggleRow(opKey: string): void {
    selected = { ...selected, [opKey]: !selected[opKey] };
  }

  function toggleGroupAll(g: Group, checked: boolean): void {
    const next = { ...selected };
    for (const o of g.ops) next[o.key] = checked;
    selected = next;
  }

  function jumpTo(title: string): void {
    document.getElementById(cardId(title))?.scrollIntoView({ behavior: "smooth", block: "start" });
    navOpen = false;
  }

  function testOp(opKey: string): void {
    push(`/connectors/${encodeURIComponent(connectorKey)}/${encodeURIComponent(connectorId)}/test?op=${encodeURIComponent(opKey)}`);
  }

  function historyOp(opKey: string): void {
    push(`/connectors/${encodeURIComponent(connectorKey)}/${encodeURIComponent(connectorId)}/history?op=${encodeURIComponent(opKey)}`);
  }

  async function toggleEnabled(op: ConnectorOp): Promise<void> {
    if (busy[op.key]) return;
    const next = !op.enabled;
    busy = { ...busy, [op.key]: true };
    ops = ops.map((o) => (o.key === op.key ? { ...o, enabled: next, system_disabled: next ? false : o.system_disabled } : o));
    try {
      const saved = await toggleConnectorOperation(connectorKey, connectorId, op.key, next);
      ops = ops.map((o) => (o.key === op.key ? { ...o, enabled: saved } : o));
    } catch (e) {
      ops = ops.map((o) => (o.key === op.key ? { ...o, enabled: !next } : o));
      toastError("Toggle failed", e instanceof Error ? e.message : String(e));
    } finally {
      busy = { ...busy, [op.key]: false };
    }
  }

  /* bulkSet drives every Enable/Disable button. keys === undefined means
     "everything" (global Enable/Disable all). An explicit (possibly empty)
     list scopes the change — a per-card all-button passes that card's keys,
     the selection bar passes the selected keys. */
  async function bulkSet(enabled: boolean, keys?: string[]): Promise<void> {
    if (bulkBusy) return;
    bulkBusy = true;
    try {
      const scope = keys ?? [];
      await bulkToggleOperations(connectorKey, connectorId, enabled, scope);
      const target = new Set(scope);
      ops = ops.map((o) =>
        scope.length === 0 || target.has(o.key)
          ? { ...o, enabled, system_disabled: enabled ? false : o.system_disabled }
          : o,
      );
      const n = scope.length === 0 ? ops.length : scope.length;
      const noun = `${n} operation${n === 1 ? "" : "s"}`;
      toastOk(enabled ? `Enabled ${noun}` : `Disabled ${noun}`);
      selected = {};
    } catch (e) {
      toastError("Bulk update failed", e instanceof Error ? e.message : String(e));
    } finally {
      bulkBusy = false;
    }
  }
</script>

<section class="mt-8">
  {#if ops.length === 0}
    <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Operations</h2>
    <p class="mt-1 text-sm text-black-700 dark:text-black-600">This connector exposes no operations.</p>
  {:else}
    <!-- Global header: title, total count, search, bulk -->
    <div class="flex flex-wrap items-center gap-2">
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100 mr-1">Operations</h2>
      <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[11px] font-medium text-black-700 dark:text-black-600">{ops.length}</span>
      <input
        type="search"
        aria-label="Search operations"
        placeholder="Search…"
        bind:value={query}
        class="ml-2 w-40 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-1.5 text-xs text-black-900 dark:text-white-100 outline-none focus:border-green-500"
      />
      {#if canConfigure}
        {#if selectedKeys.length > 0}
          <div class="ml-auto flex items-center gap-2">
            <span class="text-xs text-black-700 dark:text-black-600">{selectedKeys.length} selected</span>
            <button type="button" disabled={bulkBusy} class="rounded-lg border border-pos-300 px-3 py-1.5 text-xs font-medium text-pos-400 hover:bg-pos-100 disabled:opacity-50" onclick={() => bulkSet(true, selectedKeys)}>Enable selected</button>
            <button type="button" disabled={bulkBusy} class="rounded-lg border border-neg-300 px-3 py-1.5 text-xs font-medium text-neg-400 hover:bg-neg-100 disabled:opacity-50" onclick={() => bulkSet(false, selectedKeys)}>Disable selected</button>
          </div>
        {:else}
          <div class="ml-auto flex items-center gap-2">
            <Button variant="secondary" size="sm" disabled={bulkBusy} onclick={() => bulkSet(true)}>Enable all</Button>
            <Button variant="secondary" size="sm" disabled={bulkBusy} onclick={() => bulkSet(false)}>Disable all</Button>
          </div>
        {/if}
      {/if}
    </div>

    {#if filtered.length === 0}
      <p class="mt-4 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-4 py-8 text-center text-sm text-black-700 dark:text-black-600">No operations match “{query}”.</p>
    {:else}
      <div class="mt-4 grid grid-cols-12 gap-6">
        <!-- Category cards -->
        <div class="col-span-12 space-y-6 {categorized ? 'lg:col-span-9' : ''}">
          {#each pagedGroups as group (group.title)}
            {@const pageSel = group.pageOps.filter((o) => selected[o.key]).map((o) => o.key)}
            <div id={cardId(group.title)} class="scroll-mt-24 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
              {#if group.title}
                <div class="flex flex-wrap items-start gap-2 border-b border-white-300 dark:border-navy-600 px-4 py-3">
                  <div class="min-w-0 flex-1">
                    <div class="flex items-center gap-2">
                      <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">{group.title}</h3>
                      <span class="rounded-full bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">{group.ops.length}</span>
                    </div>
                    {#if group.description}
                      <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">{group.description}</p>
                    {/if}
                  </div>
                  <input
                    type="search"
                    aria-label={`Search ${group.title}`}
                    placeholder="Search…"
                    value={cardQuery[group.title] ?? ""}
                    oninput={(e) => setCardQuery(group.title, (e.target as HTMLInputElement).value)}
                    class="w-32 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-1.5 text-xs text-black-900 dark:text-white-100 outline-none focus:border-green-500"
                  />
                  {#if canConfigure}
                    <div class="flex items-center gap-2">
                      <button type="button" disabled={bulkBusy} class="rounded-lg border border-pos-300 px-2.5 py-1 text-[11px] font-medium text-pos-400 hover:bg-pos-100 disabled:opacity-50" onclick={() => bulkSet(true, group.ops.map((o) => o.key))}>Enable all</button>
                      <button type="button" disabled={bulkBusy} class="rounded-lg border border-neg-300 px-2.5 py-1 text-[11px] font-medium text-neg-400 hover:bg-neg-100 disabled:opacity-50" onclick={() => bulkSet(false, group.ops.map((o) => o.key))}>Disable all</button>
                    </div>
                  {/if}
                </div>
              {/if}
              {#if group.total === 0}
                <p class="px-4 py-6 text-center text-xs text-black-700 dark:text-black-600">No operations match “{cardQuery[group.title]}”.</p>
              {:else}
              <div class="overflow-x-auto resp-table-wrap">
                <table class="w-full text-sm resp-table">
                  <thead>
                    <tr class="border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
                      {#if canConfigure}
                        <th class="w-8 px-4 py-3">
                          <input
                            type="checkbox"
                            aria-label={`Select all in ${group.title || "operations"}`}
                            class="h-3.5 w-3.5 rounded accent-green-500"
                            checked={group.pageOps.length > 0 && pageSel.length === group.pageOps.length}
                            onchange={(e) => toggleGroupAll({ ...group, ops: group.pageOps }, (e.target as HTMLInputElement).checked)}
                          />
                        </th>
                      {/if}
                      <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Operation</th>
                      <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Description</th>
                      <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Actions</th>
                      <th class="px-4 py-3 text-right font-medium text-black-800 dark:text-black-600">Enabled</th>
                    </tr>
                  </thead>
                  <tbody>
                    {#each group.pageOps as op (op.key)}
                      <tr class="border-b border-white-300 dark:border-navy-600 last:border-0 align-top">
                        {#if canConfigure}
                          <td class="w-8 px-4 py-3">
                            <input
                              type="checkbox"
                              aria-label={`Select ${op.name}`}
                              class="h-3.5 w-3.5 rounded accent-green-500"
                              checked={!!selected[op.key]}
                              onchange={() => toggleRow(op.key)}
                            />
                          </td>
                        {/if}
                        <td class="px-4 py-3">
                          <div class="flex items-center gap-2">
                            <span class="font-medium text-black-900 dark:text-white-100">{op.name}</span>
                            {#if op.destructive}
                              <span class="rounded-full bg-neg-100 px-2 py-0.5 text-[10px] font-medium text-neg-400">destructive</span>
                            {/if}
                          </div>
                          <p class="mt-0.5 font-mono text-[10px] text-black-700 dark:text-black-600">{op.key}</p>
                        </td>
                        <td class="px-4 py-3 text-sm text-black-800 dark:text-black-600">{op.description}</td>
                        <td class="px-4 py-3">
                          <div class="flex items-center gap-2">
                            <button
                              type="button"
                              class="rounded-lg border border-green-400 px-3 py-1.5 text-xs font-medium text-green-600 hover:bg-green-100 dark:hover:bg-green-800"
                              onclick={() => testOp(op.key)}
                            >Test</button>
                            <button
                              type="button"
                              class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-black-600 hover:border-green-400 hover:text-green-600"
                              onclick={() => historyOp(op.key)}
                            >History</button>
                          </div>
                        </td>
                        <td class="px-4 py-3 text-right">
                          <div class="inline-flex flex-col items-end gap-1.5">
                            {#if op.system_disabled}
                              <span class="inline-flex items-center gap-1 rounded-md border border-prog-300 bg-prog-100 px-2 py-0.5 text-[10px] font-medium text-prog-400" title={`Health check warning: ${op.system_disabled_reason}. Toggle on to override.`}>⚠ {op.system_disabled_reason}</span>
                            {/if}
                            {#if canConfigure}
                              <button
                                type="button"
                                role="switch"
                                aria-checked={op.enabled}
                                aria-label={`Enable ${op.name}`}
                                disabled={busy[op.key]}
                                onclick={() => toggleEnabled(op)}
                                class="relative inline-flex h-5 w-9 items-center rounded-full transition-colors disabled:opacity-50 {op.enabled ? 'bg-green-500' : 'bg-white-400 dark:bg-navy-600'}"
                              >
                                <span class="absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white-100 shadow transition-transform {op.enabled ? 'translate-x-4' : ''}"></span>
                              </button>
                            {:else if op.enabled && !op.system_disabled}
                              <span class="rounded-full bg-pos-100 px-2 py-0.5 text-[10px] font-medium text-pos-400">enabled</span>
                            {:else if op.enabled}
                              <span class="rounded-full bg-prog-100 px-2 py-0.5 text-[10px] font-medium text-prog-400">enabled (warning)</span>
                            {:else}
                              <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">disabled</span>
                            {/if}
                          </div>
                        </td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
              {#if group.pages > 1}
                <div class="flex items-center justify-between gap-2 border-t border-white-300 dark:border-navy-600 px-4 py-2 text-xs text-black-700 dark:text-black-600">
                  <span>Showing {group.from}–{group.to} of {group.total}</span>
                  <div class="flex gap-1">
                    <button type="button" disabled={group.page <= 1} class="rounded px-2 py-1 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-40" onclick={() => cardPrev(group.title, group.page)}>← Prev</button>
                    <button type="button" disabled={group.page >= group.pages} class="rounded px-2 py-1 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-40" onclick={() => cardNext(group.title, group.page, group.pages)}>Next →</button>
                  </div>
                </div>
              {/if}
              {/if}
            </div>
          {/each}
        </div>

        <!-- Sticky sections sidebar (only when categorized) -->
        {#if categorized}
          <div class="col-span-12 lg:col-span-3">
            {#if navOpen}
              <button type="button" aria-label="Close sections" class="fixed inset-0 z-40 bg-black-900/40 lg:hidden" onclick={() => (navOpen = false)}></button>
            {/if}
            <div class="fixed inset-y-0 right-0 z-50 flex w-64 max-w-[80vw] flex-col border-l border-white-300 bg-white-100 shadow-xl transition-transform dark:border-navy-600 dark:bg-navy-700 lg:sticky lg:inset-y-auto lg:top-24 lg:z-auto lg:max-w-none lg:translate-x-0 lg:rounded-xl lg:border lg:shadow-none lg:transition-none {navOpen ? 'translate-x-0' : 'translate-x-full lg:translate-x-0'}">
              <div class="flex items-center justify-between gap-2 border-b border-white-300 px-3 py-2 dark:border-navy-600">
                <span class="text-xs font-semibold uppercase tracking-wide text-black-800 dark:text-black-600">Sections</span>
                <button type="button" title="Hide" aria-label="Hide sections" class="rounded-lg p-1.5 text-black-700 hover:bg-white-200 hover:text-green-600 dark:text-black-600 dark:hover:bg-navy-800 lg:hidden" onclick={() => (navOpen = false)}>
                  <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 16 16"><path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path></svg>
                </button>
              </div>
              <nav class="min-h-0 flex-1 overflow-y-auto p-2">
                {#each groups as group (group.title)}
                  {#if group.title}
                    <button type="button" class="flex w-full items-center justify-between gap-2 rounded-lg px-3 py-1.5 text-left text-xs font-medium text-black-800 hover:bg-white-200 hover:text-green-600 dark:text-black-600 dark:hover:bg-navy-800" onclick={() => jumpTo(group.title)}>
                      <span class="truncate">{group.title}</span>
                      <span class="rounded-full bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">{group.ops.length}</span>
                    </button>
                  {/if}
                {/each}
              </nav>
            </div>
          </div>
        {/if}
      </div>

      <!-- Mobile sections trigger -->
      {#if categorized}
        <button type="button" aria-label="Open sections" class="fixed bottom-4 right-4 z-30 inline-flex items-center gap-1.5 rounded-full bg-green-500 px-4 py-2.5 text-sm font-medium text-white-100 shadow-lg hover:bg-green-600 lg:hidden" onclick={() => (navOpen = true)}>
          <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M4 6h16M4 12h10M4 18h7" stroke-linecap="round"></path></svg>
          Sections
        </button>
      {/if}
    {/if}
  {/if}
</section>
