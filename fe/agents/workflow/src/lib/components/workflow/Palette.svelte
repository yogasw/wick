<script lang="ts">
  // Add Node panel — slide-in drawer on the RIGHT side of the canvas.
  // This is a dumb renderer: the backend `/api/workflows/palette`
  // endpoint owns categories, labels, badges, drill structure and drag
  // payloads. Adding a new node type / channel / connector on the
  // server lights up here automatically — no FE edit needed.
  import { paletteOpen, paletteAddRequest } from "$lib/stores/editor";
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

  // Tap / click to add — the touch-friendly path. Drag-and-drop still
  // works on the desktop (pointer) side; this just gives touch users
  // (and impatient mouse users) a one-tap way to drop the node at the
  // canvas centre. Canvas owns the placement via the paletteAddRequest
  // store. Drill rows keep their own onclick (enterDrill) and never
  // reach here.
  function tapAdd(item: PaletteItem) {
    if (item.kind !== "drag" || !item.drag) return;
    paletteAddRequest.set(item.drag);
  }
</script>

<aside
  class="absolute top-0 right-0 h-full w-[min(300px,85vw)] z-30 flex flex-col
         bg-white-100 dark:bg-navy-800/95 backdrop-blur border-l border-white-300 dark:border-navy-600
         shadow-xl text-black-800 dark:text-white-100"
>
  <header class="flex items-center justify-between px-4 py-3 border-b border-white-300 dark:border-navy-600">
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
    <button class="text-black-700 dark:text-black-500 hover:text-black-800 dark:text-white-100" onclick={close} aria-label="Close">✕</button>
  </header>

  <div class="px-3 py-2 border-b border-white-300 dark:border-navy-600">
    <input
      type="search"
      placeholder={drill ? `Search ${drill.label}...` : "Search nodes..."}
      class="w-full rounded bg-white-200 dark:bg-navy-700 border border-white-300 dark:border-navy-600 px-3 py-1.5 text-sm placeholder-black-700 dark:placeholder-black-600 focus:outline-none focus:border-emerald-500"
      bind:value={query}
    />
  </div>

  <div class="flex-1 overflow-y-auto py-2">
    {#if loading}
      <p class="px-4 py-6 text-xs text-black-700 dark:text-black-600 italic">Loading palette…</p>
    {:else if error}
      <p class="px-4 py-6 text-xs text-rose-400">Palette failed: {error}</p>
    {:else if drill}
      <div class="px-2 space-y-1">
        {#each drillItems as item}
          <button
            draggable="true"
            ondragstart={(e) => ondragstart(e, item)}
            onclick={() => tapAdd(item)}
            class="w-full flex flex-col items-start gap-0.5 px-3 py-2 rounded text-left text-black-800 dark:text-white-100 bg-white-200 dark:bg-navy-700 hover:bg-white-300 dark:bg-navy-600 cursor-grab transition-colors"
            title={item.description}
          >
            <div class="w-full flex items-center justify-between gap-2">
              <span class="text-sm font-medium truncate">{item.label}</span>
              {#if item.badge}<span class="text-[10px] text-black-700 dark:text-black-500 shrink-0">{item.badge}</span>{/if}
            </div>
            {#if item.description}
              <span class="text-[10px] text-black-700 dark:text-black-500 line-clamp-2 leading-snug w-full">
                {item.description}
              </span>
            {/if}
          </button>
        {/each}
        {#if drillItems.length === 0}
          <p class="px-1 py-4 text-xs text-black-700 dark:text-black-600 italic">
            {query ? `No matches for "${query}".` : "Nothing registered."}
          </p>
        {/if}
      </div>
    {:else}
      {#each filteredCategories as group}
        <div class="px-3 py-1.5 text-[11px] font-semibold tracking-wider text-black-700 dark:text-black-600">{group.title}</div>
        <div class="px-2 space-y-1">
          {#each group.items as item}
            {#if item.kind === "drill"}
              <button
                class="w-full flex items-center justify-between gap-2 px-3 py-2 rounded text-left text-black-800 dark:text-white-100 bg-white-200 dark:bg-navy-700 hover:bg-white-300 dark:bg-navy-600 transition-colors"
                onclick={() => enterDrill(item)}
              >
                <span class="text-sm font-medium truncate">{item.label}</span>
                <span class="flex items-center gap-1.5 text-[10px] text-black-700 dark:text-black-500 shrink-0">
                  {#if item.badge}<span>{item.badge}</span>{/if}
                  <span aria-hidden="true">›</span>
                </span>
              </button>
            {:else}
              <button
                draggable="true"
                ondragstart={(e) => ondragstart(e, item)}
                onclick={() => tapAdd(item)}
                class="w-full flex items-center justify-between gap-2 px-3 py-2 rounded text-left text-black-800 dark:text-white-100 bg-white-200 dark:bg-navy-700 hover:bg-white-300 dark:bg-navy-600 cursor-grab transition-colors"
                title={item.description}
              >
                <span class="text-sm font-medium truncate">{item.label}</span>
                {#if item.badge}<span class="text-[10px] text-black-700 dark:text-black-500 shrink-0">{item.badge}</span>{/if}
              </button>
            {/if}
          {/each}
        </div>
      {/each}
      {#if filteredCategories.length === 0}
        <p class="px-4 py-6 text-xs text-black-700 dark:text-black-600 italic">
          {query ? `No matches for "${query}".` : "Palette is empty."}
        </p>
      {/if}
    {/if}
  </div>
</aside>
