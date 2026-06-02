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
    badge?: string;        // short right-aligned tag ("channel", "SQL query")
    description?: string;  // shown only on hover via title=, not in body
    // Action prefill — set on per-channel / per-connector rows so the
    // canvas drop handler can seed the new node's `channel` / `module`
    // field. Keeps the palette flat (one row per channel + one per
    // connector module) instead of a generic stub the user must then
    // fill in via the inspector.
    prefill?: { type: string; channel?: string; module?: string };
    children?: Item[];
  };
  type Group = { title: string; items: Item[] };

  // Built-in node types get a short right-aligned hint that matches v1's
  // palette ("SQL query", "GET / POST", "run command", "reshape data").
  // Anything not listed here renders without a badge — the action label
  // alone already reads as the verb.
  const NODE_BADGE: Record<string, string> = {
    http: "GET / POST",
    db_query: "SQL query",
    shell: "run command",
    transform: "reshape data",
    go_script: "Go script",
    python: "Python",
    agent: "AI agent",
    classify: "AI classify",
    session_init: "session",
    branch: "if / else",
    switch: "case",
    parallel: "fan-out",
    merge: "fan-in",
    end: "halt",
  };

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
      // Generic trigger types (cron, webhook, manual, schedule_at,
      // error). Skip `channel` — each registered channel gets its own
      // expandable parent below so the operator can drag a specific
      // event, not a stub.
      const triggerItems: Item[] = (cat.trigger_types ?? [])
        .filter((t) => t.type !== "channel")
        .map((t) => ({
          type: `trigger:${t.type}`,
          label: t.label || t.type,
          badge: t.type === "error" ? "on fail" : undefined,
        }));

      // Per-channel parent rows — children = registered events. Drag
      // a child to drop a channel trigger pre-populated with channel
      // + event. Mirrors the legacy editor's trigger picker.
      for (const ch of cat.channels ?? []) {
        const eventChildren: Item[] = (ch.events ?? []).map((ev) => ({
          // Payload format `trigger:channel:<channel>:<event>` — Canvas
          // ondrop unpacks this and creates a fully-formed trigger.
          type: `trigger:channel:${ch.name}:${ev.id}`,
          label: ev.name || ev.id,
          description: ev.description,
        }));
        if (eventChildren.length === 0) continue;
        triggerItems.push({
          type: `trigger:channel-parent:${ch.name}`,
          label: ch.name,
          badge: "trigger ›",
          children: eventChildren,
        });
      }
      const aiItems: Item[] = [];
      const actionItems: Item[] = [];
      const logicItems: Item[] = [];
      const dataItems: Item[] = [];
      const datatableChildren: Item[] = [];
      // Has the catalog declared a generic `channel` / `connector`
      // node type? If so, we replace the single generic row with one
      // row per registered channel / module (v1 parity). The catalog
      // still drives availability — channels.length === 0 → no rows.
      const hasChannelType = (cat.node_types ?? []).some((n) => n.type === "channel");
      const hasConnectorType = (cat.node_types ?? []).some((n) => n.type === "connector");
      for (const n of cat.node_types ?? []) {
        if (n.type === "channel" || n.type === "connector") continue; // expanded below
        const group = categoryFor(n.type);
        const item: Item = {
          type: n.type,
          label: prettyLabel(n.type),
          badge: NODE_BADGE[n.type],
          description: n.description, // tooltip only — hover, not body
        };
        if (n.type.startsWith("datatable_")) {
          datatableChildren.push(item);
          continue;
        }
        if (group === "AI") aiItems.push(item);
        else if (group === "LOGIC") logicItems.push(item);
        else if (group === "DATA") dataItems.push(item);
        else actionItems.push(item);
      }
      // Per-channel action rows. Drag = drop a `channel` node with the
      // channel field prefilled — inspector then picks the op.
      if (hasChannelType) {
        for (const ch of cat.channels ?? []) {
          actionItems.push({
            type: `action:channel:${ch.name}`,
            label: ch.name,
            badge: "channel",
            prefill: { type: "channel", channel: ch.name },
          });
        }
      }
      // Per-connector action rows. Catalog labels each connector with
      // its display `name` (e.g. "GitHub", "HTTP / REST") and machine
      // `module` (e.g. "github", "http"). Drag = drop a `connector`
      // node with the module field prefilled.
      if (hasConnectorType) {
        for (const con of cat.connectors ?? []) {
          actionItems.push({
            type: `action:connector:${con.module}`,
            label: con.name || con.module,
            badge: "connector",
            prefill: { type: "connector", module: con.module },
          });
        }
      }
      if (datatableChildren.length > 0) {
        dataItems.push({
          type: "datatable_get", // parent stub, not draggable
          label: "Data Tables",
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

  function ondragstart(e: DragEvent, item: Item) {
    const type = item.type;
    if (type.startsWith("trigger:channel:")) {
      // `trigger:channel:<channel>:<event>` — drop a fully-formed
      // channel trigger pre-populated with the picked event.
      const rest = type.slice("trigger:channel:".length);
      const idx = rest.indexOf(":");
      if (idx >= 0) {
        e.dataTransfer?.setData(
          "application/x-wick-channel-event",
          JSON.stringify({
            channel: rest.slice(0, idx),
            event: rest.slice(idx + 1),
          }),
        );
      }
    } else if (type.startsWith("trigger:channel-parent:")) {
      // Parent row itself isn't draggable — clicking only toggles
      // expansion. Suppress the drag.
      e.preventDefault();
      return;
    } else if (type.startsWith("trigger:")) {
      e.dataTransfer?.setData("application/x-wick-trigger-type", type.slice("trigger:".length));
    } else if (item.prefill) {
      // Per-channel / per-connector action — Canvas seeds the new
      // node's channel/module field from this payload so the user
      // doesn't have to pick it again in the inspector.
      e.dataTransfer?.setData(
        "application/x-wick-action-prefill",
        JSON.stringify(item.prefill),
      );
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
                      ondragstart={(e) => ondragstart(e, child)}
                      class="w-full flex items-center justify-between gap-2 px-3 py-1.5 rounded text-left text-slate-100 bg-slate-800/70 hover:bg-slate-700 cursor-grab transition-colors"
                      title={child.description}
                    >
                      <span class="text-xs font-medium truncate">{child.label}</span>
                      {#if child.badge}<span class="text-[10px] text-slate-400 shrink-0">{child.badge}</span>{/if}
                    </button>
                  {/each}
                </div>
              {/if}
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
      {#if filtered.length === 0}
        <p class="px-4 py-6 text-xs text-slate-500 italic">No nodes match "{query}".</p>
      {/if}
    {/if}
  </div>
</aside>
