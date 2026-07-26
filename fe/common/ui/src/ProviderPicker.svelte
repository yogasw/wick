<script lang="ts">
  /* Standalone provider picker: a dropdown that descends provider TYPE ▸
     INSTANCE ▸ MODEL, each level collapsing when it has only one choice.
     Same nesting the Composer's + menu uses, but usable anywhere a plain
     <select> was (project defaults, settings). Value is "type/name" or
     "type/name::modelID". */
  import type { ComposerSelectOption } from "./composer-types.js";

  type Props = {
    options: ComposerSelectOption[];
    value: string;
    onChange: (v: string) => void;
    /** Shown when value matches nothing (e.g. a deleted instance). */
    placeholder?: string;
    id?: string;
  };
  let { options, value, onChange, placeholder = "Select provider", id }: Props = $props();

  let open = $state(false);
  let typeDrill = $state<string>(""); // level 2: instances of this type
  let modelDrill = $state<ComposerSelectOption | null>(null); // level 3
  let rootEl = $state<HTMLDivElement | undefined>();

  function splitPin(v: string): { key: string; modelID: string } {
    const i = v.indexOf("::");
    return i < 0 ? { key: v, modelID: "" } : { key: v.slice(0, i), modelID: v.slice(i + 2) };
  }
  function rawType(v: string): string {
    const key = splitPin(v).key;
    const s = key.indexOf("/");
    return s < 0 ? key : key.slice(0, s);
  }
  function hasModels(o: ComposerSelectOption): boolean {
    return !!o.models && o.models.length > 1;
  }

  const groups = $derived.by(() => {
    const gs: { type: string; opts: ComposerSelectOption[] }[] = [];
    const idx = new Map<string, number>();
    for (const o of options) {
      const t = rawType(o.value);
      let i = idx.get(t);
      if (i === undefined) { i = gs.length; idx.set(t, i); gs.push({ type: t, opts: [] }); }
      gs[i].opts.push(o);
    }
    return gs;
  });

  // Label of the current value: "claude · opus" when a model is pinned.
  const label = $derived.by(() => {
    const { key, modelID } = splitPin(value);
    const opt = options.find((o) => o.value === key);
    if (!opt) return value || placeholder;
    if (!modelID) return opt.label;
    const m = opt.models?.find((x) => x.id === modelID);
    return m ? `${opt.label} · ${m.label}` : opt.label;
  });

  function reset() { typeDrill = ""; modelDrill = null; }
  function close() { open = false; reset(); }

  function pickType(g: { type: string; opts: ComposerSelectOption[] }) {
    if (g.opts.length === 1) {
      const only = g.opts[0];
      if (hasModels(only)) { modelDrill = only; return; }
      onChange(only.value); close();
      return;
    }
    typeDrill = g.type;
  }
  function pickInstance(o: ComposerSelectOption) {
    if (hasModels(o)) { modelDrill = o; return; }
    onChange(o.value); close();
  }
  function pickModel(o: ComposerSelectOption, modelID: string) {
    onChange(`${o.value}::${modelID}`); close();
  }

  $effect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (rootEl && !rootEl.contains(e.target as Node)) close();
    }
    function onKey(e: KeyboardEvent) { if (e.key === "Escape") close(); }
    window.addEventListener("mousedown", onDown, true);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", onDown, true);
      window.removeEventListener("keydown", onKey);
    };
  });
</script>

<div bind:this={rootEl} class="relative">
  <button
    {id}
    type="button"
    onclick={() => { if (open) close(); else { reset(); open = true; } }}
    class="flex w-full items-center justify-between gap-2 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 hover:border-green-500 transition-colors"
  >
    <span class="truncate">{label}</span>
    <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"/></svg>
  </button>

  {#if open}
    <div class="absolute z-30 mt-1 w-full min-w-[16rem] overflow-hidden rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-xl">
      {#if modelDrill}
        {@const d = modelDrill}
        <button type="button" onclick={() => (modelDrill = null)} class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs font-semibold text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700">
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M10 4L6 8l4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
          {d.label}
        </button>
        <div class="border-t border-white-300 dark:border-navy-600"></div>
        {#each d.models ?? [] as m (m.id)}
          {@const pinned = `${d.value}::${m.id}`}
          {@const isSel = value === pinned || (value === d.value && m.default)}
          <button type="button" onclick={() => pickModel(d, m.id)} class="flex w-full items-start justify-between gap-3 px-3 py-1.5 text-left {isSel ? 'bg-green-500/10' : 'hover:bg-white-200 dark:hover:bg-navy-700'} text-black-900 dark:text-white-100">
            <span class="flex flex-col min-w-0">
              <span class="flex items-center gap-2 text-sm"><span class="truncate">{m.label}</span>{#if m.default}<span class="shrink-0 rounded-full bg-green-500/10 px-1.5 py-0.5 text-[10px] font-medium text-green-600 dark:text-green-400">default</span>{/if}</span>
              {#if m.desc}<span class="text-[11px] text-black-700 dark:text-black-600 leading-snug">{m.desc}</span>{/if}
            </span>
            {#if isSel}<span class="shrink-0 mt-0.5 text-green-600 dark:text-green-400">✓</span>{/if}
          </button>
        {/each}
      {:else if typeDrill}
        {@const group = groups.find((g) => g.type === typeDrill)}
        <button type="button" onclick={() => (typeDrill = "")} class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs font-semibold text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700">
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M10 4L6 8l4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
          {typeDrill}
        </button>
        <div class="border-t border-white-300 dark:border-navy-600"></div>
        {#each group?.opts ?? [] as o (o.value)}
          {@const isSel = splitPin(value).key === o.value}
          <button type="button" onclick={() => pickInstance(o)} class="flex w-full items-center justify-between gap-3 px-3 py-1.5 text-left text-sm {isSel ? 'bg-green-500/10' : 'hover:bg-white-200 dark:hover:bg-navy-700'} text-black-900 dark:text-white-100">
            <span class="flex items-center gap-2 min-w-0"><span class="truncate">{o.label}</span>{#if o.badge}<span class="shrink-0 rounded-full bg-green-500/10 px-1.5 py-0.5 text-[10px] font-medium text-green-600 dark:text-green-400">{o.badge}</span>{/if}</span>
            {#if hasModels(o)}
              <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
            {:else if isSel}<span class="shrink-0 text-green-600 dark:text-green-400">✓</span>{/if}
          </button>
        {/each}
      {:else}
        {@const selKey = splitPin(value).key}
        {#each groups as g (g.type)}
          {@const single = g.opts.length === 1 ? g.opts[0] : null}
          {@const nested = g.opts.length > 1 || (single ? hasModels(single) : false)}
          <button type="button" onclick={() => pickType(g)} class="flex w-full items-center justify-between gap-3 px-3 py-1.5 text-left text-sm {(single && single.value === selKey) ? 'bg-green-500/10' : 'hover:bg-white-200 dark:hover:bg-navy-700'} text-black-900 dark:text-white-100">
            <span class="flex items-center gap-2 min-w-0"><span class="truncate">{single ? single.label : g.type}</span>{#if single?.badge}<span class="shrink-0 rounded-full bg-green-500/10 px-1.5 py-0.5 text-[10px] font-medium text-green-600 dark:text-green-400">{single.badge}</span>{/if}</span>
            {#if nested}
              <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
            {:else if single && single.value === selKey}<span class="shrink-0 text-green-600 dark:text-green-400">✓</span>{/if}
          </button>
        {/each}
      {/if}
    </div>
  {/if}
</div>
