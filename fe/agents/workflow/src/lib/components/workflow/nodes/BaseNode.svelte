<script lang="ts">
  // Shared visual shell for every node-type card on the canvas. Mirrors
  // the legacy editor.css block-head layout: colored uppercase header
  // band + white (or navy-700 in dark) body with a sub-label line.
  // Keep this file dumb — it owns no behaviour, just layout.
  import type { Snippet } from "svelte";
  import type { NodeType } from "$lib/types/workflow";

  type Props = {
    id: string;
    type: NodeType;
    label?: string;
    selected?: boolean;
    running?: boolean;
    errored?: boolean;
    // Either pass `headBg` (raw hex) for the legacy palette, OR `color`
    // (Tailwind class) for one-off variants. headBg wins when both set.
    headBg?: string;
    color?: string;
    icon?: string;
    // Override the uppercase header text. Default = type, uppercased
    // with underscores swapped for spaces (`datatable_get` → "DATATABLE
    // GET"). Use this when a wrapper component injects a virtual type
    // (e.g. TriggerNode passes `type="end"` to skip the regular palette
    // styling, but wants the header to read "TRIGGER").
    headLabel?: string;
    inputs?: number;
    outputs?: number;
    onselect?: () => void;
    body?: Snippet;
  };

  let {
    id,
    type,
    label,
    selected = false,
    running = false,
    errored = false,
    headBg,
    color = "",
    icon,
    headLabel,
    inputs = 1,
    outputs = 1,
    onselect,
    body,
  }: Props = $props();

  // Display type as uppercase + space-separated to match legacy
  // "DATATABLE QUERY" header style. headLabel wins when supplied.
  const displayType = $derived(
    headLabel ?? type.replace(/_/g, " ").toUpperCase(),
  );
</script>

<div
  data-node-id={id}
  data-node-type={type}
  class="relative rounded-md shadow-md
         transition-all duration-150 ease-out cursor-pointer
         w-[220px] select-none
         bg-white-100 dark:bg-navy-700 text-black-800 dark:text-white-100
         border-2 {color}"
  class:ring-2={selected}
  class:ring-emerald-400={selected && !errored}
  class:ring-rose-500={errored}
  class:border-emerald-400={selected && !errored && !running}
  class:border-rose-500={errored}
  class:border-amber-400={running}
  class:border-white-400={!selected && !running && !errored}
  class:dark:border-navy-500={!selected && !running && !errored}
  onclick={onselect}
  role="button"
  tabindex="0"
  onkeydown={(e) => (e.key === "Enter" || e.key === " ") && onselect?.()}
>
  <header
    class="px-3 py-1.5 text-[10px] font-semibold tracking-[0.08em] uppercase text-white-100 flex items-center gap-1.5 rounded-t-md overflow-hidden"
    style:background-color={headBg ?? "#475569"}
  >
    {#if icon}<span class="text-[11px] leading-none">{icon}</span>{/if}
    <span class="truncate">{displayType}</span>
    {#if running}
      <span class="ml-auto inline-flex h-1.5 w-1.5 rounded-full bg-amber-300 animate-pulse" aria-label="running"></span>
    {:else if errored}
      <span class="ml-auto inline-flex h-1.5 w-1.5 rounded-full bg-rose-300" aria-label="error"></span>
    {/if}
  </header>

  <div class="px-3 py-2">
    <div class="text-xs font-medium text-black-800 dark:text-white-100 truncate">{label ?? id}</div>
    {#if body}
      <div class="mt-1 text-[11px] text-black-700 dark:text-black-600">
        {@render body()}
      </div>
    {/if}
  </div>

  <!-- Ports — top-center for input, bottom-center for output. Matches
       the legacy editor.css top→bottom flow layout (14px white solid
       circle with slate border). Drag-to-connect listener lives on the
       parent Canvas.svelte; here we just paint the visual handle. -->
  {#if inputs > 0}
    <span
      class="absolute left-1/2 -translate-x-1/2 -top-[7px] h-[14px] w-[14px] rounded-full bg-white-100 border-2 border-white-400 dark:border-navy-500 shadow"
      data-port="in"
    ></span>
  {/if}
  {#if outputs > 0}
    <span
      class="absolute left-1/2 -translate-x-1/2 -bottom-[7px] h-[14px] w-[14px] rounded-full bg-white-100 border-2 border-slate-400 dark:border-navy-500 shadow"
      data-port="out"
    ></span>
  {/if}
</div>
