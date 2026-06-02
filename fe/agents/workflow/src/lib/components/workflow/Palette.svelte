<script lang="ts">
  // Add Node panel — slide-in drawer on the RIGHT side of the canvas.
  // This is a dumb renderer: the backend `/api/workflows/palette`
  // endpoint owns categories, labels, badges, drill structure and drag
  // payloads. Adding a new node type / channel / connector on the
  // server lights up here automatically — no FE edit needed.
  import { paletteOpen } from "$lib/stores/editor";
  import { workflowAPI, type PaletteItem, type PaletteResponse } from "$lib/api/workflow";
  import { onMount } from "svelte";

  let palette = $state<PaletteResponse | null>(null);
  let query = $state("");
  let loading = $state(true);
  let error = $state<string | null>(null);

  // Current drill view — null = root, otherwise a drill_key into
  // palette.drills. The label persists across the drill so the back
  // button can render "← <parent label>".
  let drill = $state<{ key: string; label: string } | null>(null);

  onMount(async () => {
    try {
      palette = await workflowAPI.palette();
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  });

  // Root-level filter — passes through the search query across all
  // category items. Drill items get their own filter below.
  const filteredCategories = $derived.by(() => {
    if (!palette) return [];
    const q = query.trim().toLowerCase();
    return palette.categories
      .map((c) => ({
        ...c,
        items: c.items.filter((it) =>
          !q ||
          it.label.toLowerCase().includes(q) ||
          (it.description ?? "").toLowerCase().includes(q),
        ),
      }))
      .filter((c) => c.items.length > 0);
  });

  const drillItems = $derived.by<PaletteItem[]>(() => {
    if (!drill || !palette) return [];
    const items = palette.drills[drill.key] ?? [];
    const q = query.trim().toLowerCase();
    if (!q) return items;
    return items.filter(
      (it) =>
        it.label.toLowerCase().includes(q) ||
        (it.description ?? "").toLowerCase().includes(q),
    );
  });

  function enterDrill(item: PaletteItem) {
    if (item.kind !== "drill" || !item.drill_key) return;
    drill = { key: item.drill_key, label: item.label };
    query = "";
  }

  function exitDrill() {
    drill = null;
    query = "";
  }

  // Drag payload is fully described by item.drag. Each shape maps to
  // one of the three content types the Canvas drop handler understands
  // — keep that contract in sync with Canvas.ondrop.
  function ondragstart(e: DragEvent, item: PaletteItem) {
    if (item.kind !== "drag" || !item.drag) {
      e.preventDefault();
      return;
    }
    const d = item.drag;
    if (d.type === "trigger") {
      e.dataTransfer?.setData("application/x-wick-trigger-type", d.trigger_type);
    } else if (d.type === "channel-trigger") {
      e.dataTransfer?.setData(
        "application/x-wick-channel-event",
        JSON.stringify({ channel: d.channel, event: d.event }),
      );
    } else if (d.type === "node") {
      // For plain node drops (no channel/module/op) the legacy
      // `x-wick-node-type` payload suffices. Anything richer (channel
      // + op, module + op) flows through the action-prefill payload so
      // the inspector knows to lock those fields.
      if (d.channel || d.module || d.op) {
        e.dataTransfer?.setData(
          "application/x-wick-action-prefill",
          JSON.stringify({
            type: d.node_type,
            channel: d.channel,
            module: d.module,
            op: d.op,
          }),
        );
      } else {
        e.dataTransfer?.setData("application/x-wick-node-type", d.node_type);
      }
    }
    e.dataTransfer!.effectAllowed = "copy";
  }

  function close() {
    paletteOpen.set(false);
  }
</script>

<aside
  class="absolute top-0 right-0 h-full w-[300px] z-30 flex flex-col
         bg-slate-900/95 backdrop-blur border-l border-slate-800
         shadow-xl text-slate-100"
>
  <header class="flex items-center justify-between px-4 py-3 border-b border-slate-800">
    {#if drill}
      <button
        class="flex items-center gap-2 text-sm font-semibold hover:text-emerald-400"
        onclick={exitDrill}
        aria-label="Back to all nodes"
      >
        <span aria-hidden="true">←</span>
        <span>{drill.label}</span>
      </button>
    {:else}
      <span class="text-sm font-semibold">Add node</span>
    {/if}
    <button class="text-slate-400 hover:text-slate-100" onclick={close} aria-label="Close">✕</button>
  </header>

  <div class="px-3 py-2 border-b border-slate-800">
    <input
      type="search"
      placeholder={drill ? `Search ${drill.label}...` : "Search nodes..."}
      class="w-full rounded bg-slate-800 border border-slate-700 px-3 py-1.5 text-sm placeholder-slate-500 focus:outline-none focus:border-emerald-500"
      bind:value={query}
    />
  </div>

  <div class="flex-1 overflow-y-auto py-2">
    {#if loading}
      <p class="px-4 py-6 text-xs text-slate-500 italic">Loading palette…</p>
    {:else if error}
      <p class="px-4 py-6 text-xs text-rose-400">Palette failed: {error}</p>
    {:else if drill}
      <div class="px-2 space-y-1">
        {#each drillItems as item}
          <button
            draggable="true"
            ondragstart={(e) => ondragstart(e, item)}
            class="w-full flex flex-col items-start gap-0.5 px-3 py-2 rounded text-left text-slate-100 bg-slate-800 hover:bg-slate-700 cursor-grab transition-colors"
            title={item.description}
          >
            <div class="w-full flex items-center justify-between gap-2">
              <span class="text-sm font-medium truncate">{item.label}</span>
              {#if item.badge}<span class="text-[10px] text-slate-400 shrink-0">{item.badge}</span>{/if}
            </div>
            {#if item.description}
              <span class="text-[10px] text-slate-400 line-clamp-2 leading-snug w-full">
                {item.description}
              </span>
            {/if}
          </button>
        {/each}
        {#if drillItems.length === 0}
          <p class="px-1 py-4 text-xs text-slate-500 italic">
            {query ? `No matches for "${query}".` : "Nothing registered."}
          </p>
        {/if}
      </div>
    {:else}
      {#each filteredCategories as group}
        <div class="px-3 py-1.5 text-[11px] font-semibold tracking-wider text-slate-500">{group.title}</div>
        <div class="px-2 space-y-1">
          {#each group.items as item}
            {#if item.kind === "drill"}
              <button
                class="w-full flex items-center justify-between gap-2 px-3 py-2 rounded text-left text-slate-100 bg-slate-800 hover:bg-slate-700 transition-colors"
                onclick={() => enterDrill(item)}
              >
                <span class="text-sm font-medium truncate">{item.label}</span>
                <span class="flex items-center gap-1.5 text-[10px] text-slate-400 shrink-0">
                  {#if item.badge}<span>{item.badge}</span>{/if}
                  <span aria-hidden="true">›</span>
                </span>
              </button>
            {:else}
              <button
                draggable="true"
                ondragstart={(e) => ondragstart(e, item)}
                class="w-full flex items-center justify-between gap-2 px-3 py-2 rounded text-left text-slate-100 bg-slate-800 hover:bg-slate-700 cursor-grab transition-colors"
                title={item.description}
              >
                <span class="text-sm font-medium truncate">{item.label}</span>
                {#if item.badge}<span class="text-[10px] text-slate-400 shrink-0">{item.badge}</span>{/if}
              </button>
            {/if}
          {/each}
        </div>
      {/each}
      {#if filteredCategories.length === 0}
        <p class="px-4 py-6 text-xs text-slate-500 italic">
          {query ? `No matches for "${query}".` : "Palette is empty."}
        </p>
      {/if}
    {/if}
  </div>
</aside>
