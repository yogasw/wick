<script lang="ts">
  // PickerField — typeahead chip selector backing the
  // `wick:"picker=<source>"` widget. Mirrors v1 admin's picker:
  // 250 ms debounced search against /workflows/api/lookup, click an
  // entry to chip it, × to remove. Value is the same JSON shape
  // `[{id, name}, …]` v1 emits so the same Go side parses both.
  //
  // The lookup source string (e.g. "slack.channels") encodes
  // module.path — first segment is the channel module name the
  // lookup endpoint expects, the whole string is the registry key.
  import { workflowAPI } from "$lib/api/workflow";

  type Chip = { id: string; name: string };
  type Props = {
    label: string;
    source: string;          // e.g. "slack.channels"
    value: string;           // JSON `[{id,name},...]` (or empty)
    onChange: (v: string) => void;
    helper?: string;
    placeholder?: string;
    required?: boolean;
  };

  let {
    label,
    source,
    value,
    onChange,
    helper,
    placeholder = "Search…",
    required = false,
  }: Props = $props();

  // First segment of "slack.channels" → module "slack". Lookup
  // endpoint resolves the rest via the channel's LookupProvider.
  const module = $derived(source.split(".")[0] ?? "");

  // Parse the stored JSON into chips. Tolerant — empty string is a
  // valid clean state, malformed strings fall back to [].
  function parseChips(v: string): Chip[] {
    if (!v) return [];
    try {
      const arr = JSON.parse(v);
      if (!Array.isArray(arr)) return [];
      return arr.filter(
        (r): r is Chip => r && typeof r.id === "string" && typeof r.name === "string",
      );
    } catch {
      return [];
    }
  }
  const chips = $derived(parseChips(value));

  function emitChips(next: Chip[]) {
    onChange(next.length === 0 ? "" : JSON.stringify(next));
  }

  function addChip(c: Chip) {
    if (chips.some((existing) => existing.id === c.id)) return;
    emitChips([...chips, c]);
    query = "";
    results = [];
  }

  function removeChip(id: string) {
    emitChips(chips.filter((c) => c.id !== id));
  }

  // Live search — debounced 250 ms, cancels in-flight via a token
  // counter so a fast-typing user only sees the latest response.
  let query = $state("");
  let results = $state<Chip[]>([]);
  let loading = $state(false);
  let open = $state(false);
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;
  let token = 0;

  $effect(() => {
    const q = query;
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(async () => {
      if (!module || !source) return;
      const myToken = ++token;
      loading = true;
      try {
        const res = await workflowAPI.lookup(module, source, q);
        if (myToken !== token) return; // stale
        results = res ?? [];
      } catch (e) {
        if (myToken !== token) return;
        console.warn("picker lookup failed:", e);
        results = [];
      } finally {
        if (myToken === token) loading = false;
      }
    }, 250);
    return () => {
      if (debounceTimer) clearTimeout(debounceTimer);
    };
  });
</script>

<div class="space-y-1">
  <span class="block text-xs font-medium">
    {label}{#if required}<span class="text-rose-500"> *</span>{/if}
  </span>

  <!-- Chips row. -->
  {#if chips.length > 0}
    <div class="flex flex-wrap gap-1.5">
      {#each chips as c (c.id)}
        <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-300 text-[11px]">
          <span class="font-medium">{c.name}</span>
          <span class="font-mono text-[10px] opacity-70">{c.id}</span>
          <button
            type="button"
            class="ml-0.5 text-emerald-700 dark:text-emerald-300 hover:text-rose-600"
            onclick={() => removeChip(c.id)}
            aria-label="Remove {c.name}"
          >✕</button>
        </span>
      {/each}
    </div>
  {/if}

  <!-- Search input + dropdown. -->
  <div class="relative">
    <input
      class="w-full rounded border border-slate-200 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-sm"
      type="search"
      {placeholder}
      bind:value={query}
      onfocus={() => (open = true)}
      onblur={() => setTimeout(() => (open = false), 150)}
    />
    {#if open && (loading || results.length > 0)}
      <div class="absolute left-0 right-0 top-full mt-1 z-30 max-h-60 overflow-y-auto rounded border border-slate-200 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-lg">
        {#if loading}
          <div class="px-3 py-2 text-[11px] italic text-black-700 dark:text-black-600">Searching…</div>
        {/if}
        {#each results as r (r.id)}
          <button
            type="button"
            class="w-full flex items-center justify-between gap-2 px-3 py-1.5 text-left hover:bg-slate-50 dark:hover:bg-white-300 dark:bg-navy-600"
            onclick={() => addChip(r)}
            disabled={chips.some((c) => c.id === r.id)}
          >
            <span class="text-sm">{r.name}</span>
            <span class="font-mono text-[10px] text-black-700 dark:text-black-600">{r.id}</span>
          </button>
        {/each}
        {#if !loading && results.length === 0 && query}
          <div class="px-3 py-2 text-[11px] italic text-black-700 dark:text-black-600">No matches for "{query}"</div>
        {/if}
      </div>
    {/if}
  </div>

  {#if helper}
    <span class="text-[11px] text-black-700 dark:text-black-600">{helper}</span>
  {/if}
</div>
