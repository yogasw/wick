<script lang="ts">
  // Add Node panel — slide-in drawer on the RIGHT side of the canvas.
  // Items now come from the backend catalog endpoint
  // (`/api/workflows/catalog`) so registering a new node executor
  // server-side surfaces here automatically. No more hard-coded list.
  import { paletteOpen } from "$lib/stores/editor";
  import { workflowAPI } from "$lib/api/workflow";
  import { onMount } from "svelte";

  type Item = {
    type: string;          // raw drag payload (`classify`, `trigger:cron`, …)
    label: string;
    badge?: string;
    children?: Item[];
  };
  type Group = { title: string; items: Item[] };

  let groups = $state<Group[]>([]);
  let query = $state("");
  let loading = $state(true);
  let error = $state<string | null>(null);

  // Buckets — `category` derives a coarse group label from the node
  // type's prefix or known sets. Mirrors the legacy panel layout:
  // TRIGGERS / AI / ACTION / LOGIC / DATA.
  const AI_TYPES = new Set(["agent", "session_init", "classify"]);
  const LOGIC_TYPES = new Set(["branch", "switch", "parallel", "merge", "end"]);
  const ACTION_TYPES = new Set(["http", "db_query", "shell", "go_script", "python", "connector", "channel"]);
  const DATA_TYPES = new Set(["transform"]);

  function categoryFor(type: string): "AI" | "ACTION" | "LOGIC" | "DATA" {
    if (AI_TYPES.has(type)) return "AI";
    if (LOGIC_TYPES.has(type)) return "LOGIC";
    if (type.startsWith("datatable_") || DATA_TYPES.has(type)) return "DATA";
    return "ACTION";
  }

  function prettyLabel(type: string): string {
    if (type.startsWith("datatable_")) return type.replace("datatable_", "").replace(/^./, (c) => c.toUpperCase());
    if (type === "go_script") return "Go Script";
    if (type === "db_query") return "DB Query";
    if (type === "http") return "HTTP / REST";
    return type.replace(/_/g, " ").replace(/\b./g, (c) => c.toUpperCase());
  }

  onMount(async () => {
    try {
      const cat = await workflowAPI.catalog();
      const triggerItems: Item[] = (cat.trigger_types ?? []).map((t) => ({
        type: `trigger:${t.type}`,
        label: t.label || t.type,
        badge: t.type === "error" ? "on fail" : undefined,
      }));
      const aiItems: Item[] = [];
      const actionItems: Item[] = [];
      const logicItems: Item[] = [];
      const dataItems: Item[] = [];
      const datatableChildren: Item[] = [];
      for (const n of cat.node_types ?? []) {
        const cat = categoryFor(n.type);
        const item: Item = { type: n.type, label: prettyLabel(n.type) };
        if (n.type.startsWith("datatable_")) {
          datatableChildren.push({ type: n.type, label: prettyLabel(n.type) });
          continue;
        }
        if (cat === "AI") aiItems.push(item);
        else if (cat === "LOGIC") logicItems.push(item);
        else if (cat === "DATA") dataItems.push(item);
        else actionItems.push(item);
      }
      if (datatableChildren.length > 0) {
        dataItems.push({
          type: "datatable_get", // parent stub, not draggable
          label: "Tables",
          badge: `${datatableChildren.length} ops`,
          children: datatableChildren,
        });
      }
      groups = [
        { title: "TRIGGERS", items: triggerItems },
        { title: "AI", items: aiItems },
        { title: "ACTION", items: actionItems },
        { title: "LOGIC", items: logicItems },
        { title: "DATA", items: dataItems },
      ].filter((g) => g.items.length > 0);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  });

  const expanded = $state<Record<string, boolean>>({});

  const filtered = $derived(
    groups
      .map((g) => ({
        title: g.title,
        items: g.items.filter((it) =>
          it.label.toLowerCase().includes(query.trim().toLowerCase()),
        ),
      }))
      .filter((g) => g.items.length > 0),
  );

  function ondragstart(e: DragEvent, type: string) {
    if (type.startsWith("trigger:")) {
      e.dataTransfer?.setData("application/x-wick-trigger-type", type.slice("trigger:".length));
    } else {
      e.dataTransfer?.setData("application/x-wick-node-type", type);
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
    <span class="text-sm font-semibold">Add node</span>
    <button class="text-slate-400 hover:text-slate-100" onclick={close} aria-label="Close">✕</button>
  </header>

  <div class="px-3 py-2 border-b border-slate-800">
    <input
      type="search"
      placeholder="Search nodes..."
      class="w-full rounded bg-slate-800 border border-slate-700 px-3 py-1.5 text-sm placeholder-slate-500 focus:outline-none focus:border-emerald-500"
      bind:value={query}
    />
  </div>

  <div class="flex-1 overflow-y-auto py-2">
    {#if loading}
      <p class="px-4 py-6 text-xs text-slate-500 italic">Loading catalog…</p>
    {:else if error}
      <p class="px-4 py-6 text-xs text-rose-400">Catalog failed: {error}</p>
    {:else}
      {#each filtered as group}
        <div class="px-3 py-1.5 text-[11px] font-semibold tracking-wider text-slate-500">{group.title}</div>
        <div class="px-2 space-y-1">
          {#each group.items as item}
            {#if item.children}
              <button
                class="w-full flex items-center justify-between gap-2 px-3 py-2 rounded text-sm text-slate-100 bg-slate-800 hover:bg-slate-700 transition-colors"
                onclick={() => (expanded[item.label] = !expanded[item.label])}
                aria-expanded={!!expanded[item.label]}
              >
                <span class="flex items-center gap-2">
                  <span class="text-slate-400 text-[10px]">{expanded[item.label] ? "▾" : "▸"}</span>
                  <span class="truncate">{item.label}</span>
                </span>
                <span class="flex items-center gap-1 text-[10px] text-slate-400">
                  {#if item.badge}<span>{item.badge}</span>{/if}
                </span>
              </button>
              {#if expanded[item.label]}
                <div class="ml-4 space-y-1 border-l border-slate-700 pl-2">
                  {#each item.children as child}
                    <button
                      draggable="true"
                      ondragstart={(e) => ondragstart(e, child.type)}
                      class="w-full flex items-center justify-between gap-2 px-3 py-1.5 rounded text-xs text-slate-100 bg-slate-800/70 hover:bg-slate-700 cursor-grab transition-colors"
                    >
                      <span class="truncate">{child.label}</span>
                      {#if child.badge}<span class="text-[10px] text-slate-400">{child.badge}</span>{/if}
                    </button>
                  {/each}
                </div>
              {/if}
            {:else}
              <button
                draggable="true"
                ondragstart={(e) => ondragstart(e, item.type)}
                class="w-full flex items-center justify-between gap-2 px-3 py-2 rounded text-sm text-slate-100 bg-slate-800 hover:bg-slate-700 cursor-grab transition-colors"
              >
                <span class="truncate">{item.label}</span>
                {#if item.badge}<span class="text-[10px] text-slate-400">{item.badge}</span>{/if}
              </button>
            {/if}
          {/each}
        </div>
      {/each}
      {#if filtered.length === 0}
        <p class="px-4 py-6 text-xs text-slate-500 italic">No nodes match "{query}".</p>
      {/if}
    {/if}
  </div>
</aside>
