<script lang="ts">
  // Shared visual shell for every node-type card on the canvas. Mirrors
  // the legacy editor.css block-head layout: colored uppercase header
  // band + white (or navy-700 in dark) body with a sub-label line.
  // Keep this file dumb — it owns no behaviour, just layout.
  import type { Snippet } from "svelte";
  import type { NodeType } from "$lib/types/workflow";
  import { mdPlainText } from "$lib/markdown";
  import { draftWorkflow } from "$lib/stores/editor";
  import { cardWidth, flatPortLabels, nodeOutPorts, inputPorts, portTone, ERROR_KEY } from "../ports";

  type Props = {
    id: string;
    type: NodeType;
    label?: string;
    // Markdown "what for + why". Shown as a 2-line muted plain-text
    // summary under the label; full text in a hover tooltip. undefined
    // = caller doesn't track descriptions (no marker), "" = missing.
    description?: string;
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
    description,
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
  const descText = $derived(mdPlainText(description ?? ""));
  const descMissing = $derived(description !== undefined && descText === "");
  // One labelled port per output (cases, success/error) and, once a node
  // has more than one source, one labelled port per input. [] keeps the
  // single centre port on that side.
  const self = $derived($draftWorkflow?.graph?.nodes?.find((n) => n.id === id && n.type === type));
  const ports = $derived(nodeOutPorts(self, $draftWorkflow?.graph?.edges ?? []));
  const ins = $derived(inputs > 0 ? inputPorts(self, $draftWorkflow) : []);
  const toneOf = (p: { key: string; kind: string; label: string }) => portTone(p.kind === "error" ? ERROR_KEY : p.label);
  // Up to WIDE_PORTS_MAX outputs keep flat labels in a strip along the
  // card bottom, the card widening so each column stays readable; past
  // that the labels stand upright under the card, clipped to a few
  // characters, and hovering a port lays its full label flat. Several
  // inputs get a permanent strip under the header naming each source and
  // the case it sends. Anything clipped (a label, a source, the
  // description) has its full text in a hover tooltip.
  const width = $derived(cardWidth(ports.length, ins.length));
  const flat = $derived(flatPortLabels(ports.length));
  const VCAP = 10;
  const short = (s: string) => (s.length > VCAP ? s.slice(0, VCAP - 1) + "…" : s);
  // What an input carries: the case, the trigger type, "error" for a
  // fallback path, or "not wired" for a merge input with no edge yet.
  const inChip = (p: { detail?: string; trigger?: boolean; connected: boolean }) =>
    !p.connected ? "not wired" : p.trigger ? `⚡ ${p.detail ?? "trigger"}` : (p.detail ?? "");
  // Where an output leads, for its tooltip.
  const targetsOf = (p: { key: string; kind: string }) => {
    const wf = $draftWorkflow;
    const nodes = wf?.graph?.nodes ?? [];
    const ids =
      p.kind === "error"
        ? self?.fallback ? [self.fallback] : []
        : (wf?.graph?.edges ?? []).filter((e) => e.from === id && (e.case ?? "") === p.key).map((e) => e.to);
    return ids.map((t) => {
      const n = nodes.find((x) => x.id === t);
      return { label: n?.label || t, desc: mdPlainText(n?.description ?? "") };
    });
  };
  // Shared tooltip look. Opacity (not display) with a short delay so it
  // never flashes while the cursor crosses the card; pointer-events-none
  // so it never steals the hover that shows it.
  const TIP =
    "pointer-events-none absolute z-30 w-[240px] whitespace-normal rounded-md px-2.5 py-2 text-left text-[11px] font-normal normal-case leading-snug tracking-normal shadow-lg bg-black-800 text-white-100 dark:bg-white-100 dark:text-black-800 invisible opacity-0 transition-opacity delay-200 group-hover/tip:visible group-hover/tip:opacity-100";
  const inTone = (p: { detail?: string; trigger?: boolean; connected: boolean }) =>
    !p.connected
      ? "italic border border-dashed border-rose-300 text-rose-600 dark:text-rose-400"
      : p.detail === "error"
        ? portTone(ERROR_KEY)
        : p.detail && !p.trigger
          ? portTone(p.detail)
          : portTone("default");
  const inNub = (p: { detail?: string; trigger?: boolean; connected: boolean }) => {
    if (!p.connected) return "border-dashed border-rose-400";
    const t = inTone(p);
    return t.includes("rose") ? "border-rose-400" : t.includes("emerald") ? "border-emerald-400" : "border-slate-400 dark:border-navy-500";
  };
</script>

<div
  data-node-id={id}
  data-node-type={type}
  class="relative rounded-md shadow-md
         transition-all duration-150 ease-out cursor-pointer
         select-none
         bg-white-100 dark:bg-navy-700 text-black-800 dark:text-white-100
         border-2 {color}"
  style:width="{width}px"
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
    class="h-7 px-3 text-[10px] font-semibold tracking-[0.08em] uppercase text-white-100 flex items-center gap-1.5 rounded-t-md overflow-hidden"
    style:background-color={headBg ?? "#475569"}
  >
    {#if icon}<span class="text-[11px] leading-none">{icon}</span>{/if}
    <span class="truncate">{displayType}</span>
    {#if descMissing}
      <span
        class="ml-auto shrink-0 rounded px-1 text-[9px] font-semibold normal-case tracking-normal bg-amber-400 text-black-800"
        title="No description — add what this is for + why"
        aria-label="No description"
      >no desc</span>
    {/if}
    {#if running}
      <span class="{descMissing ? '' : 'ml-auto'} inline-flex h-1.5 w-1.5 rounded-full bg-amber-300 animate-pulse" aria-label="running"></span>
    {:else if errored}
      <span class="{descMissing ? '' : 'ml-auto'} inline-flex h-1.5 w-1.5 rounded-full bg-rose-300" aria-label="error"></span>
    {/if}
  </header>

  {#if ins.length > 0}
    <!-- Input ports along the top edge, one column per source (edge,
         trigger, error path, or a merge input still waiting for its wire);
         Canvas lands each source's edge on its dot (portCenterX in
         ../ports.ts). The dot is tinted by the case it receives. Under the
         header a permanent strip names each source and its case; hovering
         a column shows the full source name and its description. -->
    <div class="absolute inset-x-0 -top-[7px] grid" style:grid-template-columns="repeat({ins.length}, minmax(0, 1fr))" data-testid="in-ports">
      {#each ins as p (p.key)}
        <div class="group/port relative flex justify-center">
          <span
            class="wf-port-nub h-[14px] w-[14px] rounded-full bg-white-100 border-2 shadow transition-transform group-hover/port:scale-125 {inNub(p)}"
            data-port="in"
            data-in-port={p.key}
          ></span>
        </div>
      {/each}
    </div>
    <div class="grid border-b border-white-400 px-1 py-1 dark:border-navy-500" style:grid-template-columns="repeat({ins.length}, minmax(0, 1fr))" data-testid="in-strip">
      {#each ins as p (p.key)}
        {@const chip = inChip(p)}
        <div class="group/tip relative flex min-w-0 flex-col items-center px-0.5">
          {#if chip}
            <span class="max-w-full truncate rounded px-1.5 text-[10px] leading-4 {inTone(p)}">{chip}</span>
          {/if}
          <span class="max-w-full truncate text-[9px] leading-3 text-black-700 dark:text-black-500">{p.label}</span>
          <div class="{TIP} bottom-full left-1/2 mb-[14px] -translate-x-1/2" role="tooltip">
            <div class="font-semibold">{p.trigger ? "Trigger " : ""}{p.label}</div>
            {#if chip}
              <span class="mt-1 inline-block rounded px-1.5 text-[10px] leading-4 {inTone(p)}">{p.connected ? chip : "not wired yet"}</span>
            {/if}
            {#if p.desc}
              <div class="mt-1 opacity-80">{mdPlainText(p.desc)}</div>
            {/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}

  <div class="px-3 py-2">
    <div class="text-xs font-medium text-black-800 dark:text-white-100 truncate">{label ?? id}</div>
    {#if descText}
      <div class="group/tip relative">
        <div class="wf-node-desc mt-0.5 text-[11px] leading-snug text-black-700 dark:text-black-500">{descText}</div>
        <div class="{TIP} left-0 top-full mt-1" role="tooltip">
          <div class="font-semibold">Description</div>
          <div class="mt-0.5 opacity-80">{descText}</div>
        </div>
      </div>
    {/if}
    {#if body}
      <div class="mt-1 text-[11px] text-black-700 dark:text-black-600">
        {@render body()}
      </div>
    {/if}
  </div>

  {#if ports.length > 0}
    <!-- Output ports along the bottom edge (the flow runs top → bottom):
         one column per case, plus success/error when failures route to a
         fallback node. Canvas reads `data-out-port` (dot or label) to
         start a drag already tagged with that case, and anchors edges at
         portCenterX in ../ports.ts. -->
    {#if flat}
      <div class="grid px-1 pb-2" style:grid-template-columns="repeat({ports.length}, minmax(0, 1fr))" data-testid="out-ports">
        {#each ports as p (p.key)}
          {@const targets = targetsOf(p)}
          <div class="group/tip relative flex min-w-0 justify-center px-0.5 cursor-crosshair" data-out-port={p.key} data-out-kind={p.kind}>
            <span class="max-w-full truncate rounded px-1.5 text-[10px] leading-4 {toneOf(p)}">{p.label}</span>
            <div class="{TIP} left-1/2 top-full mt-[16px] -translate-x-1/2" role="tooltip">
              <span class="inline-block rounded px-1.5 text-[10px] leading-4 {toneOf(p)}">{p.label}</span>
              {#each targets as t}
                <div class="mt-1 font-semibold">→ {t.label}</div>
                {#if t.desc}<div class="opacity-80">{t.desc}</div>{/if}
              {:else}
                <div class="mt-1 opacity-80">Not connected — drag from here</div>
              {/each}
            </div>
          </div>
        {/each}
      </div>
    {/if}
    <div class="absolute inset-x-0 -bottom-[7px] grid" style:grid-template-columns="repeat({ports.length}, minmax(0, 1fr))" data-testid={flat ? undefined : "out-ports"}>
      {#each ports as p (p.key)}
        <div class="group/port relative flex justify-center cursor-crosshair" data-out-port={p.key} data-out-kind={p.kind}>
          <span
            class="wf-port-nub h-[14px] w-[14px] rounded-full bg-white-100 border-2 shadow transition-transform group-hover/port:scale-125 {p.kind === 'error' ? 'border-rose-400 group-hover/port:border-rose-500' : 'border-slate-400 dark:border-navy-500 group-hover/port:border-emerald-400'}"
            data-port="out"
            title="Drag to connect ({p.label})"
          ></span>
          {#if !flat}
            <!-- Upright, clipped label; on hover it fades (opacity, not
                 display, so the hover area does not shrink under the
                 cursor and flicker) and the full label shows flat. -->
            <span class="wf-port-label absolute top-[16px] left-1/2 ml-[5px] rounded bg-white-100 dark:bg-navy-700 group-hover/port:opacity-0">
              <span class="block whitespace-nowrap rounded px-px py-1 text-[10px] leading-3 [writing-mode:vertical-rl] {toneOf(p)}">{short(p.label)}</span>
            </span>
            <span class="pointer-events-none absolute top-[16px] left-1/2 z-30 ml-[5px] hidden rounded bg-white-100 shadow-md ring-1 ring-black/10 dark:bg-navy-700 group-hover/port:block">
              <span class="block whitespace-nowrap rounded px-1.5 text-[10px] leading-4 {toneOf(p)}">{p.label}</span>
            </span>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  <!-- Ports — top-center for input, bottom-center for output. Matches
       the legacy editor.css top→bottom flow layout (14px white solid
       circle with slate border). Drag-to-connect listener lives on the
       parent Canvas.svelte; here we just paint the visual handle.
       Hover-reveal: a graph of a dozen cards showed two dozen nubs at
       rest, which reads as noise. The card wrapper in Canvas.svelte
       carries `group`, so the nubs fade in when that card is hovered —
       and `.wf-connecting` (also in Canvas) pins them up mid-drag. -->
  {#if inputs > 0 && ins.length === 0}
    <span
      class="wf-port-nub absolute left-1/2 -translate-x-1/2 -top-[7px] h-[14px] w-[14px] rounded-full bg-white-100 border-2 border-white-400 dark:border-navy-500 shadow opacity-0 group-hover:opacity-100 transition-opacity"
      data-port="in"
    ></span>
  {/if}
  {#if outputs > 0 && ports.length === 0}
    <span
      class="wf-port-nub absolute left-1/2 -translate-x-1/2 -bottom-[7px] h-[14px] w-[14px] rounded-full bg-white-100 border-2 border-slate-400 dark:border-navy-500 shadow opacity-0 group-hover:opacity-100 transition-opacity"
      data-port="out"
    ></span>
  {/if}
</div>

<style>
  /* Two lines max so a long description never grows the card. */
  .wf-node-desc {
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    word-break: break-word;
  }
</style>
